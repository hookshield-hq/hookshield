package core

import (
	"testing"
	"time"
)

func TestSelfHostedHasNoLimits(t *testing.T) {
	l := SelfHosted{}.For("developer")
	if l.MonthlyRequests != Unlimited || l.MaxRetries != int(Unlimited) || l.Exceeded(1<<40) {
		t.Fatalf("self-hosted must be unlimited: %+v", l)
	}
	if l.Retention != DefaultRetention {
		t.Fatalf("default retention = %v", l.Retention)
	}
	if got := (SelfHosted{Retention: time.Hour}).For("").Retention; got != time.Hour {
		t.Fatalf("custom retention = %v", got)
	}
}

func TestStaticPlansFailsClosed(t *testing.T) {
	p := StaticPlans{Fallback: "free", Table: map[string]Limits{
		"free": {MonthlyRequests: 10, HardLimit: true, MaxRetries: 1, Retention: time.Hour},
		"paid": {MonthlyRequests: 100, MaxRetries: 5, Retention: 24 * time.Hour},
	}}
	if p.For("paid").MonthlyRequests != 100 {
		t.Fatal("known plan")
	}
	if p.For("hacker") != p.For("free") {
		t.Fatal("unknown plans must get the fallback limits")
	}
}

func TestExceeded(t *testing.T) {
	hard := Limits{MonthlyRequests: 5_000, HardLimit: true}
	if hard.Exceeded(5_000) || !hard.Exceeded(5_001) {
		t.Fatal("hard limit boundary")
	}
	if (Limits{MonthlyRequests: 10}).Exceeded(1_000) {
		t.Fatal("soft limits are billed, not rejected")
	}
	if (Limits{MonthlyRequests: Unlimited, HardLimit: true}).Exceeded(1 << 40) {
		t.Fatal("unlimited")
	}
}

func TestOrSelfHosted(t *testing.T) {
	if _, ok := OrSelfHosted(nil, time.Hour).(SelfHosted); !ok {
		t.Fatal("nil must become SelfHosted")
	}
	custom := StaticPlans{}
	if _, ok := OrSelfHosted(custom, 0).(StaticPlans); !ok {
		t.Fatal("custom plans must be kept")
	}
}

func TestEffectivePlan(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	for _, tc := range []struct {
		status string
		ends   *time.Time
		want   string
	}{
		{StatusActive, nil, "pro"},
		{StatusTrialing, nil, "pro"},
		{StatusPastDue, nil, "pro"}, // the provider is still collecting
		{StatusCancelled, &later, "pro"},
		{StatusCancelled, &earlier, "free"},
		{StatusCancelled, nil, "pro"},
		{StatusPaused, nil, "free"},
		{StatusUnpaid, nil, "free"},
		{StatusExpired, nil, "free"},
		{"unknown", nil, "free"},
	} {
		got := Subscription{Plan: "pro", Status: tc.status, EndsAt: tc.ends}.EffectivePlan("free", now)
		if got != tc.want {
			t.Errorf("%s (ends %v): %s, want %s", tc.status, tc.ends, got, tc.want)
		}
	}
}
