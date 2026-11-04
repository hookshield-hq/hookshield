package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func newCipher(t *testing.T, fill byte) *Cipher {
	t.Helper()
	c, err := New(bytes.Repeat([]byte{fill}, KeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	c := newCipher(t, 7)
	plain := []byte(`{"type":"invoice.paid"}`)
	aad := []byte("payload:123:original")

	ct, nonce, err := c.Seal(plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if len(nonce) != NonceSize {
		t.Fatalf("nonce size %d", len(nonce))
	}
	got, err := c.Open(ct, nonce, aad)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip failed: %q, %v", got, err)
	}
	if c.KeyVersion() != 1 {
		t.Fatalf("key version %d", c.KeyVersion())
	}
}

func TestOpenRejects(t *testing.T) {
	c := newCipher(t, 7)
	aad := []byte("payload:1:original")
	ct, nonce, _ := c.Seal([]byte("secret"), aad)

	tampered := append([]byte(nil), ct...)
	tampered[0] ^= 0xff
	badNonce := append([]byte(nil), nonce...)
	badNonce[0] ^= 0xff

	cases := map[string]func() ([]byte, error){
		"wrong aad":     func() ([]byte, error) { return c.Open(ct, nonce, []byte("payload:2:original")) },
		"tampered":      func() ([]byte, error) { return c.Open(tampered, nonce, aad) },
		"wrong nonce":   func() ([]byte, error) { return c.Open(ct, badNonce, aad) },
		"short nonce":   func() ([]byte, error) { return c.Open(ct, nonce[:4], aad) },
		"wrong key":     func() ([]byte, error) { return newCipher(t, 9).Open(ct, nonce, aad) },
		"empty message": func() ([]byte, error) { return c.Open(nil, nonce, aad) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := fn(); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("want ErrDecrypt, got %v", err)
			}
		})
	}
}

func TestNoncesAreUnique(t *testing.T) {
	c := newCipher(t, 1)
	seen := map[string]bool{}
	for i := 0; i < 10_000; i++ {
		_, n, err := c.Seal([]byte("x"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(n)] {
			t.Fatal("nonce reused")
		}
		seen[string(n)] = true
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(make([]byte, 16), 1); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
	if _, err := New(make([]byte, KeySize), 0); err == nil {
		t.Fatal("expected error for version 0")
	}
}

func BenchmarkSeal4KB(b *testing.B) {
	c, _ := New(make([]byte, KeySize), 1)
	msg := bytes.Repeat([]byte("a"), 4096)
	b.SetBytes(int64(len(msg)))
	for b.Loop() {
		if _, _, err := c.Seal(msg, []byte("aad")); err != nil {
			b.Fatal(err)
		}
	}
}
