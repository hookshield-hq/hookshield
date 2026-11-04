// Package password hashes and verifies dashboard passwords with argon2id
// (OWASP's first recommendation), stored in the standard PHC string format:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt b64>$<hash b64>
//
// The parameters live in the hash, so they can be raised later: NeedsRehash
// tells the login flow to upgrade old hashes transparently.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// Default follows the OWASP recommendation (m=64 MiB, t=3, p=2 is above the minimum).
var Default = Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// Policy limits.
const (
	MinLength = 10
	MaxLength = 256 // bytes; bounds hashing cost for absurd inputs
)

// Validation errors (mapped to API error codes).
var (
	ErrTooShort    = errors.New("password.too_short")
	ErrTooLong     = errors.New("password.too_long")
	ErrSameAsEmail = errors.New("password.same_as_email")
	ErrMalformed   = errors.New("password: malformed hash")
)

// Validate checks the password policy. email is used to reject passwords equal to it.
func Validate(pw, email string) error {
	if utf8.RuneCountInString(pw) < MinLength {
		return ErrTooShort
	}
	if len(pw) > MaxLength {
		return ErrTooLong
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(pw), strings.TrimSpace(email)) {
		return ErrSameAsEmail
	}
	return nil
}

// slots caps concurrent argon2 computations: each needs Default.Memory (64 MiB),
// so a burst of logins must queue instead of exhausting the server's memory.
var slots = make(chan struct{}, max(2, runtime.GOMAXPROCS(0)))

func idKey(pw, salt []byte, t, m uint32, threads uint8, n uint32) []byte {
	slots <- struct{}{}
	defer func() { <-slots }()
	return argon2.IDKey(pw, salt, t, m, threads, n)
}

// Hash returns the PHC string for pw.
func Hash(pw string) (string, error) { return hashWith(pw, Default) }

func hashWith(pw string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := idKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches the PHC hash (constant-time comparison).
func Verify(hash, pw string) (bool, error) {
	p, salt, key, err := decode(hash)
	if err != nil {
		return false, err
	}
	got := idKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// NeedsRehash reports whether hash was produced with weaker parameters than Default.
func NeedsRehash(hash string) bool {
	p, _, _, err := decode(hash)
	if err != nil {
		return true
	}
	return p.Memory < Default.Memory || p.Time < Default.Time || p.Threads < Default.Threads
}

// dummyHash is verified when an account does not exist, so a login for an
// unknown email takes as long as one for a known email (no user enumeration
// through timing).
var dummyHash, _ = Hash("hookshield-dummy-password")

// VerifyDummy burns the same CPU time as Verify on a real account.
func VerifyDummy(pw string) { _, _ = Verify(dummyHash, pw) }

func decode(hash string) (Params, []byte, []byte, error) {
	parts := strings.Split(hash, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrMalformed
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		p.Memory == 0 || p.Time == 0 || p.Threads == 0 || p.Memory > 1<<22 || p.Time > 64 {
		return Params{}, nil, nil, ErrMalformed
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	key, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(key) < 16 {
		return Params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}
