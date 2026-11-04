//go:build integration

package redisx_test

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// A worker's idle blocking readers must not starve its other Redis commands.
// Regression: with go-redis' default pool (10 per CPU) and 4 processors + 32
// dispatchers on 2 CPUs, idle dispatchers held every connection and the
// processors waited seconds for one — throughput fell to a few webhooks/s.
func TestBlockingReadersDoNotStarveThePool(t *testing.T) {
	url := os.Getenv("HS_TEST_REDIS_URL")
	if url == "" {
		t.Skip("HS_TEST_REDIS_URL not set")
	}
	readers := 10*runtime.GOMAXPROCS(0) + 8 // more than the default pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb, err := redisx.OpenFor(ctx, url, readers)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if got := rdb.Options().PoolSize; got != redisx.PoolSize(readers) {
		t.Fatalf("pool size %d, want %d", got, redisx.PoolSize(readers))
	}

	stream := "hs:test:starve:" + time.Now().Format("150405.000000")
	if err := rdb.XGroupCreateMkStream(ctx, stream, "g", "$").Err(); err != nil {
		t.Fatal(err)
	}
	defer rdb.Del(context.Background(), stream)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g", Consumer: "c", Streams: []string{stream, ">"}, Block: 2 * time.Second})
			}
		}()
	}
	time.Sleep(300 * time.Millisecond) // every reader is now blocked on the empty stream

	start := time.Now()
	for i := 0; i < 20; i++ {
		if err := rdb.Ping(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("20 PINGs took %s while the readers were blocked: the pool is starved", d)
	}
	cancel()
	wg.Wait()
}
