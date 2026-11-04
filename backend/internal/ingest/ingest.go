// Package ingest implements the HookShield edge handler: it authenticates an
// incoming webhook, applies limits, seals the request into an encrypted
// envelope and enqueues it — answering 202 in well under 20 ms.
//
// Request pipeline (cheap checks first, so floods are rejected early):
//
//	method → endpoint lookup → IP allow-list → token → status → body (size cap)
//	       → HMAC → rate limit / monthly quota → encrypt → XADD → 202
//
// Unknown endpoints and bad credentials get the same 401 response, so the
// endpoint namespace cannot be enumerated. Nothing is persisted for rejected
// requests (zero-trust: the token guard runs before any storage).
package ingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// Error codes returned to senders (stable API, documented in docs/INGEST.md).
const (
	CodeUnauthorized     = "unauthorized"
	CodeMethodNotAllowed = "method_not_allowed"
	CodePayloadTooLarge  = "payload_too_large"
	CodeEndpointPaused   = "endpoint_paused"
	CodeRateLimited      = "rate_limited"
	CodeQuotaExceeded    = "quota_exceeded"
	CodeUnavailable      = "unavailable"
)

// RequestIDHeader is set on every response that has a request ID.
const RequestIDHeader = "X-HookShield-Request-Id"

// DefaultMaxBodyBytes caps the accepted body size (1 MiB).
const DefaultMaxBodyBytes int64 = 1 << 20

// ConfigSource resolves an endpoint slug (usually *endpointcache.Cache).
type ConfigSource interface {
	Lookup(ctx context.Context, slug string) (store.IngestConfig, error)
}

// Limiter applies rate limits and quotas (usually *limits.Limiter).
type Limiter interface {
	Check(ctx context.Context, endpointID, userID, plan string) (limits.Decision, int64, error)
}

// Publisher enqueues accepted requests (usually *queue.Producer).
type Publisher interface {
	PublishIngest(ctx context.Context, m queue.IngestMessage) (string, error)
}

// Options configures a Handler.
type Options struct {
	MaxBodyBytes   int64          // 0 = DefaultMaxBodyBytes
	TrustedProxies []netip.Prefix // proxies whose X-Forwarded-For is honoured
	Logger         *slog.Logger
	Now            func() time.Time
}

// Handler serves POST/PUT/PATCH /v1/{slug}.
type Handler struct {
	configs   ConfigSource
	limiter   Limiter
	publisher Publisher
	cipher    *crypto.Cipher
	opts      Options
}

// New builds a Handler.
func New(configs ConfigSource, limiter Limiter, publisher Publisher, cipher *crypto.Cipher, opts Options) *Handler {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Handler{configs: configs, limiter: limiter, publisher: publisher, cipher: cipher, opts: opts}
}

// Register mounts the handler on mux at /v1/{slug}.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/v1/{slug}", h)
}

// HMACAAD binds an endpoint's encrypted HMAC secret to that endpoint.
func HMACAAD(endpointID string) []byte { return []byte("endpoint:" + endpointID + ":hmac") }

