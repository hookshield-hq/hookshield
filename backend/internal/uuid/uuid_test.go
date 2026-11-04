package uuid

import (
	"regexp"
	"testing"
	"time"
)

var v7RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestFormatVersionVariant(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if id := NewV7(); !v7RE.MatchString(id) {
			t.Fatalf("invalid UUIDv7: %s", id)
		}
	}
}

func TestTimeOrdered(t *testing.T) {
	base := time.UnixMilli(1_700_000_000_000)
	a := newV7At(base)
	b := newV7At(base.Add(time.Millisecond))
	if !(a < b) {
		t.Fatalf("expected %s < %s", a, b)
	}
	if a[:8] != "018bcfe5" {
		t.Fatalf("timestamp not encoded big-endian: %s", a)
	}
}

func TestUnique(t *testing.T) {
	seen := make(map[string]bool, 100_000)
	for i := 0; i < 100_000; i++ {
		id := NewV7()
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
}
