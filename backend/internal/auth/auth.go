// Package auth implements dashboard authentication (guide D6): email +
// password (argon2id) and Google sign-in, server-side sessions, email
// verification and password reset. It is HTTP-agnostic; internal/api maps it
// to routes, cookies and error codes.
//
// Security properties:
//   - only hashes are stored (passwords: argon2id; session and email tokens: SHA-256);
//   - logins for unknown emails take as long as for known ones (no enumeration
//     by timing) and forgot-password always answers the same way, at once (the
//     lookup and the email happen in the background);
//   - failed logins are rate-limited per email and per IP, sign-ups per IP;
//   - a failed password reset (weak new password) does not burn the link;
//   - when the proven owner of an email takes over an unverified registration
//     (Google sign-in or password reset), the account is emptied first;
//   - resetting or changing the password signs out every other session;
//   - Google sign-in uses state + PKCE and links accounts only through
//     Google-verified emails.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/emails"
	"github.com/hookshield-hq/hookshield/backend/internal/mailer"
	"github.com/hookshield-hq/hookshield/backend/internal/password"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
)

// Error is a failure with a stable, translatable code and an HTTP status.
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return e.Code }

func newErr(code string, status int) *Error { return &Error{Code: code, Status: status} }

// Errors returned by the service.
var (
	ErrInvalidCredentials = newErr("auth.invalid_credentials", http.StatusUnauthorized)
	ErrUnauthorized       = newErr("auth.unauthorized", http.StatusUnauthorized)
	ErrEmailTaken         = newErr("auth.email_taken", http.StatusConflict)
	ErrInvalidEmail       = newErr("auth.invalid_email", http.StatusUnprocessableEntity)
	ErrInvalidLocale      = newErr("auth.invalid_locale", http.StatusUnprocessableEntity)
	ErrInvalidTheme       = newErr("auth.invalid_theme", http.StatusUnprocessableEntity)
	ErrInvalidName        = newErr("auth.invalid_name", http.StatusUnprocessableEntity)
	ErrInvalidToken       = newErr("auth.invalid_token", http.StatusBadRequest)
	ErrTooManyAttempts    = newErr("auth.too_many_attempts", http.StatusTooManyRequests)
	ErrWrongPassword      = newErr("auth.wrong_password", http.StatusUnprocessableEntity)
	ErrGoogleDisabled     = newErr("auth.google_disabled", http.StatusNotFound)
	ErrGoogleFailed       = newErr("auth.google_failed", http.StatusBadGateway)
	ErrGoogleUnverified   = newErr("auth.google_email_unverified", http.StatusForbidden)
)

// passwordError maps password policy errors to API errors.
func passwordError(err error) *Error {
	return newErr(err.Error(), http.StatusUnprocessableEntity) // password.too_short, …
}

// Locales accepted for users.locale.
var Locales = map[string]bool{"en": true, "pt-BR": true, "es": true}

// Themes accepted for users.theme_preference.
var Themes = map[string]bool{"dark": true, "light": true, "system": true}

// Options configure the service.
type Options struct {
	AppURL           string        // dashboard base URL used in email links
	SessionTTL       time.Duration // default 30 days (sliding)
	SessionMaxAge    time.Duration // absolute lifetime whatever the activity (default 90 days)
	Mailer           mailer.Mailer // nil = account emails are logged and skipped
	Google           GoogleConfig
	LoginWindow      time.Duration // failed-login counting window (default 15 min)
	MaxFailuresEmail int64         // per email per window (default 10)
	MaxFailuresIP    int64         // per IP per window (default 50)
	MaxSignupsIP     int64         // sign-up attempts per IP per LoginWindow (default 10)
	MailCooldown     time.Duration // min time between two emails of one kind to one user (default 60s)
	MailTimeout      time.Duration // limit of a background account email (default 30s)
	// EndpointCache is invalidated for the endpoints deleted when an account
	// is taken over (nil = no ingest cache to invalidate).
	EndpointCache EndpointInvalidator
	Logger        *slog.Logger
	Now           func() time.Time
}

// EndpointInvalidator drops the ingest cache entry of an endpoint (implemented
// by *endpointcache.Cache).
type EndpointInvalidator interface {
	Invalidate(ctx context.Context, slug string) error
}

// Service implements the authentication use cases.
type Service struct {
	st   *store.Store
	rdb  *redis.Client
	keys redisx.Keys
	opts Options
	http *http.Client
	bg   sync.WaitGroup // background account emails
}

