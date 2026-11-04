//go:build integration

package processor_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testplans"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

// flakyStore wraps the real store and can simulate a database outage.
type flakyStore struct {
	*store.Store
	down atomic.Bool
}

func (f *flakyStore) GetProcessConfig(ctx context.Context, id string) (store.ProcessConfig, error) {
	if f.down.Load() {
		return store.ProcessConfig{}, errors.New("connection refused")
	}
	return f.Store.GetProcessConfig(ctx, id)
}

type env struct {
	t        *testing.T
	rdb      *redis.Client
	keys     redisx.Keys
	cipher   *crypto.Cipher
	store    *flakyStore
	seed     seed.Result
	ingest   *httptest.Server
	consumer *queue.Consumer
	proc     *processor.Processor
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	rdb, keys := testredis.New(t)
	cipher, _ := crypto.New(bytes.Repeat([]byte{3}, crypto.KeySize), 1)
	s := &flakyStore{Store: store.New(pool)}
	cache := endpointcache.New(rdb, keys, s.Store)
	res, err := seed.Run(ctx, s.Store, cache)
	if err != nil {
		t.Fatal(err)
	}

	h := ingest.New(cache, limits.New(rdb, keys, 0, nil), queue.NewProducer(rdb, keys), cipher, ingest.Options{})
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	consumer := queue.NewConsumer(rdb, keys.IngestStream(), processor.Group, "test-1")
	proc := processor.New(consumer, queue.NewProducer(rdb, keys), s, cipher, processor.Options{
		Block: 50 * time.Millisecond, ClaimMinIdle: time.Millisecond, Plans: testplans.Blueprint,
	})
	if err := consumer.EnsureGroup(ctx); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, rdb: rdb, keys: keys, cipher: cipher, store: s, seed: res, ingest: srv, consumer: consumer, proc: proc}
}

func (e *env) send(body string, headers map[string]string) string {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ingest.URL+"/v1/"+e.seed.Slug, strings.NewReader(body))
	req.Header.Set(e.seed.HeaderKey, e.seed.Token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		e.t.Fatalf("ingest: %v %v", resp, err)
	}
	resp.Body.Close()
	return resp.Header.Get(ingest.RequestIDHeader)
}

func (e *env) open(ct, nonce, aad []byte) queue.Envelope {
	e.t.Helper()
	plain, err := e.cipher.Open(ct, nonce, aad)
	if err != nil {
		e.t.Fatalf("decrypt: %v", err)
	}
	env, err := queue.UnmarshalEnvelope(plain)
	if err != nil {
		e.t.Fatal(err)
	}
	return env
}

func (e *env) streamLen(key string) int64 {
	n, _ := e.rdb.XLen(context.Background(), key).Result()
	return n
}

func (e *env) pending() int64 {
	p, err := e.rdb.XPending(context.Background(), e.keys.IngestStream(), processor.Group).Result()
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Count
}

