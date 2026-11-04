package api

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hookshield-hq/hookshield/backend/internal/ingest"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/transform"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// Defaults of a new endpoint (the column defaults of migration 0002).
const (
	defaultAuthHeader = "X-HookShield-Token"
	defaultHMACHeader = "X-HookShield-Signature"
	maxOutboundHeader = 50
	maxHeaderValue    = 4096
	maxIPAllowlist    = 100
	maxRetryDelay     = 7 * 24 * time.Hour
)

var (
	slugRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,99}$`)
	headerRE    = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,100}$")
	uuidRE      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	secretRefRE = regexp.MustCompile(`\{\{\s*secrets\.([A-Za-z0-9_]+)\s*\}\}`)
)

// Headers the destination request controls itself or that HookShield sets.
var reservedOutbound = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Keep-Alive": true, "Upgrade": true, "Te": true, "Trailer": true, "Proxy-Connection": true,
}

// endpointJSON is the public shape of an endpoint. Secrets (token, HMAC
// secret) are never part of it; they are returned once, in "secrets".
type endpointJSON struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Slug            string            `json:"slug"`
	IngestURL       string            `json:"ingest_url"`
	TargetURL       string            `json:"target_url"`
	Status          string            `json:"status"`
	Auth            endpointAuthJSON  `json:"auth"`
	OutboundHeaders map[string]string `json:"outbound_headers"`
	TransformRules  json.RawMessage   `json:"transform_rules"`
	Retry           retryJSON         `json:"retry"`
	DLQEnabled      bool              `json:"dlq_enabled"`
	Alert           alertConfigJSON   `json:"alert"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type endpointAuthJSON struct {
	Mode        string   `json:"mode"`
	Header      string   `json:"header"`
	TokenPrefix string   `json:"token_prefix,omitempty"`
	HMACHeader  string   `json:"hmac_header,omitempty"`
	IPAllowlist []string `json:"ip_allowlist"`
}

type retryJSON struct {
	Strategy          string  `json:"strategy"`
	MaxRetries        int     `json:"max_retries"`
	InitialIntervalMs int     `json:"initial_interval_ms"`
	Multiplier        float64 `json:"multiplier"`
	MaxDelayMs        int     `json:"max_delay_ms"`
	CustomScheduleMs  []int32 `json:"custom_schedule_ms"`
}

type alertConfigJSON struct {
	ThresholdFailures int     `json:"threshold_failures"`
	WebhookURL        *string `json:"webhook_url"`
}

func (s *Server) toEndpointJSON(e store.EndpointRecord) endpointJSON {
	ips := make([]string, len(e.IPAllowlist))
	for i, p := range e.IPAllowlist {
		if p.IsSingleIP() {
			ips[i] = p.Addr().String()
		} else {
			ips[i] = p.String()
		}
	}
	rules := e.TransformRules
	if len(rules) == 0 {
		rules = json.RawMessage(`{}`)
	}
	schedule := e.RetryCustomScheduleMs
	if schedule == nil {
		schedule = []int32{}
	}
	return endpointJSON{
		ID: e.ID, Name: e.Name, Description: e.Description, Slug: e.Slug,
		IngestURL: s.opts.PublicIngestURL + "/v1/" + e.Slug, TargetURL: e.TargetURL, Status: e.Status,
		Auth: endpointAuthJSON{Mode: e.AuthMode, Header: e.AuthHeaderKey, TokenPrefix: e.AuthTokenPrefix,
			HMACHeader: e.HMACHeader, IPAllowlist: ips},
		OutboundHeaders: e.OutboundHeaders, TransformRules: rules,
		Retry: retryJSON{Strategy: e.RetryStrategy, MaxRetries: e.MaxRetries, InitialIntervalMs: e.RetryInitialMs,
			Multiplier: e.RetryMultiplier, MaxDelayMs: e.RetryMaxDelayMs, CustomScheduleMs: schedule},
		DLQEnabled: e.DLQEnabled,
		Alert:      alertConfigJSON{ThresholdFailures: e.AlertThresholdFailures, WebhookURL: e.AlertWebhookURL},
		CreatedAt:  e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	}
}

// endpointInput is the body of POST (create) and PATCH (partial update).
// Absent fields keep their value (PATCH) or get the default (POST).
type endpointInput struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Slug            *string            `json:"slug"` // create only
	TargetURL       *string            `json:"target_url"`
	Status          *string            `json:"status"`
	Auth            *authInput         `json:"auth"`
	OutboundHeaders *map[string]string `json:"outbound_headers"`
	TransformRules  json.RawMessage    `json:"transform_rules"`
	Retry           *retryInput        `json:"retry"`
	DLQEnabled      *bool              `json:"dlq_enabled"`
	Alert           *alertInput        `json:"alert"`
}

