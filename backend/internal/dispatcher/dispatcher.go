// Package dispatcher is the second stage of the worker: it delivers stored
// webhooks to the customer's destination and handles the outcome.
//
// For each dispatch message:
//
//  1. skip if the log is already final or this attempt was already made (idempotent);
//  2. if the endpoint is paused, re-check later without consuming an attempt;
//  3. decrypt the transformed request (or the original when there are no rules);
//  4. build headers: request headers + outbound custom headers with
//     {{secrets.NAME}} resolved + HookShield headers (id, attempt, timestamp, event);
//  5. send through the SSRF-protected client (no redirects);
//  6. record the attempt (status, latency, encrypted response) and decide:
//     2xx → delivered; otherwise retry with the endpoint's backoff policy, or
//     give up → failed (or dead when the DLQ is enabled);
//  7. alert once when failures reach the endpoint threshold or delivery is abandoned.
//
// Delivery is at-least-once: destinations should de-duplicate with the
// X-HookShield-Id header.
//
// Concurrency: each dispatcher delivers up to Options.Concurrency messages at
// once and acknowledges each one as soon as it is handled, so a slow
// destination never holds back the rest of a batch. While a message is in
// flight its idle time is refreshed (XCLAIM) every ClaimMinIdle/4, so no other
// dispatcher reclaims and re-delivers it. At most Options.MaxPerEndpoint
// deliveries per endpoint are in flight per worker (EndpointSlots); excess
// messages are re-scheduled shortly without consuming an attempt, so one
// black-hole destination cannot occupy every delivery slot.
package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/semaphore"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/live"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/retrypolicy"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

// Group is the consumer group name on the dispatch stream.
const Group = "dispatcher"

// Headers HookShield adds to every delivery.
const (
	HeaderID        = "X-HookShield-Id"
	HeaderAttempt   = "X-HookShield-Attempt"
	HeaderTimestamp = "X-HookShield-Timestamp"
	HeaderEvent     = "X-HookShield-Event"
)

// MaxResponseBytes is how much of the destination's response body is kept.
const MaxResponseBytes = 64 << 10

// ResponseAAD binds a stored destination response to its attempt.
func ResponseAAD(logID string, attempt int) []byte {
	return []byte("attempt:" + logID + ":" + strconv.Itoa(attempt) + ":response")
}

// Error codes recorded when no request could be sent.
const (
	ErrCodeSecretNotFound = "secret_not_found"
	ErrCodeInvalidHeader  = "invalid_header"
	ErrCodeInvalidTarget  = "invalid_target"
)

var secretRef = regexp.MustCompile(`\{\{\s*secrets\.([A-Z][A-Z0-9_]{0,63})\s*\}\}`)

// Store is the persistence the dispatcher needs (implemented by *store.Store).
type Store interface {
	GetDeliveryState(ctx context.Context, logID string) (store.DeliveryState, error)
	GetDispatchConfig(ctx context.Context, endpointID string) (store.DispatchConfig, error)
	GetUserSecrets(ctx context.Context, userID string, names []string) (map[string]store.UserSecret, error)
	RecordAttempt(ctx context.Context, a store.AttemptRecord) (bool, error)
	MarkAlerted(ctx context.Context, logID string) (bool, error)
	ListOverdueRetries(ctx context.Context, before time.Time, limit int) ([]store.OverdueRetry, error)
	TouchRetry(ctx context.Context, logID string, next time.Time) error
}

// Queue schedules retries and records dead letters (implemented by *queue.Producer).
type Queue interface {
	ScheduleDispatch(ctx context.Context, m queue.DispatchMessage, at time.Time) error
	MoveDueRetries(ctx context.Context, now time.Time, limit int) (int, error)
	DeadLetter(ctx context.Context, source, entryID, reason string, fields map[string]any) error
}