func TestProcessStoresEncryptedOriginalAndTransformed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	rules := `{"version":1,
		"body":[{"op":"rename","from":"user_id","to":"customerId"},{"op":"remove","path":"card"},{"op":"set","path":"source","value":"stripe"}],
		"headers":[{"op":"remove","name":"Stripe-Signature"},{"op":"set","name":"X-Source","value":"hookshield"}]}`
	if err := e.store.SetEndpointTransformRules(ctx, e.seed.EndpointID, []byte(rules)); err != nil {
		t.Fatal(err)
	}

	id := e.send(`{"type":"invoice.paid","user_id":42,"card":"4242"}`, map[string]string{
		"Content-Type": "application/json", "Stripe-Signature": "t=1,v1=abc",
	})

	n, err := e.proc.ProcessBatch(ctx)
	if err != nil || n != 1 {
		t.Fatalf("processed %d, err %v", n, err)
	}

	l, err := e.store.GetPayloadLog(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != "queued" || l.EventType == nil || *l.EventType != "invoice.paid" || l.ContentType == nil || *l.ContentType != "application/json" ||
		l.SourceIP == nil || *l.SourceIP != "127.0.0.1" || l.IngestMicros == nil || l.TransformMicros == nil || l.ProcessedAt == nil {
		t.Fatalf("unexpected log metadata: %+v", l)
	}
	if bytes.Contains(l.OriginalEncrypted, []byte("4242")) || bytes.Contains(l.TransformedEncrypted, []byte("customerId")) {
		t.Fatal("payload stored in plain text")
	}
	// Seed user is on the Pro plan: 30-day retention.
	if got := l.ExpiresAt.Sub(l.ReceivedAt); got != 30*24*time.Hour {
		t.Fatalf("retention %v, want 30 days", got)
	}

	orig := e.open(l.OriginalEncrypted, l.OriginalNonce, processor.OriginalAAD(id))
	if string(orig.Body) != `{"type":"invoice.paid","user_id":42,"card":"4242"}` || orig.Headers["Stripe-Signature"][0] != "t=1,v1=abc" {
		t.Fatalf("original changed: %+v", orig)
	}
	if _, ok := orig.Headers["X-Hookshield-Token"]; ok {
		t.Fatal("inbound token stored")
	}

	tr := e.open(l.TransformedEncrypted, l.TransformedNonce, processor.TransformedAAD(id))
	if string(tr.Body) != `{"customerId":42,"source":"stripe","type":"invoice.paid"}` {
		t.Fatalf("transformed body %s", tr.Body)
	}
	if _, ok := tr.Headers["Stripe-Signature"]; ok || tr.Headers["X-Source"][0] != "hookshield" || tr.Method != "POST" {
		t.Fatalf("transformed headers %+v", tr.Headers)
	}

	// Ciphertexts are bound to their field: swapping them fails.
	if _, err := e.cipher.Open(l.OriginalEncrypted, l.OriginalNonce, processor.TransformedAAD(id)); err == nil {
		t.Fatal("original decrypted with the transformed AAD")
	}

	// Handed to the dispatcher, and removed from the ingest stream.
	entries, _ := e.rdb.XRange(ctx, e.keys.DispatchStream(), "-", "+").Result()
	if len(entries) != 1 {
		t.Fatalf("dispatch entries %d", len(entries))
	}
	dm, err := queue.ParseDispatchMessage(entries[0].Values)
	if err != nil || dm.LogID != id || dm.Attempt != 1 || dm.EndpointID != e.seed.EndpointID {
		t.Fatalf("dispatch message %+v %v", dm, err)
	}
	if e.streamLen(e.keys.IngestStream()) != 0 || e.pending() != 0 {
		t.Fatal("ingest entry not acknowledged and deleted")
	}
}

func TestNoRulesStoresOnlyOriginal(t *testing.T) {
	e := setup(t)
	id := e.send(`plain text body`, map[string]string{"Content-Type": "text/plain", "X-GitHub-Event": "push"})
	if n, _ := e.proc.ProcessBatch(context.Background()); n != 1 {
		t.Fatalf("processed %d", n)
	}
	l, _ := e.store.GetPayloadLog(context.Background(), id)
	if l.TransformedEncrypted != nil || l.TransformedNonce != nil {
		t.Fatal("transformed copy stored although there are no rules")
	}
	if l.EventType == nil || *l.EventType != "push" {
		t.Fatalf("event type %v", l.EventType)
	}
}

