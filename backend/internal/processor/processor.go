// Package processor is the first stage of the worker: it consumes accepted
// webhooks from the ingest stream, applies the endpoint's transform rules,
// stores the original and transformed requests encrypted in PostgreSQL and
// hands the log over to the dispatch stream.
//
// Guarantees:
//   - at-least-once: a message is acknowledged only after it is stored and
//     handed to the dispatcher; crashes are recovered by XAUTOCLAIM;
//   - idempotent: payload_logs.id is the request ID, so redeliveries never
//     create duplicates;
//   - poison messages (malformed, undecryptable, crashing the worker) go to the
//     dead-letter stream with their original (still encrypted) fields, so they
//     can be replayed, instead of blocking the queue;
//   - transient errors (database or Redis down) are retried for as long as the
//     outage lasts: they never count toward the poison limit, so an accepted
//     webhook is never dropped because a dependency was unavailable.
package processor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/eventtype"
	"github.com/hookshield-hq/hookshield/backend/internal/live"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/transform"
)

// Group is the consumer group name on the ingest stream.
const Group = "processor"

// AAD helpers bind stored ciphertexts to their row and field.
func OriginalAAD(logID string) []byte    { return []byte("payload:" + logID + ":original") }
func TransformedAAD(logID string) []byte { return []byte("payload:" + logID + ":transformed") }

// Store is the persistence the processor needs (implemented by *store.Store).
type Store interface {
	GetProcessConfig(ctx context.Context, endpointID string) (store.ProcessConfig, error)
	InsertPayloadLog(ctx context.Context, p store.NewPayloadLog) (bool, error)
}

// Publisher hands logs to the dispatcher and records poison messages.
type Publisher interface {
	PublishDispatch(ctx context.Context, m queue.DispatchMessage) (string, error)
	DeadLetter(ctx context.Context, source, entryID, reason string, fields map[string]any) error
}

// Source is the ingest stream consumer.
type Source interface {
	EnsureGroup(ctx context.Context) error
	Read(ctx context.Context, count int64, block time.Duration) ([]redis.XMessage, error)
	Claim(ctx context.Context, minIdle time.Duration, count int64) ([]redis.XMessage, error)
	Deliveries(ctx context.Context, id string) (int64, error)
	Postpone(ctx context.Context, ids ...string) error
	Done(ctx context.Context, ids ...string) error
}

// Options tune the processor loop.
type Options struct {
	BatchSize     int64         // messages per read (default 64)
	Block         time.Duration // max wait for new messages (default 2s)
	ClaimMinIdle  time.Duration // a pending message older than this is reclaimed (default 60s)
	ClaimInterval time.Duration // how often to look for stuck messages (default 30s)
	Logger        *slog.Logger
	Now           func() time.Time
	// Plans sets payload retention per account (nil = self-hosted, core.DefaultRetention).
	Plans core.Plans
	// Events receives a log.created event per stored webhook (nil = none).
	Events *live.Publisher
}

// Processor handles ingest messages.
type Processor struct {
	src    Source
	pub    Publisher
	store  Store
	cipher *crypto.Cipher
	opts   Options
}

// New builds a Processor.
func New(src Source, pub Publisher, st Store, cipher *crypto.Cipher, opts Options) *Processor {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}
	if opts.Block <= 0 {
		opts.Block = 2 * time.Second
	}
	if opts.ClaimMinIdle <= 0 {
		opts.ClaimMinIdle = 60 * time.Second
	}
	if opts.ClaimInterval <= 0 {
		opts.ClaimInterval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.Plans = core.OrSelfHosted(opts.Plans, 0)
	return &Processor{src: src, pub: pub, store: st, cipher: cipher, opts: opts}
}

// Run processes messages until ctx is cancelled.
func (p *Processor) Run(ctx context.Context) error {
	if err := p.src.EnsureGroup(ctx); err != nil {
		return err
	}
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) >= p.opts.ClaimInterval {
			lastClaim = time.Now()
			if _, err := p.ReclaimStuck(ctx); err != nil && ctx.Err() == nil {
				p.opts.Logger.Warn("reclaim failed", "err", err)
			}
		}
		if _, err := p.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
			p.opts.Logger.Warn("process batch failed", "err", err)
			sleep(ctx, time.Second)
		}
	}
	return nil
}

