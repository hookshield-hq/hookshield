// Package api is the HookShield control-plane HTTP API consumed by the
// dashboard. Routes live under /v1; every error is returned as a stable code
// ({"error":{"code":"auth.invalid_credentials"}}) that the dashboard translates
// (guide D7). See docs/API.md.
package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// Options configure the API server.
type Options struct {
	AppURL         string         // dashboard URL (redirect target after Google sign-in)
	AllowedOrigins []string       // browser origins allowed to call the API with cookies (CORS + CSRF)
	SecureCookies  bool           // set the Secure flag (true in production / HTTPS)
	TrustedProxies []netip.Prefix // for client IPs behind a reverse proxy
	Logger         *slog.Logger

	// Resource management (endpoints, secrets, alert channels, API keys).
	Store           *store.Store
	Cipher          *crypto.Cipher // seals HMAC secrets, user secrets and alert targets
	Cache           Invalidator    // ingest endpoint cache; nil = no cache to invalidate
	PublicIngestURL string         // base of the ingest URLs shown to users
	// MultiTenant hides install-wide numbers (queue depths, dead letters) from
	// GET /v1/status: on a shared install they reveal other accounts' traffic.
	MultiTenant         bool
	AllowPrivateTargets bool // accept private/loopback destinations (development)

	// Logs, replay, metrics and the live feed.
	Redis    *redis.Client
	Keys     redisx.Keys
	Limiter  Limiter       // limits applied to replays; nil = none
	LivePing time.Duration // keep-alive comment interval of /v1/live (default 15 s)

	// Billing (HookShield Cloud). nil = self-hosted: no billing routes and
	// GET /v1/features says {"billing": false}.
	Billing core.Billing
	Plans   core.Plans // limits shown on the billing page; nil = self-hosted
}

// Invalidator drops a cached endpoint after it changes (endpointcache.Cache).
type Invalidator interface {
	Invalidate(ctx context.Context, slug string) error
}

// Server holds the API dependencies.
type Server struct {
	auth    *auth.Service
	opts    Options
	origins map[string]bool
	routes  []Route

	closing   chan struct{} // closed by CloseStreams
	closeOnce sync.Once
}

// CloseStreams ends the open live feeds (Server-Sent Events). Register it with
// http.Server.RegisterOnShutdown: Shutdown waits for active requests, and a
// feed stays open for as long as a dashboard (or `hookshield listen`) does —
// clients reconnect to another instance.
func (s *Server) CloseStreams() { s.closeOnce.Do(func() { close(s.closing) }) }

// New builds the API server.
func New(a *auth.Service, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	opts.AppURL = strings.TrimRight(opts.AppURL, "/")
	opts.PublicIngestURL = strings.TrimRight(opts.PublicIngestURL, "/")
	if opts.LivePing <= 0 {
		opts.LivePing = 15 * time.Second
	}
	origins := map[string]bool{}
	for _, o := range opts.AllowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			origins[o] = true
		}
	}
	return &Server{auth: a, opts: opts, origins: origins, closing: make(chan struct{})}
}

// Route is a registered route and the access it requires (docs/openapi: x-access).
type Route struct {
	Pattern string // e.g. "GET /v1/endpoints/{id}"
	Access  string // public | read | operate | admin | session
}

// Routes lists the routes mounted by Register (used to keep openapi.json in sync).
func (s *Server) Routes() []Route { return append([]Route(nil), s.routes...) }

func (s *Server) handle(mux *http.ServeMux, pattern string, level access, h http.HandlerFunc) {
	s.routes = append(s.routes, Route{Pattern: pattern, Access: level.String()})
	if level == accessPublic {
		mux.HandleFunc(pattern, h)
		return
	}
	mux.Handle(pattern, s.guard(level, h))
}

