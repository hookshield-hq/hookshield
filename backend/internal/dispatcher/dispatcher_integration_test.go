//go:build integration

package dispatcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/dispatcher"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testplans"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// destination is a programmable customer endpoint.
type destination struct {
	mu        sync.Mutex
	responses []func(w http.ResponseWriter) // consumed in order; the last one repeats
	received  []*http.Request
	bodies    []string
	srv       *httptest.Server
}

func newDestination(t *testing.T, responses ...func(w http.ResponseWriter)) *destination {
	d := &destination{responses: responses}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.received = append(d.received, r)
		d.bodies = append(d.bodies, string(body))
		resp := d.responses[0]
		if len(d.responses) > 1 {
			d.responses = d.responses[1:]
		}
		d.mu.Unlock()
		resp(w)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *destination) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.received) }

func status(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"ok":` + map[bool]string{true: "true", false: "false"}[code < 300] + `}`))
	}
}

type spyNotifier struct {
	mu     sync.Mutex
	alerts []alert.Alert
}

func (s *spyNotifier) Notify(_ context.Context, a alert.Alert) error {
	s.mu.Lock()
	s.alerts = append(s.alerts, a)
	s.mu.Unlock()
	return nil
}

type env struct {
	t        *testing.T
	ctx      context.Context
	rdb      *redis.Client
	keys     redisx.Keys
	cipher   *crypto.Cipher
	store    *store.Store
	seed     seed.Result
	ingest   *httptest.Server
	proc     *processor.Processor
	disp     *dispatcher.Dispatcher
	producer *queue.Producer
	alerts   *spyNotifier
}

func setup(t *testing.T, dest *destination, mutate func(p *store.SetEndpointDeliveryParams)) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	rdb, keys := testredis.New(t)
	cipher, _ := crypto.New(bytes.Repeat([]byte{5}, crypto.KeySize), 1)
	s := store.New(pool)
	cache := endpointcache.New(rdb, keys, s)
	res, err := seed.Run(ctx, s, cache)
	if err != nil {
		t.Fatal(err)
	}

	params := store.SetEndpointDeliveryParams{
		TargetURL: dest.srv.URL + "/hooks?src=hs", MaxRetries: 5, RetryStrategy: "exponential",
		RetryInitialMs: 1000, RetryMultiplier: 2, RetryMaxDelayMs: 3_600_000, DLQEnabled: true, AlertThresholdFailures: 4,
	}
	if mutate != nil {
		mutate(&params)
	}
	if err := s.SetEndpointDelivery(ctx, res.EndpointID, params); err != nil {
		t.Fatal(err)
	}

	producer := queue.NewProducer(rdb, keys)
	h := ingest.New(cache, limits.New(rdb, keys, 0, nil), producer, cipher, ingest.Options{})
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pc := queue.NewConsumer(rdb, keys.IngestStream(), processor.Group, "p1")
	_ = pc.EnsureGroup(ctx)
	proc := processor.New(pc, producer, s, cipher, processor.Options{Block: 20 * time.Millisecond})

	dc := queue.NewConsumer(rdb, keys.DispatchStream(), dispatcher.Group, "d1")
	_ = dc.EnsureGroup(ctx)
	spy := &spyNotifier{}
	disp := dispatcher.New(dc, producer, s, cipher,
		outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 2 * time.Second}), spy,
		dispatcher.Options{Block: 20 * time.Millisecond, JitterPercent: -1, Plans: testplans.Blueprint})

	return &env{t: t, ctx: ctx, rdb: rdb, keys: keys, cipher: cipher, store: s, seed: res, ingest: srv,
		proc: proc, disp: disp, producer: producer, alerts: spy}
}

// send pushes a webhook through ingest + processor and returns its log ID.
func (e *env) send(body string, headers map[string]string) string {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ingest.URL+"/v1/"+e.seed.Slug+"?from=stripe", strings.NewReader(body))
	req.Header.Set(e.seed.HeaderKey, e.seed.Token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		e.t.Fatalf("ingest: %v %v", resp, err)
	}
	resp.Body.Close()
	if n, err := e.proc.ProcessBatch(e.ctx); err != nil || n != 1 {
		e.t.Fatalf("process: %d %v", n, err)
	}
	return resp.Header.Get(ingest.RequestIDHeader)
}

func (e *env) dispatch() int {
	e.t.Helper()
	n, err := e.disp.DispatchBatch(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// releaseRetries moves every scheduled retry to the dispatch stream, as if time had passed.
func (e *env) releaseRetries() int {
	n, err := e.producer.MoveDueRetries(e.ctx, time.Now().Add(48*time.Hour), 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) log(id string) store.PayloadLog {
	l, err := e.store.GetPayloadLog(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *env) attempts(id string) []store.DeliveryAttempt {
	a, err := e.store.ListDeliveryAttempts(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *env) addSecret(name, value string) {
	ct, nonce, _ := e.cipher.Seal([]byte(value), store.SecretAAD(e.seed.UserID, name))
	if err := e.store.UpsertUserSecret(e.ctx, e.seed.UserID, store.UserSecret{Name: name, Encrypted: ct, Nonce: nonce, KeyVersion: 1}); err != nil {
		e.t.Fatal(err)
	}
}

func TestDeliversTransformedRequestWithHeaders(t *testing.T) {
	dest := newDestination(t, status(200))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) {
		p.OutboundHeaders = map[string]string{"Authorization": "Bearer {{secrets.MY_API_KEY}}", "X-Team": "billing"}
	})
	e.addSecret("MY_API_KEY", "sk_live_secret")
	if err := e.store.SetEndpointTransformRules(e.ctx, e.seed.EndpointID,
		[]byte(`{"body":[{"op":"rename","from":"user_id","to":"customerId"}]}`)); err != nil {
		t.Fatal(err)
	}

	id := e.send(`{"type":"invoice.paid","user_id":7}`, map[string]string{"X-HookShield-Id": "spoofed", "Stripe-Signature": "t=1"})
	if n := e.dispatch(); n != 1 {
		t.Fatalf("dispatched %d", n)
	}

	if dest.count() != 1 {
		t.Fatalf("destination received %d requests", dest.count())
	}
	r := dest.received[0]
	if dest.bodies[0] != `{"customerId":7,"type":"invoice.paid"}` {
		t.Fatalf("body %s", dest.bodies[0])
	}
	checks := map[string]string{
		"Authorization":          "Bearer sk_live_secret",
		"X-Team":                 "billing",
		dispatcher.HeaderID:      id,
		dispatcher.HeaderAttempt: "1",
		dispatcher.HeaderEvent:   "invoice.paid",
		"Stripe-Signature":       "t=1",
		"Content-Type":           "application/json",
	}
	for k, want := range checks {
		if got := r.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if r.Header.Get(e.seed.HeaderKey) != "" {
		t.Error("inbound token forwarded to destination")
	}
	if !strings.HasPrefix(r.Header.Get("User-Agent"), "HookShield/") || r.Header.Get(dispatcher.HeaderTimestamp) == "" {
		t.Error("missing User-Agent or timestamp header")
	}
	if r.URL.Path != "/hooks" || r.URL.RawQuery != "src=hs&from=stripe" || r.Method != http.MethodPost {
		t.Errorf("request line: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}

	l := e.log(id)
	if l.Status != store.LogDelivered || l.AttemptCount != 1 || l.PersistMicros == nil {
		t.Fatalf("log %+v", l)
	}
	att := e.attempts(id)
	if len(att) != 1 || att[0].StatusCode == nil || *att[0].StatusCode != 200 || att[0].DurationMicros == nil {
		t.Fatalf("attempts %+v", att)
	}
	plain, err := e.cipher.Open(att[0].ResponseEncrypted, att[0].ResponseNonce, dispatcher.ResponseAAD(id, 1))
	if err != nil || !strings.Contains(string(plain), `"status":200`) {
		t.Fatalf("stored response: %s %v", plain, err)
	}
	if len(e.alerts.alerts) != 0 {
		t.Fatal("alert raised for a successful delivery")
	}
}

func TestRetriesThenDelivers(t *testing.T) {
	dest := newDestination(t, status(503), status(500), status(204))
	e := setup(t, dest, nil)
	id := e.send(`{"a":1}`, nil)

	e.dispatch()
	l := e.log(id)
	if l.Status != store.LogRetrying || l.AttemptCount != 1 {
		t.Fatalf("after 1st failure: %+v", l)
	}
	if n, _ := e.producer.ScheduledCount(e.ctx); n != 1 {
		t.Fatal("retry not scheduled")
	}
	// Not due yet: nothing moves.
	if n, _ := e.producer.MoveDueRetries(e.ctx, time.Now(), 10); n != 0 {
		t.Fatal("retry released before its delay")
	}

	for i := 0; i < 2; i++ {
		if e.releaseRetries() != 1 {
			t.Fatalf("round %d: nothing to release", i)
		}
		e.dispatch()
	}
	l = e.log(id)
	if l.Status != store.LogDelivered || l.AttemptCount != 3 || dest.count() != 3 {
		t.Fatalf("final %+v, destination got %d", l, dest.count())
	}
	if got := dest.received[2].Header.Get(dispatcher.HeaderAttempt); got != "3" {
		t.Fatalf("attempt header %q", got)
	}
	codes := []int32{}
	for _, a := range e.attempts(id) {
		codes = append(codes, *a.StatusCode)
	}
	if len(codes) != 3 || codes[0] != 503 || codes[1] != 500 || codes[2] != 204 {
		t.Fatalf("attempt codes %v", codes)
	}
}

func TestGivesUpIntoDLQAndAlertsOnce(t *testing.T) {
	dest := newDestination(t, status(500))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.MaxRetries = 1 })
	id := e.send(`{}`, nil)

	e.dispatch()
	e.releaseRetries()
	e.dispatch()

	l := e.log(id)
	if l.Status != store.LogDead || l.AttemptCount != 2 || dest.count() != 2 {
		t.Fatalf("log %+v, requests %d", l, dest.count())
	}
	if e.releaseRetries() != 0 {
		t.Fatal("retry scheduled after the last attempt")
	}
	dlq, _ := e.rdb.XRange(e.ctx, e.keys.DeadLetterStream(), "-", "+").Result()
	if len(dlq) != 1 || dlq[0].Values["reason"] != "max_attempts_exceeded" || dlq[0].Values["log_id"] != id {
		t.Fatalf("dead letters %+v", dlq)
	}
	// Threshold is 4 but delivery was abandoned after 2 attempts: one final alert.
	if len(e.alerts.alerts) != 1 || !e.alerts.alerts[0].Final || e.alerts.alerts[0].Attempts != 2 || e.alerts.alerts[0].LastStatus != 500 {
		t.Fatalf("alerts %+v", e.alerts.alerts)
	}
}

func TestFailedWithoutDLQ(t *testing.T) {
	dest := newDestination(t, status(400))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.MaxRetries = 0; p.DLQEnabled = false })
	id := e.send(`{}`, nil)
	e.dispatch()
	if l := e.log(id); l.Status != store.LogFailed {
		t.Fatalf("status %s", l.Status)
	}
	if n, _ := e.rdb.XLen(e.ctx, e.keys.DeadLetterStream()).Result(); n != 0 {
		t.Fatal("DLQ disabled but entry written")
	}
}

func TestAlertAtThresholdOnlyOnce(t *testing.T) {
	dest := newDestination(t, status(502))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.MaxRetries = 5; p.AlertThresholdFailures = 2 })
	e.send(`{}`, nil)
	e.dispatch()
	if len(e.alerts.alerts) != 0 {
		t.Fatal("alert before threshold")
	}
	for i := 0; i < 3; i++ {
		e.releaseRetries()
		e.dispatch()
	}
	if len(e.alerts.alerts) != 1 || e.alerts.alerts[0].Final || e.alerts.alerts[0].Attempts != 2 {
		t.Fatalf("alerts %+v", e.alerts.alerts)
	}
}

func TestPlanCapsRetries(t *testing.T) {
	dest := newDestination(t, status(500))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.MaxRetries = 10 })
	if _, err := e.store.Pool().Exec(e.ctx, `UPDATE users SET plan_type = 'developer' WHERE id = $1`, e.seed.UserID); err != nil {
		t.Fatal(err)
	}
	id := e.send(`{}`, nil)
	e.dispatch()
	for e.releaseRetries() > 0 {
		e.dispatch()
	}
	if l := e.log(id); l.AttemptCount != 4 || l.Status != store.LogDead {
		t.Fatalf("developer plan allows 3 retries (4 attempts): %+v", l)
	}
}

func TestSelfHostedDoesNotCapRetries(t *testing.T) {
	dest := newDestination(t, status(500))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.MaxRetries = 6 })
	if _, err := e.store.Pool().Exec(e.ctx, `UPDATE users SET plan_type = 'developer' WHERE id = $1`, e.seed.UserID); err != nil {
		t.Fatal(err)
	}
	e.disp = dispatcher.New(queue.NewConsumer(e.rdb, e.keys.DispatchStream(), dispatcher.Group, "self-hosted"),
		e.producer, e.store, e.cipher, outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 2 * time.Second}), nil,
		dispatcher.Options{Block: 20 * time.Millisecond, JitterPercent: -1}) // nil Plans = self-hosted
	id := e.send(`{}`, nil)
	e.dispatch()
	for e.releaseRetries() > 0 {
		e.dispatch()
	}
	if l := e.log(id); l.AttemptCount != 7 || l.Status != store.LogDead {
		t.Fatalf("self-hosted keeps the endpoint's 6 retries (7 attempts) even on a 'developer' account: %+v", l)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	dest := newDestination(t, func(w http.ResponseWriter) { w.Header().Set("Retry-After", "120"); w.WriteHeader(429) })
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.RetryInitialMs = 10_000 })
	id := e.send(`{}`, nil)
	before := time.Now()
	e.dispatch()

	var next time.Time
	if err := e.store.Pool().QueryRow(e.ctx, `SELECT next_attempt_at FROM payload_logs WHERE id = $1`, id).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if d := next.Sub(before); d < 119*time.Second || d > 125*time.Second {
		t.Fatalf("next attempt in %v, want ~120s (Retry-After)", d)
	}
}

func TestPausedEndpointHoldsWithoutConsumingAttempts(t *testing.T) {
	dest := newDestination(t, status(200))
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	if err := e.store.SetEndpointStatus(e.ctx, e.seed.EndpointID, store.StatusPaused); err != nil {
		t.Fatal(err)
	}
	e.dispatch()
	if dest.count() != 0 || len(e.attempts(id)) != 0 || e.log(id).Status != store.LogQueued {
		t.Fatal("paused endpoint must not be called nor consume attempts")
	}
	if err := e.store.SetEndpointStatus(e.ctx, e.seed.EndpointID, store.StatusActive); err != nil {
		t.Fatal(err)
	}
	e.releaseRetries()
	e.dispatch()
	if l := e.log(id); l.Status != store.LogDelivered || l.AttemptCount != 1 {
		t.Fatalf("after resume: %+v", l)
	}
}

func TestDuplicateDispatchDeliversOnce(t *testing.T) {
	dest := newDestination(t, status(200))
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	// A second hand-over of the same attempt (e.g. processor redelivery).
	e.producer.PublishDispatch(e.ctx, queue.DispatchMessage{LogID: id, EndpointID: e.seed.EndpointID, Attempt: 1})
	e.dispatch()
	e.dispatch()
	if dest.count() != 1 || len(e.attempts(id)) != 1 {
		t.Fatalf("delivered %d times", dest.count())
	}
}

// review: two copies of the same attempt handled at the same time (one batch,
// concurrent handlers) must still call the destination once.
func TestConcurrentDuplicateDispatchDeliversOnce(t *testing.T) {
	dest := newDestination(t, func(w http.ResponseWriter) { time.Sleep(300 * time.Millisecond); w.WriteHeader(200) })
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	e.producer.PublishDispatch(e.ctx, queue.DispatchMessage{LogID: id, EndpointID: e.seed.EndpointID, Attempt: 1})
	e.dispatch() // both copies in one batch, delivered concurrently
	for e.releaseRetries() > 0 {
		e.dispatch()
	}
	if dest.count() != 1 || len(e.attempts(id)) != 1 {
		t.Fatalf("delivered %d times, %d attempts", dest.count(), len(e.attempts(id)))
	}
}

func TestMissingSecretFailsAttemptWithoutCallingDestination(t *testing.T) {
	dest := newDestination(t, status(200))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) {
		p.OutboundHeaders = map[string]string{"Authorization": "Bearer {{secrets.NOPE}}"}
	})
	id := e.send(`{}`, nil)
	e.dispatch()
	att := e.attempts(id)
	if dest.count() != 0 || len(att) != 1 || att[0].ErrorCode == nil || *att[0].ErrorCode != dispatcher.ErrCodeSecretNotFound {
		t.Fatalf("attempts %+v, requests %d", att, dest.count())
	}
	if e.log(id).Status != store.LogRetrying {
		t.Fatal("missing secret should be retried (the user may add it)")
	}
	// Adding the secret fixes the next attempt.
	e.addSecret("NOPE", "now-it-exists")
	e.releaseRetries()
	e.dispatch()
	if dest.count() != 1 || dest.received[0].Header.Get("Authorization") != "Bearer now-it-exists" {
		t.Fatal("secret not applied on retry")
	}
}

func TestSweeperRecoversLostRetry(t *testing.T) {
	dest := newDestination(t, status(500), status(200))
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	e.dispatch()

	// Lose the scheduled retry and make it overdue.
	e.rdb.Del(e.ctx, e.keys.RetrySchedule())
	e.store.Pool().Exec(e.ctx, `UPDATE payload_logs SET next_attempt_at = now() - interval '10 minutes' WHERE id = $1`, id)

	n, err := e.disp.SweepOverdue(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, err %v", n, err)
	}
	e.releaseRetries()
	e.dispatch()
	if l := e.log(id); l.Status != store.LogDelivered || l.AttemptCount != 2 {
		t.Fatalf("not recovered: %+v", l)
	}
	if n, _ := e.disp.SweepOverdue(e.ctx); n != 0 {
		t.Fatal("delivered log swept again")
	}
}

func TestNetworkErrorsAreRecorded(t *testing.T) {
	dest := newDestination(t, func(w http.ResponseWriter) { time.Sleep(3 * time.Second) })
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	e.dispatch() // client timeout is 2s
	att := e.attempts(id)
	if len(att) != 1 || att[0].ErrorCode == nil || *att[0].ErrorCode != "timeout" || att[0].StatusCode != nil {
		t.Fatalf("attempts %+v", att)
	}
}

func TestSSRFBlockedInProductionMode(t *testing.T) {
	dest := newDestination(t, status(200))
	e := setup(t, dest, nil)
	strict := dispatcher.New(
		queue.NewConsumer(e.rdb, e.keys.DispatchStream(), dispatcher.Group, "strict"),
		e.producer, e.store, e.cipher, outbound.NewClient(outbound.Options{}), nil,
		dispatcher.Options{Block: 20 * time.Millisecond})
	id := e.send(`{}`, nil)
	if _, err := strict.DispatchBatch(e.ctx); err != nil {
		t.Fatal(err)
	}
	att := e.attempts(id)
	if dest.count() != 0 || len(att) != 1 || *att[0].ErrorCode != "blocked_destination" {
		t.Fatalf("loopback destination reached: %+v", att)
	}
}

func TestStoredResponseIsCapped(t *testing.T) {
	big := strings.Repeat("x", dispatcher.MaxResponseBytes*2)
	dest := newDestination(t, func(w http.ResponseWriter) { w.WriteHeader(200); _, _ = w.Write([]byte(big)) })
	e := setup(t, dest, nil)
	id := e.send(`{}`, nil)
	e.dispatch()
	att := e.attempts(id)
	plain, err := e.cipher.Open(att[0].ResponseEncrypted, att[0].ResponseNonce, dispatcher.ResponseAAD(id, 1))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct{ Body []byte }
	_ = json.Unmarshal(plain, &stored)
	if len(stored.Body) != dispatcher.MaxResponseBytes {
		t.Fatalf("stored %d bytes, want %d", len(stored.Body), dispatcher.MaxResponseBytes)
	}
}

// TestFailingDeliveryAlertReachesChannel runs the real alert path:
// dispatcher → alert.Service (stream) → alert.Sender → webhook channel.
func TestFailingDeliveryAlertReachesChannel(t *testing.T) {
	dest := newDestination(t, status(500))
	e := setup(t, dest, func(p *store.SetEndpointDeliveryParams) { p.AlertThresholdFailures = 1 })

	var mu sync.Mutex
	var got []alert.Payload
	channel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p alert.Payload
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	}))
	defer channel.Close()

	id := uuid.NewV7()
	ct, nonce, _ := alert.EncryptTarget(e.cipher, id, alert.WebhookTarget{URL: channel.URL})
	if err := e.store.CreateAlertChannel(e.ctx, e.seed.UserID, store.AlertChannel{
		ID: id, Type: store.ChannelWebhook, TargetEncrypted: ct, TargetNonce: nonce, KeyVersion: 1, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	client := outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 2 * time.Second})
	disp := dispatcher.New(queue.NewConsumer(e.rdb, e.keys.DispatchStream(), dispatcher.Group, "d-alerts"),
		e.producer, e.store, e.cipher, client, alert.NewService(e.rdb, e.keys, time.Hour),
		dispatcher.Options{Block: 20 * time.Millisecond, JitterPercent: -1})
	ac := queue.NewConsumer(e.rdb, e.keys.AlertStream(), alert.Group, "a1")
	_ = ac.EnsureGroup(e.ctx)
	sender := alert.NewSender(ac, e.store, e.cipher, client, alert.SenderOptions{Block: 20 * time.Millisecond, AppURL: "http://app"})

	logID := e.send(`{"type":"order.created"}`, nil)
	if _, err := disp.DispatchBatch(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := sender.SendBatch(e.ctx); err != nil || n != 1 {
		t.Fatalf("sender handled %d, err %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].WebhookID != logID || got[0].EventType != "order.created" || got[0].LastStatus != 500 ||
		got[0].Final || got[0].URL != "http://app/logs/"+logID {
		t.Fatalf("alert payload %+v", got)
	}
}

// sendTo pushes a webhook to another endpoint slug (same token as the seed).
func (e *env) sendTo(slug, body string) string {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ingest.URL+"/v1/"+slug, strings.NewReader(body))
	req.Header.Set(e.seed.HeaderKey, e.seed.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		e.t.Fatalf("ingest: %v %v", resp, err)
	}
	resp.Body.Close()
	if n, err := e.proc.ProcessBatch(e.ctx); err != nil || n != 1 {
		e.t.Fatalf("process: %d %v", n, err)
	}
	return resp.Header.Get(ingest.RequestIDHeader)
}

func (e *env) dispatcher(name string, opts dispatcher.Options) *dispatcher.Dispatcher {
	opts.Block, opts.JitterPercent, opts.Plans = 20*time.Millisecond, -1, testplans.Blueprint
	return dispatcher.New(queue.NewConsumer(e.rdb, e.keys.DispatchStream(), dispatcher.Group, name), e.producer, e.store, e.cipher,
		outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 5 * time.Second}), nil, opts)
}

// Regression (review): a delivery slower than ClaimMinIdle (production: a
// batch of 16 x 15 s timeouts vs 2 min) was reclaimed by another dispatcher
// and delivered twice. In-flight messages are now touched, so they are never
// reclaimed while being worked on. Timings scaled down.
func TestSlowDestinationIsNeverDeliveredTwice(t *testing.T) {
	dest := newDestination(t, func(w http.ResponseWriter) { time.Sleep(900 * time.Millisecond); w.WriteHeader(200) })
	e := setup(t, dest, nil)
	ids := []string{e.send(`{"n":1}`, nil), e.send(`{"n":2}`, nil)}
	opts := dispatcher.Options{ClaimMinIdle: 300 * time.Millisecond}
	a, b := e.dispatcher("a", opts), e.dispatcher("b", opts)

	done := make(chan int, 1)
	go func() { n, _ := a.DispatchBatch(e.ctx); done <- n }()
	time.Sleep(600 * time.Millisecond) // well past ClaimMinIdle, deliveries still running
	if n, err := b.ReclaimStuck(e.ctx); err != nil || n != 0 {
		t.Fatalf("another dispatcher reclaimed %d in-flight messages (err %v)", n, err)
	}
	if n := <-done; n != 2 {
		t.Fatalf("acknowledged %d", n)
	}
	if dest.count() != 2 {
		t.Fatalf("2 webhooks, destination received %d requests", dest.count())
	}
	for _, id := range ids {
		if l := e.log(id); l.Status != store.LogDelivered || l.AttemptCount != 1 {
			t.Fatalf("log %s: %s after %d attempts", id, l.Status, l.AttemptCount)
		}
	}
}

// A black-hole destination holds at most MaxPerEndpoint delivery slots: the
// other endpoints keep being delivered, and the deferred messages of the slow
// endpoint are delivered later without consuming an attempt.
func TestSlowEndpointDoesNotBlockOthers(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	slow := newDestination(t, func(w http.ResponseWriter) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		time.Sleep(time.Second)
		mu.Lock()
		inFlight--
		mu.Unlock()
		w.WriteHeader(200)
	})
	fast := newDestination(t, status(200))
	e := setup(t, slow, nil)
	other, err := e.store.CreateEndpoint(e.ctx, store.CreateEndpointParams{
		UserID: e.seed.UserID, Name: "fast", Slug: "fast-endpoint", TargetURL: fast.srv.URL,
		AuthTokenHash: token.Hash(e.seed.Token), AuthTokenPrefix: token.DisplayPrefix(e.seed.Token),
	})
	if err != nil {
		t.Fatal(err)
	}

	var slowIDs []string
	for i := 0; i < 6; i++ {
		slowIDs = append(slowIDs, e.send(`{"slow":true}`, nil))
	}
	fastIDs := []string{e.sendTo(other.Slug, `{"fast":1}`), e.sendTo(other.Slug, `{"fast":2}`)}

	d := e.dispatcher("d", dispatcher.Options{Concurrency: 8, MaxPerEndpoint: 2, DeferDelay: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(e.ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = d.Run(ctx) }()
	go func() { defer wg.Done(); d.RunScheduler(ctx, 20*time.Millisecond, time.Hour) }()
	defer func() { cancel(); wg.Wait() }()

	// The fast endpoint is served while the slow one is still busy.
	deadline := time.Now().Add(700 * time.Millisecond)
	for _, id := range fastIDs {
		for e.log(id).Status != store.LogDelivered {
			if time.Now().After(deadline) {
				t.Fatalf("fast endpoint blocked by the slow one: %d/2 received", fast.count())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Every slow webhook is eventually delivered, once, at its first attempt.
	deadline = time.Now().Add(10 * time.Second)
	for _, id := range slowIDs {
		for e.log(id).Status != store.LogDelivered {
			if time.Now().After(deadline) {
				t.Fatalf("slow log %s not delivered", id)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if l := e.log(id); l.AttemptCount != 1 {
			t.Fatalf("deferral consumed attempts: %d", l.AttemptCount)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Fatalf("%d concurrent deliveries to one endpoint, cap is 2", peak)
	}
	if slow.count() != 6 {
		t.Fatalf("slow destination received %d requests for 6 webhooks", slow.count())
	}
}
