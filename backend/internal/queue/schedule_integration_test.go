//go:build integration

package queue_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

func TestScheduleAndMoveDue(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	p := queue.NewProducer(rdb, keys)
	now := time.Now()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(p.ScheduleDispatch(ctx, queue.DispatchMessage{LogID: "l1", EndpointID: "e1", Attempt: 2}, now.Add(-time.Second)))
	must(p.ScheduleDispatch(ctx, queue.DispatchMessage{LogID: "l1", EndpointID: "e1", Attempt: 2}, now.Add(-time.Second))) // duplicate
	must(p.ScheduleDispatch(ctx, queue.DispatchMessage{LogID: "l2", EndpointID: "e1", Attempt: 3}, now.Add(time.Hour)))

	if n, _ := p.ScheduledCount(ctx); n != 2 {
		t.Fatalf("scheduled %d, want 2 (duplicates collapse)", n)
	}
	moved, err := p.MoveDueRetries(ctx, now, 100)
	if err != nil || moved != 1 {
		t.Fatalf("moved %d, err %v", moved, err)
	}
	entries, _ := rdb.XRange(ctx, keys.DispatchStream(), "-", "+").Result()
	if len(entries) != 1 {
		t.Fatalf("dispatch entries %d", len(entries))
	}
	m, err := queue.ParseDispatchMessage(entries[0].Values)
	if err != nil || m.LogID != "l1" || m.Attempt != 2 {
		t.Fatalf("moved message %+v %v", m, err)
	}
	if n, _ := p.ScheduledCount(ctx); n != 1 {
		t.Fatal("future retry must stay scheduled")
	}
}

func TestMoveDueIsAtomicAcrossSchedulers(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	p := queue.NewProducer(rdb, keys)
	past := time.Now().Add(-time.Minute)
	for i := 0; i < 200; i++ {
		_ = p.ScheduleDispatch(ctx, queue.DispatchMessage{LogID: "l" + string(rune('a'+i%26)) + time.Duration(i).String(), EndpointID: "e", Attempt: 2}, past)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := p.MoveDueRetries(ctx, time.Now(), 7)
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
				mu.Lock()
				total += n
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if total != 200 {
		t.Fatalf("moved %d, want exactly 200", total)
	}
	if n, _ := rdb.XLen(ctx, keys.DispatchStream()).Result(); n != 200 {
		t.Fatalf("dispatch stream has %d entries, want 200 (no loss, no duplicates)", n)
	}
}

func TestScheduleRejectsSeparator(t *testing.T) {
	rdb, keys := testredis.New(t)
	err := queue.NewProducer(rdb, keys).ScheduleDispatch(context.Background(), queue.DispatchMessage{LogID: "a|b", EndpointID: "e", Attempt: 2}, time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTrimDeadLettersByAge(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	p := queue.NewProducer(rdb, keys)
	old := time.Now().Add(-queue.DeadLetterMaxAge - time.Hour)
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: keys.DeadLetterStream(), ID: fmt.Sprintf("%d-0", old.UnixMilli()), Values: map[string]any{"reason": "old"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.DeadLetter(ctx, "ingest", "1-0", "new", map[string]any{"payload": "sealed"}); err != nil {
		t.Fatal(err)
	}
	n, err := p.TrimDeadLetters(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("trimmed %d, err %v", n, err)
	}
	entries, _ := rdb.XRange(ctx, keys.DeadLetterStream(), "-", "+").Result()
	if len(entries) != 1 || entries[0].Values["reason"] != "new" {
		t.Fatalf("left: %+v", entries)
	}
}
