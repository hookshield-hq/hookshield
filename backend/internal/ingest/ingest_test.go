package ingest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
)

// ── fakes ────────────────────────────────────────────────

type fakeConfigs struct {
	data map[string]store.IngestConfig
	err  error
}

func (f *fakeConfigs) Lookup(_ context.Context, slug string) (store.IngestConfig, error) {
	if f.err != nil {
		return store.IngestConfig{}, f.err
	}
	c, ok := f.data[slug]
	if !ok {
		return store.IngestConfig{}, store.ErrNotFound
	}
	return c, nil
}

type fakeLimiter struct {
	decision limits.Decision
	err      error
	calls    int
}

func (f *fakeLimiter) Check(context.Context, string, string, string) (limits.Decision, int64, error) {
	f.calls++
	return f.decision, 1, f.err
}

type fakePublisher struct {
	msgs []queue.IngestMessage
	err  error
}

func (f *fakePublisher) PublishIngest(_ context.Context, m queue.IngestMessage) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.msgs = append(f.msgs, m)
	return "1-0", nil
}

// ── fixture ──────────────────────────────────────────────

const hmacSecret = "whsec_test_secret"

type fixture struct {
	h       *Handler
	configs *fakeConfigs
	limiter *fakeLimiter
	pub     *fakePublisher
	cipher  *crypto.Cipher
	tok     string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c, err := crypto.New(bytes.Repeat([]byte{4}, crypto.KeySize), 1)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := token.Generate(token.PrefixInbound)
	secretCT, secretNonce, _ := c.Seal([]byte(hmacSecret), HMACAAD("e-hmac"))
	pausedCT, pausedNonce, _ := c.Seal([]byte(hmacSecret), HMACAAD("e-hmac-paused"))

	f := &fixture{
		configs: &fakeConfigs{data: map[string]store.IngestConfig{
			"ep_token": {EndpointID: "e-token", UserID: "u1", Slug: "ep_token", Status: store.StatusActive, PlanType: "pro",
				AuthMode: store.AuthToken, AuthHeaderKey: "X-HookShield-Token", AuthTokenHash: token.Hash(tok)},
			"ep_paused": {EndpointID: "e-paused", UserID: "u1", Slug: "ep_paused", Status: store.StatusPaused,
				AuthMode: store.AuthToken, AuthHeaderKey: "X-HookShield-Token", AuthTokenHash: token.Hash(tok)},
			"ep_hmac": {EndpointID: "e-hmac", UserID: "u1", Slug: "ep_hmac", Status: store.StatusActive,
				AuthMode: store.AuthHMAC, HMACHeader: "X-Signature", HMACSecretEnc: secretCT, HMACSecretNonce: secretNonce, HMACKeyVersion: 1},
			"ep_hmac_paused": {EndpointID: "e-hmac-paused", UserID: "u1", Slug: "ep_hmac_paused", Status: store.StatusPaused,
				AuthMode: store.AuthHMAC, HMACHeader: "X-Signature", HMACSecretEnc: pausedCT, HMACSecretNonce: pausedNonce, HMACKeyVersion: 1},
			"ep_ips": {EndpointID: "e-ips", UserID: "u1", Slug: "ep_ips", Status: store.StatusActive,
				AuthMode: store.AuthIPAllowlist, IPAllowlist: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
			"ep_weird": {EndpointID: "e-weird", UserID: "u1", Slug: "ep_weird", Status: store.StatusActive, AuthMode: "none"},
		}},
		limiter: &fakeLimiter{decision: limits.Allowed},
		pub:     &fakePublisher{},
		cipher:  c,
		tok:     tok,
	}
	f.h = New(f.configs, f.limiter, f.pub, c, Options{
		MaxBodyBytes:   64,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})
	return f
}

func (f *fixture) do(method, path string, body string, headers map[string]string, remote string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	f.h.Register(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b.Error.Code
}

func sign(body string, hexFormat bool) string {
	m := hmac.New(sha256.New, []byte(hmacSecret))
	m.Write([]byte(body))
	if hexFormat {
		return hex.EncodeToString(m.Sum(nil))
	}
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// ── tests ────────────────────────────────────────────────

func TestAcceptsValidTokenAndEncryptsEnvelope(t *testing.T) {
	f := newFixture(t)
	body := `{"type":"invoice.paid"}`
	rec := f.do(http.MethodPost, "/v1/ep_token?source=stripe", body, map[string]string{
		"X-HookShield-Token": f.tok,
		"Content-Type":       "application/json; charset=utf-8",
		"Stripe-Signature":   "t=1,v1=abc",
		"Connection":         "keep-alive",
	}, "198.51.100.7:5555")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "queued" || resp["id"] == "" || rec.Header().Get(RequestIDHeader) != resp["id"] {
		t.Fatalf("unexpected response %v / header %q", resp, rec.Header().Get(RequestIDHeader))
	}

	if len(f.pub.msgs) != 1 {
		t.Fatalf("published %d messages", len(f.pub.msgs))
	}
	m := f.pub.msgs[0]
	if m.ID != resp["id"] || m.EndpointID != "e-token" || m.UserID != "u1" || m.Method != "POST" ||
		m.SourceIP != "198.51.100.7" || m.SizeBytes != len(body) || m.ContentType != "application/json" || m.KeyVersion != 1 {
		t.Fatalf("unexpected message metadata: %+v", m)
	}
	if bytes.Contains(m.Ciphertext, []byte("invoice.paid")) {
		t.Fatal("payload is not encrypted in the queue message")
	}

	plain, err := f.cipher.Open(m.Ciphertext, m.Nonce, queue.IngestAAD(m.ID))
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	env, _ := queue.UnmarshalEnvelope(plain)
	if string(env.Body) != body || env.Query != "source=stripe" || env.Method != "POST" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if _, ok := env.Headers["X-Hookshield-Token"]; ok {
		t.Fatal("inbound token must never be stored")
	}
	if _, ok := env.Headers["Connection"]; ok {
		t.Fatal("hop-by-hop headers must be dropped")
	}
	if env.Headers["Stripe-Signature"][0] != "t=1,v1=abc" {
		t.Fatal("provider headers must be kept")
	}
	// Ciphertext is bound to its request ID.
	if _, err := f.cipher.Open(m.Ciphertext, m.Nonce, queue.IngestAAD("other-id")); err == nil {
		t.Fatal("envelope decrypted with a foreign request ID")
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		headers func(f *fixture) map[string]string
		remote  string
		status  int
		code    string
	}{
		{"unknown endpoint", "POST", "/v1/ep_nope", "{}", nil, "", 401, CodeUnauthorized},
		{"missing token", "POST", "/v1/ep_token", "{}", nil, "", 401, CodeUnauthorized},
		{"wrong token", "POST", "/v1/ep_token", "{}", func(*fixture) map[string]string {
			return map[string]string{"X-HookShield-Token": "hs_in_wrong"}
		}, "", 401, CodeUnauthorized},
		{"token in other header", "POST", "/v1/ep_token", "{}", func(f *fixture) map[string]string {
			return map[string]string{"Authorization": "Bearer " + f.tok}
		}, "", 401, CodeUnauthorized},
		{"GET not allowed", "GET", "/v1/ep_token", "", nil, "", 405, CodeMethodNotAllowed},
		{"DELETE not allowed", "DELETE", "/v1/ep_token", "", nil, "", 405, CodeMethodNotAllowed},
		{"paused endpoint", "POST", "/v1/ep_paused", "{}", func(f *fixture) map[string]string {
			return map[string]string{"X-HookShield-Token": f.tok}
		}, "", 503, CodeEndpointPaused},
		{"paused but bad token still 401", "POST", "/v1/ep_paused", "{}", nil, "", 401, CodeUnauthorized},
		// docs/INGEST.md: authentication comes before the paused state, in every mode.
		{"paused hmac endpoint, bad signature still 401", "POST", "/v1/ep_hmac_paused", "{}", func(*fixture) map[string]string {
			return map[string]string{"X-Signature": sign("tampered", true)}
		}, "", 401, CodeUnauthorized},
		{"paused hmac endpoint, valid signature", "POST", "/v1/ep_hmac_paused", "{}", func(*fixture) map[string]string {
			return map[string]string{"X-Signature": sign("{}", true)}
		}, "", 503, CodeEndpointPaused},
		{"body too large", "POST", "/v1/ep_token", strings.Repeat("x", 65), func(f *fixture) map[string]string {
			return map[string]string{"X-HookShield-Token": f.tok}
		}, "", 413, CodePayloadTooLarge},
		{"hmac missing", "POST", "/v1/ep_hmac", "{}", nil, "", 401, CodeUnauthorized},
		{"hmac wrong", "POST", "/v1/ep_hmac", "{}", func(*fixture) map[string]string {
			return map[string]string{"X-Signature": sign("tampered", true)}
		}, "", 401, CodeUnauthorized},
		{"ip not allowed", "POST", "/v1/ep_ips", "{}", nil, "192.0.2.1:1", 401, CodeUnauthorized},
		{"spoofed XFF from untrusted peer", "POST", "/v1/ep_ips", "{}", func(*fixture) map[string]string {
			return map[string]string{"X-Forwarded-For": "203.0.113.5"}
		}, "192.0.2.1:1", 401, CodeUnauthorized},
		{"unknown auth mode fails closed", "POST", "/v1/ep_weird", "{}", nil, "", 401, CodeUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			var hdr map[string]string
			if tc.headers != nil {
				hdr = tc.headers(f)
			}
			rec := f.do(tc.method, tc.path, tc.body, hdr, tc.remote)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("got %d %q, want %d %q (body %s)", rec.Code, errCode(t, rec), tc.status, tc.code, rec.Body)
			}
			if len(f.pub.msgs) != 0 {
				t.Fatal("rejected request was enqueued")
			}
			if tc.status == 401 && f.limiter.calls != 0 {
				t.Fatal("unauthenticated request counted against the quota")
			}
		})
	}
}

func TestHMACFormats(t *testing.T) {
	body := `{"event":"order.created"}`
	for name, sig := range map[string]string{
		"hex":        sign(body, true),
		"sha256=hex": "sha256=" + sign(body, true),
		"base64":     sign(body, false),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.do("POST", "/v1/ep_hmac", body, map[string]string{"X-Signature": sig}, "")
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status %d %s", rec.Code, rec.Body)
			}
			plain, _ := f.cipher.Open(f.pub.msgs[0].Ciphertext, f.pub.msgs[0].Nonce, queue.IngestAAD(f.pub.msgs[0].ID))
			env, _ := queue.UnmarshalEnvelope(plain)
			if env.Headers["X-Signature"][0] != sig {
				t.Fatal("signature header should be kept for auditing")
			}
		})
	}
}

