//go:build integration

package endpointcache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

// fakeLoader counts database hits and serves configs from a map.
type fakeLoader struct {
	mu    sync.Mutex
	calls atomic.Int64
	data  map[string]store.IngestConfig
	delay time.Duration
}

func (f *fakeLoader) GetIngestConfig(_ context.Context, slug string) (store.IngestConfig, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.data[slug]
	if !ok {
		return store.IngestConfig{}, store.ErrNotFound
	}
	return c, nil
}

func (f *fakeLoader) set(slug string, c store.IngestConfig) {
	f.mu.Lock()
	f.data[slug] = c
	f.mu.Unlock()
}

func TestReadThroughAndInvalidate(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	loader := &fakeLoader{data: map[string]store.IngestConfig{
		"ep_one": {EndpointID: "e1", Slug: "ep_one", Status: store.StatusActive, AuthTokenHash: []byte{1, 2, 3}},
	}}
	c := endpointcache.New(rdb, keys, loader)

	for i := 0; i < 5; i++ {
		cfg, err := c.Lookup(ctx, "ep_one")
		if err != nil || cfg.EndpointID != "e1" || len(cfg.AuthTokenHash) != 3 {
			t.Fatalf("lookup %d: %+v %v", i, cfg, err)
		}
	}
	if n := loader.calls.Load(); n != 1 {
		t.Fatalf("database hit %d times, want 1", n)
	}

	loader.set("ep_one", store.IngestConfig{EndpointID: "e1", Slug: "ep_one", Status: store.StatusPaused})
	if cfg, _ := c.Lookup(ctx, "ep_one"); cfg.Status != store.StatusActive {
		t.Fatal("expected stale cached value before invalidation")
	}
	if err := c.Invalidate(ctx, "ep_one"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := c.Lookup(ctx, "ep_one"); cfg.Status != store.StatusPaused {
		t.Fatalf("invalidation not applied: %+v", cfg)
	}
}

func TestNegativeCaching(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	loader := &fakeLoader{data: map[string]store.IngestConfig{}}
	c := endpointcache.New(rdb, keys, loader, endpointcache.WithNegativeTTL(time.Minute))

	for i := 0; i < 10; i++ {
		if _, err := c.Lookup(ctx, "ep_missing"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	}
	if n := loader.calls.Load(); n != 1 {
		t.Fatalf("unknown slug hit the database %d times, want 1", n)
	}

	// Invalid slugs never touch Redis or the database.
	for _, bad := range []string{"", "x", "UPPER", "../etc", "a b c"} {
		if _, err := c.Lookup(ctx, bad); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%q: want ErrNotFound, got %v", bad, err)
		}
	}
	if n := loader.calls.Load(); n != 1 {
		t.Fatalf("invalid slugs reached the loader (%d calls)", n)
	}
}

func TestConcurrentMissesCollapse(t *testing.T) {
	rdb, keys := testredis.New(t)
	loader := &fakeLoader{
		data:  map[string]store.IngestConfig{"ep_hot": {EndpointID: "e1", Slug: "ep_hot"}},
		delay: 50 * time.Millisecond,
	}
	c := endpointcache.New(rdb, keys, loader)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Lookup(context.Background(), "ep_hot"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := loader.calls.Load(); n != 1 {
		t.Fatalf("50 concurrent misses caused %d database queries, want 1", n)
	}
}

func TestCorruptEntryIsReloaded(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	loader := &fakeLoader{data: map[string]store.IngestConfig{"ep_x1": {EndpointID: "e1", Slug: "ep_x1"}}}
	c := endpointcache.New(rdb, keys, loader)

	rdb.Set(ctx, keys.EndpointCache("ep_x1"), "{not json", time.Minute)
	cfg, err := c.Lookup(ctx, "ep_x1")
	if err != nil || cfg.EndpointID != "e1" {
		t.Fatalf("corrupt entry not reloaded: %+v %v", cfg, err)
	}
}

// staleLoader returns the config it read, then blocks until released: it
// models a lookup that read the database just before an update.
type staleLoader struct {
	mu      sync.Mutex
	cfg     store.IngestConfig
	loaded  chan struct{}
	release chan struct{}
}

func (l *staleLoader) GetIngestConfig(context.Context, string) (store.IngestConfig, error) {
	l.mu.Lock()
	cfg, loaded, release := l.cfg, l.loaded, l.release
	l.mu.Unlock()
	if loaded != nil {
		close(loaded)
		<-release
	}
	return cfg, nil
}

// Regression (review): an Invalidate between a miss's database read and its
// SET was undone, so a rotated token or paused endpoint kept working for up to
// the cache TTL.
func TestInvalidateDuringLoadIsNotUndone(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	old := store.IngestConfig{EndpointID: "e1", Slug: "ep_race", Status: store.StatusActive, AuthTokenHash: []byte("old")}
	l := &staleLoader{cfg: old, loaded: make(chan struct{}), release: make(chan struct{})}
	c := endpointcache.New(rdb, keys, l)

	done := make(chan store.IngestConfig)
	go func() { cfg, _ := c.Lookup(ctx, "ep_race"); done <- cfg }()
	<-l.loaded // the lookup holds the old config

	// The endpoint changes (token rotated, paused) and the API invalidates.
	l.mu.Lock()
	l.cfg = store.IngestConfig{EndpointID: "e1", Slug: "ep_race", Status: store.StatusPaused, AuthTokenHash: []byte("new")}
	l.loaded = nil
	l.mu.Unlock()
	if err := c.Invalidate(ctx, "ep_race"); err != nil {
		t.Fatal(err)
	}
	close(l.release)
	<-done

	cfg, err := c.Lookup(ctx, "ep_race")
	if err != nil || string(cfg.AuthTokenHash) != "new" || cfg.Status != store.StatusPaused {
		t.Fatalf("stale config served after Invalidate: %+v %v", cfg, err)
	}
}