type authInput struct {
	Mode        *string   `json:"mode"`
	Header      *string   `json:"header"`
	HMACHeader  *string   `json:"hmac_header"`
	HMACSecret  *string   `json:"hmac_secret"`
	IPAllowlist *[]string `json:"ip_allowlist"`
}

type retryInput struct {
	Strategy          *string  `json:"strategy"`
	MaxRetries        *int     `json:"max_retries"`
	InitialIntervalMs *int     `json:"initial_interval_ms"`
	Multiplier        *float64 `json:"multiplier"`
	MaxDelayMs        *int     `json:"max_delay_ms"`
	CustomScheduleMs  *[]int32 `json:"custom_schedule_ms"`
}

type alertInput struct {
	ThresholdFailures *int    `json:"threshold_failures"`
	WebhookURL        *string `json:"webhook_url"` // "" clears it
}

// generatedSecrets are shown once, in the response that created them.
type generatedSecrets struct {
	Token      string `json:"token,omitempty"`
	HMACSecret string `json:"hmac_secret,omitempty"`
}

// apiError is a 4xx with a translatable code and optional details.
type apiError struct {
	Status  int
	Code    string
	Details any
}

func (e *apiError) Error() string { return e.Code }

func invalid(code string) *apiError {
	return &apiError{Status: http.StatusUnprocessableEntity, Code: code}
}

func (s *Server) writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		body := map[string]any{"code": ae.Code}
		if ae.Details != nil {
			body["details"] = ae.Details
		}
		writeJSON(w, ae.Status, map[string]any{"error": body})
		return
	}
	s.fail(w, err)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var errEndpointNotFound = &apiError{Status: http.StatusNotFound, Code: "endpoint.not_found"}

func newEndpointDefaults(userID string) store.EndpointRecord {
	return store.EndpointRecord{
		ID: uuid.NewV7(), UserID: userID, Status: store.StatusActive, AuthHeaderKey: defaultAuthHeader,
		OutboundHeaders: map[string]string{}, TransformRules: json.RawMessage(`{}`),
		MaxRetries: 5, RetryStrategy: "exponential", RetryInitialMs: 10_000, RetryMultiplier: 2,
		RetryMaxDelayMs: 3_600_000, DLQEnabled: true, AlertThresholdFailures: 4, HMACKeyVersion: 1,
	}
}

// applyEndpoint merges in into e and validates the result. creating = POST.
func (s *Server) applyEndpoint(e *store.EndpointRecord, in endpointInput, creating bool) (generatedSecrets, error) {
	var gen generatedSecrets
	if in.Name != nil {
		e.Name = strings.TrimSpace(*in.Name)
	}
	if n := utf8.RuneCountInString(e.Name); n < 1 || n > 100 {
		return gen, invalid("endpoint.invalid_name")
	}
	if in.Description != nil {
		e.Description = strings.TrimSpace(*in.Description)
	}
	if utf8.RuneCountInString(e.Description) > 1000 {
		return gen, invalid("endpoint.invalid_description")
	}
	if in.Slug != nil {
		if !creating {
			return gen, invalid("endpoint.slug_immutable")
		}
		e.Slug = strings.TrimSpace(*in.Slug)
		if !slugRE.MatchString(e.Slug) {
			return gen, invalid("endpoint.invalid_slug")
		}
	}
	if in.TargetURL != nil {
		e.TargetURL = strings.TrimSpace(*in.TargetURL)
	}
	if err := s.checkURL(e.TargetURL, "endpoint.invalid_target_url", "endpoint.private_target"); err != nil {
		return gen, err
	}
	if in.Status != nil {
		e.Status = *in.Status
	}
	switch e.Status {
	case store.StatusActive, store.StatusPaused, store.StatusDebug:
	default:
		return gen, invalid("endpoint.invalid_status")
	}

	authGen, err := s.applyAuth(e, in.Auth, creating)
	if err != nil {
		return gen, err
	}
	gen = authGen

	if in.OutboundHeaders != nil {
		e.OutboundHeaders = *in.OutboundHeaders
	}
	if err := validateOutboundHeaders(e.OutboundHeaders); err != nil {
		return gen, err
	}
	if in.TransformRules != nil {
		raw := in.TransformRules
		if string(raw) == "null" {
			raw = json.RawMessage(`{}`)
		}
		if err := validateRules(raw); err != nil {
			return gen, err
		}
		e.TransformRules = raw
	}
	if err := applyRetry(e, in.Retry); err != nil {
		return gen, err
	}
	if in.DLQEnabled != nil {
		e.DLQEnabled = *in.DLQEnabled
	}
	if in.Alert != nil {
		if in.Alert.ThresholdFailures != nil {
			e.AlertThresholdFailures = *in.Alert.ThresholdFailures
		}
		if in.Alert.WebhookURL != nil {
			if u := strings.TrimSpace(*in.Alert.WebhookURL); u == "" {
				e.AlertWebhookURL = nil
			} else {
				e.AlertWebhookURL = &u
			}
		}
	}
	if e.AlertThresholdFailures < 1 || e.AlertThresholdFailures > 100 {
		return gen, invalid("alert.invalid_threshold")
	}
	if e.AlertWebhookURL != nil {
		if err := s.checkURL(*e.AlertWebhookURL, "alert.invalid_webhook_url", "alert.private_target"); err != nil {
			return gen, err
		}
	}
	return gen, nil
}