// New builds the service.
func New(st *store.Store, rdb *redis.Client, keys redisx.Keys, opts Options) *Service {
	if opts.SessionMaxAge <= 0 {
		opts.SessionMaxAge = 90 * 24 * time.Hour
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 30 * 24 * time.Hour
	}
	if opts.LoginWindow <= 0 {
		opts.LoginWindow = 15 * time.Minute
	}
	if opts.MaxFailuresEmail <= 0 {
		opts.MaxFailuresEmail = 10
	}
	if opts.MaxFailuresIP <= 0 {
		opts.MaxFailuresIP = 50
	}
	if opts.MaxSignupsIP <= 0 {
		opts.MaxSignupsIP = 10
	}
	if opts.MailCooldown == 0 {
		opts.MailCooldown = time.Minute
	}
	if opts.MailTimeout <= 0 {
		opts.MailTimeout = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.AppURL = strings.TrimRight(opts.AppURL, "/")
	opts.Google.applyDefaults()
	return &Service{st: st, rdb: rdb, keys: keys, opts: opts, http: &http.Client{Timeout: 10 * time.Second}}
}

// Wait blocks until background work (account emails) is done; call it on
// shutdown (and in tests).
func (s *Service) Wait() { s.bg.Wait() }

// background runs fn after the request returns, with its own timeout (the
// request context is cancelled when the response is written). Errors are
// logged by fn.
func (s *Service) background(ctx context.Context, fn func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.MailTimeout)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer cancel()
		fn(ctx)
	}()
}

// SessionTTL returns the sliding session lifetime.
func (s *Service) SessionTTL() time.Duration { return s.opts.SessionTTL }

// sessionSlideEvery is how often activity extends a session (one write per hour at most).
const sessionSlideEvery = time.Hour

// CookieMaxAge is how long the browser should keep the cookie of ses from
// now: the sliding lifetime, never past the absolute one (HS_SESSION_MAX_AGE).
func (s *Service) CookieMaxAge(ses store.Session) time.Duration {
	left := s.opts.SessionMaxAge - s.opts.Now().Sub(ses.CreatedAt)
	return max(min(s.opts.SessionTTL, left), 0)
}

// Slides reports whether Authenticate extends ses (given the session as it
// returned it): the cookie must then be extended too, or the browser would
// drop an active session SessionTTL after sign-in.
func (s *Service) Slides(ses store.Session) bool {
	return s.opts.Now().Sub(ses.LastSeenAt) > sessionSlideEvery
}

// GoogleEnabled reports whether Google sign-in is configured.
func (s *Service) GoogleEnabled() bool { return s.opts.Google.enabled() }

// Meta describes the client of a request.
type Meta struct {
	IP        netip.Addr
	UserAgent string
}

// Login is a successful authentication: the user and the session token to set as cookie.
type Login struct {
	User    store.User
	Token   string
	Session store.Session
}

// ── registration & login ─────────────────────────────────

// RegisterInput is the sign-up form.
type RegisterInput struct {
	Email    string
	Password string
	Name     string
	Locale   string
}

// Register creates an account, sends the verification email and signs in.
func (s *Service) Register(ctx context.Context, in RegisterInput, meta Meta) (Login, error) {
	email, err := ValidEmail(in.Email)
	if err != nil {
		return Login{}, err
	}
	if in.Locale == "" {
		in.Locale = "en"
	}
	if !Locales[in.Locale] {
		return Login{}, ErrInvalidLocale
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Name)) > 100 {
		return Login{}, ErrInvalidName
	}
	if err := password.Validate(in.Password, email); err != nil {
		return Login{}, passwordError(err)
	}
	// Each sign-up costs an argon2id hash: throttle them per IP with the
	// login limiter (every attempt counts, successful or not). The hash is
	// computed for taken emails too, so all valid sign-ups cost the same.
	if meta.IP.IsValid() && s.overLimit(ctx, limit{s.keys.AuthFailures("signup-ip:" + throttleIP(meta.IP)), s.opts.MaxSignupsIP}) {
		return Login{}, ErrTooManyAttempts
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return Login{}, err
	}
	u, err := s.st.CreateUser(ctx, store.CreateUserParams{Email: email, Name: strings.TrimSpace(in.Name), Locale: in.Locale, PasswordHash: hash})
	if errors.Is(err, store.ErrConflict) {
		return Login{}, ErrEmailTaken
	}
	if err != nil {
		return Login{}, err
	}
	s.sendTokenEmail(ctx, u, store.PurposeVerifyEmail, emails.VerifyEmail, "/verify-email", 48*time.Hour, false)
	return s.startSession(ctx, u, meta)
}

