// Package config loads HookShield runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting shared by the HookShield binaries.
type Config struct {
	Env      string // development | production
	LogLevel slog.Level

	IngestAddr       string
	APIAddr          string
	WorkerHealthAddr string

	DatabaseURL string
	RedisURL    string

	// AutoMigrate makes the api apply pending migrations on boot (default true).
	AutoMigrate bool

	// RedisPrefix namespaces every Redis key (default "hs:").
	RedisPrefix string

	// Ingest limits
	MaxBodyBytes    int64          // HS_MAX_BODY_BYTES, default 1 MiB
	IngestRateLimit int64          // HS_INGEST_RATE_LIMIT, requests/second per endpoint (0 = off)
	SignupRateLimit int64          // HS_SIGNUP_RATE_LIMIT, sign-ups per IP per 15 minutes
	TrustedProxies  []netip.Prefix // HS_TRUSTED_PROXIES, proxies allowed to set X-Forwarded-For

	// ProcessorConcurrency is how many processor goroutines a worker runs (HS_PROCESSOR_CONCURRENCY, default 4).
	ProcessorConcurrency int

	// Dispatcher
	DispatcherConcurrency int           // HS_DISPATCHER_CONCURRENCY, default 8 (deliveries are I/O bound)
	DispatchTimeout       time.Duration // HS_DISPATCH_TIMEOUT, default 15s per delivery attempt
	AllowPrivateTargets   bool          // HS_ALLOW_PRIVATE_TARGETS, default true in development, false in production

	// Alerts
	AlertCooldown time.Duration // HS_ALERT_COOLDOWN, min time between alerts of one endpoint (default 10m, 0 = off)
	SMTPURL       string        // HS_SMTP_URL, smtp://user:pass@host:587 or smtps://…:465 (dev: Mailpit)
	ResendAPIKey  string        // HS_RESEND_API_KEY, production email via Resend (takes precedence over SMTP)
	ResendAPIURL  string        // HS_RESEND_API_URL (override for tests; default https://api.resend.com/emails)
	EmailFrom     string        // HS_EMAIL_FROM, sender (a domain verified in Resend in production)
	TelegramAPI   string        // HS_TELEGRAM_API_URL (default https://api.telegram.org)

	// Retention is how long payload logs are kept on self-hosted installs (HS_RETENTION, default 720h).
	// Plan-based editions (cloud) decide retention per plan instead.
	Retention time.Duration

	// EncryptionKey is the AES-256 master key (32 bytes) used for payloads at rest.
	// Read from HS_ENCRYPTION_KEY, or from the file HS_ENCRYPTION_KEY_FILE.
	EncryptionKey []byte
	// EncryptionKeyFile is where the key is kept when it is not in the
	// environment (HS_ENCRYPTION_KEY_FILE). A fresh install generates it there
	// on first start (see app's key bootstrap); empty = no file.
	EncryptionKeyFile string

	// PublicURL is the single public origin of an install that serves the
	// dashboard, the API (/v1) and ingest (/in) behind one proxy
	// (HS_PUBLIC_URL). When set it is the default of AppURL and APIURL, and
	// PublicURL+"/in" the default of PublicIngestURL.
	PublicURL string

	PublicIngestURL string
	AppURL          string // HS_APP_URL, the dashboard (email links, post-login redirects)
	APIURL          string // HS_API_URL, public URL of this API (Google OAuth callback)

	// Dashboard authentication
	CORSOrigins   []string      // HS_CORS_ORIGINS, comma-separated browser origins (default HS_APP_URL)
	SessionTTL    time.Duration // HS_SESSION_TTL, sliding session lifetime (default 720h = 30 days)
	SessionMaxAge time.Duration // HS_SESSION_MAX_AGE, absolute session lifetime (default 2160h = 90 days)
	CookieSecure  bool          // HS_COOKIE_SECURE, Secure flag on cookies (default true in production)

	// Google sign-in (enabled when both id and secret are set)
	GoogleClientID     string // GOOGLE_CLIENT_ID
	GoogleClientSecret string // GOOGLE_CLIENT_SECRET
	GoogleAuthURL      string // HS_GOOGLE_AUTH_URL (override for tests; default Google's)
	GoogleTokenURL     string // HS_GOOGLE_TOKEN_URL
	GoogleUserInfoURL  string // HS_GOOGLE_USERINFO_URL
}

