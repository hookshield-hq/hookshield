package store

import (
	"context"
	"net/netip"
)

// Endpoint auth modes (endpoints.auth_mode).
const (
	AuthToken       = "token"
	AuthHMAC        = "hmac"
	AuthIPAllowlist = "ip_allowlist"
)

// Endpoint statuses (endpoints.status).
const (
	StatusActive = "active"
	StatusPaused = "paused"
	StatusDebug  = "debug"
)

// IngestConfig is everything the ingest hot path needs to accept or reject a
// request for an endpoint. It is cached in Redis, so it only holds hashes and
// ciphertexts — never plaintext secrets.
type IngestConfig struct {
	EndpointID      string         `json:"endpoint_id"`
	UserID          string         `json:"user_id"`
	Slug            string         `json:"slug"`
	Status          string         `json:"status"`
	PlanType        string         `json:"plan_type"`
	AuthMode        string         `json:"auth_mode"`
	AuthHeaderKey   string         `json:"auth_header_key"`
	AuthTokenHash   []byte         `json:"auth_token_hash,omitempty"`
	HMACHeader      string         `json:"hmac_header,omitempty"`
	HMACSecretEnc   []byte         `json:"hmac_secret_enc,omitempty"`
	HMACSecretNonce []byte         `json:"hmac_secret_nonce,omitempty"`
	HMACKeyVersion  int16          `json:"hmac_key_version,omitempty"`
	IPAllowlist     []netip.Prefix `json:"ip_allowlist,omitempty"`
}

// GetIngestConfig loads the ingest configuration of the endpoint with slug.
// Returns ErrNotFound for unknown slugs.
func (s *Store) GetIngestConfig(ctx context.Context, slug string) (IngestConfig, error) {
	var c IngestConfig
	var header, hmacHeader *string
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.user_id, e.slug, e.status, u.plan_type,
		       e.auth_mode, e.auth_header_key, e.auth_token_hash,
		       e.hmac_header, e.hmac_secret_encrypted, e.hmac_secret_nonce, e.hmac_key_version,
		       e.ip_allowlist
		FROM endpoints e
		JOIN users u ON u.id = e.user_id
		WHERE e.slug = $1`, slug,
	).Scan(&c.EndpointID, &c.UserID, &c.Slug, &c.Status, &c.PlanType,
		&c.AuthMode, &header, &c.AuthTokenHash,
		&hmacHeader, &c.HMACSecretEnc, &c.HMACSecretNonce, &c.HMACKeyVersion,
		&c.IPAllowlist)
	if err != nil {
		return IngestConfig{}, mapErr(err)
	}
	if header != nil {
		c.AuthHeaderKey = *header
	}
	if hmacHeader != nil {
		c.HMACHeader = *hmacHeader
	}
	return c, nil
}

// CreateEndpointParams are the fields needed to create an endpoint.
// Pipeline, retry and alert settings use the schema defaults and are edited later.
type CreateEndpointParams struct {
	UserID          string
	Name            string
	Description     string
	Slug            string
	TargetURL       string
	AuthMode        string // defaults to "token"
	AuthHeaderKey   string // defaults to "X-HookShield-Token"
	AuthTokenHash   []byte
	AuthTokenPrefix string
	HMACHeader      string
	HMACSecretEnc   []byte
	HMACSecretNonce []byte
	HMACKeyVersion  int16
	IPAllowlist     []netip.Prefix
}

// Endpoint is the subset of endpoint columns returned after creation.
type Endpoint struct {
	ID        string
	UserID    string
	Name      string
	Slug      string
	TargetURL string
	Status    string
	AuthMode  string
}

// CreateEndpoint inserts an endpoint. Returns ErrConflict if the slug is taken.
func (s *Store) CreateEndpoint(ctx context.Context, p CreateEndpointParams) (Endpoint, error) {
	if p.AuthMode == "" {
		p.AuthMode = AuthToken
	}
	if p.AuthHeaderKey == "" {
		p.AuthHeaderKey = "X-HookShield-Token"
	}
	if p.HMACKeyVersion == 0 {
		p.HMACKeyVersion = 1
	}
	if p.IPAllowlist == nil {
		p.IPAllowlist = []netip.Prefix{}
	}
	var e Endpoint
	err := s.pool.QueryRow(ctx, `
		INSERT INTO endpoints (user_id, name, description, slug, target_url, auth_mode, auth_header_key,
		                       auth_token_hash, auth_token_prefix, hmac_header, hmac_secret_encrypted,
		                       hmac_secret_nonce, hmac_key_version, ip_allowlist)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11, $12, $13, $14)
		RETURNING id, user_id, name, slug, target_url, status, auth_mode`,
		p.UserID, p.Name, p.Description, p.Slug, p.TargetURL, p.AuthMode, p.AuthHeaderKey,
		p.AuthTokenHash, p.AuthTokenPrefix, p.HMACHeader, p.HMACSecretEnc,
		p.HMACSecretNonce, p.HMACKeyVersion, p.IPAllowlist,
	).Scan(&e.ID, &e.UserID, &e.Name, &e.Slug, &e.TargetURL, &e.Status, &e.AuthMode)
	return e, mapErr(err)
}

// SetEndpointToken replaces the inbound token hash of an endpoint (token rotation).
func (s *Store) SetEndpointToken(ctx context.Context, endpointID string, hash []byte, prefix string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE endpoints SET auth_token_hash = $2, auth_token_prefix = $3 WHERE id = $1`,
		endpointID, hash, prefix)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEndpointStatus changes the status (active, paused, debug) of an endpoint.
func (s *Store) SetEndpointStatus(ctx context.Context, endpointID, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE endpoints SET status = $2 WHERE id = $1`, endpointID, status)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEndpointTarget changes the destination URL of an endpoint.
func (s *Store) SetEndpointTarget(ctx context.Context, endpointID, targetURL string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE endpoints SET target_url = $2 WHERE id = $1`, endpointID, targetURL)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
