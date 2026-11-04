// Package testplans provides a plan table for tests of plan-dependent behavior
// (quotas, retry caps, retention). It mirrors the commercial tiers of the
// blueprint, which the open-source core does not ship: self-hosted installs use
// core.SelfHosted, and HookShield Cloud defines the real table.
package testplans

import (
	"time"

	"github.com/hookshield-hq/hookshield/backend/core"
)

// Plan identifiers used by the tests (and by users.plan_type).
const (
	Developer  = "developer"
	Pro        = "pro"
	Business   = "business"
	Enterprise = "enterprise"
)

// Blueprint has the four tiers of the blueprint; unknown plans fall back to Developer.
var Blueprint = core.StaticPlans{Fallback: Developer, Table: map[string]core.Limits{
	Developer:  {MonthlyRequests: 5_000, HardLimit: true, MaxRetries: 3, Retention: 24 * time.Hour},
	Pro:        {MonthlyRequests: 100_000, MaxRetries: 10, Retention: 30 * 24 * time.Hour},
	Business:   {MonthlyRequests: 1_000_000, MaxRetries: int(core.Unlimited), Retention: 90 * 24 * time.Hour},
	Enterprise: {MonthlyRequests: core.Unlimited, MaxRetries: int(core.Unlimited), Retention: 365 * 24 * time.Hour},
}}