func (s *Server) applyAuth(e *store.EndpointRecord, in *authInput, creating bool) (generatedSecrets, error) {
	var gen generatedSecrets
	oldMode := e.AuthMode
	if creating && e.AuthMode == "" {
		e.AuthMode = store.AuthToken
	}
	if in == nil {
		in = &authInput{}
	}
	if in.Mode != nil {
		e.AuthMode = *in.Mode
	}
	switch e.AuthMode {
	case store.AuthToken, store.AuthHMAC, store.AuthIPAllowlist:
	default:
		return gen, invalid("endpoint.invalid_auth_mode")
	}
	modeChanged := creating || e.AuthMode != oldMode

	if in.Header != nil {
		e.AuthHeaderKey = strings.TrimSpace(*in.Header)
	}
	if !headerRE.MatchString(e.AuthHeaderKey) {
		return gen, invalid("endpoint.invalid_header")
	}

	// Token: a new token whenever token auth is switched on.
	if e.AuthMode == store.AuthToken {
		if modeChanged || len(e.AuthTokenHash) == 0 {
			tok, err := token.Generate(token.PrefixInbound)
			if err != nil {
				return gen, err
			}
			e.AuthTokenHash, e.AuthTokenPrefix, gen.Token = token.Hash(tok), token.DisplayPrefix(tok), tok
		}
	} else {
		e.AuthTokenHash, e.AuthTokenPrefix = nil, ""
	}

	// HMAC: the provider's secret (e.g. Stripe's whsec_…) or a generated one.
	if e.AuthMode == store.AuthHMAC {
		if in.HMACHeader != nil {
			e.HMACHeader = strings.TrimSpace(*in.HMACHeader)
		}
		if e.HMACHeader == "" {
			e.HMACHeader = defaultHMACHeader
		}
		if !headerRE.MatchString(e.HMACHeader) {
			return gen, invalid("endpoint.invalid_hmac_header")
		}
		secret := ""
		switch {
		case in.HMACSecret != nil:
			secret = *in.HMACSecret
			if len(secret) < 8 || len(secret) > 512 || strings.ContainsAny(secret, "\r\n") {
				return gen, invalid("endpoint.invalid_hmac_secret")
			}
		case modeChanged || len(e.HMACSecretEnc) == 0:
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return gen, err
			}
			secret = "hs_hmac_" + hex.EncodeToString(b)
			gen.HMACSecret = secret
		}
		if secret != "" {
			ct, nonce, err := s.opts.Cipher.Seal([]byte(secret), ingest.HMACAAD(e.ID))
			if err != nil {
				return gen, err
			}
			e.HMACSecretEnc, e.HMACSecretNonce, e.HMACKeyVersion = ct, nonce, s.opts.Cipher.KeyVersion()
		}
	} else {
		e.HMACHeader, e.HMACSecretEnc, e.HMACSecretNonce = "", nil, nil
		if in.HMACSecret != nil || in.HMACHeader != nil {
			return gen, invalid("endpoint.hmac_fields_need_hmac_mode")
		}
	}

	// IP allow-list.
	if in.IPAllowlist != nil {
		if len(*in.IPAllowlist) > maxIPAllowlist {
			return gen, invalid("endpoint.invalid_ip_allowlist")
		}
		list := make([]netip.Prefix, 0, len(*in.IPAllowlist))
		for _, raw := range *in.IPAllowlist {
			raw = strings.TrimSpace(raw)
			if a, err := netip.ParseAddr(raw); err == nil {
				list = append(list, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return gen, &apiError{Status: http.StatusUnprocessableEntity, Code: "endpoint.invalid_ip_allowlist", Details: map[string]string{"value": raw}}
			}
			list = append(list, p.Masked())
		}
		e.IPAllowlist = list
	}
	if e.AuthMode == store.AuthIPAllowlist {
		if len(e.IPAllowlist) == 0 {
			return gen, invalid("endpoint.ip_allowlist_required")
		}
	} else {
		e.IPAllowlist = nil
	}
	return gen, nil
}

