package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/api"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/mailer"
	"github.com/hookshield-hq/hookshield/backend/internal/migrate"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/migrations"
)

// API runs the control-plane API, or one of its subcommands:
//
//	(no args)          serve (applies pending migrations first unless HS_AUTO_MIGRATE=false)
//	migrate up|status  manage the schema and exit
//	seed [--env]       create/refresh demo data (development only)
func API(ctx context.Context, args []string, stdout io.Writer, opts Options) error {
	cfg, err := config.Load(config.Requirement{Database: true})
	if err != nil {
		return err
	}
	log := cfg.Logger("api")

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if len(args) > 0 && args[0] == "seed" {
		return runSeed(ctx, cfg, pool, stdout, len(args) > 1 && args[1] == "--env")
	}

	if len(args) > 0 {
		if args[0] != "migrate" {
			return fmt.Errorf("unknown command %q (want: migrate up | migrate status | seed)", args[0])
		}
		sub := "up"
		if len(args) > 1 {
			sub = args[1]
		}
		switch sub {
		case "up":
			return db.Migrate(ctx, pool, log)
		case "status":
			applied, pending, err := migrate.Status(ctx, db.SQL(pool), migrations.FS)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "applied: %d\n", len(applied))
			for _, a := range applied {
				fmt.Fprintf(stdout, "  ✓ %04d\n", a.Version)
			}
			fmt.Fprintf(stdout, "pending: %d\n", len(pending))
			for _, m := range pending {
				fmt.Fprintf(stdout, "  • %04d_%s\n", m.Version, m.Name)
			}
			return nil
		default:
			return fmt.Errorf("unknown migrate command %q (want: up | status)", sub)
		}
	}

	if err := migrateOnBoot(ctx, cfg, pool, log); err != nil {
		return err
	}

	return serveAPI(ctx, cfg, pool, log, opts)
}