// Source is the dispatch stream consumer.
type Source interface {
	EnsureGroup(ctx context.Context) error
	Read(ctx context.Context, count int64, block time.Duration) ([]redis.XMessage, error)
	Claim(ctx context.Context, minIdle time.Duration, count int64) ([]redis.XMessage, error)
	Touch(ctx context.Context, ids ...string) error
	Done(ctx context.Context, ids ...string) error
}

// Options tune the dispatcher.
type Options struct {
	BatchSize     int64         // default 16
	Block         time.Duration // default 2 s
	ClaimMinIdle  time.Duration // default 2 min; in-flight messages are touched every ClaimMinIdle/4
	ClaimInterval time.Duration // default 30 s
	// Concurrency is how many messages one dispatcher handles at once (default 4).
	Concurrency int
	// MaxPerEndpoint caps in-flight deliveries per endpoint (default
	// DefaultMaxPerEndpoint); ignored when Endpoints is set.
	MaxPerEndpoint int
	// Endpoints is the per-endpoint limiter; share one between the dispatchers
	// of a process (nil = a private limiter of MaxPerEndpoint).
	Endpoints *EndpointSlots
	// DeferDelay is how long a message waits when its endpoint has no free
	// slot (default 1 s); deferring does not consume an attempt.
	DeferDelay    time.Duration
	PausedRecheck time.Duration // default 60 s
	JitterPercent float64       // ± jitter on retry delays; default 10, negative disables
	Logger        *slog.Logger
	Now           func() time.Time
	Rand          *rand.Rand
	// Plans caps max_retries per account (nil = self-hosted: no cap).
	Plans core.Plans
	// Events receives a log.updated event per recorded attempt (nil = none).
	Events *live.Publisher
}

// Dispatcher delivers webhooks.
type Dispatcher struct {
	src      Source
	q        Queue
	store    Store
	cipher   *crypto.Cipher
	client   *http.Client
	notifier alert.Notifier
	opts     Options
	rng      *rand.Rand          // goroutine-safe wrapper of opts.Rand
	slots    *semaphore.Weighted // Concurrency delivery slots

	mu       sync.Mutex
	inflight map[string]int // stream entry ID -> handlers working on it
}

// lockedSource makes a rand.Source safe for the concurrent handlers.
type lockedSource struct {
	mu  sync.Mutex
	src rand.Source
}

func (l *lockedSource) Uint64() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.src.Uint64()
}

// New builds a Dispatcher. A nil notifier discards alerts.
func New(src Source, q Queue, st Store, cipher *crypto.Cipher, client *http.Client, notifier alert.Notifier, opts Options) *Dispatcher {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 16
	}
	if opts.Block <= 0 {
		opts.Block = 2 * time.Second
	}
	if opts.ClaimMinIdle <= 0 {
		opts.ClaimMinIdle = 2 * time.Minute
	}
	if opts.ClaimInterval <= 0 {
		opts.ClaimInterval = 30 * time.Second
	}
	if opts.PausedRecheck <= 0 {
		opts.PausedRecheck = time.Minute
	}
	if opts.JitterPercent == 0 {
		opts.JitterPercent = 10
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x486f6f6b))
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Endpoints == nil {
		opts.Endpoints = NewEndpointSlots(opts.MaxPerEndpoint)
	}
	if opts.DeferDelay <= 0 {
		opts.DeferDelay = time.Second
	}
	if notifier == nil {
		notifier = alert.Noop{}
	}
	opts.Plans = core.OrSelfHosted(opts.Plans, 0)
	return &Dispatcher{
		src: src, q: q, store: st, cipher: cipher, client: client, notifier: notifier, opts: opts,
		rng:      rand.New(&lockedSource{src: opts.Rand}),
		slots:    semaphore.NewWeighted(int64(opts.Concurrency)),
		inflight: map[string]int{},
	}
}

