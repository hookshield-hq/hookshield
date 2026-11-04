// Package seed creates development data: a demo user and a demo endpoint with
// a fresh inbound token. It is idempotent — running it again keeps the user and
// endpoint and rotates the token (the plaintext is only known at creation time).
package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// Defaults used by Run.
const (
	DemoEmail  = "dev@hookshield.local"
	DemoSlug   = "ep_demo"
	DemoTarget = "http://localhost:4000/webhook"

	// FailingSlug is a demo endpoint whose destination always refuses the
	// connection, with no retries and an alert after the first failure — it
	// shows the alert flow in seconds (make demo-alert).
	FailingSlug   = "ep_failing"
	FailingTarget = "http://127.0.0.1:9/unreachable"
)

// Result is what a developer needs to send a first webhook.
type Result struct {
	UserID     string
	EndpointID string
	Slug       string
	Token      string
	HeaderKey  string
}

// Invalidator drops cached endpoint configs (optional).
type Invalidator interface {
	Invalidate(ctx context.Context, slug string) error
}

// Run creates or refreshes the demo data, delivering to DemoTarget.
func Run(ctx context.Context, s *store.Store, cache Invalidator) (Result, error) {
	return RunWithTarget(ctx, s, cache, DemoTarget)
}

// RunWithTarget creates or refreshes the demo data. The demo endpoint always
// ends up delivering to target (so switching between native and Docker
// development just works).
func RunWithTarget(ctx context.Context, s *store.Store, cache Invalidator, target string) (Result, error) {
	if target == "" {
		target = DemoTarget
	}
	u, err := s.GetUserByEmail(ctx, DemoEmail)
	if errors.Is(err, store.ErrNotFound) {
		u, err = s.CreateUser(ctx, store.CreateUserParams{Email: DemoEmail, Name: "Demo Developer", PlanType: "pro"})
	}
	if err != nil {
		return Result{}, fmt.Errorf("seed user: %w", err)
	}

	tok, err := token.Generate(token.PrefixInbound)
	if err != nil {
		return Result{}, err
	}

	res := Result{UserID: u.ID, Slug: DemoSlug, Token: tok, HeaderKey: "X-HookShield-Token"}

	ep, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "Demo endpoint", Description: "Created by `api seed`",
		Slug: DemoSlug, TargetURL: target,
		AuthTokenHash: token.Hash(tok), AuthTokenPrefix: token.DisplayPrefix(tok),
	})
	switch {
	case err == nil:
		res.EndpointID = ep.ID
	case errors.Is(err, store.ErrConflict):
		cfg, err := s.GetIngestConfig(ctx, DemoSlug)
		if err != nil {
			return Result{}, fmt.Errorf("seed endpoint lookup: %w", err)
		}
		if cfg.UserID != u.ID {
			return Result{}, fmt.Errorf("slug %q belongs to another user; refusing to touch it", DemoSlug)
		}
		if err := s.SetEndpointToken(ctx, cfg.EndpointID, token.Hash(tok), token.DisplayPrefix(tok)); err != nil {
			return Result{}, fmt.Errorf("seed token rotation: %w", err)
		}
		if err := s.SetEndpointTarget(ctx, cfg.EndpointID, target); err != nil {
			return Result{}, fmt.Errorf("seed target: %w", err)
		}
		res.EndpointID = cfg.EndpointID
	default:
		return Result{}, fmt.Errorf("seed endpoint: %w", err)
	}

	if cache != nil {
		if err := cache.Invalidate(ctx, DemoSlug); err != nil {
			return Result{}, fmt.Errorf("seed cache invalidation: %w", err)
		}
	}
	return res, nil
}

// RunFailing creates or refreshes the always-failing demo endpoint and returns
// its fresh token. Call Run first (it creates the demo user).
func RunFailing(ctx context.Context, s *store.Store, cache Invalidator) (Result, error) {
	u, err := s.GetUserByEmail(ctx, DemoEmail)
	if err != nil {
		return Result{}, fmt.Errorf("seed failing endpoint: demo user: %w", err)
	}
	tok, err := token.Generate(token.PrefixInbound)
	if err != nil {
		return Result{}, err
	}
	res := Result{UserID: u.ID, Slug: FailingSlug, Token: tok, HeaderKey: "X-HookShield-Token"}

	ep, err := s.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: u.ID, Name: "Demo failing endpoint", Description: "Always fails — shows alerts (`make demo-alert`)",
		Slug: FailingSlug, TargetURL: FailingTarget,
		AuthTokenHash: token.Hash(tok), AuthTokenPrefix: token.DisplayPrefix(tok),
	})
	switch {
	case err == nil:
		res.EndpointID = ep.ID
	case errors.Is(err, store.ErrConflict):
		cfg, err := s.GetIngestConfig(ctx, FailingSlug)
		if err != nil {
			return Result{}, err
		}
		if cfg.UserID != u.ID {
			return Result{}, fmt.Errorf("slug %q belongs to another user; refusing to touch it", FailingSlug)
		}
		if err := s.SetEndpointToken(ctx, cfg.EndpointID, token.Hash(tok), token.DisplayPrefix(tok)); err != nil {
			return Result{}, err
		}
		res.EndpointID = cfg.EndpointID
	default:
		return Result{}, fmt.Errorf("seed failing endpoint: %w", err)
	}

	if err := s.SetEndpointDelivery(ctx, res.EndpointID, store.SetEndpointDeliveryParams{
		TargetURL: FailingTarget, MaxRetries: 0, RetryStrategy: "fixed", RetryInitialMs: 1000, RetryMultiplier: 1,
		RetryMaxDelayMs: 1000, DLQEnabled: true, AlertThresholdFailures: 1,
	}); err != nil {
		return Result{}, fmt.Errorf("seed failing endpoint delivery: %w", err)
	}
	if cache != nil {
		if err := cache.Invalidate(ctx, FailingSlug); err != nil {
			return Result{}, err
		}
	}
	return res, nil
}

// EnsureEmailAlert gives the demo user an account-wide email alert channel
// (to the user's own address) unless one exists. Returns true when created.
func EnsureEmailAlert(ctx context.Context, s *store.Store, cipher *crypto.Cipher, userID, anyEndpointID string) (bool, error) {
	channels, err := s.ListAlertChannels(ctx, userID, anyEndpointID)
	if err != nil {
		return false, err
	}
	for _, c := range channels {
		if c.Type == store.ChannelEmail && c.EndpointID == nil {
			return false, nil
		}
	}
	id := uuid.NewV7()
	plain, _ := json.Marshal(map[string]string{}) // empty "to" = the account's email
	ct, nonce, err := cipher.Seal(plain, store.AlertChannelAAD(id))
	if err != nil {
		return false, err
	}
	err = s.CreateAlertChannel(ctx, userID, store.AlertChannel{
		ID: id, Type: store.ChannelEmail, TargetEncrypted: ct, TargetNonce: nonce, KeyVersion: cipher.KeyVersion(), Enabled: true,
	})
	return err == nil, err
}
