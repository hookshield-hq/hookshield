//go:build integration

package keyboot_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/keyboot"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
)

func TestFreshInstallGeneratesOneKeyForEveryService(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "keys", "encryption.key")
	var logs bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &mu}, nil))

	// api, ingest and worker start at the same time.
	keys := make([][]byte, 6)
	errs := make([]error, 6)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i], errs[i] = keyboot.Ensure(ctx, pool, nil, file, log)
		}(i)
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil {
			t.Fatalf("service %d: %v", i, errs[i])
		}
		if !bytes.Equal(keys[i], keys[0]) || len(keys[i]) != 32 {
			t.Fatalf("service %d got another key", i)
		}
	}
	if n := strings.Count(logs.String(), "BACK IT UP"); n != 1 {
		t.Fatalf("want exactly one backup warning, got %d:\n%s", n, logs.String())
	}
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v mode %v", err, st.Mode())
	}
	raw, _ := os.ReadFile(file)
	if k, err := config.ParseKey(string(raw)); err != nil || !bytes.Equal(k, keys[0]) {
		t.Fatalf("file content: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".encryption-key-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}

	// Restart: the file is read by config, Ensure just verifies.
	if k, err := keyboot.Ensure(ctx, pool, keys[0], file, quiet()); err != nil || !bytes.Equal(k, keys[0]) {
		t.Fatalf("restart: %v", err)
	}

	// Another key (another service misconfigured, or the file regenerated) is refused.
	_, err = keyboot.Ensure(ctx, pool, bytes.Repeat([]byte{7}, 32), "", quiet())
	if !errors.Is(err, keyboot.ErrKeyMismatch) {
		t.Fatalf("wrong key: %v", err)
	}

	// The key file is lost but the database is in use: never generate a new key.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	_, err = keyboot.Ensure(ctx, pool, nil, file, quiet())
	if err == nil || !strings.Contains(err.Error(), "Restore the key file") {
		t.Fatalf("lost file: %v", err)
	}
	if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
		t.Fatal("a new key file must not be written")
	}
}

func TestExistingInstallAdoptsItsKey(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ('old@example.com', 'x')`); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{3}, 32)

	// Without a key and with accounts present, nothing is generated.
	file := filepath.Join(t.TempDir(), "encryption.key")
	if _, err := keyboot.Ensure(ctx, pool, nil, file, quiet()); err == nil {
		t.Fatal("expected an error for a used database without key")
	}
	// An install from before the check value existed records it on its next start.
	if _, err := keyboot.Ensure(ctx, pool, key, "", quiet()); err != nil {
		t.Fatal(err)
	}
	if _, err := keyboot.Ensure(ctx, pool, bytes.Repeat([]byte{4}, 32), "", quiet()); !errors.Is(err, keyboot.ErrKeyMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
	if _, err := keyboot.Ensure(ctx, pool, nil, "", quiet()); err == nil {
		t.Fatal("expected an error without key and without key file")
	}
}

func migrated(t *testing.T) *pgxpool.Pool {
	pool := testdb.New(t)
	if err := db.Migrate(context.Background(), pool, quiet()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
