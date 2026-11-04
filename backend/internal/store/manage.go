package store

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"
)

// This file holds the queries behind the control-plane API (Phase 6b).
// Every query is scoped by user_id: a row of another account behaves exactly
// like a missing row (ErrNotFound), so IDs cannot be probed.

// EndpointRecord is the full, editable state of an endpoint.
type EndpointRecord struct {
	ID                     string
	UserID                 string
	Name                   string
	Description            string
	Slug                   string
	TargetURL              string
	Status                 string
	AuthMode               string
	AuthHeaderKey          string
	AuthTokenHash          []byte
	AuthTokenPrefix        string
	HMACHeader             string
	HMACSecretEnc          []byte
	HMACSecretNonce        []byte
	HMACKeyVersion         int16
	IPAllowlist            []netip.Prefix
	OutboundHeaders        map[string]string
	TransformRules         json.RawMessage
	MaxRetries             int
	RetryStrategy          string
	RetryInitialMs         int
	RetryMultiplier        float64
	RetryMaxDelayMs        int
	RetryCustomScheduleMs  []int32
	DLQEnabled             bool
	AlertThresholdFailures int
	AlertWebhookURL        *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

const endpointColumns = `id, user_id, name, coalesce(description, ''), slug, target_url, status, auth_mode,
	auth_header_key, auth_token_hash, coalesce(auth_token_prefix, ''), coalesce(hmac_header, ''),
	hmac_secret_encrypted, hmac_secret_nonce, hmac_key_version, ip_allowlist, outbound_custom_headers,
	transform_rules, max_retries, retry_strategy, retry_initial_interval_ms, retry_multiplier::float8,
	retry_max_delay_ms, retry_custom_schedule_ms, dlq_enabled, alert_threshold_failures, alert_webhook_url,
	created_at, updated_at`

func scanEndpoint(r rowScanner) (EndpointRecord, error) {
	var e EndpointRecord
	var rules []byte
	err := r.Scan(&e.ID, &e.UserID, &e.Name, &e.Description, &e.Slug, &e.TargetURL, &e.Status, &e.AuthMode,
		&e.AuthHeaderKey, &e.AuthTokenHash, &e.AuthTokenPrefix, &e.HMACHeader,
		&e.HMACSecretEnc, &e.HMACSecretNonce, &e.HMACKeyVersion, &e.IPAllowlist, &e.OutboundHeaders,
		&rules, &e.MaxRetries, &e.RetryStrategy, &e.RetryInitialMs, &e.RetryMultiplier,
		&e.RetryMaxDelayMs, &e.RetryCustomScheduleMs, &e.DLQEnabled, &e.AlertThresholdFailures, &e.AlertWebhookURL,
		&e.CreatedAt, &e.UpdatedAt)
	e.TransformRules = rules
	if e.OutboundHeaders == nil {
		e.OutboundHeaders = map[string]string{}
	}
	return e, mapErr(err)
}

// GetEndpoint returns an endpoint of userID.
func (s *Store) GetEndpoint(ctx context.Context, userID, id string) (EndpointRecord, error) {
	return scanEndpoint(s.pool.QueryRow(ctx,
		`SELECT `+endpointColumns+` FROM endpoints WHERE id = $1 AND user_id = $2`, id, userID))
}

// ListEndpoints returns every endpoint of userID, newest first.
func (s *Store) ListEndpoints(ctx context.Context, userID string) ([]EndpointRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+endpointColumns+` FROM endpoints WHERE user_id = $1 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndpointRecord{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func endpointArgs(e EndpointRecord) []any {
	if e.OutboundHeaders == nil {
		e.OutboundHeaders = map[string]string{}
	}
	if len(e.TransformRules) == 0 {
		e.TransformRules = json.RawMessage(`{}`)
	}
	if e.IPAllowlist == nil {
		e.IPAllowlist = []netip.Prefix{}
	}
	if e.HMACKeyVersion == 0 {
		e.HMACKeyVersion = 1
	}
	return []any{e.ID, e.UserID, e.Name, e.Description, e.Slug, e.TargetURL, e.Status, e.AuthMode,
		e.AuthHeaderKey, e.AuthTokenHash, e.AuthTokenPrefix, e.HMACHeader, e.HMACSecretEnc, e.HMACSecretNonce,
		e.HMACKeyVersion, e.IPAllowlist, e.OutboundHeaders, string(e.TransformRules), e.MaxRetries,
		e.RetryStrategy, e.RetryInitialMs, e.RetryMultiplier, e.RetryMaxDelayMs, e.RetryCustomScheduleMs,
		e.DLQEnabled, e.AlertThresholdFailures, e.AlertWebhookURL}
}

// InsertEndpoint stores a new endpoint. e.ID must be set by the caller (the
// HMAC secret is encrypted with the ID as associated data). Returns
// ErrConflict when the slug is taken.
func (s *Store) InsertEndpoint(ctx context.Context, e EndpointRecord) (EndpointRecord, error) {
	return scanEndpoint(s.pool.QueryRow(ctx, `
		INSERT INTO endpoints (id, user_id, name, description, slug, target_url, status, auth_mode,
		       auth_header_key, auth_token_hash, auth_token_prefix, hmac_header, hmac_secret_encrypted,
		       hmac_secret_nonce, hmac_key_version, ip_allowlist, outbound_custom_headers, transform_rules,
		       max_retries, retry_strategy, retry_initial_interval_ms, retry_multiplier, retry_max_delay_ms,
		       retry_custom_schedule_ms, dlq_enabled, alert_threshold_failures, alert_webhook_url)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, NULLIF($11, ''), NULLIF($12, ''), $13,
		        $14, $15, $16, $17, $18::jsonb, $19, $20, $21, $22, $23, $24, $25, $26, $27)
		RETURNING `+endpointColumns, endpointArgs(e)...))
}

// UpdateEndpoint writes every editable column of an endpoint of e.UserID
// (the slug never changes: senders already use it).
func (s *Store) UpdateEndpoint(ctx context.Context, e EndpointRecord) (EndpointRecord, error) {
	return scanEndpoint(s.pool.QueryRow(ctx, `
		UPDATE endpoints SET name = $3, description = NULLIF($4, ''), target_url = $6, status = $7,
		       auth_mode = $8, auth_header_key = $9, auth_token_hash = $10, auth_token_prefix = NULLIF($11, ''),
		       hmac_header = NULLIF($12, ''), hmac_secret_encrypted = $13, hmac_secret_nonce = $14,
		       hmac_key_version = $15, ip_allowlist = $16, outbound_custom_headers = $17,
		       transform_rules = $18::jsonb, max_retries = $19, retry_strategy = $20,
		       retry_initial_interval_ms = $21, retry_multiplier = $22, retry_max_delay_ms = $23,
		       retry_custom_schedule_ms = $24, dlq_enabled = $25, alert_threshold_failures = $26,
		       alert_webhook_url = $27
		WHERE id = $1 AND user_id = $2 AND slug = $5
		RETURNING `+endpointColumns, endpointArgs(e)...))
}

// DeleteEndpoint removes an endpoint of userID (its logs and channels cascade)
// and returns its slug, so the caller can invalidate the ingest cache.
func (s *Store) DeleteEndpoint(ctx context.Context, userID, id string) (slug string, err error) {
	err = s.pool.QueryRow(ctx, `DELETE FROM endpoints WHERE id = $1 AND user_id = $2 RETURNING slug`, id, userID).Scan(&slug)
	return slug, mapErr(err)
}

// SecretInfo describes a stored secret without its value.
type SecretInfo struct {
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListUserSecrets returns the names of userID's secrets (never the values).
func (s *Store) ListUserSecrets(ctx context.Context, userID string) ([]SecretInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, created_at, updated_at FROM user_secrets WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretInfo{}
	for rows.Next() {
		var i SecretInfo
		if err := rows.Scan(&i.Name, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GetUserSecretInfo returns one secret's metadata.
func (s *Store) GetUserSecretInfo(ctx context.Context, userID, name string) (SecretInfo, error) {
	var i SecretInfo
	err := s.pool.QueryRow(ctx,
		`SELECT name, created_at, updated_at FROM user_secrets WHERE user_id = $1 AND name = $2`, userID, name).
		Scan(&i.Name, &i.CreatedAt, &i.UpdatedAt)
	return i, mapErr(err)
}

// DeleteUserSecret removes a secret of userID.
func (s *Store) DeleteUserSecret(ctx context.Context, userID, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM user_secrets WHERE user_id = $1 AND name = $2`, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AlertChannelRecord is a stored alert channel with its metadata.
type AlertChannelRecord struct {
	AlertChannel
	CreatedAt time.Time
}

const alertChannelColumns = `id, endpoint_id, type, target_encrypted, target_nonce, key_version, is_enabled, created_at`

func scanAlertChannel(r rowScanner) (AlertChannelRecord, error) {
	var c AlertChannelRecord
	err := r.Scan(&c.ID, &c.EndpointID, &c.Type, &c.TargetEncrypted, &c.TargetNonce, &c.KeyVersion, &c.Enabled, &c.CreatedAt)
	return c, mapErr(err)
}

// ListAllAlertChannels returns every channel of userID, enabled or not.
func (s *Store) ListAllAlertChannels(ctx context.Context, userID string) ([]AlertChannelRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertChannelRecord{}
	for rows.Next() {
		c, err := scanAlertChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetAlertChannel returns a channel of userID.
func (s *Store) GetAlertChannel(ctx context.Context, userID, id string) (AlertChannelRecord, error) {
	return scanAlertChannel(s.pool.QueryRow(ctx,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE id = $1 AND user_id = $2`, id, userID))
}

// UpdateAlertChannel replaces the scope, target and state of a channel of userID.
func (s *Store) UpdateAlertChannel(ctx context.Context, userID string, c AlertChannel) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE alert_channels SET endpoint_id = $3, target_encrypted = $4, target_nonce = $5, key_version = $6, is_enabled = $7
		WHERE id = $1 AND user_id = $2`,
		c.ID, userID, c.EndpointID, c.TargetEncrypted, c.TargetNonce, c.KeyVersion, c.Enabled)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAlertChannel removes a channel of userID.
func (s *Store) DeleteAlertChannel(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_channels WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// API key scopes.
const (
	ScopeReadOnly = "read_only"
	ScopeIngest   = "ingest"
	ScopeAdmin    = "admin"
)

// APIKey is a control-plane API key (the secret itself is never stored).
type APIKey struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

const apiKeyColumns = `id, user_id, name, prefix, scope, created_at, last_used_at, expires_at, revoked_at`

func scanAPIKey(r rowScanner) (APIKey, error) {
	var k APIKey
	err := r.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.Scope, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt)
	return k, mapErr(err)
}

// CreateAPIKey stores a key by its hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID, name, prefix, scope string, hash []byte, expiresAt *time.Time) (APIKey, error) {
	return scanAPIKey(s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, prefix, key_hash, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+apiKeyColumns,
		userID, name, prefix, hash, scope, expiresAt))
}

// ListAPIKeys returns the keys of userID, revoked ones included, newest first.
func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+apiKeyColumns+` FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey revokes a key of userID (idempotent) and returns it.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, id string) (APIKey, error) {
	return scanAPIKey(s.pool.QueryRow(ctx, `
		UPDATE api_keys SET revoked_at = coalesce(revoked_at, now())
		WHERE id = $1 AND user_id = $2 RETURNING `+apiKeyColumns, id, userID))
}

// RevokeAllAPIKeys revokes every active key of userID and returns how many.
func (s *Store) RevokeAllAPIKeys(ctx context.Context, userID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
}

// GetAPIKeyByHash returns a key and its owner (revoked and expired keys too:
// the caller decides).
func (s *Store) GetAPIKeyByHash(ctx context.Context, hash []byte) (APIKey, User, error) {
	var k APIKey
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT k.id, k.user_id, k.name, k.prefix, k.scope, k.created_at, k.last_used_at, k.expires_at, k.revoked_at,
		       u.id, u.email, u.email_verified_at, u.name, u.locale, u.theme_preference, u.plan_type,
		       u.password_hash IS NOT NULL, u.created_at
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1`, hash).
		Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.Scope, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt,
			&u.ID, &u.Email, &u.EmailVerifiedAt, &u.Name, &u.Locale, &u.ThemePreference, &u.PlanType, &u.HasPassword, &u.CreatedAt)
	return k, u, mapErr(err)
}

// TouchAPIKey records a use of a key, at most once a minute (keeps writes low on busy keys).
func (s *Store) TouchAPIKey(ctx context.Context, id string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = $2
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < $2::timestamptz - interval '1 minute')`, id, now)
	return err
}
