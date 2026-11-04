// Package migrate applies the embedded SQL migrations to PostgreSQL.
//
// Guarantees:
//   - migrations run in version order, each inside its own transaction;
//   - a PostgreSQL advisory lock serialises concurrent runners (several
//     replicas booting at once apply each migration exactly once);
//   - the SHA-256 checksum of every applied file is stored and verified, so
//     an edited migration is detected instead of silently ignored;
//   - a database that is ahead of the binary (unknown applied version) is
//     reported, protecting against running old code on a newer schema.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

// lockID is an arbitrary constant identifying the HookShield migration lock.
const lockID int64 = 0x486f6f6b536869 // "HookShi"

var fileRE = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migration is one versioned SQL file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string // hex SHA-256 of SQL
}

// Applied describes a migration already recorded in schema_migrations.
type Applied struct {
	Version  int
	Checksum string
}

// Load reads and validates every *.sql file at the root of fsys.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	seen := map[int]string{}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("invalid migration file name %q (want NNNN_name.sql)", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		if version == 0 {
			return nil, fmt.Errorf("migration %q: version must start at 0001", e.Name())
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %04d: %q and %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  version,
			Name:     m[2],
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// ErrChecksumMismatch means an already applied migration file was modified.
var ErrChecksumMismatch = errors.New("applied migration was modified")

// ErrUnknownApplied means the database contains a migration this binary does not know.
var ErrUnknownApplied = errors.New("database has a migration unknown to this binary")

// Plan returns the migrations that still need to run, after verifying that
// every applied migration matches the embedded file.
func Plan(available []Migration, applied []Applied) ([]Migration, error) {
	byVersion := make(map[int]Migration, len(available))
	for _, m := range available {
		byVersion[m.Version] = m
	}

	done := make(map[int]bool, len(applied))
	for _, a := range applied {
		m, ok := byVersion[a.Version]
		if !ok {
			return nil, fmt.Errorf("%w: version %04d (upgrade the binary)", ErrUnknownApplied, a.Version)
		}
		if m.Checksum != a.Checksum {
			return nil, fmt.Errorf("%w: %04d_%s (create a new migration instead of editing)", ErrChecksumMismatch, m.Version, m.Name)
		}
		done[a.Version] = true
	}

	var pending []Migration
	for _, m := range available {
		if !done[m.Version] {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INT PRIMARY KEY,
    name        TEXT NOT NULL,
    checksum    TEXT NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Result reports what Up did.
type Result struct {
	Applied []Migration
}

// Up applies all pending migrations from fsys to db.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS) (Result, error) {
	available, err := Load(fsys)
	if err != nil {
		return Result{}, err
	}

	// Session-level advisory locks belong to a connection, so pin one.
	conn, err := db.Conn(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return Result{}, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Use a fresh context so the lock is released even if ctx was cancelled.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID)
	}()

	if _, err := conn.ExecContext(ctx, createTable); err != nil {
		return Result{}, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := listApplied(ctx, conn)
	if err != nil {
		return Result{}, err
	}

	pending, err := Plan(available, applied)
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, m := range pending {
		if err := apply(ctx, conn, m); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, m)
	}
	return res, nil
}

// Status returns the migrations already applied and the pending ones.
func Status(ctx context.Context, db *sql.DB, fsys fs.FS) (applied []Applied, pending []Migration, err error) {
	available, err := Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	if _, err := db.ExecContext(ctx, createTable); err != nil {
		return nil, nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	applied, err = listApplied(ctx, conn)
	if err != nil {
		return nil, nil, err
	}
	pending, err = Plan(available, applied)
	return applied, pending, err
}

func listApplied(ctx context.Context, conn *sql.Conn) ([]Applied, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	var out []Applied
	for rows.Next() {
		var a Applied
		if err := rows.Scan(&a.Version, &a.Checksum); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func apply(ctx context.Context, conn *sql.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %04d_%s: %w", m.Version, m.Name, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("apply %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("record %04d_%s: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %04d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}
