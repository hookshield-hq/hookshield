//go:build integration

package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/api"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/endpointcache"
	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testplans"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

// mgmt is a full control plane plus a real ingest service sharing the endpoint cache,
// so changes made through the API are checked from the sender's side.
type mgmt struct {
	*env
	ingest *httptest.Server
	cipher *crypto.Cipher
	rdb    *redis.Client
	keys   redisx.Keys
	pool   *pgxpool.Pool
	api    *api.Server
}

// setupMgmt builds the stack. With a billing provider the API mounts the billing
// routes and both the API and the ingest service apply the blueprint plans.
func setupMgmt(t *testing.T, allowPrivate bool, billing ...core.Billing) *mgmt {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	rdb, keys := testredis.New(t)
	st := store.New(pool)
	cipher, err := crypto.New(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	cache := endpointcache.New(rdb, keys, st)
	mail := &mailbox{}

	svc := auth.New(st, rdb, keys, auth.Options{AppURL: appOrigin, Mailer: mail})
	opts := api.Options{AppURL: appOrigin, AllowedOrigins: []string{appOrigin},
		Store: st, Cipher: cipher, Cache: cache, PublicIngestURL: "https://in.example.com/", AllowPrivateTargets: allowPrivate,
		Redis: rdb, Keys: keys, Limiter: limits.New(rdb, keys, 0, testplans.Blueprint), LivePing: 50 * time.Millisecond}
	var ingestPlans core.Plans
	if len(billing) > 0 {
		opts.Billing, opts.Plans, ingestPlans = billing[0], testplans.Blueprint, testplans.Blueprint
	}
	server := api.New(svc, opts)
	mux := http.NewServeMux()
	server.Register(mux)
	apiSrv := httptest.NewServer(server.Handler(mux))
	t.Cleanup(apiSrv.Close)

	ing := ingest.New(cache, limits.New(rdb, keys, 0, ingestPlans), queue.NewProducer(rdb, keys), cipher, ingest.Options{})
	imux := http.NewServeMux()
	ing.Register(imux)
	ingSrv := httptest.NewServer(imux)
	t.Cleanup(ingSrv.Close)

	return &mgmt{env: &env{t: t, srv: apiSrv, mail: mail, store: st}, ingest: ingSrv, cipher: cipher, rdb: rdb, keys: keys, pool: pool, api: server}
}

// signedIn returns a browser with a fresh account.
func (m *mgmt) signedIn(email string) *browser {
	b := m.browser()
	if r := b.do("POST", "/v1/auth/register", map[string]any{"email": email, "password": goodPassword}); r.status != 201 {
		m.t.Fatalf("register %s: %d", email, r.status)
	}
	return b
}

// verified marks the account of b as email-verified (as the link in the email would).
func (m *mgmt) verified(b *browser) *browser {
	m.t.Helper()
	id := str(obj(b.do("GET", "/v1/me", nil).body["user"])["id"])
	if err := m.store.MarkEmailVerified(context.Background(), id); err != nil {
		m.t.Fatal(err)
	}
	return b
}

// send posts a webhook to the ingest service.
func (m *mgmt) send(slug string, header map[string]string, body string) int {
	m.t.Helper()
	req, _ := http.NewRequest("POST", m.ingest.URL+"/v1/"+slug, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func obj(v any) map[string]any      { o, _ := v.(map[string]any); return o }
func str(v any) string              { s, _ := v.(string); return s }
func list(v any) []any              { l, _ := v.([]any); return l }
func ep(r resp) map[string]any      { return obj(r.body["endpoint"]) }
func secrets(r resp) map[string]any { return obj(r.body["secrets"]) }

func TestEndpointLifecycleSeenByIngest(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("owner@example.com")

	r := b.do("POST", "/v1/endpoints", map[string]any{"name": "Stripe", "target_url": "http://127.0.0.1:9/hook"})
	if r.status != 201 {
		t.Fatalf("create: %d %+v", r.status, r.body)
	}
	e := ep(r)
	slug, id, tok := str(e["slug"]), str(e["id"]), str(secrets(r)["token"])
	auth0 := obj(e["auth"])
	if !strings.HasPrefix(slug, "ep_") || str(e["ingest_url"]) != "https://in.example.com/v1/"+slug ||
		!strings.HasPrefix(tok, "hs_in_") || !strings.HasPrefix(tok, str(auth0["token_prefix"])) ||
		str(auth0["mode"]) != "token" || str(auth0["header"]) != "X-HookShield-Token" || str(e["status"]) != "active" {
		t.Fatalf("created endpoint: %+v secrets=%+v", e, secrets(r))
	}
	retry := obj(e["retry"])
	if retry["max_retries"] != float64(5) || str(retry["strategy"]) != "exponential" || e["dlq_enabled"] != true {
		t.Fatalf("defaults: %+v", e)
	}
	if strings.Contains(r.header.Get("Content-Type"), "json") == false {
		t.Fatal("json content type")
	}

	// The token works right away, and is never shown again.
	if code := m.send(slug, map[string]string{"X-HookShield-Token": tok}, `{}`); code != 202 {
		t.Fatalf("ingest with new token: %d", code)
	}
	if g := b.do("GET", "/v1/endpoints/"+id, nil); g.status != 200 || strings.Contains(mustJSON(g.body), tok) || g.body["secrets"] != nil {
		t.Fatalf("GET must not leak the token: %+v", g.body)
	}
	if l := b.do("GET", "/v1/endpoints", nil); len(list(l.body["endpoints"])) != 1 {
		t.Fatalf("list: %+v", l.body)
	}

	// Rotation: the old token stops working immediately (cache invalidated).
	rot := b.do("POST", "/v1/endpoints/"+id+"/rotate-secret", nil)
	newTok := str(secrets(rot)["token"])
	if rot.status != 200 || newTok == "" || newTok == tok {
		t.Fatalf("rotate: %d %+v", rot.status, rot.body)
	}
	if code := m.send(slug, map[string]string{"X-HookShield-Token": tok}, `{}`); code != 401 {
		t.Fatalf("old token after rotation: %d", code)
	}
	if code := m.send(slug, map[string]string{"X-HookShield-Token": newTok}, `{}`); code != 202 {
		t.Fatalf("new token: %d", code)
	}

	// Pausing is seen by ingest at once.
	if p := b.do("PATCH", "/v1/endpoints/"+id, map[string]any{"status": "paused", "name": "Stripe prod"}); p.status != 200 || str(ep(p)["name"]) != "Stripe prod" {
		t.Fatalf("pause: %d %+v", p.status, p.body)
	}
	if code := m.send(slug, map[string]string{"X-HookShield-Token": newTok}, `{}`); code != 503 {
		t.Fatalf("paused endpoint: %d", code)
	}

	// Switch to HMAC with the provider's secret.
	p := b.do("PATCH", "/v1/endpoints/"+id, map[string]any{"status": "active",
		"auth": map[string]any{"mode": "hmac", "hmac_header": "Stripe-Signature-Test", "hmac_secret": "whsec_provider_secret"}})
	if p.status != 200 || str(obj(ep(p)["auth"])["mode"]) != "hmac" || obj(ep(p)["auth"])["token_prefix"] != nil || len(secrets(p)) != 0 {
		t.Fatalf("switch to hmac: %d %+v", p.status, p.body)
	}
	mac := hmac.New(sha256.New, []byte("whsec_provider_secret"))
	mac.Write([]byte(`{"a":1}`))
	if code := m.send(slug, map[string]string{"Stripe-Signature-Test": hex.EncodeToString(mac.Sum(nil))}, `{"a":1}`); code != 202 {
		t.Fatalf("hmac delivery: %d", code)
	}
	if code := m.send(slug, map[string]string{"X-HookShield-Token": newTok}, `{"a":1}`); code != 401 {
		t.Fatalf("token after switching to hmac: %d", code)
	}

	// Back to token auth: a brand-new token is issued.
	back := b.do("PATCH", "/v1/endpoints/"+id, map[string]any{"auth": map[string]any{"mode": "token"}})
	if back.status != 200 || !strings.HasPrefix(str(secrets(back)["token"]), "hs_in_") {
		t.Fatalf("back to token: %d %+v", back.status, back.body)
	}

	// Delete: the slug disappears for senders.
	if d := b.do("DELETE", "/v1/endpoints/"+id, nil); d.status != 204 {
		t.Fatalf("delete: %d", d.status)
	}
	if code := m.send(slug, map[string]string{"X-HookShield-Token": str(secrets(back)["token"])}, `{}`); code != 401 {
		t.Fatalf("deleted endpoint: %d", code)
	}
	if g := b.do("GET", "/v1/endpoints/"+id, nil); g.status != 404 || g.code != "endpoint.not_found" {
		t.Fatalf("deleted GET: %d %s", g.status, g.code)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestEndpointValidation(t *testing.T) {
	m := setupMgmt(t, false) // production-like: private targets rejected
	b := m.signedIn("dev@example.com")
	ok := map[string]any{"name": "x", "target_url": "https://api.example.com/hook"}
	with := func(kv ...any) map[string]any {
		out := map[string]any{}
		for k, v := range ok {
			out[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	cases := []struct {
		body   map[string]any
		status int
		code   string
	}{
		{with("name", "  "), 422, "endpoint.invalid_name"},
		{with("target_url", "ftp://x"), 422, "endpoint.invalid_target_url"},
		{with("target_url", "http://127.0.0.1:8080/x"), 422, "endpoint.private_target"},
		{with("target_url", "http://localhost/x"), 422, "endpoint.private_target"},
		{with("target_url", "http://169.254.169.254/latest"), 422, "endpoint.private_target"},
		{with("slug", "AB"), 422, "endpoint.invalid_slug"},
		{with("status", "on"), 422, "endpoint.invalid_status"},
		{with("auth", map[string]any{"mode": "basic"}), 422, "endpoint.invalid_auth_mode"},
		{with("auth", map[string]any{"mode": "ip_allowlist"}), 422, "endpoint.ip_allowlist_required"},
		{with("auth", map[string]any{"mode": "ip_allowlist", "ip_allowlist": []string{"10.0.0.0/33"}}), 422, "endpoint.invalid_ip_allowlist"},
		{with("auth", map[string]any{"mode": "hmac", "hmac_secret": "short"}), 422, "endpoint.invalid_hmac_secret"},
		{with("auth", map[string]any{"hmac_secret": "long enough secret"}), 422, "endpoint.hmac_fields_need_hmac_mode"},
		{with("outbound_headers", map[string]string{"Host": "evil"}), 422, "endpoint.invalid_outbound_header"},
		{with("outbound_headers", map[string]string{"X-HookShield-Id": "spoof"}), 422, "endpoint.invalid_outbound_header"},
		{with("outbound_headers", map[string]string{"X-Ok": "a\r\nInjected: 1"}), 422, "endpoint.invalid_outbound_header_value"},
		{with("transform_rules", map[string]any{"version": 1, "body": []any{map[string]any{"op": "explode"}}}), 422, "transform.invalid"},
		{with("retry", map[string]any{"strategy": "custom"}), 422, "retry.invalid_custom_schedule"},
		{with("retry", map[string]any{"max_retries": 101}), 422, "retry.invalid_max_retries"},
		{with("retry", map[string]any{"initial_interval_ms": 10}), 422, "retry.invalid_initial_interval"},
		{with("retry", map[string]any{"multiplier": 0.5}), 422, "retry.invalid_multiplier"},
		{with("alert", map[string]any{"threshold_failures": 0}), 422, "alert.invalid_threshold"},
		{with("alert", map[string]any{"webhook_url": "http://10.0.0.1/alert"}), 422, "alert.private_target"},
		{with("plan", "pro"), 400, "validation.invalid_json"},
	}
	for _, c := range cases {
		if r := b.do("POST", "/v1/endpoints", c.body); r.status != c.status || r.code != c.code {
			t.Errorf("%v: got %d %q, want %d %q", c.body, r.status, r.code, c.status, c.code)
		}
	}

	r := b.do("POST", "/v1/endpoints", with("transform_rules", map[string]any{"version": 1, "body": []any{map[string]any{"op": "explode"}}}))
	if d := obj(r.body["error"])["details"]; len(list(d)) != 1 || str(obj(list(d)[0])["section"]) != "body" {
		t.Fatalf("transform problems must be listed: %+v", r.body)
	}

	// Explicit slugs, conflicts, immutability; a full valid configuration.
	full := with("slug", "stripe-prod", "description", "Payments", "status", "debug",
		"auth", map[string]any{"mode": "ip_allowlist", "ip_allowlist": []string{"203.0.113.7", "198.51.100.0/24"}},
		"outbound_headers", map[string]string{"Authorization": "Bearer {{secrets.API_KEY}}"},
		"transform_rules", map[string]any{"version": 1, "body": []any{map[string]any{"op": "rename", "from": "user_id", "to": "customerId"}}},
		"retry", map[string]any{"strategy": "custom", "custom_schedule_ms": []int{1000, 5000, 60000}, "max_retries": 3},
		"alert", map[string]any{"threshold_failures": 2, "webhook_url": "https://alerts.example.com/x"})
	r = b.do("POST", "/v1/endpoints", full)
	e := ep(r)
	if r.status != 201 || str(e["slug"]) != "stripe-prod" || len(secrets(r)) != 0 ||
		mustJSON(obj(e["auth"])["ip_allowlist"]) != `["203.0.113.7","198.51.100.0/24"]` ||
		mustJSON(obj(e["retry"])["custom_schedule_ms"]) != `[1000,5000,60000]` ||
		str(obj(e["alert"])["webhook_url"]) != "https://alerts.example.com/x" {
		t.Fatalf("full create: %d %+v", r.status, r.body)
	}
	if w := list(r.body["warnings"]); len(w) != 1 || str(obj(w[0])["secret"]) != "API_KEY" {
		t.Fatalf("missing secret warning: %+v", r.body["warnings"])
	}
	if c := b.do("POST", "/v1/endpoints", with("slug", "stripe-prod")); c.status != 409 || c.code != "endpoint.slug_taken" {
		t.Fatalf("slug conflict: %d %s", c.status, c.code)
	}
	if p := b.do("PATCH", "/v1/endpoints/"+str(e["id"]), map[string]any{"slug": "other"}); p.status != 422 || p.code != "endpoint.slug_immutable" {
		t.Fatalf("slug change: %d %s", p.status, p.code)
	}
	if p := b.do("PATCH", "/v1/endpoints/"+str(e["id"]), map[string]any{"alert": map[string]any{"webhook_url": ""}}); obj(ep(p)["alert"])["webhook_url"] != nil {
		t.Fatalf("clearing the alert webhook: %+v", p.body)
	}
	if g := b.do("GET", "/v1/endpoints/not-a-uuid", nil); g.status != 404 {
		t.Fatalf("bad id: %d", g.status)
	}
	if rs := b.do("POST", "/v1/endpoints/"+str(e["id"])+"/rotate-secret", nil); rs.status != 409 || rs.code != "endpoint.no_secret_to_rotate" {
		t.Fatalf("rotate on ip_allowlist: %d %s", rs.status, rs.code)
	}
}

func TestAccountsAreIsolated(t *testing.T) {
	m := setupMgmt(t, true)
	a := m.signedIn("a@example.com")
	bob := m.signedIn("b@example.com")
	id := str(ep(a.do("POST", "/v1/endpoints", map[string]any{"name": "A", "target_url": "https://a.example.com"}))["id"])

	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/endpoints/" + id}, {"PATCH", "/v1/endpoints/" + id}, {"DELETE", "/v1/endpoints/" + id},
		{"POST", "/v1/endpoints/" + id + "/rotate-secret"},
	} {
		var body any
		if c.method == "PATCH" {
			body = map[string]any{"name": "hijacked"}
		}
		if r := bob.do(c.method, c.path, body); r.status != 404 {
			t.Errorf("%s %s by another account: %d", c.method, c.path, r.status)
		}
	}
	if l := bob.do("GET", "/v1/endpoints", nil); len(list(l.body["endpoints"])) != 0 {
		t.Fatal("other accounts' endpoints listed")
	}
	if r := bob.do("POST", "/v1/alert-channels", map[string]any{"type": "email", "endpoint_id": id, "target": map[string]any{}}); r.code != "alert.invalid_endpoint" {
		t.Fatalf("binding a channel to another account's endpoint: %d %s", r.status, r.code)
	}
	if g := a.do("GET", "/v1/endpoints/"+id, nil); str(ep(g)["name"]) != "A" {
		t.Fatal("owner's endpoint changed")
	}
}

func TestSecrets(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	r := b.do("POST", "/v1/endpoints", map[string]any{"name": "x", "target_url": "https://x.example.com",
		"outbound_headers": map[string]string{"Authorization": "Bearer {{secrets.STRIPE_KEY}}"}})
	id := str(ep(r)["id"])
	if len(list(r.body["warnings"])) != 1 {
		t.Fatalf("warning expected: %+v", r.body)
	}

	if p := b.do("PUT", "/v1/secrets/STRIPE_KEY", map[string]string{"value": "sk_live_123"}); p.status != 200 ||
		str(obj(p.body["secret"])["reference"]) != "{{secrets.STRIPE_KEY}}" || strings.Contains(mustJSON(p.body), "sk_live_123") {
		t.Fatalf("put secret: %d %+v", p.status, p.body)
	}
	if g := b.do("GET", "/v1/endpoints/"+id, nil); len(list(g.body["warnings"])) != 0 {
		t.Fatalf("warning must clear once the secret exists: %+v", g.body["warnings"])
	}
	l := b.do("GET", "/v1/secrets", nil)
	if len(list(l.body["secrets"])) != 1 || strings.Contains(mustJSON(l.body), "sk_live") {
		t.Fatalf("list secrets: %+v", l.body)
	}

	// Stored encrypted, bound to user and name: what the dispatcher decrypts.
	me := user(b.do("GET", "/v1/me", nil))
	got, err := m.store.GetUserSecrets(context.Background(), str(me["id"]), []string{"STRIPE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := m.cipher.Open(got["STRIPE_KEY"].Encrypted, got["STRIPE_KEY"].Nonce, store.SecretAAD(str(me["id"]), "STRIPE_KEY"))
	if err != nil || string(plain) != "sk_live_123" {
		t.Fatalf("stored secret: %q %v", plain, err)
	}

	for _, c := range []struct {
		path, value, code string
	}{{"/v1/secrets/lower", "x", "secret.invalid_name"}, {"/v1/secrets/OK", "", "secret.invalid_value"}, {"/v1/secrets/OK", "a\nb", "secret.invalid_value"}} {
		if r := b.do("PUT", c.path, map[string]string{"value": c.value}); r.status != 422 || r.code != c.code {
			t.Errorf("%s %q: %d %s", c.path, c.value, r.status, r.code)
		}
	}
	if d := b.do("DELETE", "/v1/secrets/STRIPE_KEY", nil); d.status != 204 {
		t.Fatalf("delete: %d", d.status)
	}
	if d := b.do("DELETE", "/v1/secrets/STRIPE_KEY", nil); d.status != 404 || d.code != "secret.not_found" {
		t.Fatalf("delete twice: %d %s", d.status, d.code)
	}
}

func TestAlertChannels(t *testing.T) {
	m := setupMgmt(t, false)
	b := m.signedIn("dev@example.com")
	epID := str(ep(b.do("POST", "/v1/endpoints", map[string]any{"name": "x", "target_url": "https://x.example.com"}))["id"])

	// Email alerts need a verified account: an unverified one could mail
	// anyone from this install's domain (security review).
	if r := b.do("POST", "/v1/alert-channels", map[string]any{"type": "email", "target": map[string]any{"to": "victim@example.com"}}); r.status != 403 || r.code != "alert.email_unverified" {
		t.Fatalf("email channel of an unverified account: %d %s", r.status, r.code)
	}
	m.verified(b)

	slack := "https://hooks.slack.com/services/T000/B000/SECRETXYZW"
	bot := "123456789:AAH-very-secret-bot-token-1234"
	created := map[string]string{}
	for typ, target := range map[string]any{
		"webhook":  map[string]any{"url": "https://ops.example.com/hookshield?key=topsecret"},
		"slack":    map[string]any{"webhook_url": slack},
		"telegram": map[string]any{"bot_token": bot, "chat_id": "-100123"},
		"email":    map[string]any{"to": "ops@example.com"},
	} {
		r := b.do("POST", "/v1/alert-channels", map[string]any{"type": typ, "target": target})
		if r.status != 201 {
			t.Fatalf("create %s: %d %+v", typ, r.status, r.body)
		}
		created[typ] = str(obj(r.body["alert_channel"])["id"])
		for _, secret := range []string{"topsecret", "SECRETXYZW"[:6], "very-secret"} {
			if strings.Contains(mustJSON(r.body), secret) {
				t.Errorf("%s response leaks %q: %s", typ, secret, mustJSON(r.body))
			}
		}
	}
	l := b.do("GET", "/v1/alert-channels", nil)
	if len(list(l.body["alert_channels"])) != 4 {
		t.Fatalf("list: %+v", l.body)
	}

	// What the alert sender reads back is the full target.
	me := user(b.do("GET", "/v1/me", nil))
	chans, err := m.store.ListAlertChannels(context.Background(), str(me["id"]), epID)
	if err != nil || len(chans) != 4 {
		t.Fatalf("store channels: %d %v", len(chans), err)
	}
	for _, c := range chans {
		plain, err := m.cipher.Open(c.TargetEncrypted, c.TargetNonce, store.AlertChannelAAD(c.ID))
		if err != nil {
			t.Fatal(err)
		}
		if c.Type == "slack" {
			var st alert.SlackTarget
			if json.Unmarshal(plain, &st); st.WebhookURL != slack {
				t.Fatalf("slack target for the sender: %s", plain)
			}
		}
		if c.Type == "telegram" {
			var tt alert.TelegramTarget
			if json.Unmarshal(plain, &tt); tt.BotToken != bot || tt.ChatID != "-100123" {
				t.Fatalf("telegram target for the sender: %s", plain)
			}
		}
	}

	// Scope to one endpoint, disable, change target, back to account-wide, delete.
	id := created["email"]
	p := b.do("PATCH", "/v1/alert-channels/"+id, map[string]any{"endpoint_id": epID, "enabled": false, "target": map[string]any{"to": "oncall@example.com"}})
	c := obj(p.body["alert_channel"])
	if p.status != 200 || str(c["endpoint_id"]) != epID || c["enabled"] != false || str(obj(c["target"])["to"]) != "oncall@example.com" {
		t.Fatalf("patch: %d %+v", p.status, p.body)
	}
	if p := b.do("PATCH", "/v1/alert-channels/"+id, map[string]any{"endpoint_id": nil}); obj(p.body["alert_channel"])["endpoint_id"] != nil {
		t.Fatalf("back to account-wide: %+v", p.body)
	}
	if d := b.do("DELETE", "/v1/alert-channels/"+id, nil); d.status != 204 {
		t.Fatalf("delete: %d", d.status)
	}

	for _, bad := range []map[string]any{
		{"type": "sms", "target": map[string]any{}},
		{"type": "webhook", "target": map[string]any{"url": "http://192.168.0.10/x"}},
		{"type": "slack", "target": map[string]any{"webhook_url": "https://evil.example.com/services/x"}},
		{"type": "telegram", "target": map[string]any{"bot_token": "nope", "chat_id": "1"}},
		{"type": "email", "target": map[string]any{"to": "not-an-email"}},
	} {
		if r := b.do("POST", "/v1/alert-channels", bad); r.status != 422 {
			t.Errorf("%v accepted: %d %s", bad, r.status, r.code)
		}
	}
}

func TestAPIKeysAndScopes(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")

	mk := func(name, scope string) string {
		r := b.do("POST", "/v1/api-keys", map[string]any{"name": name, "scope": scope})
		k := str(r.body["key"])
		if r.status != 201 || !strings.HasPrefix(k, "hs_live_") || !strings.HasPrefix(k, str(obj(r.body["api_key"])["prefix"])) {
			t.Fatalf("create %s key: %d %+v", scope, r.status, r.body)
		}
		return k
	}
	readKey, adminKey := mk("CI read", "read_only"), mk("Terraform", "admin")
	if l := b.do("GET", "/v1/api-keys", nil); len(list(l.body["api_keys"])) != 2 || strings.Contains(mustJSON(l.body), readKey) {
		t.Fatalf("list keys: %+v", l.body)
	}

	bearer := func(key, method, path string, body any) resp {
		c := m.browser()
		c.origin = ""
		req, _ := http.NewRequest(method, m.srv.URL+path, strings.NewReader(mustJSON(body)))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := resp{status: res.StatusCode}
		_ = json.NewDecoder(res.Body).Decode(&out.body)
		out.code = str(obj(out.body["error"])["code"])
		return out
	}
	newEP := map[string]any{"name": "via key", "target_url": "https://x.example.com"}

	if r := bearer(readKey, "GET", "/v1/endpoints", nil); r.status != 200 {
		t.Fatalf("read key GET: %d", r.status)
	}
	if r := bearer(readKey, "POST", "/v1/endpoints", newEP); r.status != 403 || r.code != "auth.insufficient_scope" {
		t.Fatalf("read key POST: %d %s", r.status, r.code)
	}
	if r := bearer(adminKey, "POST", "/v1/endpoints", newEP); r.status != 201 {
		t.Fatalf("admin key POST: %d %+v", r.status, r.body)
	}
	if r := bearer(adminKey, "GET", "/v1/me", nil); r.status != 200 || str(user(r)["email"]) != "dev@example.com" {
		t.Fatalf("admin key /v1/me: %d", r.status)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/v1/api-keys"}, {"POST", "/v1/api-keys"}, {"PATCH", "/v1/me"}, {"POST", "/v1/auth/change-password"}} {
		if r := bearer(adminKey, c.method, c.path, map[string]any{}); r.status != 403 || r.code != "auth.session_required" {
			t.Errorf("admin key on %s %s: %d %s", c.method, c.path, r.status, r.code)
		}
	}
	if r := bearer("hs_live_doesnotexist", "GET", "/v1/endpoints", nil); r.status != 401 {
		t.Fatalf("unknown key: %d", r.status)
	}

	keys := list(b.do("GET", "/v1/api-keys", nil).body["api_keys"])
	var adminID string
	for _, k := range keys {
		if str(obj(k)["scope"]) == "admin" {
			adminID = str(obj(k)["id"])
			if obj(k)["last_used_at"] == nil {
				t.Error("last_used_at not recorded")
			}
		}
	}
	if d := b.do("DELETE", "/v1/api-keys/"+adminID, nil); d.status != 204 {
		t.Fatalf("revoke: %d", d.status)
	}
	if r := bearer(adminKey, "GET", "/v1/endpoints", nil); r.status != 401 {
		t.Fatalf("revoked key: %d", r.status)
	}
	if r := b.do("POST", "/v1/api-keys", map[string]any{"name": "x", "scope": "root"}); r.code != "api_key.invalid_scope" {
		t.Fatalf("invalid scope: %s", r.code)
	}
}

func TestTransformPreview(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	r := b.do("POST", "/v1/transform/preview", map[string]any{
		"rules": map[string]any{"version": 1,
			"body":    []any{map[string]any{"op": "rename", "from": "user_id", "to": "customerId"}, map[string]any{"op": "set", "path": "source", "value": "hookshield"}},
			"headers": []any{map[string]any{"op": "set", "name": "X-Env", "value": "prod"}, map[string]any{"op": "remove", "name": "X-Debug"}}},
		"request": map[string]any{"headers": map[string]any{"X-Debug": "1", "Accept": []string{"a", "b"}}, "body": map[string]any{"user_id": 42}},
	})
	req := obj(r.body["request"])
	if r.status != 200 || mustJSON(req["body"]) != `{"customerId":42,"source":"hookshield"}` {
		t.Fatalf("preview: %d %+v", r.status, r.body)
	}
	h := obj(req["headers"])
	if h["X-Debug"] != nil || mustJSON(h["X-Env"]) != `["prod"]` || mustJSON(h["Accept"]) != `["a","b"]` {
		t.Fatalf("preview headers: %+v", h)
	}
	if r := b.do("POST", "/v1/transform/preview", map[string]any{"rules": map[string]any{"version": 1, "body": []any{map[string]any{"op": "set"}}}}); r.status != 422 || r.code != "transform.invalid" {
		t.Fatalf("invalid rules: %d %s", r.status, r.code)
	}
	text := b.do("POST", "/v1/transform/preview", map[string]any{
		"rules": map[string]any{"version": 1, "body": []any{map[string]any{"op": "set", "path": "a", "value": 1}}}, "request": map[string]any{"body_text": "plain"}})
	if str(obj(text.body["request"])["body_text"]) != "plain" || len(list(text.body["warnings"])) == 0 {
		t.Fatalf("non-JSON body: %+v", text.body)
	}

	// Rules that would blow the body up: copying into itself is invalid, and
	// alternating copies stop at the size budget (security review).
	self := b.do("POST", "/v1/transform/preview", map[string]any{
		"rules": map[string]any{"version": 1, "body": []any{map[string]any{"op": "copy", "from": "a", "to": "a.b"}}}, "request": map[string]any{"body": map[string]any{"a": 1}}})
	if self.status != 422 || self.code != "transform.invalid" || !strings.Contains(mustJSON(self.body), "transform.target_inside_source") {
		t.Fatalf("copy into itself: %d %+v", self.status, self.body)
	}
	var rules []any
	for i := 0; i < 40; i++ {
		from, to := "a", fmt.Sprintf("b.n%d", i)
		if i%2 == 1 {
			from, to = "b", fmt.Sprintf("a.n%d", i)
		}
		rules = append(rules, map[string]any{"op": "copy", "from": from, "to": to})
	}
	boom := b.do("POST", "/v1/transform/preview", map[string]any{
		"rules": map[string]any{"version": 1, "body": rules}, "request": map[string]any{"body": map[string]any{"a": map[string]any{"x": strings.Repeat("y", 100)}, "b": map[string]any{}}}})
	if boom.status != 422 || boom.code != "transform.output_too_large" {
		t.Fatalf("exploding copies: %d %s", boom.status, boom.code)
	}
}