// Run delivers messages until ctx is cancelled. New messages are read as soon
// as a delivery slot frees up: a slow delivery holds one slot, not the loop.
func (d *Dispatcher) Run(ctx context.Context) error {
	if err := d.src.EnsureGroup(ctx); err != nil {
		return err
	}
	stopKeepAlive := d.keepAlive(ctx)
	var running sync.WaitGroup
	defer func() {
		running.Wait() // deliveries end quickly once ctx is cancelled
		stopKeepAlive()
	}()
	onDone := func(_ bool, err error) {
		if err != nil && ctx.Err() == nil {
			d.opts.Logger.Warn("acknowledge failed", "err", err)
		}
	}
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) >= d.opts.ClaimInterval {
			lastClaim = time.Now()
			msgs, err := d.fetch(ctx, func(n int64) ([]redis.XMessage, error) {
				return d.src.Claim(ctx, d.opts.ClaimMinIdle, n)
			})
			if err != nil && ctx.Err() == nil {
				d.opts.Logger.Warn("reclaim failed", "err", err)
			}
			d.start(ctx, msgs, &running, onDone)
		}
		msgs, err := d.fetch(ctx, func(n int64) ([]redis.XMessage, error) {
			return d.src.Read(ctx, n, d.opts.Block)
		})
		if err != nil && ctx.Err() == nil {
			d.opts.Logger.Warn("dispatch read failed", "err", err)
			sleep(ctx, time.Second)
		}
		d.start(ctx, msgs, &running, onDone)
	}
	return nil
}

// DispatchBatch reads one batch, handles it and waits for it; returns how
// many messages were acknowledged.
func (d *Dispatcher) DispatchBatch(ctx context.Context) (int, error) {
	return d.runOnce(ctx, func(n int64) ([]redis.XMessage, error) {
		return d.src.Read(ctx, n, d.opts.Block)
	})
}

// ReclaimStuck re-handles messages abandoned by a crashed dispatcher and
// waits for them; returns how many were acknowledged.
func (d *Dispatcher) ReclaimStuck(ctx context.Context) (int, error) {
	return d.runOnce(ctx, func(n int64) ([]redis.XMessage, error) {
		return d.src.Claim(ctx, d.opts.ClaimMinIdle, n)
	})
}

func (d *Dispatcher) runOnce(ctx context.Context, get func(n int64) ([]redis.XMessage, error)) (int, error) {
	stopKeepAlive := d.keepAlive(ctx)
	defer stopKeepAlive()
	msgs, err := d.fetch(ctx, get)
	if err != nil {
		return 0, err
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		acked int
		errs  []error
	)
	d.start(ctx, msgs, &wg, func(ok bool, err error) {
		mu.Lock()
		defer mu.Unlock()
		if ok {
			acked++
		}
		if err != nil {
			errs = append(errs, err)
		}
	})
	wg.Wait()
	return acked, errors.Join(errs...)
}

// fetch waits for at least one free delivery slot, takes as many free slots as
// possible (up to BatchSize), reads that many messages with get and gives back
// the slots it did not use. Each returned message owns one slot.
func (d *Dispatcher) fetch(ctx context.Context, get func(n int64) ([]redis.XMessage, error)) ([]redis.XMessage, error) {
	if err := d.slots.Acquire(ctx, 1); err != nil {
		return nil, nil // cancelled
	}
	n := int64(1)
	for n < d.opts.BatchSize && d.slots.TryAcquire(1) {
		n++
	}
	msgs, err := get(n)
	if int64(len(msgs)) > n { // never more than asked, but stay safe
		msgs = msgs[:n]
	}
	d.slots.Release(n - int64(len(msgs)))
	return msgs, err
}

// start handles msgs concurrently; each one already owns a delivery slot,
// released when it is done. Each message is acknowledged as soon as it is
// handled; onDone reports (acknowledged, ack error) per message.
func (d *Dispatcher) start(ctx context.Context, msgs []redis.XMessage, wg *sync.WaitGroup, onDone func(bool, error)) {
	for _, m := range msgs {
		d.track(m.ID, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer d.slots.Release(1)
			defer d.track(m.ID, -1)
			if !d.handle(ctx, m) {
				// Transient failure: left pending, reclaimed after ClaimMinIdle
				// (it stops being touched once it leaves the in-flight set).
				onDone(false, nil)
				return
			}
			err := d.src.Done(ctx, m.ID)
			onDone(err == nil, err)
		}()
	}
}

