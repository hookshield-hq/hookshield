package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "development" || cfg.IngestAddr != ":8080" || cfg.APIAddr != ":8081" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.IsProduction() {
		t.Fatal("default env must not be production")
	}
}

func TestLoadRequirements(t *testing.T) {
	_, err := load(env(nil), Requirement{Database: true, Redis: true, EncryptionKey: true})
	if err == nil {
		t.Fatal("expected error for missing required settings")
	}
	for _, want := range []string{"HS_DATABASE_URL", "HS_REDIS_URL", "HS_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestEncryptionKeyValidation(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := load(env(map[string]string{"HS_ENCRYPTION_KEY": short}), Requirement{}); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
	if _, err := load(env(map[string]string{"HS_ENCRYPTION_KEY": "not base64!"}), Requirement{}); err == nil {
		t.Fatal("expected error for invalid base64")
	}

	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cfg, err := load(env(map[string]string{"HS_ENCRYPTION_KEY": good}), Requirement{EncryptionKey: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("key length = %d", len(cfg.EncryptionKey))
	}
}

func TestInvalidEnvAndLogLevel(t *testing.T) {
	_, err := load(env(map[string]string{"HS_ENV": "staging", "HS_LOG_LEVEL": "loud"}), Requirement{})
	if err == nil || !strings.Contains(err.Error(), "HS_ENV") || !strings.Contains(err.Error(), "HS_LOG_LEVEL") {
		t.Fatalf("expected HS_ENV and HS_LOG_LEVEL errors, got %v", err)
	}
}

func TestTrailingSlashTrimmed(t *testing.T) {
	cfg, err := load(env(map[string]string{"HS_PUBLIC_INGEST_URL": "https://in.hookshield.dev/"}), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicIngestURL != "https://in.hookshield.dev" {
		t.Fatalf("got %q", cfg.PublicIngestURL)
	}
}

func TestAutoMigrate(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil || !cfg.AutoMigrate {
		t.Fatalf("AutoMigrate should default to true (cfg=%+v, err=%v)", cfg, err)
	}
	cfg, err = load(env(map[string]string{"HS_AUTO_MIGRATE": "false"}), Requirement{})
	if err != nil || cfg.AutoMigrate {
		t.Fatalf("HS_AUTO_MIGRATE=false not honoured (cfg=%+v, err=%v)", cfg, err)
	}
	if _, err := load(env(map[string]string{"HS_AUTO_MIGRATE": "maybe"}), Requirement{}); err == nil {
		t.Fatal("expected error for invalid HS_AUTO_MIGRATE")
	}
}

func TestIngestSettings(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxBodyBytes != 1<<20 || cfg.IngestRateLimit != 100 || len(cfg.TrustedProxies) != 0 || cfg.RedisPrefix != "hs:" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}

	cfg, err = load(env(map[string]string{
		"HS_MAX_BODY_BYTES":    "2048",
		"HS_INGEST_RATE_LIMIT": "0",
		"HS_TRUSTED_PROXIES":   "10.0.0.0/8, 172.18.0.1 ,::1",
	}), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxBodyBytes != 2048 || cfg.IngestRateLimit != 0 || len(cfg.TrustedProxies) != 3 ||
		cfg.TrustedProxies[1].String() != "172.18.0.1/32" || cfg.TrustedProxies[2].String() != "::1/128" {
		t.Fatalf("unexpected parsed settings: %+v", cfg)
	}

	for k, v := range map[string]string{
		"HS_MAX_BODY_BYTES":    "0",
		"HS_INGEST_RATE_LIMIT": "-1",
		"HS_TRUSTED_PROXIES":   "not-an-ip",
	} {
		if _, err := load(env(map[string]string{k: v}), Requirement{}); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: expected error mentioning %s, got %v", k, v, k, err)
		}
	}
}

func TestProcessorConcurrency(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil || cfg.ProcessorConcurrency != 4 {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	for _, bad := range []string{"0", "257", "x"} {
		if _, err := load(env(map[string]string{"HS_PROCESSOR_CONCURRENCY": bad}), Requirement{}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDispatcherSettings(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DispatcherConcurrency != 8 || cfg.DispatchTimeout != 15*time.Second || !cfg.AllowPrivateTargets {
		t.Fatalf("development defaults: %+v", cfg)
	}

	prod, err := load(env(map[string]string{"HS_ENV": "production"}), Requirement{})
	if err != nil || prod.AllowPrivateTargets {
		t.Fatalf("production must block private targets by default: %+v %v", prod, err)
	}
	prod, _ = load(env(map[string]string{"HS_ENV": "production", "HS_ALLOW_PRIVATE_TARGETS": "true"}), Requirement{})
	if !prod.AllowPrivateTargets {
		t.Fatal("explicit opt-in ignored")
	}

	for k, v := range map[string]string{
		"HS_DISPATCHER_CONCURRENCY": "0",
		"HS_DISPATCH_TIMEOUT":       "500ms",
		"HS_ALLOW_PRIVATE_TARGETS":  "maybe",
	} {
		if _, err := load(env(map[string]string{k: v}), Requirement{}); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: expected error, got %v", k, v, err)
		}
	}
}

func TestAlertSettings(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AlertCooldown != 10*time.Minute || cfg.SMTPURL != "" || cfg.TelegramAPI != "https://api.telegram.org" ||
		cfg.EmailFrom != "HookShield <no-reply@hookshield.dev>" {
		t.Fatalf("defaults: %+v", cfg)
	}
	cfg, err = load(env(map[string]string{"HS_ALERT_COOLDOWN": "0", "HS_TELEGRAM_API_URL": "http://tg.local/"}), Requirement{})
	if err != nil || cfg.AlertCooldown != 0 || cfg.TelegramAPI != "http://tg.local" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if _, err := load(env(map[string]string{"HS_ALERT_COOLDOWN": "-1m"}), Requirement{}); err == nil {
		t.Fatal("negative cooldown accepted")
	}
}

func TestAuthSettings(t *testing.T) {
	cfg, err := load(env(map[string]string{"HS_APP_URL": "https://app.example.com/"}), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "https://app.example.com" {
		t.Fatalf("CORS default = %v", cfg.CORSOrigins)
	}
	if cfg.SessionTTL != 720*time.Hour || cfg.CookieSecure || cfg.GoogleEnabled() || cfg.APIURL != "http://localhost:8081" {
		t.Fatalf("auth defaults: %+v", cfg)
	}

	prod, err := load(env(map[string]string{"HS_ENV": "production", "GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "s",
		"HS_CORS_ORIGINS": "https://a.example.com, https://b.example.com", "HS_SESSION_TTL": "24h"}), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if !prod.CookieSecure || !prod.GoogleEnabled() || len(prod.CORSOrigins) != 2 || prod.SessionTTL != 24*time.Hour {
		t.Fatalf("production auth settings: %+v", prod)
	}

	_, err = load(env(map[string]string{"GOOGLE_CLIENT_ID": "id", "HS_CORS_ORIGINS": "*", "HS_SESSION_TTL": "5m", "HS_COOKIE_SECURE": "maybe"}), Requirement{})
	for _, want := range []string{"GOOGLE_CLIENT_SECRET", "HS_CORS_ORIGINS", "HS_SESSION_TTL", "HS_COOKIE_SECURE"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestResendSettings(t *testing.T) {
	cfg, err := load(env(map[string]string{"HS_RESEND_API_KEY": "re_abc"}), Requirement{})
	if err != nil || cfg.ResendAPIKey != "re_abc" {
		t.Fatalf("resend key: %v %+v", err, cfg)
	}
	if _, err := load(env(map[string]string{"HS_RESEND_API_KEY": "abc"}), Requirement{}); err == nil || !strings.Contains(err.Error(), "HS_RESEND_API_KEY") {
		t.Fatalf("invalid key accepted: %v", err)
	}
}

func TestRetention(t *testing.T) {
	cfg, err := load(env(nil), Requirement{})
	if err != nil || cfg.Retention != 720*time.Hour {
		t.Fatalf("default retention: %v %v", err, cfg)
	}
	if _, err := load(env(map[string]string{"HS_RETENTION": "10m"}), Requirement{}); err == nil || !strings.Contains(err.Error(), "HS_RETENTION") {
		t.Fatalf("short retention accepted: %v", err)
	}
}

func TestParseKeyFormats(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	for name, in := range map[string]string{
		"base64":         base64.StdEncoding.EncodeToString(raw),
		"base64 raw":     base64.RawStdEncoding.EncodeToString(raw),
		"base64 url":     base64.URLEncoding.EncodeToString(raw),
		"hex":            hex.EncodeToString(raw),
		"with newline":   base64.StdEncoding.EncodeToString(raw) + "\n",
		"hex upper case": strings.ToUpper(hex.EncodeToString(raw)),
	} {
		key, err := ParseKey(in)
		if err != nil || !bytes.Equal(key, raw) {
			t.Errorf("%s: key=%x err=%v", name, key, err)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("z", 64), base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) should fail", bad)
		}
	}
}

func TestEncryptionKeyFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "encryption.key")

	// Missing file: not an error (generated on first start), key stays empty.
	cfg, err := load(env(map[string]string{"HS_ENCRYPTION_KEY_FILE": file}), Requirement{EncryptionKey: true})
	if err != nil || cfg.EncryptionKey != nil || cfg.EncryptionKeyFile != file {
		t.Fatalf("missing file: cfg=%+v err=%v", cfg, err)
	}

	key := bytes.Repeat([]byte{9}, 32)
	if err := os.WriteFile(file, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = load(env(map[string]string{"HS_ENCRYPTION_KEY_FILE": file}), Requirement{EncryptionKey: true})
	if err != nil || !bytes.Equal(cfg.EncryptionKey, key) {
		t.Fatalf("file key: %x err=%v", cfg.EncryptionKey, err)
	}

	// The environment wins over the file.
	other := bytes.Repeat([]byte{1}, 32)
	cfg, err = load(env(map[string]string{"HS_ENCRYPTION_KEY_FILE": file, "HS_ENCRYPTION_KEY": hex.EncodeToString(other)}), Requirement{})
	if err != nil || !bytes.Equal(cfg.EncryptionKey, other) {
		t.Fatalf("env key: %x err=%v", cfg.EncryptionKey, err)
	}

	if err := os.WriteFile(file, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(env(map[string]string{"HS_ENCRYPTION_KEY_FILE": file}), Requirement{}); err == nil || !strings.Contains(err.Error(), "HS_ENCRYPTION_KEY_FILE") {
		t.Fatalf("garbage file: %v", err)
	}
}

func TestPublicURL(t *testing.T) {
	cfg, err := load(env(map[string]string{"HS_PUBLIC_URL": "https://hooks.example.com/"}), Requirement{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppURL != "https://hooks.example.com" || cfg.APIURL != "https://hooks.example.com" ||
		cfg.PublicIngestURL != "https://hooks.example.com/in" || len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "https://hooks.example.com" {
		t.Fatalf("derived URLs: %+v", cfg)
	}
	// Explicit values still win.
	cfg, err = load(env(map[string]string{"HS_PUBLIC_URL": "https://hooks.example.com", "HS_PUBLIC_INGEST_URL": "https://in.example.com"}), Requirement{})
	if err != nil || cfg.PublicIngestURL != "https://in.example.com" || cfg.AppURL != "https://hooks.example.com" {
		t.Fatalf("override: %+v %v", cfg, err)
	}
	if _, err := load(env(map[string]string{"HS_PUBLIC_URL": "hooks.example.com"}), Requirement{}); err == nil {
		t.Fatal("expected error for URL without scheme")
	}
}
