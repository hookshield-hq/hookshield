//go:build integration

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/api"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
)

const appOrigin = "http://localhost:3000"

type mailbox struct {
	mu   sync.Mutex
	msgs []struct{ to, subject, body string }
	gate chan struct{} // when set, Send blocks until it is closed (a slow provider)
}

func (m *mailbox) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	gate := m.gate
	m.mu.Unlock()
	if gate != nil {
		<-gate
	}
	m.mu.Lock()
	m.msgs = append(m.msgs, struct{ to, subject, body string }{to, subject, body})
	m.mu.Unlock()
	return nil
}

func (m *mailbox) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.msgs) }

var tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

func (m *mailbox) lastToken(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.msgs) == 0 {
		t.Fatal("no email sent")
	}
	match := tokenRE.FindStringSubmatch(m.msgs[len(m.msgs)-1].body)
	if match == nil {
		t.Fatalf("no token link in email: %s", m.msgs[len(m.msgs)-1].body)
	}
	return match[1]
}

// fakeGoogle implements the token and userinfo endpoints and checks PKCE.
type fakeGoogle struct {
	srv       *httptest.Server
	mu        sync.Mutex
	challenge string
	user      map[string]any
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		g.mu.Lock()
		ok := r.Form.Get("code") == "good-code" && base64.RawURLEncoding.EncodeToString(sum[:]) == g.challenge &&
			r.Form.Get("client_secret") == "secret" && r.Form.Get("grant_type") == "authorization_code"
		g.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at-123", "token_type": "Bearer"})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(g.user)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

type env struct {
	t      *testing.T
	srv    *httptest.Server
	mail   *mailbox
	google *fakeGoogle
	store  *store.Store
	svc    *auth.Service
	cache  *spyCache
}

// spyCache records the endpoint cache invalidations.
type spyCache struct {
	mu    sync.Mutex
	slugs []string
}

func (c *spyCache) Invalidate(_ context.Context, slug string) error {
	c.mu.Lock()
	c.slugs = append(c.slugs, slug)
	c.mu.Unlock()
	return nil
}

func (c *spyCache) invalidated() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.slugs...)
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	rdb, keys := testredis.New(t)
	st := store.New(pool)
	mail := &mailbox{}
	g := newFakeGoogle(t)
	cache := &spyCache{}

	svc := auth.New(st, rdb, keys, auth.Options{
		AppURL: appOrigin, Mailer: mail, MaxFailuresEmail: 5, MailCooldown: time.Minute, EndpointCache: cache,
		Google: auth.GoogleConfig{ClientID: "cid", ClientSecret: "secret", RedirectURL: "http://api/v1/auth/google/callback",
			AuthURL: g.srv.URL + "/auth", TokenURL: g.srv.URL + "/token", UserInfoURL: g.srv.URL + "/userinfo"},
	})
	server := api.New(svc, api.Options{AppURL: appOrigin, AllowedOrigins: []string{appOrigin}})
	mux := http.NewServeMux()
	server.Register(mux)
	srv := httptest.NewServer(server.Handler(mux))
	t.Cleanup(srv.Close)
	t.Cleanup(svc.Wait)
	return &env{t: t, srv: srv, mail: mail, google: g, store: st, svc: svc, cache: cache}
}

