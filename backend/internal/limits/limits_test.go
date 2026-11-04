package limits

import (
	"testing"
	"time"
)

func TestPeriodIsUTCMonth(t *testing.T) {
	sp := time.FixedZone("BRT", -3*3600)
	// 21:30 on Nov 30 in São Paulo is already December in UTC.
	if got := Period(time.Date(2026, 11, 30, 21, 30, 0, 0, sp)); got != "202612" {
		t.Fatalf("got %s", got)
	}
}

func TestDecisionString(t *testing.T) {
	for d, want := range map[Decision]string{Allowed: "allowed", RateLimited: "rate_limited", QuotaExceeded: "quota_exceeded"} {
		if d.String() != want {
			t.Errorf("%d: %s", d, d.String())
		}
	}
}
