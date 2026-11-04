package retrypolicy

import (
	"math/rand/v2"
	"net/http"
	"testing"
	"time"
)

func TestExponentialBlueprintDefaults(t *testing.T) {
	// Defaults from the schema: 10 s initial, 2x, capped at 1 hour.
	p := Policy{Strategy: Exponential, MaxRetries: 5, Initial: 10 * time.Second, Multiplier: 2, MaxDelay: time.Hour}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second}
	for i, w := range want {
		if got := p.Delay(i+1, 0, nil); got != w {
			t.Errorf("after attempt %d: got %v, want %v", i+1, got, w)
		}
	}
	if got := p.Delay(20, 0, nil); got != time.Hour {
		t.Errorf("must cap at MaxDelay, got %v", got)
	}
	if got := p.Delay(500, 0, nil); got != time.Hour {
		t.Errorf("huge attempt numbers must not overflow, got %v", got)
	}
}

func TestFixedAndCustom(t *testing.T) {
	fixed := Policy{Strategy: Fixed, Initial: 30 * time.Second, MaxDelay: time.Hour}
	if fixed.Delay(1, 0, nil) != 30*time.Second || fixed.Delay(7, 0, nil) != 30*time.Second {
		t.Fatal("fixed delay must not grow")
	}
	custom := Policy{Strategy: Custom, Schedule: []time.Duration{5 * time.Second, time.Minute, 10 * time.Minute}, MaxDelay: time.Hour}
	for n, want := range map[int]time.Duration{1: 5 * time.Second, 2: time.Minute, 3: 10 * time.Minute, 9: 10 * time.Minute} {
		if got := custom.Delay(n, 0, nil); got != want {
			t.Errorf("custom after %d: %v, want %v", n, got, want)
		}
	}
}

func TestRetryAfterAndBounds(t *testing.T) {
	p := Policy{Strategy: Fixed, Initial: 10 * time.Second, MaxDelay: time.Minute}
	if got := p.Delay(1, 30*time.Second, nil); got != 30*time.Second {
		t.Errorf("longer Retry-After must win: %v", got)
	}
	if got := p.Delay(1, 2*time.Second, nil); got != 10*time.Second {
		t.Errorf("shorter Retry-After must not shorten: %v", got)
	}
	if got := p.Delay(1, time.Hour, nil); got != time.Minute {
		t.Errorf("Retry-After capped by MaxDelay: %v", got)
	}
	if got := (Policy{Strategy: Fixed}).Delay(1, 0, nil); got != time.Second {
		t.Errorf("minimum delay is 1s: %v", got)
	}
}

func TestJitterStaysInRange(t *testing.T) {
	p := Policy{Strategy: Fixed, Initial: 100 * time.Second, MaxDelay: time.Hour, JitterPercent: 10}
	rng := rand.New(rand.NewPCG(1, 2))
	seen := map[time.Duration]bool{}
	for i := 0; i < 1000; i++ {
		d := p.Delay(1, 0, rng)
		if d < 90*time.Second || d > 110*time.Second {
			t.Fatalf("jittered delay %v outside ±10%%", d)
		}
		seen[d] = true
	}
	if len(seen) < 100 {
		t.Fatal("jitter does not spread delays")
	}
}

func TestAttemptsAndPlanCap(t *testing.T) {
	p := Policy{MaxRetries: 5}
	if p.MaxAttempts() != 6 || !p.ShouldRetry(5) || p.ShouldRetry(6) {
		t.Fatal("max_retries=5 means 6 attempts in total")
	}
	if (Policy{MaxRetries: 0}).ShouldRetry(1) {
		t.Fatal("max_retries=0 means a single attempt")
	}
	cases := []struct{ endpoint, plan, want int }{{5, 3, 3}, {2, 3, 2}, {50, -1, 50}, {-1, 3, 0}}
	for _, c := range cases {
		if got := CapRetries(c.endpoint, c.plan); got != c.want {
			t.Errorf("CapRetries(%d,%d)=%d, want %d", c.endpoint, c.plan, got, c.want)
		}
	}
}

func TestSuccessAndRetryAfterParsing(t *testing.T) {
	for code, ok := range map[int]bool{200: true, 204: true, 299: true, 199: false, 301: false, 404: false, 500: false} {
		if Success(code) != ok {
			t.Errorf("Success(%d) = %v", code, !ok)
		}
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if ParseRetryAfter("120", now) != 2*time.Minute {
		t.Error("seconds form")
	}
	if got := ParseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now); got != 90*time.Second {
		t.Errorf("date form: %v", got)
	}
	for _, bad := range []string{"", "soon", "-5", "0"} {
		if ParseRetryAfter(bad, now) != 0 {
			t.Errorf("%q should be ignored", bad)
		}
	}
}