func TestIPAllowlistAndTrustedProxy(t *testing.T) {
	f := newFixture(t)
	if rec := f.do("POST", "/v1/ep_ips", "{}", nil, "203.0.113.9:443"); rec.Code != http.StatusAccepted {
		t.Fatalf("direct allowed IP: %d", rec.Code)
	}
	// Behind a trusted proxy (10/8), the client IP comes from X-Forwarded-For.
	rec := f.do("POST", "/v1/ep_ips", "{}", map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.50"}, "10.1.2.3:80")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("via trusted proxy: %d", rec.Code)
	}
	if got := f.pub.msgs[1].SourceIP; got != "203.0.113.50" {
		t.Fatalf("source IP %q, want rightmost untrusted hop", got)
	}
}

func TestLimitsAndFailures(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(f *fixture)
		status     int
		code       string
		retryAfter string
	}{
		{"rate limited", func(f *fixture) { f.limiter.decision = limits.RateLimited }, 429, CodeRateLimited, "1"},
		{"quota exceeded", func(f *fixture) { f.limiter.decision = limits.QuotaExceeded }, 429, CodeQuotaExceeded, ""},
		{"limiter down", func(f *fixture) { f.limiter.err = errors.New("redis down") }, 503, CodeUnavailable, "5"},
		{"queue down", func(f *fixture) { f.pub.err = errors.New("redis down") }, 503, CodeUnavailable, "5"},
		{"config store down", func(f *fixture) { f.configs.err = errors.New("db down") }, 503, CodeUnavailable, "5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			rec := f.do("POST", "/v1/ep_token", "{}", map[string]string{"X-HookShield-Token": f.tok}, "")
			if rec.Code != tc.status || errCode(t, rec) != tc.code || rec.Header().Get("Retry-After") != tc.retryAfter {
				t.Fatalf("got %d %q retry-after=%q", rec.Code, errCode(t, rec), rec.Header().Get("Retry-After"))
			}
		})
	}
}

func TestIngestMicrosMeasured(t *testing.T) {
	f := newFixture(t)
	calls := 0
	base := time.Unix(1_700_000_000, 0)
	f.h.opts.Now = func() time.Time {
		calls++
		return base.Add(time.Duration(calls-1) * 250 * time.Microsecond)
	}
	f.do("POST", "/v1/ep_token", "{}", map[string]string{"X-HookShield-Token": f.tok}, "")
	if m := f.pub.msgs[0]; m.IngestMicros != 250 || !m.ReceivedAt.Equal(base) {
		t.Fatalf("ingest_us=%d received_at=%v", m.IngestMicros, m.ReceivedAt)
	}
}