// browser is an HTTP client with a cookie jar that behaves like the dashboard.
type browser struct {
	e      *env
	client *http.Client
	origin string
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{e: e, origin: appOrigin, client: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type resp struct {
	status int
	code   string
	body   map[string]any
	header http.Header
}

func (b *browser) do(method, path string, body any) resp {
	b.e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, b.e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if b.origin != "" {
		req.Header.Set("Origin", b.origin)
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer res.Body.Close()
	out := resp{status: res.StatusCode, header: res.Header}
	_ = json.NewDecoder(res.Body).Decode(&out.body)
	if e, ok := out.body["error"].(map[string]any); ok {
		out.code, _ = e["code"].(string)
	}
	return out
}

func user(r resp) map[string]any { u, _ := r.body["user"].(map[string]any); return u }

const goodPassword = "a long enough passphrase"

func TestRegisterVerifyAndSession(t *testing.T) {
	e := setup(t)
	b := e.browser()

	r := b.do("POST", "/v1/auth/register", map[string]any{"email": "Ana@Example.com", "password": goodPassword, "name": "Ana", "locale": "pt-BR"})
	if r.status != 201 || user(r)["email"] != "ana@example.com" || user(r)["email_verified"] != false || user(r)["has_password"] != true {
		t.Fatalf("register: %d %+v", r.status, r.body)
	}
	cookie := r.header.Get("Set-Cookie")
	for _, want := range []string{"hs_session=hs_sess_", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("session cookie %q missing %q", cookie, want)
		}
	}

	if ts, _ := user(r)["created_at"].(string); !strings.HasSuffix(ts, "Z") {
		t.Errorf("created_at must be UTC: %q", ts)
	}
	if me := b.do("GET", "/v1/me", nil); me.status != 200 || user(me)["locale"] != "pt-BR" {
		t.Fatalf("me: %d %+v", me.status, me.body)
	}
	if e.mail.count() != 1 || e.mail.msgs[0].subject != "Confirme seu e-mail no HookShield" {
		t.Fatalf("verification email (pt-BR): %+v", e.mail.msgs)
	}
	if !strings.Contains(e.mail.msgs[0].body, appOrigin+"/verify-email?token=") {
		t.Fatalf("verification link: %s", e.mail.msgs[0].body)
	}

	tok := e.mail.lastToken(t)
	if r := b.do("POST", "/v1/auth/verify-email", map[string]string{"token": tok}); r.status != 204 {
		t.Fatalf("verify: %d %+v", r.status, r.body)
	}
	if me := b.do("GET", "/v1/me", nil); user(me)["email_verified"] != true {
		t.Fatal("email not verified")
	}
	if r := b.do("POST", "/v1/auth/verify-email", map[string]string{"token": tok}); r.status != 400 || r.code != "auth.invalid_token" {
		t.Fatalf("token reuse: %d %s", r.status, r.code)
	}

	if r := b.do("POST", "/v1/auth/logout", nil); r.status != 204 {
		t.Fatalf("logout: %d", r.status)
	}
	if me := b.do("GET", "/v1/me", nil); me.status != 401 || me.code != "auth.unauthorized" {
		t.Fatalf("after logout: %d %s", me.status, me.code)
	}
}

func TestRegisterValidation(t *testing.T) {
	e := setup(t)
	b := e.browser()
	b.do("POST", "/v1/auth/register", map[string]any{"email": "a@example.com", "password": goodPassword})

	cases := []struct {
		body   any
		status int
		code   string
	}{
		{map[string]any{"email": "A@example.com", "password": goodPassword}, 409, "auth.email_taken"},
		{map[string]any{"email": "b@example.com", "password": "short"}, 422, "password.too_short"},
		{map[string]any{"email": "b@example.com", "password": "b@example.com"}, 422, "password.same_as_email"},
		{map[string]any{"email": "not-an-email", "password": goodPassword}, 422, "auth.invalid_email"},
		{map[string]any{"email": "b@localhost", "password": goodPassword}, 422, "auth.invalid_email"},
		{map[string]any{"email": "b@example.com", "password": goodPassword, "locale": "fr"}, 422, "auth.invalid_locale"},
		{map[string]any{"email": "b@example.com", "password": goodPassword, "admin": true}, 400, "validation.invalid_json"},
	}
	for _, c := range cases {
		if r := b.do("POST", "/v1/auth/register", c.body); r.status != c.status || r.code != c.code {
			t.Errorf("%v: got %d %q, want %d %q", c.body, r.status, r.code, c.status, c.code)
		}
	}
}

func TestLoginAndThrottling(t *testing.T) {
	e := setup(t)
	e.browser().do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword})

	b := e.browser()
	wrong := b.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": "nope-nope-nope"})
	unknown := b.do("POST", "/v1/auth/login", map[string]string{"email": "ghost@example.com", "password": goodPassword})
	if wrong.status != 401 || wrong.code != "auth.invalid_credentials" || unknown.code != wrong.code || unknown.status != wrong.status {
		t.Fatalf("wrong=%d %s unknown=%d %s (must be identical)", wrong.status, wrong.code, unknown.status, unknown.code)
	}
	if r := b.do("POST", "/v1/auth/login", map[string]string{"email": "DEV@example.com", "password": goodPassword}); r.status != 200 {
		t.Fatalf("valid login: %d %+v", r.status, r.body)
	}
	if me := b.do("GET", "/v1/me", nil); me.status != 200 {
		t.Fatal("session not set by login")
	}

	// 5 failures (test limit) lock the email for the window, even with the right password.
	for i := 0; i < 5; i++ {
		b.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": "wrong-password-x"})
	}
	if r := b.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": goodPassword}); r.status != 429 || r.code != "auth.too_many_attempts" {
		t.Fatalf("throttle: %d %s", r.status, r.code)
	}
	if r := b.do("POST", "/v1/auth/login", map[string]string{"email": "ghost@example.com", "password": goodPassword}); r.status != 401 {
		t.Fatalf("other emails must not be locked: %d", r.status)
	}

	// Parallel attempts cannot slip past the limit (security review: the
	// counter used to be read before the password check and written after it,
	// so 60 parallel guesses were all checked).
	e.browser().do("POST", "/v1/auth/register", map[string]any{"email": "race@example.com", "password": goodPassword})
	var mu sync.Mutex
	counts := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.browser().do("POST", "/v1/auth/login", map[string]string{"email": "race@example.com", "password": "wrong-password-x"})
			mu.Lock()
			counts[r.status]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if counts[401] != 5 || counts[429] != 15 {
		t.Fatalf("20 parallel guesses with a limit of 5: %v", counts)
	}
}

