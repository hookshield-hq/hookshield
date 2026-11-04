package store

import (
	"context"
	"strings"
	"time"
)

// User is an account row (credentials excluded).
type User struct {
	ID              string
	Email           string
	EmailVerifiedAt *time.Time
	Name            *string
	Locale          string
	ThemePreference string
	PlanType        string
	HasPassword     bool // false for Google-only accounts
	CreatedAt       time.Time
}

// userColumns is the SELECT list matching scanUser.
const userColumns = `id, email, email_verified_at, name, locale, theme_preference, plan_type, password_hash IS NOT NULL, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(r rowScanner) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.Name, &u.Locale, &u.ThemePreference, &u.PlanType, &u.HasPassword, &u.CreatedAt)
	return u, mapErr(err)
}

// CreateUserParams are the fields accepted when creating a user.
type CreateUserParams struct {
	Email         string
	Name          string // optional
	Locale        string // optional, defaults to "en"
	PlanType      string // optional, defaults to "developer"
	PasswordHash  string // optional (Google-only accounts have none)
	EmailVerified bool   // true when the email is already verified (e.g. by Google)
}

// NormalizeEmail lower-cases and trims an email address.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// CreateUser inserts a user. Emails are normalised to lower case.
// Returns ErrConflict if the email is taken.
func (s *Store) CreateUser(ctx context.Context, p CreateUserParams) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, locale, plan_type, password_hash, email_verified_at)
		VALUES ($1, NULLIF($2, ''), COALESCE(NULLIF($3, ''), 'en'), COALESCE(NULLIF($4, ''), 'developer'), NULLIF($5, ''),
		        CASE WHEN $6 THEN now() END)
		RETURNING `+userColumns,
		NormalizeEmail(p.Email), p.Name, p.Locale, p.PlanType, p.PasswordHash, p.EmailVerified))
}

// GetUserByEmail looks a user up by (case-insensitive) email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, NormalizeEmail(email)))
}
