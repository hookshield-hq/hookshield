// Package token generates and verifies HookShield secrets (inbound endpoint
// tokens, API keys, session tokens).
//
// Tokens carry 256 bits of randomness, so a single SHA-256 is a safe and fast
// storage hash (no slow KDF needed — that is only required for low-entropy
// passwords). Only the hash is persisted; the plaintext is shown to the user once.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

// Known prefixes. They make leaked secrets recognisable (e.g. by secret scanners).
const (
	PrefixInbound = "hs_in_"   // endpoint inbound token
	PrefixLive    = "hs_live_" // control-plane API key
	PrefixSession = "hs_sess_" // dashboard session cookie
)

// randomBytes is the entropy of a token (256 bits).
const randomBytes = 32

// visibleChars is how many random characters DisplayPrefix keeps after the prefix.
const visibleChars = 4

// Generate returns prefix + 43 URL-safe base64 characters of randomness.
func Generate(prefix string) (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash returns the SHA-256 digest stored in the database.
func Hash(tok string) []byte {
	sum := sha256.Sum256([]byte(tok))
	return sum[:]
}

// Verify reports whether tok matches the stored hash, in constant time.
func Verify(hash []byte, tok string) bool {
	if len(hash) != sha256.Size || tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(hash, sum[:]) == 1
}

// DisplayPrefix returns the non-secret part shown in the UI, e.g. "hs_in_8f3a".
func DisplayPrefix(tok string) string {
	for _, p := range []string{PrefixInbound, PrefixLive, PrefixSession} {
		if strings.HasPrefix(tok, p) && len(tok) >= len(p)+visibleChars {
			return tok[:len(p)+visibleChars]
		}
	}
	if len(tok) > visibleChars {
		return tok[:visibleChars]
	}
	return ""
}