func TestCSRFAndCORS(t *testing.T) {
	e := setup(t)
	b := e.browser()
	reg := b.do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword})
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'", "Referrer-Policy": "no-referrer",
	} {
		if got := reg.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	evil := e.browser()
	evil.client.Jar = b.client.Jar // a malicious site riding the user's cookies
	evil.origin = "https://evil.example"
	if r := evil.do("PATCH", "/v1/me", map[string]string{"name": "pwned"}); r.status != 403 || r.code != "auth.csrf" {
		t.Fatalf("cross-origin write: %d %s", r.status, r.code)
	}
	noOrigin := e.browser()
	noOrigin.client.Jar = b.client.Jar
	noOrigin.origin = ""
	if r := noOrigin.do("POST", "/v1/auth/logout", nil); r.status != 403 {
		t.Fatalf("cookie write without Origin: %d", r.status)
	}
	if r := noOrigin.do("GET", "/v1/me", nil); r.status != 200 {
		t.Fatalf("GET must not need Origin: %d", r.status)
	}
	// A cookie-less client without Origin (curl, server) may call public endpoints.
	curl := e.browser()
	curl.origin = ""
	if r := curl.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": goodPassword}); r.status != 200 {
		t.Fatalf("curl login: %d %s", r.status, r.code)
	}

	pre := func(origin string) http.Header {
		req, _ := http.NewRequest(http.MethodOptions, e.srv.URL+"/v1/me", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "PATCH")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header
	}
	if h := pre(appOrigin); h.Get("Access-Control-Allow-Origin") != appOrigin || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight for dashboard: %v", h)
	}
	if h := pre("https://evil.example"); h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight for evil origin allowed: %v", h)
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	e := setup(t)
	a := e.browser()
	a.do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword, "locale": "es"})
	sent := e.mail.count()

	anon := e.browser()
	r := anon.do("POST", "/v1/auth/forgot-password", map[string]string{"email": "ghost@example.com"})
	if e.svc.Wait(); r.status != 202 || e.mail.count() != sent {
		t.Fatalf("unknown email: %d, emails %d", r.status, e.mail.count())
	}
	r = anon.do("POST", "/v1/auth/forgot-password", map[string]string{"email": "dev@example.com"})
	if e.svc.Wait(); r.status != 202 || e.mail.count() != sent+1 {
		t.Fatalf("known email: %d, emails %d", r.status, e.mail.count())
	}
	if subj := e.mail.msgs[e.mail.count()-1].subject; subj != "Restablece tu contraseña de HookShield" {
		t.Fatalf("reset email not localized: %q", subj)
	}
	anon.do("POST", "/v1/auth/forgot-password", map[string]string{"email": "dev@example.com"})
	if e.svc.Wait(); e.mail.count() != sent+1 {
		t.Fatal("second reset email within the cooldown")
	}

	tok := e.mail.lastToken(t)
	if r := anon.do("POST", "/v1/auth/reset-password", map[string]string{"token": tok, "password": "short"}); r.status != 422 {
		t.Fatalf("weak new password: %d", r.status)
	}
	// Regression (review): the rejected password used to burn the link. The
	// password is validated first, so the same link still works…
	if r := anon.do("POST", "/v1/auth/reset-password", map[string]string{"token": tok, "password": "a brand new passphrase"}); r.status != 204 {
		t.Fatalf("link burnt by a rejected password: %d %s", r.status, r.code)
	}
	// …once: a used link is refused.
	if r := anon.do("POST", "/v1/auth/reset-password", map[string]string{"token": tok, "password": "yet another passphrase"}); r.status != 400 || r.code != "auth.invalid_token" {
		t.Fatalf("token must be single-use: %d", r.status)
	}
	if r := e.browser().do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": "a brand new passphrase"}); r.status != 200 {
		t.Fatalf("login with the new password: %d", r.status)
	}
}

