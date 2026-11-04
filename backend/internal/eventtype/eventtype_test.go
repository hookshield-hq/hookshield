package eventtype

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string][]string
		body    string
		want    string
	}{
		{"github header", map[string][]string{"X-GitHub-Event": {"push"}}, `{"type":"ignored"}`, "push"},
		{"shopify header lowercase", map[string][]string{"x-shopify-topic": {"orders/create"}}, ``, "orders/create"},
		{"stripe body type", nil, `{"id":"evt_1","type":"invoice.paid"}`, "invoice.paid"},
		{"event_type field", nil, `{"event_type":"user.created"}`, "user.created"},
		{"event field", nil, `{"event":"charge"}`, "charge"},
		{"non-string type ignored", nil, `{"type":5,"event":"x"}`, "x"},
		{"blank header falls back to body", map[string][]string{"X-Event-Type": {"  "}}, `{"type":"a"}`, "a"},
		{"not json", nil, `type=invoice.paid`, ""},
		{"json array", nil, `[{"type":"a"}]`, ""},
		{"nothing", nil, `{"id":1}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.headers, []byte(tc.body)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClipKeepsValidUTF8(t *testing.T) {
	long := strings.Repeat("é", 150) // 300 bytes
	got := Detect(nil, []byte(`{"type":"`+long+`"}`))
	if len(got) > MaxLen || !utf8.ValidString(got) {
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

// Regression (security review): a sender-chosen type with control characters
// made every delivery fail (invalid X-HookShield-Event header).
func TestControlCharactersAreReplaced(t *testing.T) {
	if got := Detect(nil, []byte(`{"type":"invoice.paid\r\nX-Evil: 1"}`)); got != "invoice.paid  X-Evil: 1" {
		t.Fatalf("got %q", got)
	}
	if got := Detect(nil, []byte(`{"type":"\u0000\u0007"}`)); got != "" {
		t.Fatalf("only control characters: %q", got)
	}
}