func (d *Dispatcher) track(id string, delta int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inflight[id] += delta; d.inflight[id] <= 0 {
		delete(d.inflight, id)
	}
}

// keepAlive refreshes the idle time of in-flight messages every ClaimMinIdle/4
// until the returned stop function is called, so a long delivery (or a long
// wait in a batch) is never mistaken for a crashed consumer and re-delivered.
func (d *Dispatcher) keepAlive(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(max(d.opts.ClaimMinIdle/4, time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
			}
			d.mu.Lock()
			ids := make([]string, 0, len(d.inflight))
			for id := range d.inflight {
				ids = append(ids, id)
			}
			d.mu.Unlock()
			if err := d.src.Touch(ctx, ids...); err != nil && ctx.Err() == nil {
				d.opts.Logger.Warn("refresh in-flight messages failed", "err", err, "count", len(ids))
			}
		}
	}()
	return func() { close(done); <-finished }
}

// RunScheduler moves due retries to the dispatch stream every interval and,
// every sweepEvery, re-schedules retries whose scheduling was lost.
// Safe to run in every worker: moving is atomic.
func (d *Dispatcher) RunScheduler(ctx context.Context, interval, sweepEvery time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	if sweepEvery <= 0 {
		sweepEvery = time.Minute
	}
	lastSweep := time.Time{}
	for ctx.Err() == nil {
		if _, err := d.q.MoveDueRetries(ctx, d.opts.Now(), 500); err != nil && ctx.Err() == nil {
			d.opts.Logger.Warn("move due retries failed", "err", err)
		}
		if time.Since(lastSweep) >= sweepEvery {
			lastSweep = time.Now()
			if n, err := d.SweepOverdue(ctx); err != nil && ctx.Err() == nil {
				d.opts.Logger.Warn("sweep overdue retries failed", "err", err)
			} else if n > 0 {
				d.opts.Logger.Warn("re-scheduled lost retries", "count", n)
			}
		}
		sleep(ctx, interval)
	}
}

// SweepOverdue re-schedules "retrying" logs whose next attempt is more than
// 2 minutes late (their scheduled message was lost, e.g. a crash right after
// recording the previous attempt). Duplicate scheduling is harmless.
func (d *Dispatcher) SweepOverdue(ctx context.Context) (int, error) {
	now := d.opts.Now()
	overdue, err := d.store.ListOverdueRetries(ctx, now.Add(-2*time.Minute), 500)
	if err != nil {
		return 0, err
	}
	for _, o := range overdue {
		m := queue.DispatchMessage{LogID: o.LogID, EndpointID: o.EndpointID, Attempt: o.NextAttempt}
		if err := d.q.ScheduleDispatch(ctx, m, now); err != nil {
			return 0, err
		}
		if err := d.store.TouchRetry(ctx, o.LogID, now.Add(5*time.Minute)); err != nil {
			return 0, err
		}
	}
	return len(overdue), nil
}

