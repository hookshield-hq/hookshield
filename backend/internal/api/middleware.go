package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
	ctxAPIKey
)

// RequestIDHeader carries the request ID in requests and responses.
const RequestIDHeader = "X-Request-Id"

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.opts.Logger.Error("panic", "panic", v, "path", r.URL.Path, "stack", string(debug.Stack()))
				httpx.Error(w, http.StatusInternalServerError, "internal")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController reach the real writer (Flush, deadlines for SSE).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// accessLog logs method, path, status and duration — never bodies, cookies or query strings.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if rec.status == http.StatusOK && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") {
			level = slog.LevelDebug // health probes run every few seconds
		}
		s.opts.Logger.Log(r.Context(), level, "request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(), "request_id", w.Header().Get(RequestIDHeader))
	})
}

// cors allows the dashboard origins to call the API with cookies.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.origins[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type, X-Request-Id")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrf blocks cross-site state-changing requests. Browsers always send Origin
// on POST/PUT/PATCH/DELETE, so a cookie-authenticated request must come from an
// allowed origin. Requests without Origin and without a session cookie (curl,
// server-to-server) are not CSRF-capable and pass.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		switch {
		case origin != "" && !s.origins[origin]:
			httpx.Error(w, http.StatusForbidden, "auth.csrf")
			return
		case origin == "" && strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site"):
			httpx.Error(w, http.StatusForbidden, "auth.csrf")
			return
		case origin == "" && hasCookie(r, sessionCookie):
			httpx.Error(w, http.StatusForbidden, "auth.csrf")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasCookie(r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	return err == nil && c.Value != ""
}

// access is what a route demands from the caller.
type access int

const (
	// accessPublic: no caller needed (sign-in routes, the OpenAPI document).
	accessPublic access = iota - 1
	// accessRead: a dashboard session or any API key.
	accessRead
	// accessOperate: a session, or an API key with scope ingest or admin (replays, test sends).
	accessOperate
	// accessAdmin: a session, or an API key with scope admin (configuration changes).
	accessAdmin
	// accessSession: a dashboard session only (password, profile, API keys).
	accessSession
)

func (a access) String() string {
	return map[access]string{accessPublic: "public", accessRead: "read", accessOperate: "operate",
		accessAdmin: "admin", accessSession: "session"}[a]
}

// guard authenticates the caller (session cookie first, then
// Authorization: Bearer <API key>) and checks the access level.
func (s *Server) guard(level access, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			ses, u, err := s.auth.Authenticate(r.Context(), c.Value)
			if err != nil {
				s.fail(w, err)
				return
			}
			if s.auth.Slides(ses) {
				// The session was just extended: extend the cookie too.
				s.setSessionCookie(w, c.Value, s.auth.CookieMaxAge(ses))
			}
			ctx := context.WithValue(context.WithValue(r.Context(), ctxUser, u), ctxSession, ses)
			h(w, r.WithContext(ctx))
			return
		}
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || key == "" {
			httpx.Error(w, http.StatusUnauthorized, "auth.unauthorized")
			return
		}
		k, u, err := s.auth.AuthenticateAPIKey(r.Context(), strings.TrimSpace(key))
		if err != nil {
			s.fail(w, err)
			return
		}
		if !scopeAllows(k.Scope, level) {
			code := "auth.insufficient_scope"
			if level == accessSession {
				code = "auth.session_required"
			}
			httpx.Error(w, http.StatusForbidden, code)
			return
		}
		ctx := context.WithValue(context.WithValue(r.Context(), ctxUser, u), ctxAPIKey, k)
		h(w, r.WithContext(ctx))
	})
}

// stillAuthorized re-checks the credentials of a long-lived request (the live
// feed): a logout, password reset or revoked API key ends it.
func (s *Server) stillAuthorized(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_, _, err := s.auth.Authenticate(r.Context(), c.Value)
		return err == nil
	}
	if key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && key != "" {
		_, _, err := s.auth.AuthenticateAPIKey(r.Context(), strings.TrimSpace(key))
		return err == nil
	}
	return false
}

func scopeAllows(scope string, level access) bool {
	switch level {
	case accessRead:
		return scope == store.ScopeReadOnly || scope == store.ScopeIngest || scope == store.ScopeAdmin
	case accessOperate:
		return scope == store.ScopeIngest || scope == store.ScopeAdmin
	case accessAdmin:
		return scope == store.ScopeAdmin
	default:
		return false
	}
}

// requireAuth is the dashboard-session guard used by account routes.
func (s *Server) requireAuth(h http.HandlerFunc) http.Handler { return s.guard(accessSession, h) }

func userFrom(r *http.Request) store.User { u, _ := r.Context().Value(ctxUser).(store.User); return u }
func sessionFrom(r *http.Request) store.Session {
	s, _ := r.Context().Value(ctxSession).(store.Session)
	return s
}

// RequireUser runs h only for a user signed in to the dashboard (401
// auth.unauthorized otherwise; API keys get 403 auth.session_required).
// Exported for extra routes added by app.Options.APIRoutes (e.g. billing).
func (s *Server) RequireUser(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }

// CurrentUser returns the user authenticated by RequireUser (zero value outside it).
func CurrentUser(r *http.Request) store.User { return userFrom(r) }