func validateOutboundHeaders(h map[string]string) error {
	if len(h) > maxOutboundHeader {
		return invalid("endpoint.too_many_outbound_headers")
	}
	for k, v := range h {
		canon := http.CanonicalHeaderKey(k)
		if !headerRE.MatchString(k) || reservedOutbound[canon] || strings.HasPrefix(canon, "X-Hookshield-") {
			return &apiError{Status: http.StatusUnprocessableEntity, Code: "endpoint.invalid_outbound_header", Details: map[string]string{"header": k}}
		}
		if len(v) > maxHeaderValue || strings.ContainsAny(v, "\r\n\x00") {
			return &apiError{Status: http.StatusUnprocessableEntity, Code: "endpoint.invalid_outbound_header_value", Details: map[string]string{"header": k}}
		}
	}
	return nil
}

func validateRules(raw []byte) error {
	_, err := transform.Parse(raw)
	var ve *transform.ValidationError
	if errors.As(err, &ve) {
		return &apiError{Status: http.StatusUnprocessableEntity, Code: "transform.invalid", Details: ve.Problems}
	}
	if err != nil {
		return invalid("transform.invalid_json")
	}
	return nil
}

func applyRetry(e *store.EndpointRecord, in *retryInput) error {
	if in != nil {
		if in.Strategy != nil {
			e.RetryStrategy = *in.Strategy
		}
		if in.MaxRetries != nil {
			e.MaxRetries = *in.MaxRetries
		}
		if in.InitialIntervalMs != nil {
			e.RetryInitialMs = *in.InitialIntervalMs
		}
		if in.Multiplier != nil {
			e.RetryMultiplier = *in.Multiplier
		}
		if in.MaxDelayMs != nil {
			e.RetryMaxDelayMs = *in.MaxDelayMs
		}
		if in.CustomScheduleMs != nil {
			e.RetryCustomScheduleMs = *in.CustomScheduleMs
		}
	}
	maxMs := int(maxRetryDelay / time.Millisecond)
	switch e.RetryStrategy {
	case "fixed", "exponential", "custom":
	default:
		return invalid("retry.invalid_strategy")
	}
	switch {
	case e.MaxRetries < 0 || e.MaxRetries > 100:
		return invalid("retry.invalid_max_retries")
	case e.RetryInitialMs < 1000 || e.RetryInitialMs > maxMs:
		return invalid("retry.invalid_initial_interval")
	case e.RetryMultiplier < 1 || e.RetryMultiplier > 10:
		return invalid("retry.invalid_multiplier")
	case e.RetryMaxDelayMs < e.RetryInitialMs || e.RetryMaxDelayMs > maxMs:
		return invalid("retry.invalid_max_delay")
	}
	if e.RetryStrategy == "custom" {
		if len(e.RetryCustomScheduleMs) == 0 || len(e.RetryCustomScheduleMs) > 100 {
			return invalid("retry.invalid_custom_schedule")
		}
		for _, d := range e.RetryCustomScheduleMs {
			if d < 1000 || int(d) > maxMs {
				return invalid("retry.invalid_custom_schedule")
			}
		}
	}
	return nil
}

// checkURL validates an outbound URL. Without HS_ALLOW_PRIVATE_TARGETS,
// literal private addresses and localhost are rejected up front (the
// dispatcher also re-checks every resolved address at delivery time).
func (s *Server) checkURL(raw, invalidCode, privateCode string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(raw) > 2048 {
		return invalid(invalidCode)
	}
	if s.opts.AllowPrivateTargets {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
		return invalid(privateCode)
	}
	if ip := net.ParseIP(host); ip != nil {
		if a, ok := netip.AddrFromSlice(ip); ok && outbound.IsBlocked(a.Unmap()) {
			return invalid(privateCode)
		}
	}
	return nil
}

