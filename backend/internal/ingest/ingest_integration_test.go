//go:build integration

package ingest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

type env struct {
	srv    *httptest.Server
	rdb    *redis.Client
	keys   redisx.Keys
	cipher *crypto.Cipher
	store  *store.Store
	cache  *endpointcache.Cache
	seed   seed.Result
}

func setup(t *testing.T, ratePerSec int64) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	rdb, keys := testredis.New(t)
	cipher, err := crypto.New(bytes.Repeat([]byte{9}, crypto.KeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	cache := endpointcache.New(rdb, keys, s)
	res, err := seed.Run(ctx, s, cache)
	if err != nil {
		t.Fatal(err)
	}

	h := ingest.New(cache, limits.New(rdb, keys, ratePerSec, nil), queue.NewProducer(rdb, keys), cipher, ingest.Options{})
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &env{srv: srv, rdb: rdb, keys: keys, cipher: cipher, store: s, cache: cache, seed: res}
}

func (e *env) post(t testing.TB, client *http.Client, tok, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/"+e.seed.Slug, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set(e.seed.HeaderKey, tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestEndToEndEnqueuesEncryptedMessage(t *testing.T) {
	e := setup(t, 0)
	ctx := context.Background()

	resp := e.post(t, http.DefaultClient, e.seed.Token, `{"type":"demo.ping"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", resp.StatusCode)
	}
	id := resp.Header.Get(ingest.RequestIDHeader)

	entries, err := e.rdb.XRange(ctx, e.keys.IngestStream(), "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("stream entries: %d, %v", len(entries), err)
	}
	m, err := queue.ParseIngestMessage(entries[0].Values)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != id || m.EndpointID != e.seed.EndpointID || m.UserID != e.seed.UserID {
		t.Fatalf("unexpected message: %+v", m)
	}
	raw := fmt.Sprint(entries[0].Values)
	if strings.Contains(raw, "demo.ping") || strings.Contains(raw, e.seed.Token) {
		t.Fatal("plaintext payload or token found in Redis")
	}
	plain, err := e.cipher.Open(m.Ciphertext, m.Nonce, queue.IngestAAD(m.ID))
	if err != nil {
		t.Fatal(err)
	}
	env, _ := queue.UnmarshalEnvelope(plain)
	if string(env.Body) != `{"type":"demo.ping"}` {
		t.Fatalf("body %q", env.Body)
	}

	// Usage was counted once for the demo user.
	n, _ := e.rdb.Get(ctx, e.keys.MonthlyUsage(e.seed.UserID, limits.Period(time.Now()))).Int64()
	if n != 1 {
		t.Fatalf("usage = %d, want 1", n)
	}
}

func TestEndToEndRejectsAndRevocation(t *testing.T) {
	e := setup(t, 0)
	ctx := context.Background()

	if r := e.post(t, http.DefaultClient, "", "{}"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", r.StatusCode)
	}

	// Rotating the token (seed again) revokes the old one immediately thanks to cache invalidation.
	old := e.seed.Token
	if r := e.post(t, http.DefaultClient, old, "{}"); r.StatusCode != http.StatusAccepted {
		t.Fatalf("warm cache: %d", r.StatusCode)
	}
	fresh, err := seed.Run(ctx, e.store, e.cache)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.post(t, http.DefaultClient, old, "{}"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still accepted: %d", r.StatusCode)
	}
	if r := e.post(t, http.DefaultClient, fresh.Token, "{}"); r.StatusCode != http.StatusAccepted {
		t.Fatalf("new token rejected: %d", r.StatusCode)
	}

	// Pausing applies immediately after invalidation.
	if err := e.store.SetEndpointStatus(ctx, fresh.EndpointID, store.StatusPaused); err != nil {
		t.Fatal(err)
	}
	_ = e.cache.Invalidate(ctx, fresh.Slug)
	if r := e.post(t, http.DefaultClient, fresh.Token, "{}"); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("paused endpoint: %d", r.StatusCode)
	}

	if n, _ := e.rdb.XLen(ctx, e.keys.IngestStream()).Result(); n != 2 {
		t.Fatalf("stream has %d entries, want 2 (only accepted requests)", n)
	}
}

func TestEndToEndRateLimit(t *testing.T) {
	e := setup(t, 5)
	var accepted, limited int
	for i := 0; i < 8; i++ {
		switch e.post(t, http.DefaultClient, e.seed.Token, "{}").StatusCode {
		case http.StatusAccepted:
			accepted++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	// The 8 requests may straddle a second boundary; at least 3 must be limited unless they did.
	if accepted < 5 || accepted+limited != 8 {
		t.Fatalf("accepted=%d limited=%d", accepted, limited)
	}
}

// TestLatencyBudget checks the < 20 ms p99 target with real Postgres + Redis
// (warm cache, 16 concurrent senders, 4,000 requests).
//
// It is a performance test, so it only runs when explicitly requested
// (HS_PERF_TEST=1, see `make test-perf`) and never under -race, whose
// instrumentation makes latency numbers meaningless.
func TestLatencyBudget(t *testing.T) {
	if os.Getenv("HS_PERF_TEST") != "1" {
		t.Skip("set HS_PERF_TEST=1 to run the latency budget test (make test-perf)")
	}
	if raceEnabled {
		t.Skip("latency budget is not meaningful with -race")
	}
	e := setup(t, 0)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}
	body := `{"type":"invoice.paid","data":{"object":{"id":"in_123","amount":4200}}}`

	e.post(t, client, e.seed.Token, body) // warm the endpoint cache

	const workers, perWorker = 16, 250
	durations := make([]time.Duration, 0, workers*perWorker)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				start := time.Now()
				if r := e.post(t, client, e.seed.Token, body); r.StatusCode != http.StatusAccepted {
					t.Errorf("status %d", r.StatusCode)
					return
				}
				local = append(local, time.Since(start))
			}
			mu.Lock()
			durations = append(durations, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	pct := func(p float64) time.Duration { return durations[int(float64(len(durations)-1)*p)] }
	t.Logf("requests=%d p50=%v p95=%v p99=%v max=%v", len(durations), pct(0.50), pct(0.95), pct(0.99), durations[len(durations)-1])
	if p99 := pct(0.99); p99 > 20*time.Millisecond {
		t.Fatalf("p99 %v exceeds the 20 ms budget", p99)
	}

	// Every accepted request is in the stream.
	n, _ := e.rdb.XLen(context.Background(), e.keys.IngestStream()).Result()
	if n != int64(len(durations)+1) {
		t.Fatalf("stream has %d entries, want %d", n, len(durations)+1)
	}
}