// handle reports whether the entry can be acknowledged (false = transient error, retry later).
func (d *Dispatcher) handle(ctx context.Context, entry redis.XMessage) bool {
	log := d.opts.Logger.With("entry", entry.ID)

	msg, err := queue.ParseDispatchMessage(entry.Values)
	if err != nil {
		log.Error("malformed dispatch message moved to dead-letter stream")
		return d.q.DeadLetter(ctx, "dispatch", entry.ID, "malformed", entry.Values) == nil
	}
	log = log.With("log_id", msg.LogID, "attempt", msg.Attempt)

	// Per-endpoint cap: a slow destination must not take every delivery slot.
	// Re-check shortly without consuming an attempt (like a paused endpoint).
	if !d.opts.Endpoints.TryAcquire(msg.EndpointID) {
		log.Debug("endpoint busy; delivery deferred", "endpoint_id", msg.EndpointID)
		return d.q.ScheduleDispatch(ctx, msg, d.opts.Now().Add(d.opts.DeferDelay)) == nil
	}
	defer d.opts.Endpoints.Release(msg.EndpointID)

	state, err := d.store.GetDeliveryState(ctx, msg.LogID)
	if errors.Is(err, store.ErrNotFound) {
		return true // log expired or endpoint deleted
	}
	if err != nil {
		log.Warn("load delivery state failed; will retry", "err", err)
		return false
	}
	if state.Final() || state.AttemptCount >= msg.Attempt {
		return true // already handled (duplicate or stale message)
	}
	// A copy of this very attempt is being delivered by another handler of
	// this process (redelivered message): check again shortly, when its
	// result is recorded, instead of calling the destination twice.
	attemptKey := msg.LogID + "#" + strconv.Itoa(msg.Attempt)
	if !d.opts.Endpoints.TryClaimAttempt(attemptKey) {
		return d.q.ScheduleDispatch(ctx, msg, d.opts.Now().Add(d.opts.DeferDelay)) == nil
	}
	defer d.opts.Endpoints.ReleaseAttempt(attemptKey)

	cfg, err := d.store.GetDispatchConfig(ctx, state.EndpointID)
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		log.Warn("load dispatch config failed; will retry", "err", err)
		return false
	}

	if cfg.Status == store.StatusPaused {
		// Hold without consuming an attempt.
		if err := d.q.ScheduleDispatch(ctx, msg, d.opts.Now().Add(d.opts.PausedRecheck)); err != nil {
			return false
		}
		return true
	}

	env, err := d.payload(state)
	if err != nil {
		log.Error("cannot decrypt stored payload", "err", err)
		return d.q.DeadLetter(ctx, "dispatch", entry.ID, "decrypt_failed", entry.Values) == nil
	}

	policy := policyFor(cfg, d.opts.Plans)
	policy.JitterPercent = d.opts.JitterPercent

	result := d.deliver(ctx, cfg, state, env, msg.Attempt)

	rec := store.AttemptRecord{
		LogID:             state.ID,
		AttemptNumber:     msg.Attempt,
		StartedAt:         result.startedAt,
		DurationMicros:    result.duration.Microseconds(),
		StatusCode:        result.status,
		ErrorCode:         result.errCode,
		KeyVersion:        d.cipher.KeyVersion(),
		ResponseEncrypted: result.respCT,
		ResponseNonce:     result.respNonce,
		PersistMicros:     msg.PersistMicros,
	}
	var next *time.Time
	switch {
	case retrypolicy.Success(result.status):
		rec.NewStatus = store.LogDelivered
	case policy.ShouldRetry(msg.Attempt):
		at := d.opts.Now().Add(policy.Delay(msg.Attempt, result.retryAfter, d.rng))
		next = &at
		rec.NewStatus = store.LogRetrying
		rec.NextAttemptAt = next
	case cfg.DLQEnabled:
		rec.NewStatus = store.LogDead
	default:
		rec.NewStatus = store.LogFailed
	}

	recorded, err := d.store.RecordAttempt(ctx, rec)
	if err != nil {
		log.Warn("record attempt failed; will retry", "err", err)
		return false
	}
	if !recorded {
		return true // another dispatcher recorded this attempt first
	}
	d.opts.Events.Publish(ctx, cfg.UserID, live.Event{
		Type: live.LogUpdated, LogID: msg.LogID, EndpointID: msg.EndpointID, Status: rec.NewStatus,
		Attempt: msg.Attempt, StatusCode: result.status, LatencyMs: float64(rec.DurationMicros) / 1000, At: d.opts.Now(),
	})

	log.Debug("delivery attempt", "status", result.status, "error", result.errCode, "outcome", rec.NewStatus)

	if next != nil {
		nm := queue.DispatchMessage{LogID: msg.LogID, EndpointID: msg.EndpointID, Attempt: msg.Attempt + 1}
		if err := d.q.ScheduleDispatch(ctx, nm, *next); err != nil {
			// The sweeper recovers it from payload_logs.next_attempt_at.
			log.Warn("schedule retry failed; sweeper will recover it", "err", err)
		}
	}
	if rec.NewStatus == store.LogDead {
		_ = d.q.DeadLetter(ctx, "dispatch", entry.ID, "max_attempts_exceeded",
			map[string]any{"log_id": msg.LogID, "endpoint_id": msg.EndpointID})
	}

	if rec.NewStatus != store.LogDelivered {
		final := rec.NewStatus == store.LogDead || rec.NewStatus == store.LogFailed
		if msg.Attempt >= cfg.AlertThresholdFailures || final {
			d.alert(ctx, log, cfg, state, msg.Attempt, result, final)
		}
	}
	return true
}