// GoogleEnabled reports whether Google sign-in is configured.
func (c *Config) GoogleEnabled() bool { return c.GoogleClientID != "" && c.GoogleClientSecret != "" }

// Requirement tells Load which secrets a given binary needs to boot.
type Requirement struct {
	Database      bool
	Redis         bool
	EncryptionKey bool
}

// Load reads the environment, applies defaults and validates what req demands.
func Load(req Requirement) (*Config, error) {
	return load(os.Getenv, req)
}

func load(getenv func(string) string, req Requirement) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	cfg := &Config{
		Env:              get("HS_ENV", "development"),
		IngestAddr:       get("HS_INGEST_ADDR", ":8080"),
		APIAddr:          get("HS_API_ADDR", ":8081"),
		WorkerHealthAddr: get("HS_WORKER_HEALTH_ADDR", ":8082"),
		DatabaseURL:      get("HS_DATABASE_URL", ""),
		RedisURL:         get("HS_REDIS_URL", ""),
		PublicURL:        strings.TrimRight(get("HS_PUBLIC_URL", ""), "/"),
		RedisPrefix:      get("HS_REDIS_PREFIX", "hs:"),
	}

	var errs []error

	defIngest, defApp, defAPI := "http://localhost:8080", "http://localhost:3000", "http://localhost:8081"
	if cfg.PublicURL != "" {
		if !strings.HasPrefix(cfg.PublicURL, "http://") && !strings.HasPrefix(cfg.PublicURL, "https://") {
			errs = append(errs, fmt.Errorf("HS_PUBLIC_URL must start with http:// or https://, got %q", cfg.PublicURL))
		}
		defIngest, defApp, defAPI = cfg.PublicURL+"/in", cfg.PublicURL, cfg.PublicURL
	}
	cfg.PublicIngestURL = strings.TrimRight(get("HS_PUBLIC_INGEST_URL", defIngest), "/")
	cfg.AppURL = strings.TrimRight(get("HS_APP_URL", defApp), "/")

	if cfg.Env != "development" && cfg.Env != "production" {
		errs = append(errs, fmt.Errorf("HS_ENV must be development or production, got %q", cfg.Env))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("HS_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("HS_LOG_LEVEL: %w", err))
	}

	if v, err := strconv.ParseBool(get("HS_AUTO_MIGRATE", "true")); err != nil {
		errs = append(errs, fmt.Errorf("HS_AUTO_MIGRATE must be true or false: %w", err))
	} else {
		cfg.AutoMigrate = v
	}

	if v, err := strconv.ParseInt(get("HS_MAX_BODY_BYTES", "1048576"), 10, 64); err != nil || v <= 0 {
		errs = append(errs, fmt.Errorf("HS_MAX_BODY_BYTES must be a positive integer"))
	} else {
		cfg.MaxBodyBytes = v
	}

	if v, err := strconv.ParseInt(get("HS_INGEST_RATE_LIMIT", "100"), 10, 64); err != nil || v < 0 {
		errs = append(errs, fmt.Errorf("HS_INGEST_RATE_LIMIT must be an integer >= 0"))
	} else {
		cfg.IngestRateLimit = v
	}

	// Sign-ups per IP: strict in production, generous in development (the
	// end-to-end tests create many accounts from one address).
	signupDefault := "1000"
	if cfg.IsProduction() {
		signupDefault = "10"
	}
	if v, err := strconv.ParseInt(get("HS_SIGNUP_RATE_LIMIT", signupDefault), 10, 64); err != nil || v < 1 {
		errs = append(errs, fmt.Errorf("HS_SIGNUP_RATE_LIMIT must be an integer >= 1"))
	} else {
		cfg.SignupRateLimit = v
	}

	if v, err := strconv.Atoi(get("HS_PROCESSOR_CONCURRENCY", "4")); err != nil || v < 1 || v > 256 {
		errs = append(errs, fmt.Errorf("HS_PROCESSOR_CONCURRENCY must be between 1 and 256"))
	} else {
		cfg.ProcessorConcurrency = v
	}

	if v, err := strconv.Atoi(get("HS_DISPATCHER_CONCURRENCY", "8")); err != nil || v < 1 || v > 1024 {
		errs = append(errs, fmt.Errorf("HS_DISPATCHER_CONCURRENCY must be between 1 and 1024"))
	} else {
		cfg.DispatcherConcurrency = v
	}

	if v, err := time.ParseDuration(get("HS_DISPATCH_TIMEOUT", "15s")); err != nil || v < time.Second || v > 5*time.Minute {
		errs = append(errs, fmt.Errorf("HS_DISPATCH_TIMEOUT must be a duration between 1s and 5m (e.g. 15s)"))
	} else {
		cfg.DispatchTimeout = v
	}

	if v, err := time.ParseDuration(get("HS_ALERT_COOLDOWN", "10m")); err != nil || v < 0 {
		errs = append(errs, fmt.Errorf("HS_ALERT_COOLDOWN must be a duration >= 0 (e.g. 10m)"))
	} else {
		cfg.AlertCooldown = v
	}
	cfg.SMTPURL = get("HS_SMTP_URL", "")
	cfg.ResendAPIKey = get("HS_RESEND_API_KEY", "")
	cfg.ResendAPIURL = get("HS_RESEND_API_URL", "")
	if cfg.ResendAPIKey != "" && !strings.HasPrefix(cfg.ResendAPIKey, "re_") {
		errs = append(errs, errors.New("HS_RESEND_API_KEY must be a Resend API key (starts with re_)"))
	}
	cfg.EmailFrom = get("HS_EMAIL_FROM", "HookShield <no-reply@hookshield.dev>")
	cfg.TelegramAPI = strings.TrimRight(get("HS_TELEGRAM_API_URL", "https://api.telegram.org"), "/")

	defaultPrivate := "false"
	if cfg.Env == "development" {
		defaultPrivate = "true"
	}
	if v, err := strconv.ParseBool(get("HS_ALLOW_PRIVATE_TARGETS", defaultPrivate)); err != nil {
		errs = append(errs, fmt.Errorf("HS_ALLOW_PRIVATE_TARGETS must be true or false"))
	} else {
		cfg.AllowPrivateTargets = v
	}

	for _, raw := range strings.Split(get("HS_TRUSTED_PROXIES", ""), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if a, err := netip.ParseAddr(raw); err == nil {
				cfg.TrustedProxies = append(cfg.TrustedProxies, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
		}
		pfx, err := netip.ParsePrefix(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("HS_TRUSTED_PROXIES: invalid entry %q", raw))
			continue
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, pfx.Masked())
	}

	if v, err := time.ParseDuration(get("HS_RETENTION", "720h")); err != nil || v < time.Hour {
		errs = append(errs, fmt.Errorf("HS_RETENTION must be a duration of at least 1h (e.g. 720h = 30 days)"))
	} else {
		cfg.Retention = v
	}
	cfg.APIURL = strings.TrimRight(get("HS_API_URL", defAPI), "/")
	for _, o := range strings.Split(get("HS_CORS_ORIGINS", cfg.AppURL), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o == "" {
			continue
		}
		if o == "*" || !(strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://")) {
			errs = append(errs, fmt.Errorf("HS_CORS_ORIGINS: %q must be an origin like https://app.example.com (no wildcards)", o))
			continue
		}
		cfg.CORSOrigins = append(cfg.CORSOrigins, o)
	}
	if v, err := time.ParseDuration(get("HS_SESSION_TTL", "720h")); err != nil || v < time.Hour || v > 24*365*time.Hour {
		errs = append(errs, fmt.Errorf("HS_SESSION_TTL must be a duration between 1h and 8760h (e.g. 720h)"))
	} else {
		cfg.SessionTTL = v
	}
	if v, err := time.ParseDuration(get("HS_SESSION_MAX_AGE", "2160h")); err != nil || v < time.Hour || v > 24*365*time.Hour {
		errs = append(errs, fmt.Errorf("HS_SESSION_MAX_AGE must be a duration between 1h and 8760h (e.g. 2160h)"))
	} else {
		cfg.SessionMaxAge = v
	}
	defaultSecure := "false"
	if cfg.Env == "production" {
		defaultSecure = "true"
	}
	if v, err := strconv.ParseBool(get("HS_COOKIE_SECURE", defaultSecure)); err != nil {
		errs = append(errs, fmt.Errorf("HS_COOKIE_SECURE must be true or false"))
	} else {
		cfg.CookieSecure = v
	}
	cfg.GoogleClientID = get("GOOGLE_CLIENT_ID", "")
	cfg.GoogleClientSecret = get("GOOGLE_CLIENT_SECRET", "")
	if (cfg.GoogleClientID == "") != (cfg.GoogleClientSecret == "") {
		errs = append(errs, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together (or both left empty to disable Google sign-in)"))
	}
	cfg.GoogleAuthURL = get("HS_GOOGLE_AUTH_URL", "")
	cfg.GoogleTokenURL = get("HS_GOOGLE_TOKEN_URL", "")
	cfg.GoogleUserInfoURL = get("HS_GOOGLE_USERINFO_URL", "")

	if req.Database && cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("HS_DATABASE_URL is required"))
	}
	if req.Redis && cfg.RedisURL == "" {
		errs = append(errs, errors.New("HS_REDIS_URL is required"))
	}

	cfg.EncryptionKeyFile = get("HS_ENCRYPTION_KEY_FILE", "")
	if raw := get("HS_ENCRYPTION_KEY", ""); raw != "" {
		if key, err := ParseKey(raw); err != nil {
			errs = append(errs, fmt.Errorf("HS_ENCRYPTION_KEY: %w", err))
		} else {
			cfg.EncryptionKey = key
		}
	} else if cfg.EncryptionKeyFile != "" {
		switch raw, err := os.ReadFile(cfg.EncryptionKeyFile); {
		case errors.Is(err, os.ErrNotExist):
			// Generated on first start of a fresh install (app key bootstrap).
		case err != nil:
			errs = append(errs, fmt.Errorf("HS_ENCRYPTION_KEY_FILE: %w", err))
		default:
			if key, err := ParseKey(string(raw)); err != nil {
				errs = append(errs, fmt.Errorf("HS_ENCRYPTION_KEY_FILE %s: %w", cfg.EncryptionKeyFile, err))
			} else {
				cfg.EncryptionKey = key
			}
		}
	} else if req.EncryptionKey {
		errs = append(errs, errors.New("HS_ENCRYPTION_KEY is required (generate with: openssl rand -base64 32), or set HS_ENCRYPTION_KEY_FILE to have it generated on first start"))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// ParseKey decodes a 32-byte encryption key written as base64 (padded or
// not, standard or URL alphabet — e.g. `openssl rand -base64 32`) or as 64
// hex characters (e.g. `openssl rand -hex 32`). Surrounding whitespace is ignored.
func ParseKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 64 {
		if key, err := hex.DecodeString(raw); err == nil {
			return key, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(raw); err == nil {
			if len(key) != 32 {
				return nil, fmt.Errorf("must decode to 32 bytes, got %d (generate with: openssl rand -base64 32)", len(key))
			}
			return key, nil
		}
	}
	return nil, errors.New("must be 32 bytes as base64 (openssl rand -base64 32) or 64 hex characters (openssl rand -hex 32)")
}

// IsProduction reports whether the process runs with HS_ENV=production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// Logger builds the structured logger used by every binary (JSON in production, text in dev).
func (c *Config) Logger(service string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	var h slog.Handler
	if c.IsProduction() {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h).With("service", service)
}