// Login checks email + password, with per-email and per-IP throttling.
func (s *Service) Login(ctx context.Context, email, pw string, meta Meta) (Login, error) {
	email = store.NormalizeEmail(email)
	emailKey := s.keys.AuthFailures("email:" + sha(email))
	ipKey := ""
	if meta.IP.IsValid() {
		ipKey = s.keys.AuthFailures("ip:" + throttleIP(meta.IP))
	}
	// Count the attempt before checking the password: checking first let
	// parallel requests all pass the limit (every one a real password check).
	limits := []limit{{emailKey, s.opts.MaxFailuresEmail}}
	if ipKey != "" {
		limits = append(limits, limit{ipKey, s.opts.MaxFailuresIP})
	}
	if s.overLimit(ctx, limits...) {
		return Login{}, ErrTooManyAttempts
	}

	u, hash, err := s.st.GetCredentials(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Login{}, err
	}
	if errors.Is(err, store.ErrNotFound) || hash == "" {
		password.VerifyDummy(pw) // same cost as a real check
		return Login{}, ErrInvalidCredentials
	}
	ok, err := password.Verify(hash, pw)
	if err != nil || !ok {
		return Login{}, ErrInvalidCredentials
	}
	// A successful login does not count against the email or the IP.
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, emailKey)
	if ipKey != "" {
		pipe.Decr(ctx, ipKey)
	}
	_, _ = pipe.Exec(ctx)

	if password.NeedsRehash(hash) {
		if nh, err := password.Hash(pw); err == nil {
			_ = s.st.SetPassword(ctx, u.ID, nh)
		}
	}
	return s.startSession(ctx, u, meta)
}

// limit is one throttling counter and its maximum per LoginWindow.
type limit struct {
	key string
	max int64
}

// overLimit counts one attempt on every key (atomically, window starting at
// the first attempt) and reports whether any is over its limit. Redis down =
// not limited (logins keep working).
func (s *Service) overLimit(ctx context.Context, limits ...limit) bool {
	pipe := s.rdb.TxPipeline()
	counts := make([]*redis.IntCmd, len(limits))
	for i, l := range limits {
		counts[i] = pipe.Incr(ctx, l.key)
		pipe.ExpireNX(ctx, l.key, s.opts.LoginWindow)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false
	}
	for i, l := range limits {
		if counts[i].Val() > l.max {
			return true
		}
	}
	return false
}

// throttleIP is the client identity for login throttling: the address for
// IPv4, the /64 network for IPv6 (one host gets a whole /64 to rotate through).
func throttleIP(a netip.Addr) string {
	a = a.Unmap()
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

func (s *Service) startSession(ctx context.Context, u store.User, meta Meta) (Login, error) {
	tok, err := token.Generate(token.PrefixSession)
	if err != nil {
		return Login{}, err
	}
	ses, err := s.st.CreateSession(ctx, u.ID, token.Hash(tok), meta.IP, meta.UserAgent, s.opts.Now().Add(s.opts.SessionTTL))
	if err != nil {
		return Login{}, err
	}
	return Login{User: u, Token: tok, Session: ses}, nil
}

// Authenticate resolves a session cookie. Activity slides the expiry, at most
// once per hour to avoid a write per request.
func (s *Service) Authenticate(ctx context.Context, sessionToken string) (store.Session, store.User, error) {
	if !strings.HasPrefix(sessionToken, token.PrefixSession) {
		return store.Session{}, store.User{}, ErrUnauthorized
	}
	ses, u, err := s.st.GetSession(ctx, token.Hash(sessionToken))
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, store.User{}, ErrUnauthorized
	}
	if err != nil {
		return store.Session{}, store.User{}, err
	}
	if s.opts.Now().Sub(ses.CreatedAt) > s.opts.SessionMaxAge {
		_ = s.st.DeleteSession(ctx, token.Hash(sessionToken))
		return store.Session{}, store.User{}, ErrUnauthorized
	}
	if s.Slides(ses) {
		_ = s.st.TouchSession(ctx, ses.ID, s.opts.Now().Add(s.opts.SessionTTL))
	}
	return ses, u, nil
}