// ProcessBatch reads and handles one batch of new messages. It returns how
// many messages were acknowledged.
func (p *Processor) ProcessBatch(ctx context.Context) (int, error) {
	msgs, err := p.src.Read(ctx, p.opts.BatchSize, p.opts.Block)
	if err != nil {
		return 0, err
	}
	return p.handleAll(ctx, msgs)
}

// ReclaimStuck re-processes messages left unacknowledged by a crashed consumer.
func (p *Processor) ReclaimStuck(ctx context.Context) (int, error) {
	msgs, err := p.src.Claim(ctx, p.opts.ClaimMinIdle, p.opts.BatchSize)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	p.opts.Logger.Info("reclaimed stuck messages", "count", len(msgs))
	// A message that keeps killing its consumer (crash, out of memory) must not
	// take down every worker in turn: after MaxDeliveries it is dead-lettered.
	// Transient failures reset the counter (see handleAll), so only deliveries
	// that never returned count here.
	var poison []string
	keep := msgs[:0]
	for _, m := range msgs {
		if n, err := p.src.Deliveries(ctx, m.ID); err == nil && n > queue.MaxDeliveries {
			if p.deadLetter(ctx, m, "too_many_deliveries", p.opts.Logger.With("entry", m.ID, "deliveries", n)) {
				poison = append(poison, m.ID)
			}
			continue
		}
		keep = append(keep, m)
	}
	if err := p.src.Done(ctx, poison...); err != nil {
		return 0, err
	}
	n, err := p.handleAll(ctx, keep)
	return n + len(poison), err
}

func (p *Processor) handleAll(ctx context.Context, msgs []redis.XMessage) (int, error) {
	var done, retry []string
	for _, m := range msgs {
		if p.handle(ctx, m) {
			done = append(done, m.ID)
		} else {
			retry = append(retry, m.ID)
		}
	}
	// A transient failure is not a sign of poison: reset the delivery counter
	// so a long outage never dead-letters (and deletes) accepted webhooks. If
	// this fails too (Redis down), the reclaim that would count it fails as well.
	if err := p.src.Postpone(ctx, retry...); err != nil && ctx.Err() == nil {
		p.opts.Logger.Warn("postpone failed messages failed", "err", err, "count", len(retry))
	}
	if err := p.src.Done(ctx, done...); err != nil {
		return 0, err
	}
	return len(done), nil
}

