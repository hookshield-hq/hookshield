// Package core is HookShield's public extension API.
//
// The open-source core runs without commercial restrictions (self-hosted).
// HookShield Cloud, built in a separate repository, imports this package and
// github.com/hookshield-hq/hookshield/backend/app, and injects its own
// implementations (plans and quotas, billing routes). Go forbids importing
// internal/ packages from another module, so every extension point lives here.
//
// Rule for contributors: a capability that the cloud may restrict or bill is
// an interface in this package with a self-hosted default, never an import of a
// payment provider.
package core

import "time"

// Unlimited marks a limit that does not apply.
const Unlimited int64 = -1

// DefaultRetention is how long payload logs are kept on self-hosted installs
// unless HS_RETENTION says otherwise.
const DefaultRetention = 30 * 24 * time.Hour

// Limits are the restrictions that apply to one account.
type Limits struct {
	// MonthlyRequests included per calendar month (UTC), or Unlimited.
	MonthlyRequests int64
	// HardLimit rejects requests above MonthlyRequests with 429 quota_exceeded.
	// When false, extra requests are accepted (billed as overage by the cloud).
	HardLimit bool
	// MaxRetries caps the max_retries of the account's endpoints (int(Unlimited) = no cap).
	MaxRetries int
	// Retention is how long payload logs are kept.
	Retention time.Duration
}

// Exceeded reports whether a request that brings the monthly count to n must be rejected.
func (l Limits) Exceeded(n int64) bool {
	return l.HardLimit && l.MonthlyRequests != Unlimited && n > l.MonthlyRequests
}

// User is the signed-in dashboard user, as seen by extensions.
type User struct {
	ID            string
	Email         string
	Name          string
	Locale        string // en | pt-BR | es
	PlanType      string // users.plan_type
	EmailVerified bool
}

// Plans resolves the limits of an account from its plan identifier
// (users.plan_type). It is called on hot paths: implementations must not do I/O.
type Plans interface {
	For(plan string) Limits
}

// SelfHosted is the default Plans: no quota and no retry cap, whatever the
// plan column says. Retention <= 0 means DefaultRetention.
type SelfHosted struct {
	Retention time.Duration
}

// For implements Plans.
func (s SelfHosted) For(string) Limits {
	r := s.Retention
	if r <= 0 {
		r = DefaultRetention
	}
	return Limits{MonthlyRequests: Unlimited, MaxRetries: int(Unlimited), Retention: r}
}

// StaticPlans is a Plans backed by a fixed table. Unknown plans get the limits
// of Fallback, so a typo or a stale value fails closed.
type StaticPlans struct {
	Table    map[string]Limits
	Fallback string
}

// For implements Plans.
func (p StaticPlans) For(plan string) Limits {
	if l, ok := p.Table[plan]; ok {
		return l
	}
	return p.Table[p.Fallback]
}

// OrSelfHosted returns p, or SelfHosted{Retention: retention} when p is nil.
func OrSelfHosted(p Plans, retention time.Duration) Plans {
	if p == nil {
		return SelfHosted{Retention: retention}
	}
	return p
}