// AuthenticateAPIKey resolves an API key (Authorization: Bearer hs_live_…).
// Unknown, revoked and expired keys all get ErrUnauthorized.
func (s *Service) AuthenticateAPIKey(ctx context.Context, key string) (store.APIKey, store.User, error) {
	if !strings.HasPrefix(key, token.PrefixLive) {
		return store.APIKey{}, store.User{}, ErrUnauthorized
	}
	k, u, err := s.st.GetAPIKeyByHash(ctx, token.Hash(key))
	if errors.Is(err, store.ErrNotFound) {
		return store.APIKey{}, store.User{}, ErrUnauthorized
	}
	if err != nil {
		return store.APIKey{}, store.User{}, err
	}
	now := s.opts.Now()
	if k.RevokedAt != nil || (k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)) {
		return store.APIKey{}, store.User{}, ErrUnauthorized
	}
	if err := s.st.TouchAPIKey(ctx, k.ID, now); err != nil {
		s.opts.Logger.Warn("api key last_used_at not recorded", "key_id", k.ID, "err", err)
	}
	return k, u, nil
}

// Logout deletes the session.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.st.DeleteSession(ctx, token.Hash(sessionToken))
}

// ── email verification & password reset ──────────────────

// VerifyEmail consumes a verification token.
func (s *Service) VerifyEmail(ctx context.Context, tok string) error {
	userID, err := s.st.ConsumeEmailToken(ctx, token.Hash(tok), store.PurposeVerifyEmail)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	return s.st.MarkEmailVerified(ctx, userID)
}

// ResendVerification sends a new verification email (throttled; silent when already verified).
func (s *Service) ResendVerification(ctx context.Context, u store.User) {
	if u.EmailVerifiedAt != nil {
		return
	}
	s.sendTokenEmail(ctx, u, store.PurposeVerifyEmail, emails.VerifyEmail, "/verify-email", 48*time.Hour, true)
}

// ForgotPassword emails a reset link when the account exists. It never reveals
// whether the email is registered: it returns at once, and the lookup and the
// email (sent only for existing accounts) happen in the background, so the
// response time does not depend on the account existing either.
func (s *Service) ForgotPassword(ctx context.Context, email string) {
	s.background(ctx, func(ctx context.Context) {
		u, err := s.st.GetUserByEmail(ctx, email)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			s.opts.Logger.Error("forgot password: load user failed", "err", err)
			return
		}
		s.sendTokenEmail(ctx, u, store.PurposeResetPassword, emails.ResetPassword, "/reset-password", time.Hour, true)
	})
}

// ResetPassword sets a new password from a reset token and signs out every session.
// Proving access to the inbox also verifies the email. The new password is
// validated before the token is consumed, so a rejected password does not
// burn the link.
func (s *Service) ResetPassword(ctx context.Context, tok, newPassword string) error {
	hash := token.Hash(tok)
	userID, err := s.st.PeekEmailToken(ctx, hash, store.PurposeResetPassword)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	u, err := s.st.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := password.Validate(newPassword, u.Email); err != nil {
		return passwordError(err)
	}
	// Consuming is atomic: of two concurrent resets with one link, one wins.
	if consumed, err := s.st.ConsumeEmailToken(ctx, hash, store.PurposeResetPassword); errors.Is(err, store.ErrNotFound) || (err == nil && consumed != u.ID) {
		return ErrInvalidToken
	} else if err != nil {
		return err
	}
	if u.EmailVerifiedAt == nil {
		// The registrant never proved owning this address; the person holding
		// the link just did. Same pre-hijacking defence as Google sign-in: the
		// account starts clean (sessions and API keys included).
		if err := s.resetAccount(ctx, u.ID); err != nil {
			return err
		}
	} else if _, err := s.st.RevokeAllAPIKeys(ctx, u.ID); err != nil {
		// Account recovery evicts everyone: sessions (setPassword) and API keys,
		// which an intruder could have minted with a stolen session or password.
		return err
	}
	if err := s.setPassword(ctx, u, newPassword, ""); err != nil {
		return err
	}
	return s.st.MarkEmailVerified(ctx, u.ID)
}

