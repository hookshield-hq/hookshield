package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// GoogleConfig configures Google sign-in (OAuth 2.0 authorization code + PKCE).
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // e.g. https://api.hookshield.dev/v1/auth/google/callback
	AuthURL      string // overridable for tests
	TokenURL     string
	UserInfoURL  string
}

func (g *GoogleConfig) applyDefaults() {
	if g.AuthURL == "" {
		g.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if g.TokenURL == "" {
		g.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if g.UserInfoURL == "" {
		g.UserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	}
}

func (g GoogleConfig) enabled() bool {
	return g.ClientID != "" && g.ClientSecret != "" && g.RedirectURL != ""
}

const oauthStateTTL = 10 * time.Minute

type pendingLogin struct {
	Verifier string `json:"v"`
	Redirect string `json:"r"`
}

// SafeRedirect accepts only local absolute paths ("/dashboard"), preventing
// open redirects through the ?redirect= parameter.
func SafeRedirect(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || strings.ContainsAny(p, "\r\n") {
		return "/"
	}
	return p
}

// GoogleStart prepares a Google sign-in and returns the URL to send the
// browser to and the state value to store in a short-lived cookie.
func (s *Service) GoogleStart(ctx context.Context, redirect string) (authURL, state string, err error) {
	if !s.GoogleEnabled() {
		return "", "", ErrGoogleDisabled
	}
	state, err = randomString(32)
	if err != nil {
		return "", "", err
	}
	verifier, err := randomString(48)
	if err != nil {
		return "", "", err
	}
	b, _ := json.Marshal(pendingLogin{Verifier: verifier, Redirect: SafeRedirect(redirect)})
	if err := s.rdb.Set(ctx, s.keys.OAuthState(state), b, oauthStateTTL).Err(); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {s.opts.Google.ClientID},
		"redirect_uri":          {s.opts.Google.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return s.opts.Google.AuthURL + "?" + q.Encode(), state, nil
}

type googleUser struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Locale        string `json:"locale"`
}

// GoogleCallback completes a sign-in. cookieState must equal state (the value
// set by GoogleStart in the browser), which binds the flow to this browser.
// It returns the session and the local path to redirect to.
func (s *Service) GoogleCallback(ctx context.Context, state, cookieState, code string, meta Meta) (Login, string, error) {
	if !s.GoogleEnabled() {
		return Login{}, "/", ErrGoogleDisabled
	}
	if state == "" || code == "" || state != cookieState {
		return Login{}, "/", ErrInvalidToken
	}
	raw, err := s.rdb.GetDel(ctx, s.keys.OAuthState(state)).Bytes()
	if err != nil {
		return Login{}, "/", ErrInvalidToken // unknown, expired or replayed state
	}
	var pending pendingLogin
	if err := json.Unmarshal(raw, &pending); err != nil {
		return Login{}, "/", ErrInvalidToken
	}

	gu, err := s.googleIdentity(ctx, code, pending.Verifier)
	if err != nil {
		s.opts.Logger.Warn("google sign-in failed", "err", err)
		return Login{}, pending.Redirect, ErrGoogleFailed
	}
	if !gu.EmailVerified {
		return Login{}, pending.Redirect, ErrGoogleUnverified
	}

	u, err := s.st.GetUserByOAuth(ctx, "google", gu.Sub)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		u, err = s.st.GetUserByEmail(ctx, gu.Email)
		if errors.Is(err, store.ErrNotFound) {
			locale := normalizeGoogleLocale(gu.Locale)
			u, err = s.st.CreateUser(ctx, store.CreateUserParams{Email: gu.Email, Name: gu.Name, Locale: locale, EmailVerified: true})
		}
		if err != nil {
			return Login{}, pending.Redirect, err
		}
		if u.EmailVerifiedAt == nil {
			// Someone registered this address without proving they own it —
			// possibly an attacker waiting for the owner ("pre-hijacking").
			// Google just proved ownership: the owner takes the account over
			// and it starts clean — endpoints (inbound tokens, HMAC secrets,
			// logs), alert channels, secrets, API keys, sessions and password
			// are deleted, so the registrant keeps no access or foothold.
			if err := s.resetAccount(ctx, u.ID); err != nil {
				return Login{}, pending.Redirect, err
			}
		}
		// Google verified this address, so linking it to the existing account is safe.
		if err := s.st.LinkOAuth(ctx, u.ID, "google", gu.Sub, gu.Email); err != nil {
			return Login{}, pending.Redirect, err
		}
		if err := s.st.MarkEmailVerified(ctx, u.ID); err != nil {
			return Login{}, pending.Redirect, err
		}
		if u, err = s.st.GetUserByID(ctx, u.ID); err != nil {
			return Login{}, pending.Redirect, err
		}
	default:
		return Login{}, pending.Redirect, err
	}

	login, err := s.startSession(ctx, u, meta)
	return login, pending.Redirect, err
}

func (s *Service) googleIdentity(ctx context.Context, code, verifier string) (googleUser, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {s.opts.Google.ClientID},
		"client_secret": {s.opts.Google.ClientSecret},
		"redirect_uri":  {s.opts.Google.RedirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.Google.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleUser{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := s.doJSON(req, &tok); err != nil {
		return googleUser{}, errorf("token exchange: %v", err)
	}
	if tok.AccessToken == "" {
		return googleUser{}, errorf("token exchange: empty access token")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, s.opts.Google.UserInfoURL, nil)
	if err != nil {
		return googleUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var gu googleUser
	if err := s.doJSON(req, &gu); err != nil {
		return googleUser{}, errorf("userinfo: %v", err)
	}
	if gu.Sub == "" || gu.Email == "" {
		return googleUser{}, errorf("userinfo: missing sub or email")
	}
	if gu.Email, err = ValidEmail(gu.Email); err != nil {
		return googleUser{}, errorf("userinfo: invalid email")
	}
	return gu, nil
}

func (s *Service) doJSON(req *http.Request, out any) error {
	resp, err := s.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return ue.Err // never log URLs (they may carry codes)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return errorf("HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func normalizeGoogleLocale(l string) string {
	switch strings.ToLower(strings.SplitN(strings.ReplaceAll(l, "_", "-"), "-", 2)[0]) {
	case "pt":
		return "pt-BR"
	case "es":
		return "es"
	}
	return "en"
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
