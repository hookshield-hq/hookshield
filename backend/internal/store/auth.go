package store

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── users ────────────────────────────────────────────────

// GetUserByID returns a user.
func (s *Store) GetUserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// GetCredentials returns a user and its password hash ("" for Google-only accounts).
func (s *Store) GetCredentials(ctx context.Context, email string) (User, string, error) {
	var hash *string
	var u User
	err := s.pool.QueryRow(ctx, `SELECT `+userColumns+`, password_hash FROM users WHERE email = $1`, NormalizeEmail(email)).
		Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.Name, &u.Locale, &u.ThemePreference, &u.PlanType, &u.HasPassword, &u.CreatedAt, &hash)
	if err != nil {
		return User{}, "", mapErr(err)
	}
	if hash == nil {
		return u, "", nil
	}
	return u, *hash, nil
}

// SetPassword replaces a user's password hash.
func (s *Store) SetPassword(ctx context.Context, userID, hash string) error {
	return execOne(s, ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
}

// ClearPassword removes the password of an account (Google sign-in only from now on).
func (s *Store) ClearPassword(ctx context.Context, userID string) error {
	return execOne(s, ctx, `UPDATE users SET password_hash = NULL WHERE id = $1`, userID)
}

// MarkEmailVerified sets email_verified_at if it is not set yet.
func (s *Store) MarkEmailVerified(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, userID)
	return mapErr(err)
}

// ProfileUpdate holds optional profile changes (nil = unchanged).
type ProfileUpdate struct {
	Name            *string
	Locale          *string
	ThemePreference *string
}

// UpdateProfile applies the non-nil fields and returns the updated user.
func (s *Store) UpdateProfile(ctx context.Context, userID string, p ProfileUpdate) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		UPDATE users SET
			name             = CASE WHEN $2::boolean THEN NULLIF($3, '') ELSE name END,
			locale           = COALESCE($4, locale),
			theme_preference = COALESCE($5, theme_preference)
		WHERE id = $1
		RETURNING `+userColumns,
		userID, p.Name != nil, deref(p.Name), p.Locale, p.ThemePreference))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func execOne(s *Store, ctx context.Context, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── sessions ─────────────────────────────────────────────

// Session is a dashboard login.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession stores a session (only the token hash).
func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, ip netip.Addr, userAgent string, expires time.Time) (Session, error) {
	var ipArg any
	if ip.IsValid() {
		ipArg = ip.String()
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	var ses Session
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, ip, user_agent, expires_at)
		VALUES ($1, $2, $3::inet, NULLIF($4, ''), $5)
		RETURNING id, user_id, created_at, last_seen_at, expires_at`,
		userID, tokenHash, ipArg, userAgent, expires,
	).Scan(&ses.ID, &ses.UserID, &ses.CreatedAt, &ses.LastSeenAt, &ses.ExpiresAt)
	return ses, mapErr(err)
}

// GetSession returns a valid (not expired) session and its user.
func (s *Store) GetSession(ctx context.Context, tokenHash []byte) (Session, User, error) {
	var ses Session
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at,
		       u.id, u.email, u.email_verified_at, u.name, u.locale, u.theme_preference, u.plan_type,
		       u.password_hash IS NOT NULL, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash,
	).Scan(&ses.ID, &ses.UserID, &ses.CreatedAt, &ses.LastSeenAt, &ses.ExpiresAt,
		&u.ID, &u.Email, &u.EmailVerifiedAt, &u.Name, &u.Locale, &u.ThemePreference, &u.PlanType, &u.HasPassword, &u.CreatedAt)
	return ses, u, mapErr(err)
}

// TouchSession records activity and slides the expiry.
func (s *Store) TouchSession(ctx context.Context, sessionID string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1`, sessionID, expires)
	return mapErr(err)
}

// DeleteSession removes one session (logout).
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return mapErr(err)
}

// DeleteUserSessions removes every session of a user except keepID ("" = all).
func (s *Store) DeleteUserSessions(ctx context.Context, userID, keepID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND ($2 = '' OR id::text <> $2)`, userID, keepID)
	return tag.RowsAffected(), mapErr(err)
}

