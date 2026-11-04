//go:build integration

package seed_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
)

type spyCache struct{ slugs []string }

func (s *spyCache) Invalidate(_ context.Context, slug string) error {
	s.slugs = append(s.slugs, slug)
	return nil
}

func TestSeedIsIdempotentAndRotatesToken(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	cache := &spyCache{}

	first, err := seed.Run(ctx, s, cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := seed.Run(ctx, s, cache)
	if err != nil {
		t.Fatal(err)
	}

	if first.UserID != second.UserID || first.EndpointID != second.EndpointID {
		t.Fatal("seed must reuse the demo user and endpoint")
	}
	if first.Token == second.Token {
		t.Fatal("seed must rotate the token")
	}
	cfg, err := s.GetIngestConfig(ctx, seed.DemoSlug)
	if err != nil {
		t.Fatal(err)
	}
	if token.Verify(cfg.AuthTokenHash, first.Token) || !token.Verify(cfg.AuthTokenHash, second.Token) {
		t.Fatal("only the latest token may be valid")
	}
	if len(cache.slugs) != 2 || cache.slugs[0] != seed.DemoSlug {
		t.Fatalf("cache not invalidated: %v", cache.slugs)
	}
}

func TestSeedUpdatesTarget(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	if _, err := seed.Run(ctx, s, nil); err != nil {
		t.Fatal(err)
	}
	res, err := seed.RunWithTarget(ctx, s, nil, "http://sink:4000/webhook")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.GetDispatchConfig(ctx, res.EndpointID)
	if err != nil || cfg.TargetURL != "http://sink:4000/webhook" {
		t.Fatalf("target %q, %v", cfg.TargetURL, err)
	}
}

func TestSeedFailingEndpointAndEmailAlert(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	demo, err := seed.Run(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	f1, err := seed.RunFailing(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := seed.RunFailing(ctx, s, nil)
	if err != nil || f1.EndpointID != f2.EndpointID || f1.Token == f2.Token {
		t.Fatalf("failing endpoint not idempotent: %+v %+v %v", f1, f2, err)
	}
	cfg, _ := s.GetDispatchConfig(ctx, f1.EndpointID)
	if cfg.TargetURL != seed.FailingTarget || cfg.MaxRetries != 0 || cfg.AlertThresholdFailures != 1 {
		t.Fatalf("failing endpoint config %+v", cfg)
	}

	cipher, _ := crypto.New(bytes.Repeat([]byte{1}, crypto.KeySize), 1)
	created, err := seed.EnsureEmailAlert(ctx, s, cipher, demo.UserID, demo.EndpointID)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	created, err = seed.EnsureEmailAlert(ctx, s, cipher, demo.UserID, demo.EndpointID)
	if err != nil || created {
		t.Fatalf("second call must not duplicate: created=%v err=%v", created, err)
	}
	chans, _ := s.ListAlertChannels(ctx, demo.UserID, f1.EndpointID)
	if len(chans) != 1 || chans[0].Type != store.ChannelEmail || chans[0].EndpointID != nil {
		t.Fatalf("channels %+v", chans)
	}
}
