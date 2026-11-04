// Package retrypolicy decides whether and when a failed delivery is retried.
//
// Vocabulary: an "attempt" is one HTTP request to the destination. The first
// delivery is attempt 1; max_retries is the number of additional attempts, so
// an endpoint with max_retries = 5 is tried at most 6 times.
package retrypolicy

import (
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Strategies (endpoints.retry_strategy).
const (
	Fixed       = "fixed"
	Exponential = "exponential"
	Custom      = "custom"
)

// Policy is an endpoint's retry configuration, already capped by its plan.
type Policy struct {
	Strategy      string
	MaxRetries    int
	Initial       time.Duration
	Multiplier    float64
	MaxDelay      time.Duration
	Schedule      []time.Duration // for Custom
	JitterPercent float64         // ±percentage applied to every delay (default 0)
}

// CapRetries limits the endpoint's max_retries to what the plan allows
// (planMax < 0 means no plan limit).
func CapRetries(endpointMax, planMax int) int {
	if planMax >= 0 && endpointMax > planMax {
		return planMax
	}
	if endpointMax < 0 {
		return 0
	}
	return endpointMax
}

// MaxAttempts is the total number of attempts (first delivery + retries).
func (p Policy) MaxAttempts() int { return 1 + p.MaxRetries }

// ShouldRetry reports whether another attempt follows failed attempt n (1-based).
func (p Policy) ShouldRetry(n int) bool { return n < p.MaxAttempts() }

// Delay returns the wait before the attempt that follows failed attempt n
// (1-based). retryAfter, when positive, is the destination's Retry-After hint:
// it is honoured if longer than the computed delay, never beyond MaxDelay.
func (p Policy) Delay(n int, retryAfter time.Duration, rng *rand.Rand) time.Duration {
	if n < 1 {
		n = 1
	}
	var d time.Duration
	switch p.Strategy {
	case Fixed:
		d = p.Initial
	case Custom:
		if len(p.Schedule) == 0 {
			d = p.Initial
		} else if n-1 < len(p.Schedule) {
			d = p.Schedule[n-1]
		} else {
			d = p.Schedule[len(p.Schedule)-1]
		}
	default: // exponential
		mult := p.Multiplier
		if mult < 1 {
			mult = 1
		}
		f := float64(p.Initial) * math.Pow(mult, float64(n-1))
		if f > float64(math.MaxInt64/2) {
			f = float64(math.MaxInt64 / 2)
		}
		d = time.Duration(f)
	}

	if p.JitterPercent > 0 && rng != nil {
		spread := float64(d) * p.JitterPercent / 100
		d += time.Duration((rng.Float64()*2 - 1) * spread)
	}
	if retryAfter > d {
		d = retryAfter
	}
	if p.MaxDelay > 0 && d > p.MaxDelay {
		d = p.MaxDelay
	}
	if d < time.Second {
		d = time.Second
	}
	return d
}

// Success reports whether an HTTP status counts as delivered (2xx).
func Success(status int) bool { return status >= 200 && status < 300 }

// ParseRetryAfter reads a Retry-After header (seconds or HTTP date).
func ParseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
