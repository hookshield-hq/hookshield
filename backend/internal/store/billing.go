package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Subscription is a row of billing_subscriptions.
type Subscription struct {
	UserID            string
	Provider          string
	CustomerID        string
	SubscriptionID    string
	ItemID            string
	Plan              string
	Interval          string // "month" or "year" ("" is saved as "month")
	Status            string
	RenewsAt          *time.Time
	EndsAt            *time.Time
	TestMode          bool
	ProviderUpdatedAt time.Time
}

const subscriptionCols = `user_id, provider, customer_id, subscription_id, item_id, plan, billing_interval, status, renews_at, ends_at, test_mode, provider_updated_at`

func scanSubscription(row pgx.Row) (Subscription, error) {
	var s Subscription
	err := row.Scan(&s.UserID, &s.Provider, &s.CustomerID, &s.SubscriptionID, &s.ItemID, &s.Plan, &s.Interval, &s.Status, &s.RenewsAt, &s.EndsAt, &s.TestMode, &s.ProviderUpdatedAt)
	return s, mapErr(err)
}

// GetSubscription returns the subscription of a user (ErrNotFound when none).
func (s *Store) GetSubscription(ctx context.Context, userID string) (Subscription, error) {
	return scanSubscription(s.pool.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM billing_subscriptions WHERE user_id = $1`, userID))
}

// GetSubscriptionByProviderID finds a subscription by the provider's ID.
func (s *Store) GetSubscriptionByProviderID(ctx context.Context, provider, id string) (Subscription, error) {
	return scanSubscription(s.pool.QueryRow(ctx,
		`SELECT `+subscriptionCols+` FROM billing_subscriptions WHERE provider = $1 AND subscription_id = $2`, provider, id))
}

// SaveSubscription upserts sub unless the stored row has a newer
// provider_updated_at, and sets users.plan_type to plan in the same
// transaction. It returns whether the row was written and the slugs of the
// user's endpoints (their cached configuration carries the plan).
func (s *Store) SaveSubscription(ctx context.Context, sub Subscription, plan string) (applied bool, slugs []string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// An account has one subscription. Another subscription arriving while the
	// stored one is still in effect is refused (ErrConflict) instead of
	// silently replacing it: a payer must never be downgraded by an event of a
	// subscription that is not theirs, or by a duplicate one.
	// Lock the account first: with no subscription row yet, FOR UPDATE below
	// locks nothing, and two first subscriptions arriving together would both
	// pass the check (the second silently overwriting the first).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, sub.UserID); err != nil {
		return false, nil, mapErr(err)
	}
	var cur struct {
		id, status string
		endsAt     *time.Time
	}
	err = tx.QueryRow(ctx, `SELECT subscription_id, status, ends_at FROM billing_subscriptions WHERE user_id = $1 FOR UPDATE`, sub.UserID).
		Scan(&cur.id, &cur.status, &cur.endsAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return false, nil, mapErr(err)
	case cur.id != sub.SubscriptionID && subscriptionInEffect(cur.status, cur.endsAt):
		return false, nil, ErrConflict
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO billing_subscriptions (`+subscriptionCols+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id) DO UPDATE SET
			provider = EXCLUDED.provider, customer_id = EXCLUDED.customer_id, subscription_id = EXCLUDED.subscription_id,
			item_id = EXCLUDED.item_id, plan = EXCLUDED.plan, billing_interval = EXCLUDED.billing_interval, status = EXCLUDED.status, renews_at = EXCLUDED.renews_at,
			ends_at = EXCLUDED.ends_at, test_mode = EXCLUDED.test_mode, provider_updated_at = EXCLUDED.provider_updated_at
		WHERE billing_subscriptions.provider_updated_at <= EXCLUDED.provider_updated_at`,
		sub.UserID, sub.Provider, sub.CustomerID, sub.SubscriptionID, sub.ItemID, sub.Plan, interval(sub.Interval), sub.Status,
		sub.RenewsAt, sub.EndsAt, sub.TestMode, sub.ProviderUpdatedAt.UTC())
	if err != nil {
		return false, nil, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET plan_type = $2 WHERE id = $1`, sub.UserID, plan); err != nil {
		return false, nil, mapErr(err)
	}
	rows, err := tx.Query(ctx, `SELECT slug FROM endpoints WHERE user_id = $1`, sub.UserID)
	if err != nil {
		return false, nil, err
	}
	slugs, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return false, nil, err
	}
	return true, slugs, tx.Commit(ctx)
}