func (d *Dispatcher) alert(ctx context.Context, log *slog.Logger, cfg store.DispatchConfig, state store.DeliveryState, attempts int, r deliveryResult, final bool) {
	first, err := d.store.MarkAlerted(ctx, state.ID)
	if err != nil || !first {
		return
	}
	a := alert.Alert{
		LogID: state.ID, EndpointID: cfg.EndpointID, UserID: cfg.UserID, Attempts: attempts,
		LastStatus: r.status, LastError: r.errCode, Final: final, At: d.opts.Now(),
	}
	if state.EventType != nil {
		a.EventType = *state.EventType
	}
	if err := d.notifier.Notify(ctx, a); err != nil {
		log.Warn("alert notification failed", "err", err)
	}
}

func policyFor(cfg store.DispatchConfig, plans core.Plans) retrypolicy.Policy {
	p := retrypolicy.Policy{
		Strategy:   cfg.RetryStrategy,
		MaxRetries: retrypolicy.CapRetries(cfg.MaxRetries, plans.For(cfg.PlanType).MaxRetries),
		Initial:    time.Duration(cfg.RetryInitialMs) * time.Millisecond,
		Multiplier: cfg.RetryMultiplier,
		MaxDelay:   time.Duration(cfg.RetryMaxDelayMs) * time.Millisecond,
	}
	for _, ms := range cfg.RetryCustomScheduleMs {
		p.Schedule = append(p.Schedule, time.Duration(ms)*time.Millisecond)
	}
	return p
}

func (d *Dispatcher) payload(s store.DeliveryState) (queue.Envelope, error) {
	if s.KeyVersion != d.cipher.KeyVersion() {
		return queue.Envelope{}, fmt.Errorf("unknown key version %d", s.KeyVersion)
	}
	ct, nonce, aad := s.OriginalEncrypted, s.OriginalNonce, processor.OriginalAAD(s.ID)
	if s.TransformedEncrypted != nil {
		ct, nonce, aad = s.TransformedEncrypted, s.TransformedNonce, processor.TransformedAAD(s.ID)
	}
	plain, err := d.cipher.Open(ct, nonce, aad)
	if err != nil {
		return queue.Envelope{}, err
	}
	return queue.UnmarshalEnvelope(plain)
}

type deliveryResult struct {
	startedAt  time.Time
	duration   time.Duration
	status     int
	errCode    string
	retryAfter time.Duration
	respCT     []byte
	respNonce  []byte
}

// skipHeaders are never forwarded: connection-level, recomputed by the client,
// or reserved for HookShield (so senders cannot spoof them).
var skipHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Keep-Alive": true, "Te": true,
	"Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Proxy-Authorization": true,
	"Proxy-Connection": true, "Accept-Encoding": true,
}

