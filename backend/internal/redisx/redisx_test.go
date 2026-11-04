package redisx

import "testing"

func TestKeys(t *testing.T) {
	k := NewKeys("")
	cases := map[string]string{
		k.EndpointCache("ep_1"):           "hs:cache:ep:ep_1",
		k.IngestStream():                  "hs:stream:ingest",
		k.DispatchStream():                "hs:stream:dispatch",
		k.RetrySchedule():                 "hs:zset:retry",
		k.DeadLetterStream():              "hs:stream:dlq",
		k.MonthlyUsage("u1", "202611"):    "hs:usage:u1:202611",
		k.RateWindow("e1", 1700000000):    "hs:rl:e1:1700000000",
		k.AlertStream():                   "hs:stream:alerts",
		k.AuthFailures("ip:1.2.3.4"):      "hs:auth:fail:ip:1.2.3.4",
		k.AuthMailCooldown("reset", "u1"): "hs:auth:mail:reset:u1",
		k.OAuthState("abc"):               "hs:oauth:abc",
		k.AlertCooldown("e1"):             "hs:alert:cooldown:e1",
		k.AlertSuppressed("e1"):           "hs:alert:suppressed:e1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if NewKeys("test:").IngestStream() != "test:stream:ingest" {
		t.Error("custom prefix not applied")
	}
}

func TestParseUsageKey(t *testing.T) {
	k := NewKeys("hs:")
	user, period, ok := k.ParseUsageKey(k.MonthlyUsage("0193-abc", "202611"))
	if !ok || user != "0193-abc" || period != "202611" {
		t.Fatalf("round trip: %q %q %v", user, period, ok)
	}
	for _, bad := range []string{"hs:usage:", "hs:usage:x", "hs:usage::202611", "hs:usage:x:", "other:usage:x:202611", "hs:rate:x:1"} {
		if _, _, ok := k.ParseUsageKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