// Regression (review): forgot-password sent the email synchronously, and only
// for existing accounts, so its response time revealed whether an email is
// registered. It now answers before the lookup and the email.
func TestForgotPasswordDoesNotWaitForTheMailer(t *testing.T) {
	e := setup(t)
	e.browser().do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword})
	sent := e.mail.count()
	gate := make(chan struct{})
	e.mail.mu.Lock()
	e.mail.gate = gate // the provider hangs
	e.mail.mu.Unlock()

	release := time.AfterFunc(3*time.Second, func() { close(gate) }) // the provider answers after 3 s
	start := time.Now()
	r := e.browser().do("POST", "/v1/auth/forgot-password", map[string]string{"email": "dev@example.com"})
	if elapsed := time.Since(start); r.status != 202 || elapsed > time.Second {
		t.Fatalf("forgot-password waited for the mailer: %d after %s", r.status, elapsed)
	}
	if release.Stop() {
		close(gate)
	}
	e.svc.Wait()
	if e.mail.count() != sent+1 {
		t.Fatalf("reset email not sent in the background: %d", e.mail.count()-sent)
	}
}

// Regression (review): sign-ups (an argon2id hash each) had no rate limit.
func TestRegisterIsRateLimitedPerIP(t *testing.T) {
	e := setup(t)
	for i := 0; i < 10; i++ { // default MaxSignupsIP
		r := e.browser().do("POST", "/v1/auth/register", map[string]any{"email": fmt.Sprintf("u%d@example.com", i), "password": goodPassword})
		if r.status != 201 {
			t.Fatalf("sign-up %d: %d %s", i, r.status, r.code)
		}
	}
	r := e.browser().do("POST", "/v1/auth/register", map[string]any{"email": "one-more@example.com", "password": goodPassword})
	if r.status != 429 || r.code != "auth.too_many_attempts" {
		t.Fatalf("11th sign-up from one IP: %d %s", r.status, r.code)
	}
	if _, err := e.store.GetUserByEmail(context.Background(), "one-more@example.com"); err == nil {
		t.Fatal("throttled sign-up created an account")
	}
}

