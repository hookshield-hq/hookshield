// Package keyboot makes sure every HookShield service runs with the same
// encryption key, and gives fresh installs one automatically.
//
// The key comes from HS_ENCRYPTION_KEY, or from the file HS_ENCRYPTION_KEY_FILE.
// When neither holds a key and the database is fresh (no account, no stored
// check value), the first service to start generates a random key into the
// file — atomically, so services starting together end up with the same key —
// and logs a warning to back it up.
//
// The database keeps a value encrypted with the key (system_settings,
// "encryption_key_check"). A service whose key cannot open it refuses to
// start: a wrong or regenerated key never silently writes data that the other
// services (or the rest of the database) cannot decrypt.
package keyboot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
)

const (
	checkName = "encryption_key_check"
	checkText = "hookshield encryption key check v1"
)

var checkAAD = []byte("system:" + checkName)

// ErrKeyMismatch means the configured key is not the one this database uses.
var ErrKeyMismatch = errors.New("encryption key does not match this database")

// Ensure returns the encryption key for this install. key and file are
// cfg.EncryptionKey and cfg.EncryptionKeyFile; see the package doc.
func Ensure(ctx context.Context, pool *pgxpool.Pool, key []byte, file string, log *slog.Logger) ([]byte, error) {
	if len(key) == 0 {
		if file == "" {
			return nil, errors.New("HS_ENCRYPTION_KEY is required (generate with: openssl rand -base64 32), or set HS_ENCRYPTION_KEY_FILE to have it generated on first start")
		}
		// A service starting next to this one may have created the file
		// after this process read its configuration.
		if k, _, err := readKeyFile(file); err == nil {
			return checked(ctx, pool, k)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		fresh, err := isFresh(ctx, pool)
		if err != nil {
			return nil, err
		}
		if !fresh {
			if k, _, err := readKeyFile(file); err == nil { // created between the two checks
				return checked(ctx, pool, k)
			}
			return nil, fmt.Errorf("no encryption key: HS_ENCRYPTION_KEY is empty and %s does not exist, but this database already holds data encrypted with a key. "+
				"Restore the key file from your backup or set HS_ENCRYPTION_KEY; a new key could not decrypt the existing data", file)
		}
		var created bool
		key, created, err = createKeyFile(file)
		if err != nil {
			return nil, fmt.Errorf("generate encryption key in %s: %w", file, err)
		}
		if created {
			log.Warn("generated a new encryption key — BACK IT UP NOW: without it the stored webhooks, secrets and alert targets cannot be decrypted",
				"file", file)
		}
	}
	return checked(ctx, pool, key)
}

func checked(ctx context.Context, pool *pgxpool.Pool, key []byte) ([]byte, error) {
	if err := verify(ctx, pool, key); err != nil {
		return nil, err
	}
	return key, nil
}

// isFresh reports whether the database has no account and no key check yet.
func isFresh(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var used bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM system_settings WHERE name = $1)`, checkName).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("check for existing data: %w", err)
	}
	return !used, nil
}

// verify stores the check value on first use and opens it afterwards.
func verify(ctx context.Context, pool *pgxpool.Pool, key []byte) error {
	c, err := crypto.New(key, 1)
	if err != nil {
		return err
	}
	ct, nonce, err := c.Seal([]byte(checkText), checkAAD)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO system_settings (name, value) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`,
		checkName, append(nonce, ct...)); err != nil {
		return fmt.Errorf("store encryption key check: %w", err)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE name = $1`, checkName).Scan(&stored); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("encryption key check disappeared while starting; retry")
		}
		return fmt.Errorf("read encryption key check: %w", err)
	}
	if len(stored) < crypto.NonceSize {
		return fmt.Errorf("%w: stored check value is corrupt", ErrKeyMismatch)
	}
	if pt, err := c.Open(stored[crypto.NonceSize:], stored[:crypto.NonceSize], checkAAD); err != nil || string(pt) != checkText {
		return fmt.Errorf("%w: the database was set up with another HS_ENCRYPTION_KEY. Use that key (check every service and the key file) — "+
			"starting with a different one would make stored data unreadable", ErrKeyMismatch)
	}
	return nil
}

// createKeyFile writes a new random key to path unless the file already
// exists (another service won the race), and returns the key in the file.
// The file appears complete or not at all: it is written to a temporary file
// and hard-linked into place.
func createKeyFile(path string) (key []byte, created bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	key = make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	content := []byte(base64.StdEncoding.EncodeToString(key) + "\n")

	tmp, err := os.CreateTemp(filepath.Dir(path), ".encryption-key-*")
	if err != nil {
		return nil, false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return nil, false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, false, err
	}
	if err := tmp.Close(); err != nil {
		return nil, false, err
	}

	switch err := os.Link(tmpName, path); {
	case err == nil:
		syncDir(filepath.Dir(path))
		return key, true, nil
	case errors.Is(err, os.ErrExist):
		return readKeyFile(path)
	default:
		// Filesystems without hard links: exclusive create (content written right after).
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return readKeyFile(path)
		}
		if err != nil {
			return nil, false, err
		}
		if _, err := f.Write(content); err != nil {
			f.Close()
			return nil, false, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, false, err
		}
		return key, true, f.Close()
	}
}

func readKeyFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	key, err := config.ParseKey(string(raw))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return key, false, nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}