func (d *Dispatcher) deliver(ctx context.Context, cfg store.DispatchConfig, s store.DeliveryState, env queue.Envelope, attempt int) deliveryResult {
	r := deliveryResult{startedAt: d.opts.Now()}

	target, err := url.Parse(cfg.TargetURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		r.errCode = ErrCodeInvalidTarget
		return r
	}
	if env.Query != "" {
		if target.RawQuery == "" {
			target.RawQuery = env.Query
		} else {
			target.RawQuery += "&" + env.Query
		}
	}

	outHeaders, code := d.resolveOutboundHeaders(ctx, cfg)
	if code != "" {
		r.errCode = code
		return r
	}

	method := env.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(env.Body))
	if err != nil {
		r.errCode = ErrCodeInvalidTarget
		return r
	}
	for k, vs := range env.Headers {
		ck := http.CanonicalHeaderKey(k)
		if skipHeaders[ck] || strings.HasPrefix(ck, "X-Hookshield-") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(ck, v)
		}
	}
	for k, v := range outHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "HookShield/"+version.Version)
	req.Header.Set(HeaderID, s.ID)
	req.Header.Set(HeaderAttempt, strconv.Itoa(attempt))
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(r.startedAt.Unix(), 10))
	if s.EventType != nil && *s.EventType != "" {
		req.Header.Set(HeaderEvent, *s.EventType)
	}

	resp, err := d.client.Do(req)
	r.duration = d.opts.Now().Sub(r.startedAt)
	if err != nil {
		r.errCode = outbound.ErrorCode(err)
		return r
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // let the connection be reused
	r.duration = d.opts.Now().Sub(r.startedAt)
	r.status = resp.StatusCode
	r.retryAfter = retrypolicy.ParseRetryAfter(resp.Header.Get("Retry-After"), d.opts.Now())

	stored, _ := json.Marshal(struct {
		Status  int                 `json:"status"`
		Headers map[string][]string `json:"headers"`
		Body    []byte              `json:"body"`
	}{resp.StatusCode, resp.Header, body})
	if ct, nonce, err := d.cipher.Seal(stored, ResponseAAD(s.ID, attempt)); err == nil {
		r.respCT, r.respNonce = ct, nonce
	}
	return r
}

// resolveOutboundHeaders expands {{secrets.NAME}} in the endpoint's custom headers.
func (d *Dispatcher) resolveOutboundHeaders(ctx context.Context, cfg store.DispatchConfig) (map[string]string, string) {
	out := make(map[string]string, len(cfg.OutboundHeaders))
	if len(cfg.OutboundHeaders) == 0 {
		return out, ""
	}
	var names []string
	seen := map[string]bool{}
	for _, v := range cfg.OutboundHeaders {
		for _, m := range secretRef.FindAllStringSubmatch(v, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				names = append(names, m[1])
			}
		}
	}
	values := map[string]string{}
	if len(names) > 0 {
		secrets, err := d.store.GetUserSecrets(ctx, cfg.UserID, names)
		if err != nil {
			return nil, ErrCodeSecretNotFound
		}
		for _, n := range names {
			sec, ok := secrets[n]
			if !ok || sec.KeyVersion != d.cipher.KeyVersion() {
				return nil, ErrCodeSecretNotFound
			}
			plain, err := d.cipher.Open(sec.Encrypted, sec.Nonce, store.SecretAAD(cfg.UserID, n))
			if err != nil {
				return nil, ErrCodeSecretNotFound
			}
			values[n] = string(plain)
		}
	}
	for k, v := range cfg.OutboundHeaders {
		v = secretRef.ReplaceAllStringFunc(v, func(ref string) string {
			return values[secretRef.FindStringSubmatch(ref)[1]]
		})
		if strings.ContainsAny(k+v, "\r\n") || k == "" {
			return nil, ErrCodeInvalidHeader
		}
		out[http.CanonicalHeaderKey(k)] = v
	}
	return out, ""
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
