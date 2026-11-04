//go:build integration

package limits

import (
	"context"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/testplans"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

func TestRateLimit(t *testing.T) {
	rdb, keys := testredis.New(t)
	l := New(rdb, keys, 3, testplans.Blueprint)
	fixed := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return fixed }
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if d, _, err := l.Check(ctx, "e1", "u1", testplans.Pro); err != nil || d != Allowed {
			t.Fatalf("request %d: %v %v", i, d, err)
		}
	}
	if d, used, _ := l.Check(ctx, "e1", "u1", testplans.Pro); d != RateLimited || used != 3 {
		t.Fatalf("4th request in the same second: %v (used %d), want rate_limited with 3 counted", d, used)
	}
	// Another endpoint has its own window.
	if d, _, _ := l.Check(ctx, "e2", "u1", testplans.Pro); d != Allowed {
		t.Fatalf("other endpoint: %v", d)
	}
	// Next second opens a new window.
	l.now = func() time.Time { return fixed.Add(time.Second) }
	if d, _, _ := l.Check(ctx, "e1", "u1", testplans.Pro); d != Allowed {
		t.Fatalf("next second: %v", d)
	}
}

func TestDeveloperQuota(t *testing.T) {
	rdb, keys := testredis.New(t)
	l := New(rdb, keys, 0, testplans.Blueprint)
	now := time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	ctx := context.Background()

	// Pretend 4,999 requests were already accepted this month.
	rdb.Set(ctx, keys.MonthlyUsage("u1", "202611"), 4_999, time.Hour)

	if d, used, _ := l.Check(ctx, "e1", "u1", testplans.Developer); d != Allowed || used != 5_000 {
		t.Fatalf("request #5000: %v (used %d)", d, used)
	}
	if d, used, _ := l.Check(ctx, "e1", "u1", testplans.Developer); d != QuotaExceeded || used != 5_000 {
		t.Fatalf("request #5001: %v (used %d), want quota_exceeded", d, used)
	}
	// Rejected requests are not counted.
	if v, _ := rdb.Get(ctx, keys.MonthlyUsage("u1", "202611")).Int64(); v != 5_000 {
		t.Fatalf("usage counter = %d, want 5000", v)
	}
	// Pro overage is accepted.
	if d, _, _ := l.Check(ctx, "e1", "u1", testplans.Pro); d != Allowed {
		t.Fatalf("pro over quota: %v", d)
	}
	// A new month resets the quota.
	l.now = func() time.Time { return now.Add(2 * time.Minute) }
	if d, used, _ := l.Check(ctx, "e1", "u1", testplans.Developer); d != Allowed || used != 1 {
		t.Fatalf("new month: %v (used %d)", d, used)
	}
	if ttl := rdb.TTL(ctx, keys.MonthlyUsage("u1", "202612")).Val(); ttl < 39*24*time.Hour {
		t.Fatalf("usage key TTL too short: %v", ttl)
	}
}

func TestSelfHostedHasNoQuota(t *testing.T) {
	rdb, keys := testredis.New(t)
	l := New(rdb, keys, 0, nil) // nil plans = self-hosted
	ctx := context.Background()
	rdb.Set(ctx, keys.MonthlyUsage("u1", Period(time.Now())), 1_000_000, time.Hour)
	d, used, err := l.Check(ctx, "e1", "u1", testplans.Developer)
	if err != nil || d != Allowed || used != 1_000_001 {
		t.Fatalf("self-hosted must never hit a quota (but still count usage): %v used=%d err=%v", d, used, err)
	}
}