// warnings lists {{secrets.NAME}} references that do not exist yet: the
// endpoint is saved, but deliveries fail with secret_not_found until they do.
func (s *Server) warnings(ctx context.Context, e store.EndpointRecord) []map[string]string {
	out := []map[string]string{}
	seen := map[string]bool{}
	var names []string
	for _, v := range e.OutboundHeaders {
		for _, m := range secretRefRE.FindAllStringSubmatch(v, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				names = append(names, m[1])
			}
		}
	}
	if len(names) == 0 {
		return out
	}
	have, err := s.opts.Store.GetUserSecrets(ctx, e.UserID, names)
	if err != nil {
		return out
	}
	for _, n := range names {
		if _, ok := have[n]; !ok {
			out = append(out, map[string]string{"code": "endpoint.secret_missing", "secret": n})
		}
	}
	return out
}

func (s *Server) invalidate(ctx context.Context, slug string) {
	if s.opts.Cache == nil {
		return
	}
	if err := s.opts.Cache.Invalidate(ctx, slug); err != nil {
		// The cache entry expires on its own (10 min); log loudly so it is noticed.
		s.opts.Logger.Error("endpoint cache invalidation failed", "slug", slug, "err", err)
	}
}

func randomSlug() string {
	b := make([]byte, 7)
	_, _ = rand.Read(b)
	return "ep_" + strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}

// ── handlers ──────────────────────────────────────────────

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := s.opts.Store.ListEndpoints(r.Context(), userFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]endpointJSON, len(eps))
	for i, e := range eps {
		out[i] = s.toEndpointJSON(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": out})
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request) {
	e, err := s.loadEndpoint(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoint": s.toEndpointJSON(e), "warnings": s.warnings(r.Context(), e)})
}

func (s *Server) loadEndpoint(r *http.Request) (store.EndpointRecord, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return store.EndpointRecord{}, errEndpointNotFound
	}
	e, err := s.opts.Store.GetEndpoint(r.Context(), userFrom(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return e, errEndpointNotFound
	}
	return e, err
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var in endpointInput
	if !decode(w, r, &in) {
		return
	}
	e := newEndpointDefaults(userFrom(r).ID)
	explicitSlug := in.Slug != nil
	if !explicitSlug {
		e.Slug = randomSlug()
	}
	gen, err := s.applyEndpoint(&e, in, true)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var saved store.EndpointRecord
	for attempt := 0; ; attempt++ {
		saved, err = s.opts.Store.InsertEndpoint(r.Context(), e)
		if !errors.Is(err, store.ErrConflict) || explicitSlug || attempt == 3 {
			break
		}
		e.Slug = randomSlug()
	}
	if errors.Is(err, store.ErrConflict) {
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "endpoint.slug_taken"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.invalidate(r.Context(), saved.Slug) // clears a cached "unknown slug"
	writeJSON(w, http.StatusCreated, map[string]any{
		"endpoint": s.toEndpointJSON(saved), "secrets": gen, "warnings": s.warnings(r.Context(), saved)})
}

func (s *Server) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	var in endpointInput
	if !decode(w, r, &in) {
		return
	}
	e, err := s.loadEndpoint(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	gen, err := s.applyEndpoint(&e, in, false)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	saved, err := s.opts.Store.UpdateEndpoint(r.Context(), e)
	if errors.Is(err, store.ErrNotFound) {
		s.writeErr(w, errEndpointNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.invalidate(r.Context(), saved.Slug)
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoint": s.toEndpointJSON(saved), "secrets": gen, "warnings": s.warnings(r.Context(), saved)})
}

// rotateSecret issues a new inbound token (token mode) or HMAC secret (hmac mode).
func (s *Server) rotateSecret(w http.ResponseWriter, r *http.Request) {
	e, err := s.loadEndpoint(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	var gen generatedSecrets
	switch e.AuthMode {
	case store.AuthToken:
		tok, err := token.Generate(token.PrefixInbound)
		if err != nil {
			s.fail(w, err)
			return
		}
		e.AuthTokenHash, e.AuthTokenPrefix, gen.Token = token.Hash(tok), token.DisplayPrefix(tok), tok
	case store.AuthHMAC:
		e.HMACSecretEnc = nil // forces a generated secret
		mode := store.AuthHMAC
		if gen, err = s.applyAuth(&e, &authInput{Mode: &mode}, false); err != nil {
			s.writeErr(w, err)
			return
		}
	default:
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "endpoint.no_secret_to_rotate"})
		return
	}
	saved, err := s.opts.Store.UpdateEndpoint(r.Context(), e)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.invalidate(r.Context(), saved.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"endpoint": s.toEndpointJSON(saved), "secrets": gen})
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		s.writeErr(w, errEndpointNotFound)
		return
	}
	slug, err := s.opts.Store.DeleteEndpoint(r.Context(), userFrom(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeErr(w, errEndpointNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.invalidate(r.Context(), slug)
	w.WriteHeader(http.StatusNoContent)
}