// hopByHop headers are connection-scoped and never forwarded or stored.
var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Proxy-Connection": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := h.opts.Now()
	ctx := r.Context()

	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		w.Header().Set("Allow", "POST, PUT, PATCH")
		httpx.Error(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
		return
	}

	cfg, err := h.configs.Lookup(ctx, r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, CodeUnauthorized)
		return
	}
	if err != nil {
		h.unavailable(w, "endpoint lookup failed", err)
		return
	}

	ip := httpx.ClientIP(r, h.opts.TrustedProxies)

	// Header/IP based checks run before the body is read.
	switch cfg.AuthMode {
	case store.AuthIPAllowlist:
		if !ipAllowed(ip, cfg.IPAllowlist) {
			httpx.Error(w, http.StatusUnauthorized, CodeUnauthorized)
			return
		}
	case store.AuthToken:
		if !token.Verify(cfg.AuthTokenHash, r.Header.Get(cfg.AuthHeaderKey)) {
			httpx.Error(w, http.StatusUnauthorized, CodeUnauthorized)
			return
		}
	case store.AuthHMAC:
		// verified after reading the body
	default:
		httpx.Error(w, http.StatusUnauthorized, CodeUnauthorized) // fail closed
		return
	}

	if cfg.Status == store.StatusPaused {
		w.Header().Set("Retry-After", "60")
		httpx.Error(w, http.StatusServiceUnavailable, CodeEndpointPaused)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge)
			return
		}
		httpx.Error(w, http.StatusBadRequest, "bad_request")
		return
	}

	if cfg.AuthMode == store.AuthHMAC && !h.verifyHMAC(cfg, r.Header.Get(cfg.HMACHeader), body) {
		httpx.Error(w, http.StatusUnauthorized, CodeUnauthorized)
		return
	}

	decision, _, err := h.limiter.Check(ctx, cfg.EndpointID, cfg.UserID, cfg.PlanType)
	if err != nil {
		h.unavailable(w, "limiter failed", err)
		return
	}
	switch decision {
	case limits.RateLimited:
		w.Header().Set("Retry-After", "1")
		httpx.Error(w, http.StatusTooManyRequests, CodeRateLimited)
		return
	case limits.QuotaExceeded:
		httpx.Error(w, http.StatusTooManyRequests, CodeQuotaExceeded)
		return
	}

	id := uuid.NewV7()
	env := queue.Envelope{
		Method:  r.Method,
		Query:   r.URL.RawQuery,
		Headers: storedHeaders(r.Header, cfg),
		Body:    body,
	}
	plain, err := env.Marshal()
	if err != nil {
		h.unavailable(w, "envelope encode failed", err)
		return
	}
	ct, nonce, err := h.cipher.Seal(plain, queue.IngestAAD(id))
	if err != nil {
		h.unavailable(w, "encrypt failed", err)
		return
	}

	msg := queue.IngestMessage{
		ID:          id,
		EndpointID:  cfg.EndpointID,
		UserID:      cfg.UserID,
		KeyVersion:  h.cipher.KeyVersion(),
		Ciphertext:  ct,
		Nonce:       nonce,
		ReceivedAt:  start.UTC(),
		SourceIP:    ipString(ip),
		SizeBytes:   len(body),
		ContentType: mediaType(r.Header.Get("Content-Type")),
		Method:      r.Method,
	}
	msg.IngestMicros = h.opts.Now().Sub(start).Microseconds()

	if _, err := h.publisher.PublishIngest(ctx, msg); err != nil {
		h.unavailable(w, "enqueue failed", err)
		return
	}

	w.Header().Set(RequestIDHeader, id)
	httpx.JSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "queued"})
}

func (h *Handler) unavailable(w http.ResponseWriter, msg string, err error) {
	h.opts.Logger.Error(msg, "err", err)
	w.Header().Set("Retry-After", "5")
	httpx.Error(w, http.StatusServiceUnavailable, CodeUnavailable)
}

// verifyHMAC checks an HMAC-SHA256 signature of body. Accepted header formats:
// "<hex>", "sha256=<hex>" (GitHub style) and "<base64>" (Shopify style).
func (h *Handler) verifyHMAC(cfg store.IngestConfig, header string, body []byte) bool {
	header = strings.TrimSpace(header)
	if header == "" || cfg.HMACKeyVersion != h.cipher.KeyVersion() {
		return false
	}
	secret, err := h.cipher.Open(cfg.HMACSecretEnc, cfg.HMACSecretNonce, HMACAAD(cfg.EndpointID))
	if err != nil {
		h.opts.Logger.Error("hmac secret decrypt failed", "endpoint_id", cfg.EndpointID)
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)

	candidate := strings.TrimPrefix(header, "sha256=")
	if got, err := hex.DecodeString(candidate); err == nil && hmac.Equal(got, want) {
		return true
	}
	if got, err := base64.StdEncoding.DecodeString(candidate); err == nil && hmac.Equal(got, want) {
		return true
	}
	return false
}

// storedHeaders returns the request headers worth keeping: hop-by-hop headers
// and the inbound auth token are removed (the token must never be stored).
func storedHeaders(in http.Header, cfg store.IngestConfig) map[string][]string {
	out := make(map[string][]string, len(in))
	authKey := http.CanonicalHeaderKey(cfg.AuthHeaderKey)
	for k, v := range in {
		if hopByHop[k] {
			continue
		}
		if cfg.AuthMode == store.AuthToken && k == authKey {
			continue
		}
		out[k] = v
	}
	return out
}

func mediaType(ct string) string {
	if ct == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return mt
}

func ipAllowed(ip netip.Addr, allow []netip.Prefix) bool {
	if !ip.IsValid() {
		return false
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func ipString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
