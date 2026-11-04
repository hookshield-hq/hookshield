// Package db opens the PostgreSQL connection pool shared by HookShield
// services and exposes helpers used at boot (readiness checks, migrations).
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/hookshield-hq/hookshield/backend/internal/migrate"
	"github.com/hookshield-hq/hookshield/backend/migrations"
)

// Open creates a connection pool from a PostgreSQL URL and verifies it with a ping.
// Pool sizing can be tuned through the URL (e.g. ?pool_max_conns=20).
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// SQL wraps the pool in a *sql.DB for code that uses database/sql (migrations).
// Closing the returned *sql.DB does not close the pool.
func SQL(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDBFromPool(pool)
}

// Migrate applies every pending embedded migration and logs what ran.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	sqlDB := SQL(pool)
	defer sqlDB.Close()

	start := time.Now()
	res, err := migrate.Up(ctx, sqlDB, migrations.FS)
	for _, m := range res.Applied {
		log.Info("migration applied", "version", m.Version, "name", m.Name)
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("database schema up to date", "applied", len(res.Applied), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// ReadyCheck returns a readiness probe that pings the pool.
func ReadyCheck(pool *pgxpool.Pool) func(context.Context) error {
	return pool.Ping
}
