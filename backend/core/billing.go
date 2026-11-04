package core

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Billing is a subscription provider (HookShield Cloud implements it with
// Paddle). The core owns the HTTP surface — /v1/billing routes,
// sessions, errors, translations — and the subscription records; an
// implementation only talks to its provider. Self-hosted installs have none:
// the billing routes are not mounted and GET /v1/features reports
// {"billing": false}.
type Billing interface {
	// Offers is the plan catalog shown on the billing page, cheapest first.
	Offers() []Offer
	// Checkout starts a subscription to req.Plan, or switches the current
	// one (req.Current) to it. A new subscription returns the provider's
	// checkout URL; a switch returns the updated subscription and no URL.
	Checkout(ctx context.Context, req CheckoutRequest) (CheckoutResult, error)
	// Portal returns a URL where the customer manages payment methods,
	// invoices and cancellation.
	Portal(ctx context.Context, user User, sub Subscription) (string, error)
	// HandleWebhook verifies and applies a provider notification (raw body
	// and headers), saving subscription changes through acct.
	HandleWebhook(ctx context.Context, body []byte, header http.Header, acct Accounts) error
}

// Offer is one plan of the catalog. Limits (quota, retries, retention) come
// from Plans.For(ID), so prices and limits cannot disagree.
type Offer struct {
	ID string // plan identifier, as in users.plan_type
	// MonthlyPriceCents is the base price per month; 0 = free.
	MonthlyPriceCents int64
	Currency          string // ISO 4217, e.g. "USD"
	// OveragePriceCents is charged per OverageUnit requests above the quota;
	// 0 = no overage (the quota is a hard limit or the plan is unlimited).
	OveragePriceCents int64
	OverageUnit       int64
	// Purchasable plans have a self-service checkout. The others (free plan,
	// enterprise) show ContactURL, when set.
	Purchasable bool
	ContactURL  string
}

// CheckoutRequest asks for a subscription to Plan.
type CheckoutRequest struct {
	User      User
	Plan      string
	ReturnURL string        // where the provider sends the customer back
	Current   *Subscription // the active subscription, if any
}

// CheckoutResult is either a checkout URL (new subscription) or the switched
// subscription (plan change, applied immediately).
type CheckoutResult struct {
	URL          string
	Subscription *Subscription
}

// Subscription statuses (provider statuses are mapped to these).
const (
	StatusActive    = "active"
	StatusTrialing  = "trialing"
	StatusPastDue   = "past_due"  // payment failed, provider still retrying
	StatusCancelled = "cancelled" // ends at EndsAt; usable until then
	StatusPaused    = "paused"
	StatusUnpaid    = "unpaid"
	StatusExpired   = "expired"
)

// Subscription is the billing state of an account (table billing_subscriptions).
type Subscription struct {
	UserID         string
	Provider       string // e.g. "paddle"
	CustomerID     string
	SubscriptionID string
	// ItemID is the provider's line the usage is reported to (metered billing).
	ItemID    string
	Plan      string // plan identifier the subscription grants
	Status    string // one of the Status constants
	RenewsAt  *time.Time
	EndsAt    *time.Time
	TestMode  bool
	UpdatedAt time.Time // provider's last change; older notifications are ignored
}

// EffectivePlan is the plan an account gets from its subscription: the
// subscribed plan while it can be used (active, trialing, past due — the
// provider is still collecting — or cancelled before EndsAt), otherwise free.
func (s Subscription) EffectivePlan(free string, now time.Time) string {
	switch s.Status {
	case StatusActive, StatusTrialing, StatusPastDue:
		return s.Plan
	case StatusCancelled:
		if s.EndsAt == nil || now.Before(*s.EndsAt) {
			return s.Plan
		}
	}
	return free
}

// Accounts is what a Billing implementation may change (implemented by the core).
type Accounts interface {
	// User returns an account by ID (ErrNotFound when it does not exist).
	User(ctx context.Context, id string) (User, error)
	// SubscriptionByProviderID finds the subscription the provider calls id.
	SubscriptionByProviderID(ctx context.Context, provider, id string) (Subscription, error)
	// SaveSubscription stores sub unless a newer change is already stored
	// (UpdatedAt), then sets the account's plan to sub.EffectivePlan and
	// drops the cached endpoint configuration, so quotas, retry caps and
	// retention follow immediately. It reports whether sub was applied.
	SaveSubscription(ctx context.Context, sub Subscription) (bool, error)
}

// Errors a Billing implementation returns; the core maps them to API error codes.
var (
	ErrNotFound         = errors.New("not found")                 // 404 billing.not_found
	ErrInvalidSignature = errors.New("invalid webhook signature") // 400 billing.invalid_signature
	ErrInvalidPayload   = errors.New("invalid webhook payload")   // 400 billing.invalid_payload
	ErrUnknownPlan      = errors.New("plan cannot be purchased")  // 422 billing.invalid_plan
	ErrNoSubscription   = errors.New("no subscription")           // 409 billing.no_subscription
	ErrProvider         = errors.New("billing provider error")    // 502 billing.provider_error
	// ErrSubscriptionConflict: SaveSubscription refused a subscription because
	// the account already has another one in effect (409 billing.subscription_conflict).
	// A provider should cancel (and refund) the duplicate.
	ErrSubscriptionConflict = errors.New("account already has a subscription in effect")
)

// UsageRecord is the monthly usage of an account with a metered subscription.
type UsageRecord struct {
	UserID      string
	PeriodStart time.Time // first day of the month, UTC
	Requests    int64     // accepted requests in the month
	// UnitsReported is how much of this month was already reported to the
	// provider, in the implementation's unit (e.g. blocks of 10,000 requests).
	UnitsReported int64
	// IncludedRequests is the largest monthly quota recorded for this period
	// (RaiseIncluded); 0 when none was recorded.
	IncludedRequests int64
	Subscription     Subscription
}

// UsageStore gives billing jobs the usage to report (implemented by the core,
// which copies the live Redis counters to PostgreSQL every minute).
type UsageStore interface {
	// PendingUsage lists the usage of the given months for accounts whose
	// subscription with provider has a metered line and is still billable.
	PendingUsage(ctx context.Context, provider string, periods []time.Time) ([]UsageRecord, error)
	// MarkUsageReported moves UnitsReported from `from` to `to`; false when
	// another runner changed it first (compare-and-set).
	MarkUsageReported(ctx context.Context, userID string, periodStart time.Time, from, to int64) (bool, error)
	// ReleaseUsage gives back units claimed with MarkUsageReported that were
	// not billed after all (UnitsReported -= units, never below 0), safe
	// against other runners that claimed more units meanwhile.
	ReleaseUsage(ctx context.Context, userID string, periodStart time.Time, units int64) error
	// RaiseIncluded records that the account's quota for the period is at
	// least requests (never lowers it), so a later downgrade does not turn
	// usage that was included into overage.
	RaiseIncluded(ctx context.Context, userID string, periodStart time.Time, requests int64) error
}
