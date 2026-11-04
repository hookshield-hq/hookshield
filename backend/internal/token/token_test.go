package token

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	a, err := Generate(PrefixInbound)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(PrefixInbound)
	if a == b {
		t.Fatal("tokens must be unique")
	}
	if !strings.HasPrefix(a, PrefixInbound) || len(a) != len(PrefixInbound)+43 {
		t.Fatalf("unexpected token format %q (len %d)", a, len(a))
	}
	if strings.ContainsAny(a[len(PrefixInbound):], "+/=") {
		t.Fatalf("token must be URL-safe: %q", a)
	}
}

func TestVerify(t *testing.T) {
	tok, _ := Generate(PrefixInbound)
	h := Hash(tok)

	if !Verify(h, tok) {
		t.Fatal("valid token rejected")
	}
	for name, candidate := range map[string]string{
		"other token": tok + "x",
		"empty":       "",
		"prefix only": PrefixInbound,
	} {
		if Verify(h, candidate) {
			t.Errorf("%s accepted", name)
		}
	}
	if Verify(h[:10], tok) {
		t.Error("short hash accepted")
	}
	if Verify(nil, tok) {
		t.Error("nil hash accepted")
	}
}

func TestDisplayPrefix(t *testing.T) {
	cases := map[string]string{
		"hs_in_ABCDEFG":    "hs_in_ABCD",
		"hs_live_xyz12345": "hs_live_xyz1",
		"abcdefgh":         "abcd",
		"ab":               "",
	}
	for in, want := range cases {
		if got := DisplayPrefix(in); got != want {
			t.Errorf("DisplayPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