func TestRedeliveryIsIdempotent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.send(`{"a":1}`, nil)

	// Copy the entry before it is processed, then process it.
	entries, _ := e.rdb.XRange(ctx, e.keys.IngestStream(), "-", "+").Result()
	if _, err := e.proc.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a redelivery of the same request (e.g. crash between persist and ack).
	e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: e.keys.IngestStream(), Values: entries[0].Values})
	if n, err := e.proc.ProcessBatch(ctx); err != nil || n != 1 {
		t.Fatalf("redelivery: %d %v", n, err)
	}

	var rows int
	if err := e.store.Pool().QueryRow(ctx, `SELECT count(*) FROM payload_logs WHERE id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows for one request", rows)
	}
}

func TestPoisonMessagesGoToDeadLetter(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// 1) malformed entry
	e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: e.keys.IngestStream(), Values: map[string]any{"garbage": "1"}})
	// 2) tampered ciphertext
	e.send(`{"a":1}`, nil)
	entries, _ := e.rdb.XRange(ctx, e.keys.IngestStream(), "-", "+").Result()
	last := entries[len(entries)-1]
	vals := map[string]any{}
	for k, v := range last.Values {
		vals[k] = v
	}
	ct := []byte(vals["ciphertext"].(string))
	ct[0] ^= 0xff
	vals["ciphertext"] = ct
	e.rdb.XDel(ctx, e.keys.IngestStream(), last.ID)
	e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: e.keys.IngestStream(), Values: vals})

	if n, err := e.proc.ProcessBatch(ctx); err != nil || n != 2 {
		t.Fatalf("processed %d, err %v", n, err)
	}
	dlq, _ := e.rdb.XRange(ctx, e.keys.DeadLetterStream(), "-", "+").Result()
	if len(dlq) != 2 || dlq[0].Values["reason"] != "malformed" || dlq[1].Values["reason"] != "decrypt_failed" {
		t.Fatalf("dead letters: %+v", dlq)
	}
	// The original fields are kept for replay; the payload stays sealed.
	if dlq[0].Values["garbage"] != "1" || dlq[1].Values["ciphertext"] != string(ct) {
		t.Fatalf("dead letters must keep the original fields: %+v", dlq)
	}
	for _, d := range dlq {
		for _, v := range d.Values {
			if strings.Contains(fmt.Sprint(v), `{"a":1}`) {
				t.Fatal("plaintext payload in the dead-letter stream")
			}
		}
	}
	if e.streamLen(e.keys.IngestStream()) != 0 || e.streamLen(e.keys.DispatchStream()) != 0 {
		t.Fatal("poison messages must be removed and never dispatched")
	}
}

func TestDeletedEndpointIsDropped(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.send(`{"a":1}`, nil)
	if _, err := e.store.Pool().Exec(ctx, `DELETE FROM endpoints WHERE id = $1`, e.seed.EndpointID); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.proc.ProcessBatch(ctx); n != 1 {
		t.Fatalf("processed %d", n)
	}
	if _, err := e.store.GetPayloadLog(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("log stored for a deleted endpoint: %v", err)
	}
	if e.streamLen(e.keys.DispatchStream()) != 0 {
		t.Fatal("dispatched for a deleted endpoint")
	}
}

func TestTransientFailureIsRetriedViaReclaim(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.send(`{"a":1}`, nil)

	e.store.down.Store(true)
	if n, _ := e.proc.ProcessBatch(ctx); n != 0 {
		t.Fatalf("acknowledged %d messages during an outage", n)
	}
	if e.pending() != 1 {
		t.Fatal("message should stay pending for retry")
	}

	e.store.down.Store(false)
	time.Sleep(5 * time.Millisecond) // exceed ClaimMinIdle
	if n, err := e.proc.ReclaimStuck(ctx); err != nil || n != 1 {
		t.Fatalf("reclaimed %d, err %v", n, err)
	}
	if _, err := e.store.GetPayloadLog(ctx, id); err != nil {
		t.Fatalf("log not stored after recovery: %v", err)
	}
	if e.pending() != 0 || e.streamLen(e.keys.IngestStream()) != 0 {
		t.Fatal("recovered message not acknowledged")
	}
}

func TestDeveloperPlanRetention(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.store.Pool().Exec(ctx, `UPDATE users SET plan_type = 'developer' WHERE id = $1`, e.seed.UserID); err != nil {
		t.Fatal(err)
	}
	id := e.send(`{}`, nil)
	e.proc.ProcessBatch(ctx)
	l, _ := e.store.GetPayloadLog(ctx, id)
	if got := l.ExpiresAt.Sub(l.ReceivedAt); got != 24*time.Hour {
		t.Fatalf("retention %v, want 24h", got)
	}
}

func TestSelfHostedRetentionIgnoresPlan(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.store.Pool().Exec(ctx, `UPDATE users SET plan_type = 'developer' WHERE id = $1`, e.seed.UserID); err != nil {
		t.Fatal(err)
	}
	selfHosted := processor.New(queue.NewConsumer(e.rdb, e.keys.IngestStream(), processor.Group, "self-hosted"),
		queue.NewProducer(e.rdb, e.keys), e.store, e.cipher,
		processor.Options{Block: 50 * time.Millisecond, Plans: core.SelfHosted{Retention: 7 * 24 * time.Hour}})
	id := e.send(`{}`, nil)
	if _, err := selfHosted.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	l, err := e.store.GetPayloadLog(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.ExpiresAt.Sub(l.ReceivedAt); got != 7*24*time.Hour {
		t.Fatalf("retention %v, want HS_RETENTION (7 days) whatever the plan", got)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.proc.Run(ctx) }()

	id := e.send(`{"x":1}`, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := e.store.GetPayloadLog(context.Background(), id); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not process the message")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

// Regression (security review): rules that blow the body up are never applied,
// and the webhook is dead-lettered instead of delivered untransformed.
func TestExplodingTransformIsDeadLettered(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var rules []string
	for i := 0; i < 40; i++ { // alternating copies: valid rules, Fibonacci growth
		from, to := "a", fmt.Sprintf("b.n%d", i)
		if i%2 == 1 {
			from, to = "b", fmt.Sprintf("a.n%d", i)
		}
		rules = append(rules, fmt.Sprintf(`{"op":"copy","from":%q,"to":%q}`, from, to))
	}
	if err := e.store.SetEndpointTransformRules(ctx, e.seed.EndpointID, []byte(`{"version":1,"body":[`+strings.Join(rules, ",")+`]}`)); err != nil {
		t.Fatal(err)
	}
	id := e.send(`{"a":{"x":"`+strings.Repeat("y", 200)+`"},"b":{}}`, nil)
	start := time.Now()
	if n, err := e.proc.ProcessBatch(ctx); err != nil || n != 1 {
		t.Fatalf("processed %d, err %v", n, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if _, err := e.store.GetPayloadLog(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a log was stored: %v", err)
	}
	dlq, _ := e.rdb.XRange(ctx, e.keys.DeadLetterStream(), "-", "+").Result()
	if len(dlq) != 1 || dlq[0].Values["reason"] != "transform_too_large" || e.streamLen(e.keys.DispatchStream()) != 0 {
		t.Fatalf("dead letters %+v, dispatch stream %d", dlq, e.streamLen(e.keys.DispatchStream()))
	}

	// Stored rules that no longer validate (saved before a validation rule
	// existed) fail closed too: the body is never forwarded untransformed.
	if err := e.store.SetEndpointTransformRules(ctx, e.seed.EndpointID, []byte(`{"version":1,"body":[{"op":"copy","from":"a","to":"a.b"}]}`)); err != nil {
		t.Fatal(err)
	}
	e.send(`{"a":1}`, nil)
	if n, err := e.proc.ProcessBatch(ctx); err != nil || n != 1 {
		t.Fatalf("processed %d, err %v", n, err)
	}
	dlq, _ = e.rdb.XRange(ctx, e.keys.DeadLetterStream(), "-", "+").Result()
	if len(dlq) != 2 || dlq[1].Values["reason"] != "transform_invalid" || e.streamLen(e.keys.DispatchStream()) != 0 {
		t.Fatalf("invalid stored rules: %+v", dlq)
	}
}

// A message that keeps crashing its consumer (the delivery never comes back)
// is dead-lettered after queue.MaxDeliveries instead of being reclaimed
// forever, and the dead letter keeps everything needed to replay it.
func TestMessageReclaimedTooOftenIsDeadLettered(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.send(`{"a":1}`, nil)
	orig, _ := e.rdb.XRange(ctx, e.keys.IngestStream(), "-", "+").Result()

	// A consumer that reads the message and dies every time (no ack, no
	// Postpone): deliveries 1..MaxDeliveries.
	crasher := queue.NewConsumer(e.rdb, e.keys.IngestStream(), processor.Group, "crasher")
	if msgs, err := crasher.Read(ctx, 10, 10*time.Millisecond); err != nil || len(msgs) != 1 {
		t.Fatalf("read %d %v", len(msgs), err)
	}
	for i := 1; i < queue.MaxDeliveries; i++ {
		time.Sleep(5 * time.Millisecond)
		if msgs, err := crasher.Claim(ctx, time.Millisecond, 10); err != nil || len(msgs) != 1 {
			t.Fatalf("claim %d %v", len(msgs), err)
		}
	}
	time.Sleep(5 * time.Millisecond)
	if n, err := e.proc.ReclaimStuck(ctx); err != nil || n != 1 { // delivery MaxDeliveries+1
		t.Fatalf("reclaimed %d, err %v", n, err)
	}
	if e.pending() != 0 || e.streamLen(e.keys.IngestStream()) != 0 {
		t.Fatalf("still pending after %d deliveries", queue.MaxDeliveries+1)
	}
	dlq, _ := e.rdb.XRange(ctx, e.keys.DeadLetterStream(), "-", "+").Result()
	if len(dlq) != 1 || dlq[0].Values["reason"] != "too_many_deliveries" {
		t.Fatalf("dead letters: %+v", dlq)
	}
	for k, v := range orig[0].Values {
		if dlq[0].Values[k] != v {
			t.Fatalf("dead letter lost field %q: %v != %v", k, dlq[0].Values[k], v)
		}
	}

	// Replay: the original fields go back to the ingest stream and are processed.
	replay := map[string]any{}
	for k := range orig[0].Values {
		replay[k] = dlq[0].Values[k]
	}
	e.rdb.XAdd(ctx, &redis.XAddArgs{Stream: e.keys.IngestStream(), Values: replay})
	if n, err := e.proc.ProcessBatch(ctx); err != nil || n != 1 {
		t.Fatalf("replay processed %d, err %v", n, err)
	}
	if _, err := e.store.GetPayloadLog(ctx, id); err != nil {
		t.Fatalf("replayed dead letter not stored: %v", err)
	}
}

// Regression (review): a database outage longer than MaxDeliveries reclaim
// cycles used to dead-letter and delete accepted webhooks. Transient failures
// must never count as poison.
func TestLongOutageLosesNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := e.send(`{"a":1}`, nil) // ingest answered 202
	e.store.down.Store(true)
	if n, _ := e.proc.ProcessBatch(ctx); n != 0 {
		t.Fatalf("acknowledged %d during an outage", n)
	}
	for i := 0; i < 3*queue.MaxDeliveries; i++ {
		time.Sleep(5 * time.Millisecond)
		_, _ = e.proc.ReclaimStuck(ctx)
	}
	if e.pending() != 1 || e.streamLen(e.keys.DeadLetterStream()) != 0 {
		t.Fatalf("pending %d, dead letters %d during the outage", e.pending(), e.streamLen(e.keys.DeadLetterStream()))
	}
	e.store.down.Store(false)
	time.Sleep(5 * time.Millisecond)
	if n, err := e.proc.ReclaimStuck(ctx); err != nil || n != 1 {
		t.Fatalf("reclaimed %d, err %v", n, err)
	}
	if _, err := e.store.GetPayloadLog(ctx, id); err != nil {
		t.Fatalf("webhook accepted with 202 lost after the outage: %v", err)
	}
	if e.pending() != 0 || e.streamLen(e.keys.IngestStream()) != 0 || e.streamLen(e.keys.DeadLetterStream()) != 0 {
		t.Fatal("recovered message not acknowledged, or dead-lettered")
	}
}
