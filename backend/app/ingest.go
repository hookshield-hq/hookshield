package app

import (
	"context"
	"net/http"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// Ingest runs the edge service (POST/PUT/PATCH /v1/{slug}) until ctx is done.
// See docs/INGEST.md for the public contract.
func Ingest(ctx context.Context, opts Options) error {
	cfg, err := config.Load(config.Requirement{Database: true, Redis: true, EncryptionKey: true})
	if err != nil {
		return err
	}
	log := cfg.Logger("ingest")
	plans := core.OrSelfHosted(opts.Plans, cfg.Retention)

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb, err := redisx.Open(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()

	if err := migrateOnBoot(ctx, cfg, pool, log); err != nil {
		return err
	}
	cipher, err := cipherFor(ctx, cfg, pool, log)
	if err != nil {
		return err
	}

	keys := redisx.NewKeys(cfg.RedisPrefix)
	handler := ingest.New(
		endpointcache.New(rdb, keys, store.New(pool)),
		limits.New(rdb, keys, cfg.IngestRateLimit, plans),
		queue.NewProducer(rdb, keys),
		cipher,
		ingest.Options{
			MaxBodyBytes:   cfg.MaxBodyBytes,
			TrustedProxies: cfg.TrustedProxies,
			Logger:         log,
		},
	)

	mux := http.NewServeMux()
	handler.Register(mux)
	httpx.Health(mux, "ingest", map[string]httpx.ReadyCheck{
		"database": db.ReadyCheck(pool),
		"redis":    redisx.ReadyCheck(rdb),
	})

	log.Info("ingest ready",
		"max_body_bytes", cfg.MaxBodyBytes,
		"rate_limit_per_sec", cfg.IngestRateLimit,
		"trusted_proxies", len(cfg.TrustedProxies))
	return httpx.Serve(ctx, httpx.NewServer(cfg.IngestAddr, mux), log)
}
