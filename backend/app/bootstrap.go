package app

import (
	"context"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/keyboot"
)

// warnIfDevelopment flags an install that looks public but runs with
// HS_ENV=development (deliveries to private addresses allowed, cookies without
// the Secure flag).
func warnIfDevelopment(cfg *config.Config, log *slog.Logger) {
	if cfg.IsProduction() {
		return
	}
	for _, u := range []string{cfg.AppURL, cfg.APIURL, cfg.PublicIngestURL} {
		if pu, err := url.Parse(u); err == nil && !isLocalHost(pu.Hostname()) {
			log.Warn("HS_ENV=development on a public URL: SSRF protection is off (HS_ALLOW_PRIVATE_TARGETS) and cookies are not Secure — set HS_ENV=production", "url", u)
			return
		}
	}
}

func isLocalHost(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && (ip.IsLoopback() || ip.IsPrivate())
}

// migrateOnBoot applies pending migrations unless HS_AUTO_MIGRATE=false.
// Every service does it (an advisory lock serialises them), so services can
// start in any order — one-click platforms start them all at once.
func migrateOnBoot(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) error {
	warnIfDevelopment(cfg, log)
	if !cfg.AutoMigrate {
		log.Info("HS_AUTO_MIGRATE=false: skipping migrations")
		return nil
	}
	return db.Migrate(ctx, pool, log)
}

// cipherFor returns the payload cipher with the install's encryption key,
// generating the key on the first start of a fresh install (HS_ENCRYPTION_KEY_FILE)
// and refusing a key that does not match the database (see internal/keyboot).
func cipherFor(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) (*crypto.Cipher, error) {
	key, err := keyboot.Ensure(ctx, pool, cfg.EncryptionKey, cfg.EncryptionKeyFile, log)
	if err != nil {
		return nil, err
	}
	cfg.EncryptionKey = key
	return crypto.New(key, 1)
}
