//go:build integration

package app

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

// Two workers share the schedule of a job: each tick runs on one of them only.
func TestJobRunsOnOneWorkerPerInterval(t *testing.T) {
	rdb, keys := testredis.New(t)
	var runs atomic.Int32
	job := Job{Name: "count", Every: time.Second, Run: func(context.Context, JobContext) error {
		runs.Add(1)
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	jc := JobContext{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runJob(ctx, job, rdb, keys, jc)
		}()
	}
	wg.Wait()
	// Ticks at ~0, 1 and 2 s: 3 runs, never 2 per tick (that would be 6).
	if n := runs.Load(); n != 3 {
		t.Fatalf("runs = %d, want one per interval", n)
	}
}