func TestResetPasswordSignsOutEverywhere(t *testing.T) {
	e := setup(t)
	a := e.browser()
	a.do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword})
	other := e.browser()
	other.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": goodPassword})
	key := newAPIKey(t, e, user(a.do("GET", "/v1/me", nil))["id"].(string))

	e.browser().do("POST", "/v1/auth/forgot-password", map[string]string{"email": "dev@example.com"})
	e.svc.Wait()
	tok := e.mail.lastToken(t)
	if r := e.browser().do("POST", "/v1/auth/reset-password", map[string]string{"token": tok, "password": "a brand new passphrase"}); r.status != 204 {
		t.Fatalf("reset: %d %s", r.status, r.code)
	}
	for _, b := range []*browser{a, other} {
		if me := b.do("GET", "/v1/me", nil); me.status != 401 {
			t.Fatal("old sessions must be revoked after a reset")
		}
	}
	// Account recovery also evicts API keys an intruder may have minted (security review).
	if code := bearerStatus(t, e, key); code != 401 {
		t.Fatalf("API key still works after a reset: %d", code)
	}
	if r := e.browser().do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": "a brand new passphrase"}); r.status != 200 {
		t.Fatalf("login with new password: %d", r.status)
	}
	if last := e.mail.msgs[e.mail.count()-1].subject; last != "Your HookShield password was changed" {
		t.Fatalf("password-changed notice missing: %q", last)
	}
}

func TestChangePasswordAndProfile(t *testing.T) {
	e := setup(t)
	a := e.browser()
	a.do("POST", "/v1/auth/register", map[string]any{"email": "dev@example.com", "password": goodPassword})
	other := e.browser()
	other.do("POST", "/v1/auth/login", map[string]string{"email": "dev@example.com", "password": goodPassword})

	if r := a.do("POST", "/v1/auth/change-password", map[string]string{"current_password": "wrong", "new_password": "another passphrase"}); r.status != 422 || r.code != "auth.wrong_password" {
		t.Fatalf("wrong current: %d %s", r.status, r.code)
	}
	if r := a.do("POST", "/v1/auth/change-password", map[string]string{"current_password": goodPassword, "new_password": "another passphrase"}); r.status != 204 {
		t.Fatalf("change: %d %s", r.status, r.code)
	}
	if me := a.do("GET", "/v1/me", nil); me.status != 200 {
		t.Fatal("current session must survive a password change")
	}
	if me := other.do("GET", "/v1/me", nil); me.status != 401 {
		t.Fatal("other sessions must be revoked")
	}

	r := a.do("PATCH", "/v1/me", map[string]any{"name": "  Ana  ", "locale": "es", "theme_preference": "light"})
	u := user(r)
	if r.status != 200 || u["name"] != "Ana" || u["locale"] != "es" || u["theme_preference"] != "light" {
		t.Fatalf("profile: %d %+v", r.status, r.body)
	}
	if r := a.do("PATCH", "/v1/me", map[string]any{"name": strings.Repeat("ã", 100)}); r.status != 200 {
		t.Fatalf("100 accented characters must fit: %d %s", r.status, r.code)
	}
	if r := a.do("PATCH", "/v1/me", map[string]any{"name": strings.Repeat("ã", 101)}); r.status != 422 || r.code != "auth.invalid_name" {
		t.Fatalf("101 characters: %d %s", r.status, r.code)
	}
	if r := a.do("PATCH", "/v1/me", map[string]any{"theme_preference": "neon"}); r.status != 422 || r.code != "auth.invalid_theme" {
		t.Fatalf("invalid theme: %d %s", r.status, r.code)
	}
	if r := a.do("PATCH", "/v1/me", map[string]any{"plan_type": "enterprise"}); r.status != 400 {
		t.Fatalf("plan must not be writable: %d", r.status)
	}
}