// serveAPI wires the auth service and the REST API and blocks until ctx is done.
func serveAPI(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger, opts Options) error {
	if cfg.RedisURL == "" {
		return fmt.Errorf("HS_REDIS_URL is required to serve the API (sessions and login throttling live in Redis)")
	}
	rdb, err := redisx.Open(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()

	mail, mailKind, err := mailer.FromConfig(cfg.ResendAPIKey, cfg.ResendAPIURL, cfg.SMTPURL, cfg.EmailFrom)
	if err != nil {
		return err
	}
	if mail == nil {
		log.Warn("no mailer (set HS_RESEND_API_KEY or HS_SMTP_URL): verification and password-reset emails are logged, not sent")
	}

	cipher, err := cipherFor(ctx, cfg, pool, log)
	if err != nil {
		return err
	}
	st := store.New(pool)
	keys := redisx.NewKeys(cfg.RedisPrefix)

	cache := endpointcache.New(rdb, keys, st)
	authSvc := auth.New(st, rdb, keys, auth.Options{
		AppURL:        cfg.AppURL,
		EndpointCache: cache,
		SessionTTL:    cfg.SessionTTL,
		SessionMaxAge: cfg.SessionMaxAge,
		MaxSignupsIP:  cfg.SignupRateLimit,
		Mailer:        mail,
		Logger:        log,
		Google: auth.GoogleConfig{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.APIURL + "/v1/auth/google/callback",
			AuthURL:      cfg.GoogleAuthURL,
			TokenURL:     cfg.GoogleTokenURL,
			UserInfoURL:  cfg.GoogleUserInfoURL,
		},
	})
	if !cfg.GoogleEnabled() {
		log.Info("GOOGLE_CLIENT_ID not set: Google sign-in is disabled")
	}

	srv := api.New(authSvc, api.Options{
		AppURL: cfg.AppURL,
		// The dashboard's origins, plus the API's own: pages it serves itself
		// (HookShield Cloud's checkout and admin pages) post forms to it.
		AllowedOrigins: append(append([]string{}, cfg.CORSOrigins...), originOf(cfg.APIURL)),
		SecureCookies:  cfg.CookieSecure,
		TrustedProxies: cfg.TrustedProxies,
		Logger:         log,

		Store:               st,
		Cipher:              cipher,
		Cache:               cache,
		PublicIngestURL:     cfg.PublicIngestURL,
		AllowPrivateTargets: cfg.AllowPrivateTargets,

		Redis:   rdb,
		Keys:    keys,
		Limiter: limits.New(rdb, keys, cfg.IngestRateLimit, core.OrSelfHosted(opts.Plans, cfg.Retention)),

		Billing:     opts.Billing,
		MultiTenant: opts.Plans != nil || opts.Billing != nil, // HookShield Cloud
		Plans:       core.OrSelfHosted(opts.Plans, cfg.Retention),
	})
	mux := http.NewServeMux()
	httpx.Health(mux, "api", map[string]httpx.ReadyCheck{
		"database": db.ReadyCheck(pool),
		"redis":    redisx.ReadyCheck(rdb),
	})
	srv.Register(mux)
	if opts.APIRoutes != nil {
		opts.APIRoutes(mux, APIContext{
			DB: pool, Redis: rdb, Logger: log,
			RequireUser: srv.RequireUser,
			User:        func(r *http.Request) core.User { return api.CoreUser(api.CurrentUser(r)) },
		})
	}

	log.Info("api ready", "addr", cfg.APIAddr, "app_url", cfg.AppURL, "cors_origins", cfg.CORSOrigins, "google", cfg.GoogleEnabled(), "mailer", mailKind)
	httpSrv := httpx.NewServer(cfg.APIAddr, srv.Handler(mux))
	httpSrv.RegisterOnShutdown(srv.CloseStreams)
	err = httpx.Serve(ctx, httpSrv, log)
	authSvc.Wait() // let background account emails finish (each has a timeout)
	return err
}

func runSeed(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, stdout io.Writer, envOutput bool) error {
	if cfg.IsProduction() {
		return fmt.Errorf("seed is disabled when HS_ENV=production")
	}
	// Logs go to stderr so `api seed --env` output stays machine-readable.
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))); err != nil {
		return err
	}

	var cache seed.Invalidator
	var resetAlertCooldown func(endpointID string)
	if cfg.RedisURL != "" {
		rdb, err := redisx.Open(ctx, cfg.RedisURL)
		if err != nil {
			return err
		}
		defer rdb.Close()
		keys := redisx.NewKeys(cfg.RedisPrefix)
		cache = endpointcache.New(rdb, keys, store.New(pool))
		resetAlertCooldown = func(endpointID string) {
			rdb.Del(ctx, keys.AlertCooldown(endpointID), keys.AlertSuppressed(endpointID))
		}
	}

	target := os.Getenv("HS_SEED_TARGET")
	st := store.New(pool)
	res, err := seed.RunWithTarget(ctx, st, cache, target)
	if err != nil {
		return err
	}
	failing, err := seed.RunFailing(ctx, st, cache)
	if err != nil {
		return err
	}
	if resetAlertCooldown != nil {
		// Every seed starts a fresh alert window, so `make demo-alert` always produces an alert.
		resetAlertCooldown(failing.EndpointID)
	}
	emailAlert := false
	if len(cfg.EncryptionKey) > 0 {
		cipher, err := crypto.New(cfg.EncryptionKey, 1)
		if err != nil {
			return err
		}
		if _, err := seed.EnsureEmailAlert(ctx, st, cipher, res.UserID, res.EndpointID); err != nil {
			return err
		}
		emailAlert = true
	}
	if envOutput {
		// Machine-readable, for scripts: eval "$(api seed --env)"
		demoTarget := target
		if demoTarget == "" {
			demoTarget = seed.DemoTarget
		}
		fmt.Fprintf(stdout, "HS_DEMO_SLUG=%s\nHS_DEMO_TOKEN=%s\nHS_DEMO_HEADER=%s\nHS_DEMO_ENDPOINT_ID=%s\nHS_DEMO_TARGET=%s\nHS_FAILING_SLUG=%s\nHS_FAILING_TOKEN=%s\n",
			res.Slug, res.Token, res.HeaderKey, res.EndpointID, demoTarget, failing.Slug, failing.Token)
		return nil
	}
	fmt.Fprintf(stdout, `Demo data ready.

  user      %s
  endpoint  %s (slug %s)
  token     %s   <- shown only now; run seed again to rotate

  failing   %s (token %s) — always fails, alerts after 1 attempt (make demo-alert)

Send a test webhook:

  curl -i -X POST %s/v1/%s \
    -H '%s: %s' \
    -H 'Content-Type: application/json' \
    -d '{"type":"demo.ping"}'
`, seed.DemoEmail, res.EndpointID, res.Slug, res.Token, failing.Slug, failing.Token,
		cfg.PublicIngestURL, res.Slug, res.HeaderKey, res.Token)
	if !emailAlert {
		fmt.Fprintln(stdout, "\n(HS_ENCRYPTION_KEY not set: the demo email alert channel was not created)")
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// originOf is the scheme://host[:port] of a URL ("" when it has none).
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