// handle processes one entry and reports whether it can be acknowledged.
// false means "transient failure, retry later".
func (p *Processor) handle(ctx context.Context, entry redis.XMessage) bool {
	log := p.opts.Logger.With("entry", entry.ID)

	msg, err := queue.ParseIngestMessage(entry.Values)
	if err != nil {
		return p.deadLetter(ctx, entry, "malformed", log)
	}
	log = log.With("id", msg.ID, "endpoint_id", msg.EndpointID)

	cfg, err := p.store.GetProcessConfig(ctx, msg.EndpointID)
	if errors.Is(err, store.ErrNotFound) {
		log.Info("endpoint deleted before processing; dropping message")
		return true
	}
	if err != nil {
		log.Warn("load endpoint config failed; will retry", "err", err)
		return false
	}

	start := p.opts.Now()

	if msg.KeyVersion != p.cipher.KeyVersion() {
		return p.deadLetter(ctx, entry, "unknown_key_version", log)
	}
	plain, err := p.cipher.Open(msg.Ciphertext, msg.Nonce, queue.IngestAAD(msg.ID))
	if err != nil {
		return p.deadLetter(ctx, entry, "decrypt_failed", log)
	}
	env, err := queue.UnmarshalEnvelope(plain)
	if err != nil {
		return p.deadLetter(ctx, entry, "envelope_invalid", log)
	}

	rules, err := transform.Parse(cfg.TransformRules)
	if err != nil {
		// The API validates rules before saving; reaching this means bad data in
		// the DB (or rules saved before a validation rule existed). Fail closed:
		// forwarding the body unchanged could leak fields the rules remove.
		log.Error("stored transform rules are invalid", "err", err)
		return p.deadLetter(ctx, entry, "transform_invalid", log)
	}

	origCT, origNonce, err := p.cipher.Seal(plain, OriginalAAD(msg.ID))
	if err != nil {
		log.Error("encrypt original failed", "err", err)
		return false
	}

	var trCT, trNonce []byte
	if !rules.Empty() {
		res := rules.Apply(transform.Message{Headers: env.Headers, Body: env.Body}, p.opts.Now())
		if res.Err != nil {
			// Never deliver the untransformed body as if the rules had run
			// (they may remove sensitive fields).
			return p.deadLetter(ctx, entry, "transform_too_large", log)
		}
		for _, w := range res.Warnings {
			log.Debug("transform warning", "warning", w)
		}
		tenv := queue.Envelope{Method: env.Method, Query: env.Query, Headers: res.Message.Headers, Body: res.Message.Body}
		tplain, err := tenv.Marshal()
		if err != nil {
			log.Error("encode transformed envelope failed", "err", err)
			return false
		}
		if trCT, trNonce, err = p.cipher.Seal(tplain, TransformedAAD(msg.ID)); err != nil {
			log.Error("encrypt transformed failed", "err", err)
			return false
		}
	}
	transformMicros := p.opts.Now().Sub(start).Microseconds()

	eventType := eventtype.Detect(env.Headers, env.Body)
	persistStart := p.opts.Now()
	inserted, err := p.store.InsertPayloadLog(ctx, store.NewPayloadLog{
		ID:                   msg.ID,
		EndpointID:           msg.EndpointID,
		HTTPMethod:           msg.Method,
		EventType:            eventType,
		ContentType:          msg.ContentType,
		SizeBytes:            msg.SizeBytes,
		SourceIP:             msg.SourceIP,
		KeyVersion:           p.cipher.KeyVersion(),
		OriginalEncrypted:    origCT,
		OriginalNonce:        origNonce,
		TransformedEncrypted: trCT,
		TransformedNonce:     trNonce,
		IngestMicros:         msg.IngestMicros,
		TransformMicros:      transformMicros,
		ReceivedAt:           msg.ReceivedAt,
		ExpiresAt:            msg.ReceivedAt.Add(p.opts.Plans.For(cfg.PlanType).Retention),
		ReplayOf:             msg.ReplayOf,
	})
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		log.Warn("store payload log failed; will retry", "err", err)
		return false
	}
	persistMicros := p.opts.Now().Sub(persistStart).Microseconds()
	if !inserted {
		log.Info("payload log already stored (redelivery)")
	} else {
		p.opts.Events.Publish(ctx, cfg.UserID, live.Event{
			Type: live.LogCreated, LogID: msg.ID, EndpointID: msg.EndpointID, Status: store.LogQueued,
			Method: msg.Method, EventType: eventType, ReplayOf: msg.ReplayOf, At: msg.ReceivedAt,
			LatencyMs: float64(msg.IngestMicros+transformMicros+persistMicros) / 1000,
		})
	}

	// Hand over to the dispatcher even on redelivery: the dispatcher only acts
	// on logs still waiting for delivery, so a duplicate hand-over is harmless,
	// while skipping it could leave a stored log undelivered.
	if _, err := p.pub.PublishDispatch(ctx, queue.DispatchMessage{
		LogID: msg.ID, EndpointID: msg.EndpointID, Attempt: 1, PersistMicros: persistMicros,
	}); err != nil {
		log.Warn("publish dispatch failed; will retry", "err", err)
		return false
	}
	return true
}

func (p *Processor) deadLetter(ctx context.Context, entry redis.XMessage, reason string, log *slog.Logger) bool {
	log.Error("poison message moved to dead-letter stream", "reason", reason)
	if err := p.pub.DeadLetter(ctx, "ingest", entry.ID, reason, entry.Values); err != nil {
		log.Warn("dead-letter write failed; will retry", "err", err)
		return false
	}
	return true
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