func (b *browser) googleFlow(redirect string) (location string) {
	b.e.t.Helper()
	r := b.do("GET", "/v1/auth/google/start?redirect="+url.QueryEscape(redirect), nil)
	if r.status != 302 {
		b.e.t.Fatalf("start: %d", r.status)
	}
	authURL, _ := url.Parse(r.header.Get("Location"))
	q := authURL.Query()
	if q.Get("client_id") != "cid" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "openid email profile" || q.Get("state") == "" {
		b.e.t.Fatalf("auth URL %s", authURL)
	}
	b.e.google.mu.Lock()
	b.e.google.challenge = q.Get("code_challenge")
	b.e.google.mu.Unlock()
	cb := b.do("GET", "/v1/auth/google/callback?code=good-code&state="+url.QueryEscape(q.Get("state")), nil)
	if cb.status != 302 {
		b.e.t.Fatalf("callback: %d", cb.status)
	}
	return cb.header.Get("Location")
}

func TestGoogleSignInCreatesAndLinksAccounts(t *testing.T) {
	e := setup(t)
	e.google.user = map[string]any{"sub": "g-1", "email": "Neo@Example.com", "email_verified": true, "name": "Neo", "locale": "pt-BR"}

	b := e.browser()
	if loc := b.googleFlow("/endpoints"); loc != appOrigin+"/endpoints" {
		t.Fatalf("redirect after sign-in: %q", loc)
	}
	me := b.do("GET", "/v1/me", nil)
	u := user(me)
	if me.status != 200 || u["email"] != "neo@example.com" || u["email_verified"] != true || u["has_password"] != false || u["locale"] != "pt-BR" {
		t.Fatalf("google account: %d %+v", me.status, me.body)
	}
	firstID := u["id"]

	// Signing in again reuses the same account.
	b2 := e.browser()
	b2.googleFlow("/")
	if user(b2.do("GET", "/v1/me", nil))["id"] != firstID {
		t.Fatal("second google sign-in created another account")
	}

	// A verified password account with the same email is linked, not duplicated,
	// and keeps its password and sessions.
	pw := e.browser()
	pw.do("POST", "/v1/auth/register", map[string]any{"email": "trinity@example.com", "password": goodPassword})
	pwID := user(pw.do("GET", "/v1/me", nil))["id"]
	if r := pw.do("POST", "/v1/auth/verify-email", map[string]string{"token": e.mail.lastToken(t)}); r.status != 204 {
		t.Fatalf("verify: %d %s", r.status, r.code)
	}
	e.google.user = map[string]any{"sub": "g-2", "email": "trinity@example.com", "email_verified": true}
	g := e.browser()
	g.googleFlow("/")
	gu := user(g.do("GET", "/v1/me", nil))
	if gu["id"] != pwID || gu["has_password"] != true || gu["email_verified"] != true || pw.do("GET", "/v1/me", nil).status != 200 {
		t.Fatalf("linking a verified account: %+v (password account %v)", gu, pwID)
	}

	// Pre-hijacking (security review): someone registers the address first,
	// never verifies it, and keeps a session and an API key. When the real
	// owner signs in with Google, the account becomes theirs and every way in
	// the registrant set up is removed.
	squatter := e.browser()
	squatter.do("POST", "/v1/auth/register", map[string]any{"email": "morpheus@example.com", "password": goodPassword})
	squatID := user(squatter.do("GET", "/v1/me", nil))["id"]
	backdoor := newAPIKey(t, e, squatID.(string))
	if code := bearerStatus(t, e, backdoor); code != 200 {
		t.Fatalf("api key before the takeover: %d", code)
	}
	squatSlug := plantFootholds(t, e, squatID.(string))
	e.google.user = map[string]any{"sub": "g-3", "email": "morpheus@example.com", "email_verified": true}
	owner := e.browser()
	owner.googleFlow("/")
	ou := user(owner.do("GET", "/v1/me", nil))
	if ou["id"] != squatID || ou["has_password"] != false || ou["email_verified"] != true {
		t.Fatalf("owner's account: %+v", ou)
	}
	if r := squatter.do("GET", "/v1/me", nil); r.status != 401 {
		t.Fatalf("squatter's session still works: %d", r.status)
	}
	if r := e.browser().do("POST", "/v1/auth/login", map[string]string{"email": "morpheus@example.com", "password": goodPassword}); r.status != 401 {
		t.Fatalf("squatter's password still works: %d", r.status)
	}
	if code := bearerStatus(t, e, backdoor); code != 401 {
		t.Fatalf("squatter's API key still works: %d", code)
	}
	// Regression (review): the squatter's endpoints (inbound token, HMAC
	// secret, destination), secrets and alert channels used to survive.
	assertAccountEmpty(t, e, squatID.(string), squatSlug)

	// Open redirects are neutralized.
	e.google.user = map[string]any{"sub": "g-1", "email": "neo@example.com", "email_verified": true}
	if loc := e.browser().googleFlow("//evil.example/x"); loc != appOrigin+"/" {
		t.Fatalf("open redirect: %q", loc)
	}
}