// resetAccount empties an unverified account taken over by the proven owner
// of its email: endpoints (with their logs, inbound tokens and HMAC secrets),
// alert channels, secrets, API keys, sessions and the password are deleted,
// so whoever registered the address first keeps no access and no foothold
// (e.g. an alert channel or destination of theirs). The user row is kept.
func (s *Service) resetAccount(ctx context.Context, userID string) error {
	slugs, err := s.st.ResetAccount(ctx, userID)
	if err != nil {
		return err
	}
	if s.opts.EndpointCache != nil {
		for _, slug := range slugs {
			if err := s.opts.EndpointCache.Invalidate(ctx, slug); err != nil {
				// The endpoint is gone from the database: its cached token only
				// lets webhooks in that the processor then drops (TTL 10 min).
				s.opts.Logger.Warn("invalidate endpoint cache failed", "slug", slug, "err", err)
			}
		}
	}
	s.opts.Logger.Warn("unverified account taken over by the email owner: account emptied", "user_id", userID, "endpoints", len(slugs))
	return nil
}

// ChangePassword changes the password of a signed-in user. Accounts created
// with Google (no password yet) can set one without the current password.
// Every other session is signed out.
func (s *Service) ChangePassword(ctx context.Context, u store.User, currentSessionID, current, next string) error {
	if u.HasPassword {
		_, hash, err := s.st.GetCredentials(ctx, u.Email)
		if err != nil {
			return err
		}
		if ok, _ := password.Verify(hash, current); !ok {
			return ErrWrongPassword
		}
	}
	if err := password.Validate(next, u.Email); err != nil {
		return passwordError(err)
	}
	return s.setPassword(ctx, u, next, currentSessionID)
}

func (s *Service) setPassword(ctx context.Context, u store.User, pw, keepSession string) error {
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	if err := s.st.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	if _, err := s.st.DeleteUserSessions(ctx, u.ID, keepSession); err != nil {
		return err
	}
	s.send(ctx, u, emails.PasswordChanged, "")
	return nil
}

// sendTokenEmail creates a one-time token and emails its link. When throttle
// is true, at most one email per purpose and user is sent per MailCooldown.
func (s *Service) sendTokenEmail(ctx context.Context, u store.User, purpose string, kind emails.Kind, path string, ttl time.Duration, throttle bool) {
	if throttle && s.opts.MailCooldown > 0 {
		ok, err := s.rdb.SetNX(ctx, s.keys.AuthMailCooldown(purpose, u.ID), "1", s.opts.MailCooldown).Result()
		if err != nil || !ok {
			return
		}
	}
	tok, err := token.Generate("")
	if err != nil {
		return
	}
	if err := s.st.CreateEmailToken(ctx, u.ID, purpose, token.Hash(tok), s.opts.Now().Add(ttl)); err != nil {
		s.opts.Logger.Error("create email token failed", "err", err, "purpose", purpose)
		return
	}
	s.send(ctx, u, kind, s.opts.AppURL+path+"?token="+tok)
}

func (s *Service) send(ctx context.Context, u store.User, kind emails.Kind, link string) {
	subject, body, err := emails.Render(kind, u.Locale, link)
	if err != nil {
		return
	}
	if s.opts.Mailer == nil {
		s.opts.Logger.Warn("account email not sent: HS_SMTP_URL is not configured", "kind", kind, "user_id", u.ID)
		return
	}
	if err := s.opts.Mailer.Send(ctx, u.Email, subject, body); err != nil {
		s.opts.Logger.Error("account email failed", "kind", kind, "user_id", u.ID, "err", err)
	}
}

// ── profile ──────────────────────────────────────────────

// UpdateProfile validates and applies profile changes.
func (s *Service) UpdateProfile(ctx context.Context, u store.User, p store.ProfileUpdate) (store.User, error) {
	if p.Locale != nil && !Locales[*p.Locale] {
		return store.User{}, ErrInvalidLocale
	}
	if p.ThemePreference != nil && !Themes[*p.ThemePreference] {
		return store.User{}, ErrInvalidTheme
	}
	if p.Name != nil {
		trimmed := strings.TrimSpace(*p.Name)
		if utf8.RuneCountInString(trimmed) > 100 {
			return store.User{}, ErrInvalidName
		}
		p.Name = &trimmed
	}
	return s.st.UpdateProfile(ctx, u.ID, p)
}

// ValidEmail normalizes and validates an email address.
func ValidEmail(raw string) (string, error) {
	email := store.NormalizeEmail(raw)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// errorf wraps unexpected errors with context.
func errorf(format string, args ...any) error { return fmt.Errorf("auth: "+format, args...) }
