package auth

import (
	"net/netip"
	"testing"
)

func TestThrottleIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":              "203.0.113.7",
		"::ffff:203.0.113.7":       "203.0.113.7",
		"2001:db8:1:2:aaaa::1":     "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::": "2001:db8:1:2::/64", // same /64 = same bucket
	} {
		if got := throttleIP(netip.MustParseAddr(in)); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}