// newAPIKey creates an admin API key for userID (the auth-only test server has no key routes).
func newAPIKey(t *testing.T, e *env, userID string) string {
	t.Helper()
	key, err := token.Generate(token.PrefixLive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), userID, "test", token.DisplayPrefix(key), store.ScopeAdmin, token.Hash(key), nil); err != nil {
		t.Fatal(err)
	}
	return key
}

// bearerStatus is the status of GET /v1/me with an API key.
func bearerStatus(t *testing.T, e *env, key string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// plantFootholds gives userID an endpoint (with an endpoint alert channel), an
// account-wide alert channel and a secret, and returns the endpoint slug.
func plantFootholds(t *testing.T, e *env, userID string) string {
	t.Helper()
	ctx := context.Background()
	slug := "squat-" + userID[:8]
	ep, err := e.store.CreateEndpoint(ctx, store.CreateEndpointParams{
		UserID: userID, Name: "trap", Slug: slug, TargetURL: "https://attacker.example/hook",
		AuthTokenHash: token.Hash("hs_in_squatter"), HMACHeader: "X-Sig", HMACSecretEnc: []byte("ct"), HMACSecretNonce: []byte("nonce"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO alert_channels (user_id, endpoint_id, type, target_encrypted, target_nonce) VALUES ($1, $2, 'webhook', 'x', 'n')`,
		`INSERT INTO alert_channels (user_id, type, target_encrypted, target_nonce) SELECT $1, 'email', 'x', 'n' WHERE $2::uuid IS NOT NULL`,
		`INSERT INTO user_secrets (user_id, name, value_encrypted, value_nonce) SELECT $1, 'SQUAT', 'x', 'n' WHERE $2::uuid IS NOT NULL`,
	} {
		if _, err := e.store.Pool().Exec(ctx, q, userID, ep.ID); err != nil {
			t.Fatal(err)
		}
	}
	return slug
}

// assertAccountEmpty checks that userID kept no endpoint, alert channel,
// secret, API key or session, and that the endpoint cache was invalidated.
func assertAccountEmpty(t *testing.T, e *env, userID, slug string) {
	t.Helper()
	for _, table := range []string{"endpoints", "alert_channels", "user_secrets", "api_keys"} {
		var n int
		if err := e.store.Pool().QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d %s left after the takeover", n, table)
		}
	}
	if got := e.cache.invalidated(); len(got) != 1 || got[0] != slug {
		t.Errorf("endpoint cache invalidations %v, want [%s]", got, slug)
	}
}

// Taking over an unverified registration through a password reset (proof of
// inbox access) empties it like a Google takeover; a verified account keeps
// its data on a reset.
func TestResetPasswordTakesOverUnverifiedAccount(t *testing.T) {
	e := setup(t)
	squatter := e.browser()
	squatter.do("POST", "/v1/auth/register", map[string]any{"email": "trinity@example.com", "password": goodPassword})
	squatID := user(squatter.do("GET", "/v1/me", nil))["id"].(string)
	slug := plantFootholds(t, e, squatID)

	e.browser().do("POST", "/v1/auth/forgot-password", map[string]string{"email": "trinity@example.com"})
	e.svc.Wait()
	if r := e.browser().do("POST", "/v1/auth/reset-password", map[string]string{"token": e.mail.lastToken(t), "password": "the real owner's pass"}); r.status != 204 {
		t.Fatalf("reset: %d %s", r.status, r.code)
	}
	assertAccountEmpty(t, e, squatID, slug)
	if r := squatter.do("GET", "/v1/me", nil); r.status != 401 {
		t.Fatalf("squatter's session still works: %d", r.status)
	}

	// A verified account: a reset is plain account recovery, its data stays.
	ctx := context.Background()
	e.browser().do("POST", "/v1/auth/register", map[string]any{"email": "neo@example.com", "password": goodPassword})
	neo, err := e.store.GetUserByEmail(ctx, "neo@example.com")
	if err != nil || e.store.MarkEmailVerified(ctx, neo.ID) != nil {
		t.Fatal(err)
	}
	plantFootholds(t, e, neo.ID)
	e.browser().do("POST", "/v1/auth/forgot-password", map[string]string{"email": "neo@example.com"})
	e.svc.Wait()
	if r := e.browser().do("POST", "/v1/auth/reset-password", map[string]string{"token": e.mail.lastToken(t), "password": "neo's new passphrase"}); r.status != 204 {
		t.Fatalf("reset: %d %s", r.status, r.code)
	}
	var endpoints, secrets int
	_ = e.store.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM endpoints WHERE user_id = $1), (SELECT count(*) FROM user_secrets WHERE user_id = $1)`, neo.ID).Scan(&endpoints, &secrets)
	if endpoints != 1 || secrets != 1 || len(e.cache.invalidated()) != 1 {
		t.Fatalf("verified account lost data on reset: endpoints %d, secrets %d", endpoints, secrets)
	}
}

func TestGoogleSignInRejections(t *testing.T) {
	e := setup(t)
	e.google.user = map[string]any{"sub": "g-9", "email": "x@example.com", "email_verified": false}
	if loc := e.browser().googleFlow("/"); loc != appOrigin+"/login?error=auth.google_email_unverified" {
		t.Fatalf("unverified email: %q", loc)
	}

	e.google.user = map[string]any{"sub": "g-9", "email": "x@example.com", "email_verified": true}
	b := e.browser()
	start := b.do("GET", "/v1/auth/google/start", nil)
	state := func() string { u, _ := url.Parse(start.header.Get("Location")); return u.Query().Get("state") }()

	other := e.browser() // another browser: no state cookie (login CSRF attempt)
	if r := other.do("GET", "/v1/auth/google/callback?code=good-code&state="+state, nil); r.header.Get("Location") != appOrigin+"/login?error=auth.invalid_token" {
		t.Fatalf("state without cookie: %q", r.header.Get("Location"))
	}
	if r := b.do("GET", "/v1/auth/google/callback?code=bad-code&state="+state, nil); !strings.Contains(r.header.Get("Location"), "error=auth.") {
		t.Fatalf("bad code: %q", r.header.Get("Location"))
	}
	if r := b.do("GET", "/v1/auth/google/callback?error=access_denied", nil); r.header.Get("Location") != appOrigin+"/login?error=auth.google_failed" {
		t.Fatalf("cancelled: %q", r.header.Get("Location"))
	}
	if r := e.browser().do("GET", "/v1/auth/providers", nil); r.body["google"] != true || r.body["password"] != true {
		t.Fatalf("providers: %+v", r.body)
	}
}
