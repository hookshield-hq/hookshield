package password

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected PHC string %q", h)
	}
	if ok, err := Verify(h, "correct horse battery staple"); !ok || err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if ok, _ := Verify(h, "correct horse battery stapl"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := Hash("correct horse battery staple")
	if h == h2 {
		t.Fatal("salt must make hashes unique")
	}
	if NeedsRehash(h) {
		t.Fatal("fresh hash must not need a rehash")
	}
}

func TestWeakParamsNeedRehash(t *testing.T) {
	weak, _ := hashWith("long enough password", Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32})
	if ok, err := Verify(weak, "long enough password"); !ok || err != nil {
		t.Fatal("hashes with other params must still verify")
	}
	if !NeedsRehash(weak) {
		t.Fatal("weak hash should need a rehash")
	}
}

func TestMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=99999999,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!$aGFzaA"} {
		if _, err := Verify(h, "x"); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: want ErrMalformed, got %v", h, err)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]struct {
		pw, email string
		want      error
	}{
		"ok":              {"a-long-passphrase", "a@b.co", nil},
		"short":           {"short", "", ErrTooShort},
		"unicode counted": {"ççççççççç", "", ErrTooShort}, // 9 runes
		"too long":        {strings.Repeat("x", MaxLength+1), "", ErrTooLong},
		"same as email":   {"Dev@Example.com", "dev@example.com", ErrSameAsEmail},
	}
	for name, c := range cases {
		if got := Validate(c.pw, c.email); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestVerifyDummyRuns(t *testing.T) {
	VerifyDummy("anything") // must not panic
}