// interval stores "" (callers that predate yearly billing) as "month".
func interval(v string) string {
	if v == "" {
		return "month"
	}
	return v
}

func subscriptionInEffect(status string, endsAt *time.Time) bool {
	switch status {
	case "active", "trialing", "past_due":
		return true
	case "cancelled":
		return endsAt != nil && endsAt.After(time.Now())
	}
	return false
}

// UsageRow is the monthly usage of an account with a metered subscription.
type UsageRow struct {
	UserID        string
	PeriodStart   time.Time // first day of the month (UTC)
	Requests      int64
	UnitsReported int64
	Included      int64 // included_requests
	Subscription  Subscription
}

// PendingUsage lists the usage of the given months for accounts whose
// subscription has a metered line (item_id) and is still billable.
func (s *Store) PendingUsage(ctx context.Context, provider string, periods []time.Time) ([]UsageRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.user_id, u.period_start, u.requests, u.units_reported, u.included_requests,
		       b.user_id, b.provider, b.customer_id, b.subscription_id, b.item_id, b.plan, b.billing_interval, b.status, b.renews_at, b.ends_at, b.test_mode, b.provider_updated_at
		FROM usage_counters u
		JOIN billing_subscriptions b ON b.user_id = u.user_id
		WHERE b.provider = $1 AND b.item_id <> '' AND b.status IN ('active', 'trialing', 'past_due', 'cancelled')
		  AND u.period_start = ANY($2) AND u.requests > 0
		ORDER BY u.period_start, u.user_id`, provider, periods)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRow, error) {
		var u UsageRow
		b := &u.Subscription
		err := r.Scan(&u.UserID, &u.PeriodStart, &u.Requests, &u.UnitsReported, &u.Included,
			&b.UserID, &b.Provider, &b.CustomerID, &b.SubscriptionID, &b.ItemID, &b.Plan, &b.Interval, &b.Status, &b.RenewsAt, &b.EndsAt, &b.TestMode, &b.ProviderUpdatedAt)
		return u, err
	})
}

// MarkUsageReported moves units_reported from `from` to `to` for one period
// (compare-and-set: false when another runner reported first).
func (s *Store) MarkUsageReported(ctx context.Context, userID string, periodStart time.Time, from, to int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE usage_counters SET units_reported = $4, updated_at = now()
		WHERE user_id = $1 AND period_start = $2 AND units_reported = $3`, userID, periodStart, from, to)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseUsage subtracts units from units_reported (claims that were not
// billed). Subtracting, not setting a value, keeps claims other runners made
// meanwhile.
func (s *Store) ReleaseUsage(ctx context.Context, userID string, periodStart time.Time, units int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE usage_counters SET units_reported = GREATEST(units_reported - $3, 0), updated_at = now()
		WHERE user_id = $1 AND period_start = $2`, userID, periodStart, units)
	return err
}

// RaiseIncluded records the quota included in a period, never lowering it.
func (s *Store) RaiseIncluded(ctx context.Context, userID string, periodStart time.Time, requests int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO usage_counters (user_id, period_start, included_requests) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, period_start) DO UPDATE
		SET included_requests = EXCLUDED.included_requests, updated_at = now()
		WHERE usage_counters.included_requests < EXCLUDED.included_requests`, userID, periodStart, requests)
	if err != nil {
		var fk interface{ SQLState() string }
		if errors.As(err, &fk) && fk.SQLState() == "23503" {
			return nil // the account was deleted meanwhile
		}
	}
	return err
}

// RecordUsage stores the monthly request count of a user, never lowering it
// (Redis is the live counter; this is its durable copy for billing and history).
func (s *Store) RecordUsage(ctx context.Context, userID string, periodStart time.Time, requests int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO usage_counters (user_id, period_start, requests) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, period_start) DO UPDATE
		SET requests = GREATEST(usage_counters.requests, EXCLUDED.requests), updated_at = now()
		WHERE usage_counters.requests < EXCLUDED.requests`, userID, periodStart, requests)
	if err != nil {
		var fk interface{ SQLState() string }
		if errors.As(err, &fk) && fk.SQLState() == "23503" {
			return nil // the account was deleted meanwhile
		}
	}
	return err
}
