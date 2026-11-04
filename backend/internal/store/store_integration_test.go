//go:build integration

package store_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	pool := testdb.New(t)
	if err := db.Migrate(context.Background(), pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return store.New(pool)
}

func TestUsers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, store.CreateUserParams{Email: "  Dev@Example.COM ", Locale: "pt-BR"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "dev@example.com" || u.Locale != "pt-BR" || u.PlanType != "developer" || u.ThemePreference != "dark" {
		t.Fatalf("unexpected user: %+v", u)
	}

	if _, err := s.CreateUser(ctx, store.CreateUserParams{Email: "DEV@example.com"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate email: want ErrConflict, got %v", err)
	}

	got, err := s.GetUserByEmail(ctx, "DEV@EXAMPLE.com")
	if err != nil || got.ID != u.ID {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if _, err := s.GetUserByEmail(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEndpointIngestConfig(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, store.CreateUserParams{Email: "owner@example.com", PlanType: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := token.Generate(token.PrefixInbound)
	ep, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "Stripe", Slug: "ep_stripe", TargetURL: "https://example.com/hook",
		AuthTokenHash: token.Hash(tok), AuthTokenPrefix: token.DisplayPrefix(tok),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Status != store.StatusActive || ep.AuthMode != store.AuthToken {
		t.Fatalf("unexpected endpoint: %+v", ep)
	}

	cfg, err := s.GetIngestConfig(ctx, "ep_stripe")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EndpointID != ep.ID || cfg.UserID != u.ID || cfg.PlanType != "pro" ||
		cfg.AuthHeaderKey != "X-HookShield-Token" || !token.Verify(cfg.AuthTokenHash, tok) {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	if _, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "dup", Slug: "ep_stripe", TargetURL: "https://x.io", AuthTokenHash: []byte{1},
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate slug: want ErrConflict, got %v", err)
	}
	if _, err := s.GetIngestConfig(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// Token rotation and status changes are visible in the ingest config.
	tok2, _ := token.Generate(token.PrefixInbound)
	if err := s.SetEndpointToken(ctx, ep.ID, token.Hash(tok2), token.DisplayPrefix(tok2)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpointStatus(ctx, ep.ID, store.StatusPaused); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetIngestConfig(ctx, "ep_stripe")
	if token.Verify(cfg.AuthTokenHash, tok) || !token.Verify(cfg.AuthTokenHash, tok2) || cfg.Status != store.StatusPaused {
		t.Fatalf("rotation/status not applied: %+v", cfg)
	}
	if err := s.SetEndpointStatus(ctx, "00000000-0000-0000-0000-000000000000", store.StatusActive); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEndpointAllowlistAndHMAC(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, store.CreateUserParams{Email: "o@example.com"})

	_, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "ips", Slug: "ep_ips", TargetURL: "https://x.io",
		AuthMode: store.AuthIPAllowlist, IPAllowlist: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.GetIngestConfig(ctx, "ep_ips")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.IPAllowlist) != 1 || cfg.IPAllowlist[0].String() != "10.0.0.0/8" {
		t.Fatalf("allowlist not loaded: %+v", cfg.IPAllowlist)
	}

	_, err = s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "hmac", Slug: "ep_hmac", TargetURL: "https://x.io", AuthMode: store.AuthHMAC,
		HMACHeader: "X-Signature", HMACSecretEnc: []byte{1, 2}, HMACSecretNonce: []byte{3, 4}, HMACKeyVersion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetIngestConfig(ctx, "ep_hmac")
	if cfg.HMACHeader != "X-Signature" || cfg.HMACKeyVersion != 2 || len(cfg.HMACSecretEnc) != 2 {
		t.Fatalf("hmac config not loaded: %+v", cfg)
	}
}

func TestPurgeAuthRecords(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	u, err := s.CreateUser(ctx, store.CreateUserParams{Email: "purge@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ip := netip.MustParseAddr("203.0.113.7")
	mk := func(name string, expires time.Time) []byte {
		h := []byte(name + "-hash-padding-to-be-unique-000000")
		if _, err := s.CreateSession(ctx, u.ID, h, ip, "test", expires); err != nil {
			t.Fatal(err)
		}
		return h
	}
	live := mk("live", now.Add(time.Hour))
	mk("expired", now.Add(-time.Minute))
	tooOld := mk("too-old", now.Add(time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE sessions SET created_at = $1 WHERE token_hash = $2`, now.Add(-91*24*time.Hour), tooOld); err != nil {
		t.Fatal(err)
	}
	for i, exp := range []time.Time{now.Add(time.Hour), now.Add(-time.Minute)} {
		if err := s.CreateEmailToken(ctx, u.ID, store.PurposeVerifyEmail, []byte{byte(i), 1, 2, 3}, exp); err != nil {
			t.Fatal(err)
		}
	}
	// the first token was invalidated (used) by the second; the second expired
	if err := s.CreateEmailToken(ctx, u.ID, store.PurposeResetPassword, []byte{9, 9, 9}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	sessions, tokens, err := s.PurgeAuthRecords(ctx, now, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if sessions != 2 || tokens != 2 {
		t.Fatalf("purged sessions=%d tokens=%d, want 2 and 2", sessions, tokens)
	}
	if _, _, err := s.GetSession(ctx, live); err != nil {
		t.Fatalf("live session must stay: %v", err)
	}
	if id, err := s.ConsumeEmailToken(ctx, []byte{9, 9, 9}, store.PurposeResetPassword); err != nil || id != u.ID {
		t.Fatalf("valid token must stay: %q %v", id, err)
	}
}
