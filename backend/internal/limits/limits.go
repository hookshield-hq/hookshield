// Package limits enforces per-endpoint rate limits and per-user monthly quotas
// (quotas come from the injected core.Plans: none on self-hosted installs)
// on the ingest hot path, with a single Redis round trip.
//
// Counters:
//   - rate:  requests of an endpoint in the current second (fixed window, expires after 2 s);
//   - usage: accepted requests of a user in the current UTC month (expires after 40 days).
//
// Rejected requests are not counted towards usage. The monthly counter is the
// source for quota checks; the worker periodically persists it to
// usage_counters for billing (Phase 9).
package limits

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"

	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
)

// Decision is the outcome of Check.
type Decision int

const (
	// Allowed means the request may proceed (and was counted).
	Allowed Decision = iota
	// RateLimited means the endpoint exceeded its per-second limit.
	RateLimited
	// QuotaExceeded means the user's plan has no requests left this month.
	QuotaExceeded
)

func (d Decision) String() string {
	switch d {
	case Allowed:
		return "allowed"
	case RateLimited:
		return "rate_limited"
	case QuotaExceeded:
		return "quota_exceeded"
	}
	return fmt.Sprintf("decision(%d)", int(d))
}

// Limiter checks and counts requests.
type Limiter struct {
	rdb        *redis.Client
	keys       redisx.Keys
	ratePerSec int64
	now        func() time.Time
	usageTTL   time.Duration
	rateKeyTTL time.Duration
	plans      core.Plans
}

// New returns a Limiter. ratePerSec <= 0 disables the per-endpoint rate limit.
// plans decides the monthly quota; nil means self-hosted (no quota). Usage is
// counted either way, so the dashboard can show it.
func New(rdb *redis.Client, keys redisx.Keys, ratePerSec int64, plans core.Plans) *Limiter {
	return &Limiter{
		rdb: rdb, keys: keys, ratePerSec: ratePerSec, now: time.Now, plans: core.OrSelfHosted(plans, 0),
		usageTTL: 40 * 24 * time.Hour, rateKeyTTL: 2 * time.Second,
	}
}

// Period returns the usage period key for t, e.g. "202611" (UTC).
func Period(t time.Time) string { return t.UTC().Format("200601") }

// Check counts one request for endpointID/userID and decides whether to accept it.
func (l *Limiter) Check(ctx context.Context, endpointID, userID, plan string) (Decision, int64, error) {
	now := l.now()
	usageKey := l.keys.MonthlyUsage(userID, Period(now))
	rateKey := l.keys.RateWindow(endpointID, now.Unix())

	pipe := l.rdb.TxPipeline()
	rate := pipe.Incr(ctx, rateKey)
	pipe.Expire(ctx, rateKey, l.rateKeyTTL)
	usage := pipe.Incr(ctx, usageKey)
	pipe.Expire(ctx, usageKey, l.usageTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return Allowed, 0, fmt.Errorf("limits: %w", err)
	}

	n := usage.Val()
	if l.ratePerSec > 0 && rate.Val() > l.ratePerSec {
		l.rdb.Decr(ctx, usageKey)
		return RateLimited, n - 1, nil
	}
	if l.plans.For(plan).Exceeded(n) {
		l.rdb.Decr(ctx, usageKey)
		return QuotaExceeded, n - 1, nil
	}
	return Allowed, n, nil
}
