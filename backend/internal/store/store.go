// Package store contains the PostgreSQL queries used by HookShield services.
// Each file groups the queries of one aggregate (users, endpoints, ...).
// Queries are written by hand with pgx; every exported method is covered by
// integration tests (build tag `integration`).
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when a unique constraint is violated (e.g. email or slug taken).
var ErrConflict = errors.New("store: already exists")

// Store wraps the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool (tests and maintenance jobs).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// mapErr converts driver errors into store sentinel errors.
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errors.Join(ErrConflict, err)
	}
	return err
}
