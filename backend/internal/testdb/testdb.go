// Package testdb gives integration tests an isolated, throw-away PostgreSQL database.
//
// Tests using it must carry the `integration` build tag and need
// HS_TEST_DATABASE_URL pointing at a server where the user may CREATE DATABASE
// (the dev compose stack qualifies):
//
//	HS_TEST_DATABASE_URL=postgres://hookshield:hookshield@localhost:5432/hookshield?sslmode=disable \
//	  go test -tags integration ./...
//
// Without the variable the tests are skipped, so `go test -tags integration`
// never fails just because no database is around.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/internal/db"
)

// EnvVar is the environment variable holding the admin connection URL.
const EnvVar = "HS_TEST_DATABASE_URL"

// New creates an empty database, returns a pool connected to it and drops it
// when the test finishes. Migrations are NOT applied — call db.Migrate if needed.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()

	adminURL := os.Getenv(EnvVar)
	if adminURL == "" {
		t.Skipf("%s not set; skipping integration test", EnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(context.Background())

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "hs_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name

	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})
	return pool
}
