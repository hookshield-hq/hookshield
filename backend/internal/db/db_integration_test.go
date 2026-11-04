//go:build integration

package db_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/migrate"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/migrations"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func countApplied(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	available, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, pool, quiet); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := countApplied(t, pool); got != len(available) {
		t.Fatalf("applied %d, want %d", got, len(available))
	}

	// Second run must be a no-op.
	res, err := migrate.Up(ctx, db.SQL(pool), migrations.FS)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("second run applied %d migrations", len(res.Applied))
	}

	applied, pending, err := migrate.Status(ctx, db.SQL(pool), migrations.FS)
	if err != nil || len(pending) != 0 || len(applied) != len(available) {
		t.Fatalf("status: applied=%d pending=%d err=%v", len(applied), len(pending), err)
	}
}

func TestMigrateConcurrentRunners(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	const runners = 6
	var wg sync.WaitGroup
	errs := make(chan error, runners)
	for range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := migrate.Up(ctx, db.SQL(pool), migrations.FS)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent run failed: %v", err)
		}
	}

	available, _ := migrate.Load(migrations.FS)
	if got := countApplied(t, pool); got != len(available) {
		t.Fatalf("applied %d, want %d (each migration exactly once)", got, len(available))
	}
}

func TestMigrateDetectsEditedMigration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	if err := db.Migrate(ctx, pool, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	_, err := migrate.Up(ctx, db.SQL(pool), migrations.FS)
	if !errors.Is(err, migrate.ErrChecksumMismatch) {
		t.Fatalf("want ErrChecksumMismatch, got %v", err)
	}
}

func TestMigrateFailedMigrationRollsBack(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	broken := fstestFS(map[string]string{
		"0001_ok.sql":     "CREATE TABLE ok_table (id INT);",
		"0002_broken.sql": "CREATE TABLE half_done (id INT); SELECT * FROM does_not_exist;",
	})
	if _, err := migrate.Up(ctx, db.SQL(pool), broken); err == nil {
		t.Fatal("expected failure")
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('half_done') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("failed migration left a partial table behind")
	}
	if got := countApplied(t, pool); got != 1 {
		t.Fatalf("applied %d, want 1 (only the good migration)", got)
	}
}

// TestSchemaConstraints runs backend/migrations/tests/schema_test.sql
// (psql meta-commands stripped) against a freshly migrated database.
func TestSchemaConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, quiet); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("../../migrations/tests/schema_test.sql")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `\`) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// Exec without arguments uses the simple protocol, which accepts multiple statements.
	if _, err := conn.Exec(ctx, b.String()); err != nil {
		t.Fatalf("schema test failed: %v", err)
	}
}

func TestReadyCheck(t *testing.T) {
	pool := testdb.New(t)
	if err := db.ReadyCheck(pool)(context.Background()); err != nil {
		t.Fatalf("ready check: %v", err)
	}
}

func TestOpenRejectsBadURL(t *testing.T) {
	if _, err := db.Open(context.Background(), "not a url://"); err == nil {
		t.Fatal("expected error")
	}
}