// PurgeAuthRecords deletes dashboard sessions that expired or passed the
// absolute lifetime maxAge, and one-time email tokens that were used or
// expired. Runs as a worker job, so no stale IP address or user agent stays
// in the database (the privacy policy promises it).
func (s *Store) PurgeAuthRecords(ctx context.Context, now time.Time, maxAge time.Duration) (sessions, tokens int64, err error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1 OR created_at <= $2`, now, now.Add(-maxAge))
	if err != nil {
		return 0, 0, mapErr(err)
	}
	sessions = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `DELETE FROM email_tokens WHERE expires_at <= $1 OR used_at IS NOT NULL`, now)
	if err != nil {
		return sessions, 0, mapErr(err)
	}
	return sessions, tag.RowsAffected(), nil
}

// ── one-time email tokens ────────────────────────────────

// Email token purposes (email_tokens.purpose).
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"
)

// CreateEmailToken stores a new token and invalidates older unused tokens of
// the same purpose, so only the latest link works.
func (s *Store) CreateEmailToken(ctx context.Context, userID, purpose string, tokenHash []byte, expires time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE email_tokens SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_tokens (user_id, purpose, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, purpose, tokenHash, expires); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// ConsumeEmailToken marks a valid token as used and returns its user.
// Expired, used or unknown tokens return ErrNotFound.
func (s *Store) ConsumeEmailToken(ctx context.Context, tokenHash []byte, purpose string) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		UPDATE email_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, tokenHash, purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, mapErr(err)
}

// PeekEmailToken returns the user of a valid token without consuming it, so
// the caller can validate its input first (a failed attempt must not burn
// the link). Expired, used or unknown tokens return ErrNotFound.
func (s *Store) PeekEmailToken(ctx context.Context, tokenHash []byte, purpose string) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		SELECT user_id FROM email_tokens
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()`, tokenHash, purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, mapErr(err)
}

// ResetAccount empties an account whose previous holder must keep no access
// (an unverified registration taken over by the proven owner of the email).
// In one transaction it deletes the endpoints (their logs, attempts and
// endpoint alert channels cascade), every alert channel, secret, API key and
// session, and the password. The user row itself (and billing) is kept.
// It returns the slugs of the deleted endpoints so the caller can invalidate
// the ingest cache.
func (s *Store) ResetAccount(ctx context.Context, userID string) (slugs []string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `DELETE FROM endpoints WHERE user_id = $1 RETURNING slug`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		slugs = append(slugs, slug)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	for _, q := range []string{
		`DELETE FROM alert_channels WHERE user_id = $1`,
		`DELETE FROM user_secrets WHERE user_id = $1`,
		`DELETE FROM api_keys WHERE user_id = $1`,
		`DELETE FROM sessions WHERE user_id = $1`,
		`UPDATE users SET password_hash = NULL WHERE id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return nil, mapErr(err)
		}
	}
	return slugs, mapErr(tx.Commit(ctx))
}

// ── OAuth ────────────────────────────────────────────────

// GetUserByOAuth returns the user linked to an external identity.
func (s *Store) GetUserByOAuth(ctx context.Context, provider, providerUserID string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.email_verified_at, u.name, u.locale, u.theme_preference, u.plan_type,
		       u.password_hash IS NOT NULL, u.created_at
		FROM oauth_accounts o JOIN users u ON u.id = o.user_id
		WHERE o.provider = $1 AND o.provider_user_id = $2`, provider, providerUserID))
}

// LinkOAuth links an external identity to a user (idempotent).
func (s *Store) LinkOAuth(ctx context.Context, userID, provider, providerUserID, email string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_accounts (user_id, provider, provider_user_id, email)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (provider, provider_user_id) DO NOTHING`, userID, provider, providerUserID, NormalizeEmail(email))
	return mapErr(err)
}
