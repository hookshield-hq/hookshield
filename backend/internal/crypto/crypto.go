// Package crypto implements HookShield's encryption at rest: AES-256-GCM with
// a random 12-byte nonce per message and caller-supplied additional
// authenticated data (AAD).
//
// AAD binds a ciphertext to its context (for example "payload:<log id>:original"),
// so a ciphertext copied into another row or field fails to decrypt instead of
// being silently accepted.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// NonceSize is the GCM nonce length in bytes.
const NonceSize = 12

// ErrDecrypt is returned when a ciphertext cannot be authenticated
// (wrong key, wrong AAD, wrong nonce or tampered data).
var ErrDecrypt = errors.New("crypto: message authentication failed")

// Cipher seals and opens messages with one master key.
type Cipher struct {
	aead    cipher.AEAD
	version int16
}

// New returns a Cipher for a 32-byte key. version identifies the key
// generation and is stored next to ciphertexts to support rotation.
func New(key []byte, version int16) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeySize, len(key))
	}
	if version < 1 {
		return nil, errors.New("crypto: key version must be >= 1")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, version: version}, nil
}

// KeyVersion returns the key generation of this cipher.
func (c *Cipher) KeyVersion() int16 { return c.version }

// Seal encrypts plaintext and returns the ciphertext (with the GCM tag
// appended) and the freshly generated nonce.
func (c *Cipher) Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return c.aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

// Open decrypts and authenticates a ciphertext produced by Seal.
func (c *Cipher) Open(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, ErrDecrypt
	}
	pt, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
