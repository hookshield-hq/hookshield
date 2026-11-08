// Package usage copies the live monthly request counters (Redis, written on
// the ingest hot path by package limits) to usage_counters in PostgreSQL:
// a durable history for the dashboard and the source billing reports from.
package usage

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// Flush records the counters of the current and the previous month (the
// previous one keeps changing for a moment after midnight UTC) and returns
// how many accounts were written.
func Flush(ctx context.Context, rdb *redis.Client, keys redisx.Keys, st *store.Store, now time.Time) (int, error) {
	now = now.UTC()
	cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periods := map[string]time.Time{limits.Period(cur): cur, limits.Period(cur.AddDate(0, -1, 0)): cur.AddDate(0, -1, 0)}

	n := 0
	iter := rdb.Scan(ctx, 0, keys.UsagePattern(), 500).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		user, period, ok := keys.ParseUsageKey(key)
		start, wanted := periods[period]
		if !ok || !wanted {
			continue
		}
		v, err := rdb.Get(ctx, key).Int64()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return n, err
		}
		if v <= 0 {
			continue
		}
		if err := st.RecordUsage(ctx, user, start, v); err != nil {
			return n, err
		}
		n++
	}
	return n, iter.Err()
}

// Store implements core.UsageStore on PostgreSQL.
type Store struct{ St *store.Store }

// PendingUsage implements core.UsageStore.
func (s Store) PendingUsage(ctx context.Context, provider string, periods []time.Time) ([]core.UsageRecord, error) {
	rows, err := s.St.PendingUsage(ctx, provider, periods)
	if err != nil {
		return nil, err
	}
	out := make([]core.UsageRecord, 0, len(rows))
	for _, r := range rows {
		b := r.Subscription
		out = append(out, core.UsageRecord{
			UserID: r.UserID, PeriodStart: r.PeriodStart.UTC(), Requests: r.Requests, UnitsReported: r.UnitsReported, IncludedRequests: r.Included,
			Subscription: core.Subscription{
				UserID: b.UserID, Provider: b.Provider, CustomerID: b.CustomerID, SubscriptionID: b.SubscriptionID,
				ItemID: b.ItemID, Plan: b.Plan, Interval: core.NormalizeInterval(b.Interval), Status: b.Status, RenewsAt: b.RenewsAt, EndsAt: b.EndsAt,
				TestMode: b.TestMode, UpdatedAt: b.ProviderUpdatedAt,
			},
		})
	}
	return out, nil
}

// MarkUsageReported implements core.UsageStore.
func (s Store) MarkUsageReported(ctx context.Context, userID string, periodStart time.Time, from, to int64) (bool, error) {
	return s.St.MarkUsageReported(ctx, userID, periodStart, from, to)
}

// ReleaseUsage implements core.UsageStore.
func (s Store) ReleaseUsage(ctx context.Context, userID string, periodStart time.Time, units int64) error {
	return s.St.ReleaseUsage(ctx, userID, periodStart, units)
}

// RaiseIncluded implements core.UsageStore.
func (s Store) RaiseIncluded(ctx context.Context, userID string, periodStart time.Time, requests int64) error {
	return s.St.RaiseIncluded(ctx, userID, periodStart, requests)
}