// Register mounts every route on mux. Health endpoints are registered by the caller.
func (s *Server) Register(mux *http.ServeMux) {
	s.handle(mux, "GET /v1/openapi.json", accessPublic, serveOpenAPI)
	s.handle(mux, "GET /v1/auth/providers", accessPublic, s.providers)
	s.handle(mux, "POST /v1/auth/register", accessPublic, s.register)
	s.handle(mux, "POST /v1/auth/login", accessPublic, s.login)
	s.handle(mux, "POST /v1/auth/logout", accessPublic, s.logout)
	s.handle(mux, "POST /v1/auth/verify-email", accessPublic, s.verifyEmail)
	s.handle(mux, "POST /v1/auth/forgot-password", accessPublic, s.forgotPassword)
	s.handle(mux, "POST /v1/auth/reset-password", accessPublic, s.resetPassword)
	s.handle(mux, "GET /v1/auth/google/start", accessPublic, s.googleStart)
	s.handle(mux, "GET /v1/auth/google/callback", accessPublic, s.googleCallback)

	s.handle(mux, "POST /v1/auth/resend-verification", accessSession, s.resendVerification)
	s.handle(mux, "POST /v1/auth/change-password", accessSession, s.changePassword)
	s.handle(mux, "GET /v1/me", accessRead, s.me)
	s.handle(mux, "PATCH /v1/me", accessSession, s.updateMe)

	if s.opts.Store == nil || s.opts.Cipher == nil {
		return // auth-only server (some tests)
	}
	s.handle(mux, "GET /v1/endpoints", accessRead, s.listEndpoints)
	s.handle(mux, "POST /v1/endpoints", accessAdmin, s.createEndpoint)
	s.handle(mux, "GET /v1/endpoints/{id}", accessRead, s.getEndpoint)
	s.handle(mux, "PATCH /v1/endpoints/{id}", accessAdmin, s.updateEndpoint)
	s.handle(mux, "DELETE /v1/endpoints/{id}", accessAdmin, s.deleteEndpoint)
	s.handle(mux, "POST /v1/endpoints/{id}/rotate-secret", accessAdmin, s.rotateSecret)

	s.handle(mux, "GET /v1/secrets", accessRead, s.listSecrets)
	s.handle(mux, "PUT /v1/secrets/{name}", accessAdmin, s.putSecret)
	s.handle(mux, "DELETE /v1/secrets/{name}", accessAdmin, s.deleteSecret)

	s.handle(mux, "GET /v1/alert-channels", accessRead, s.listAlertChannels)
	s.handle(mux, "POST /v1/alert-channels", accessAdmin, s.createAlertChannel)
	s.handle(mux, "PATCH /v1/alert-channels/{id}", accessAdmin, s.updateAlertChannel)
	s.handle(mux, "DELETE /v1/alert-channels/{id}", accessAdmin, s.deleteAlertChannel)

	s.handle(mux, "POST /v1/transform/preview", accessRead, s.previewTransform)

	if s.opts.Redis != nil {
		s.handle(mux, "GET /v1/logs", accessRead, s.listLogs)
		s.handle(mux, "GET /v1/logs/{id}", accessRead, s.getLog)
		s.handle(mux, "POST /v1/logs/{id}/replay", accessOperate, s.replayLog)
		s.handle(mux, "POST /v1/endpoints/{id}/test", accessOperate, s.testEndpoint)
		s.handle(mux, "GET /v1/metrics/summary", accessRead, s.metricsSummary)
		s.handle(mux, "GET /v1/metrics/series", accessRead, s.metricsSeries)
		s.handle(mux, "GET /v1/metrics/endpoints", accessRead, s.metricsEndpoints)
		s.handle(mux, "GET /v1/status", accessRead, s.status)
		s.handle(mux, "GET /v1/search", accessRead, s.search)
		s.handle(mux, "GET /v1/live", accessRead, s.liveFeed)
	}

	s.registerBilling(mux)

	// API keys manage access itself: dashboard session only.
	s.handle(mux, "GET /v1/api-keys", accessSession, s.listAPIKeys)
	s.handle(mux, "POST /v1/api-keys", accessSession, s.createAPIKey)
	s.handle(mux, "DELETE /v1/api-keys/{id}", accessSession, s.revokeAPIKey)
}

// Handler wraps mux with the API middleware chain.
func (s *Server) Handler(mux http.Handler) http.Handler {
	return s.recoverer(s.requestID(s.accessLog(securityHeaders(s.cors(s.csrf(mux))))))
}

// securityHeaders: the API answers JSON (and one event stream) only, so
// browsers may not render, frame, sniff or cache any of it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

const (
	sessionCookie    = "hs_session"
	oauthStateCookie = "hs_oauth_state"
)

func (s *Server) setSessionCookie(w http.ResponseWriter, tok string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}
