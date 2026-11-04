package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/token"
	"github.com/hookshield-hq/hookshield/backend/internal/transform"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// ── secrets ───────────────────────────────────────────────

var secretNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

const maxSecretValue = 8 << 10

type secretJSON struct {
	Name      string    `json:"name"`
	Reference string    `json:"reference"` // what to paste in an outbound header
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toSecretJSON(i store.SecretInfo) secretJSON {
	return secretJSON{Name: i.Name, Reference: "{{secrets." + i.Name + "}}", CreatedAt: i.CreatedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC()}
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Store.ListUserSecrets(r.Context(), userFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]secretJSON, len(list))
	for i, it := range list {
		out[i] = toSecretJSON(it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": out})
}

// putSecret creates or replaces a secret. The value is encrypted and can never be read back.
func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !secretNameRE.MatchString(name) {
		s.writeErr(w, invalid("secret.invalid_name"))
		return
	}
	var in struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Value == "" || len(in.Value) > maxSecretValue || strings.ContainsAny(in.Value, "\r\n\x00") {
		s.writeErr(w, invalid("secret.invalid_value"))
		return
	}
	u := userFrom(r)
	ct, nonce, err := s.opts.Cipher.Seal([]byte(in.Value), store.SecretAAD(u.ID, name))
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.opts.Store.UpsertUserSecret(r.Context(), u.ID, store.UserSecret{
		Name: name, Encrypted: ct, Nonce: nonce, KeyVersion: s.opts.Cipher.KeyVersion()}); err != nil {
		s.fail(w, err)
		return
	}
	info, err := s.opts.Store.GetUserSecretInfo(r.Context(), u.ID, name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": toSecretJSON(info)})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.opts.Store.DeleteUserSecret(r.Context(), userFrom(r).ID, name)
	if errors.Is(err, store.ErrNotFound) || !secretNameRE.MatchString(name) {
		s.writeErr(w, &apiError{Status: http.StatusNotFound, Code: "secret.not_found"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── alert channels ────────────────────────────────────────

var (
	telegramTokenRE = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)
	telegramChatRE  = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
)

type alertChannelJSON struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	EndpointID *string        `json:"endpoint_id"` // null = every endpoint
	Enabled    bool           `json:"enabled"`
	Target     map[string]any `json:"target"` // masked: secrets inside URLs/tokens are never returned
	CreatedAt  time.Time      `json:"created_at"`
}

// channelTarget is the decrypted destination; one shape for every type.
type channelTarget struct {
	URL        string `json:"url,omitempty"`         // webhook
	WebhookURL string `json:"webhook_url,omitempty"` // slack
	BotToken   string `json:"bot_token,omitempty"`   // telegram
	ChatID     string `json:"chat_id,omitempty"`     // telegram
	To         string `json:"to,omitempty"`          // email ("" = the account email)
}

func (s *Server) validateTarget(typ string, t channelTarget, u store.User) (any, error) {
	switch typ {
	case store.ChannelWebhook:
		if err := s.checkURL(strings.TrimSpace(t.URL), "alert.invalid_target", "alert.private_target"); err != nil {
			return nil, err
		}
		return alert.WebhookTarget{URL: strings.TrimSpace(t.URL)}, nil
	case store.ChannelSlack:
		u, err := url.Parse(strings.TrimSpace(t.WebhookURL))
		if err != nil || u.Scheme != "https" || u.Host != "hooks.slack.com" || len(u.Path) < 10 {
			return nil, invalid("alert.invalid_target")
		}
		return alert.SlackTarget{WebhookURL: u.String()}, nil
	case store.ChannelTelegram:
		if !telegramTokenRE.MatchString(t.BotToken) || !telegramChatRE.MatchString(t.ChatID) {
			return nil, invalid("alert.invalid_target")
		}
		return alert.TelegramTarget{BotToken: t.BotToken, ChatID: t.ChatID}, nil
	case store.ChannelEmail:
		to := strings.TrimSpace(t.To)
		if _, err := auth.ValidEmail(to); to != "" && err != nil {
			return nil, invalid("alert.invalid_target")
		}
		// Unverified accounts could use alert emails to mail anyone from this
		// install's domain (or their "own" address, which may not be theirs).
		if u.EmailVerifiedAt == nil {
			return nil, &apiError{Status: http.StatusForbidden, Code: "alert.email_unverified"}
		}
		return alert.EmailTarget{To: to}, nil
	}
	return nil, invalid("alert.invalid_type")
}

func maskTarget(typ string, t channelTarget) map[string]any {
	tail := func(v string) string {
		if len(v) <= 4 {
			return "…"
		}
		return "…" + v[len(v)-4:]
	}
	switch typ {
	case store.ChannelWebhook:
		if u, err := url.Parse(t.URL); err == nil {
			return map[string]any{"url": u.Scheme + "://" + u.Host + "/…"}
		}
	case store.ChannelSlack:
		return map[string]any{"webhook_url": "https://hooks.slack.com/services/" + tail(t.WebhookURL)}
	case store.ChannelTelegram:
		bot := t.BotToken
		if i := strings.IndexByte(bot, ':'); i > 0 {
			bot = bot[:i] + ":" + tail(bot)
		}
		return map[string]any{"chat_id": t.ChatID, "bot_token": bot}
	case store.ChannelEmail:
		return map[string]any{"to": t.To}
	}
	return map[string]any{}
}

func (s *Server) toChannelJSON(c store.AlertChannelRecord) alertChannelJSON {
	var t channelTarget
	if plain, err := s.opts.Cipher.Open(c.TargetEncrypted, c.TargetNonce, store.AlertChannelAAD(c.ID)); err == nil {
		_ = json.Unmarshal(plain, &t)
	}
	return alertChannelJSON{ID: c.ID, Type: c.Type, EndpointID: c.EndpointID, Enabled: c.Enabled,
		Target: maskTarget(c.Type, t), CreatedAt: c.CreatedAt.UTC()}
}

var errChannelNotFound = &apiError{Status: http.StatusNotFound, Code: "alert.channel_not_found"}

// checkScope verifies that an endpoint_id belongs to the user.
func (s *Server) checkScope(r *http.Request, endpointID *string) error {
	if endpointID == nil {
		return nil
	}
	if !uuidRE.MatchString(*endpointID) {
		return invalid("alert.invalid_endpoint")
	}
	if _, err := s.opts.Store.GetEndpoint(r.Context(), userFrom(r).ID, *endpointID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return invalid("alert.invalid_endpoint")
		}
		return err
	}
	return nil
}

func (s *Server) sealTarget(c *store.AlertChannel, target any) error {
	plain, err := json.Marshal(target)
	if err != nil {
		return err
	}
	ct, nonce, err := s.opts.Cipher.Seal(plain, store.AlertChannelAAD(c.ID))
	if err != nil {
		return err
	}
	c.TargetEncrypted, c.TargetNonce, c.KeyVersion = ct, nonce, s.opts.Cipher.KeyVersion()
	return nil
}

func (s *Server) listAlertChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Store.ListAllAlertChannels(r.Context(), userFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]alertChannelJSON, len(list))
	for i, c := range list {
		out[i] = s.toChannelJSON(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert_channels": out})
}

func (s *Server) createAlertChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type       string        `json:"type"`
		EndpointID *string       `json:"endpoint_id"`
		Enabled    *bool         `json:"enabled"`
		Target     channelTarget `json:"target"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.checkScope(r, in.EndpointID)
	var target any
	if err == nil {
		target, err = s.validateTarget(in.Type, in.Target, userFrom(r))
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	c := store.AlertChannel{ID: uuid.NewV7(), EndpointID: in.EndpointID, Type: in.Type, Enabled: in.Enabled == nil || *in.Enabled}
	if err := s.sealTarget(&c, target); err != nil {
		s.fail(w, err)
		return
	}
	u := userFrom(r)
	if err := s.opts.Store.CreateAlertChannel(r.Context(), u.ID, c); err != nil {
		s.fail(w, err)
		return
	}
	saved, err := s.opts.Store.GetAlertChannel(r.Context(), u.ID, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"alert_channel": s.toChannelJSON(saved)})
}

func (s *Server) loadChannel(r *http.Request) (store.AlertChannelRecord, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return store.AlertChannelRecord{}, errChannelNotFound
	}
	c, err := s.opts.Store.GetAlertChannel(r.Context(), userFrom(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return c, errChannelNotFound
	}
	return c, err
}

func (s *Server) updateAlertChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EndpointID json.RawMessage `json:"endpoint_id"` // absent = keep, null = every endpoint
		Enabled    *bool           `json:"enabled"`
		Target     *channelTarget  `json:"target"` // full replacement
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.loadChannel(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if in.EndpointID != nil {
		var id *string
		if err := json.Unmarshal(in.EndpointID, &id); err != nil {
			s.writeErr(w, invalid("alert.invalid_endpoint"))
			return
		}
		if err := s.checkScope(r, id); err != nil {
			s.writeErr(w, err)
			return
		}
		c.EndpointID = id
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Target != nil {
		target, err := s.validateTarget(c.Type, *in.Target, userFrom(r))
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if err := s.sealTarget(&c.AlertChannel, target); err != nil {
			s.fail(w, err)
			return
		}
	}
	u := userFrom(r)
	if err := s.opts.Store.UpdateAlertChannel(r.Context(), u.ID, c.AlertChannel); err != nil {
		s.fail(w, err)
		return
	}
	saved, err := s.opts.Store.GetAlertChannel(r.Context(), u.ID, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert_channel": s.toChannelJSON(saved)})
}

func (s *Server) deleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadChannel(r)
	if err == nil {
		err = s.opts.Store.DeleteAlertChannel(r.Context(), userFrom(r).ID, c.ID)
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── API keys ──────────────────────────────────────────────

type apiKeyJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toAPIKeyJSON(k store.APIKey) apiKeyJSON {
	return apiKeyJSON{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scope: k.Scope, CreatedAt: k.CreatedAt.UTC(),
		LastUsedAt: utcPtr(k.LastUsedAt), ExpiresAt: utcPtr(k.ExpiresAt), RevokedAt: utcPtr(k.RevokedAt)}
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.opts.Store.ListAPIKeys(r.Context(), userFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]apiKeyJSON, len(keys))
	for i, k := range keys {
		out[i] = toAPIKeyJSON(k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": out})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 100 {
		s.writeErr(w, invalid("api_key.invalid_name"))
		return
	}
	switch in.Scope {
	case store.ScopeReadOnly, store.ScopeIngest, store.ScopeAdmin:
	default:
		s.writeErr(w, invalid("api_key.invalid_scope"))
		return
	}
	var expires *time.Time
	if in.ExpiresInDays != nil {
		if *in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650 {
			s.writeErr(w, invalid("api_key.invalid_expiry"))
			return
		}
		t := time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	key, err := token.Generate(token.PrefixLive)
	if err != nil {
		s.fail(w, err)
		return
	}
	k, err := s.opts.Store.CreateAPIKey(r.Context(), userFrom(r).ID, in.Name, token.DisplayPrefix(key), in.Scope, token.Hash(key), expires)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"api_key": toAPIKeyJSON(k), "key": key})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	notFound := &apiError{Status: http.StatusNotFound, Code: "api_key.not_found"}
	if !uuidRE.MatchString(id) {
		s.writeErr(w, notFound)
		return
	}
	_, err := s.opts.Store.RevokeAPIKey(r.Context(), userFrom(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeErr(w, notFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── transform preview ─────────────────────────────────────

// previewTransform runs rules over a sample request, without saving anything
// (live preview of the pipeline editor).
func (s *Server) previewTransform(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules   json.RawMessage `json:"rules"`
		Request struct {
			Headers  map[string]json.RawMessage `json:"headers"`   // "value" or ["v1","v2"]
			Body     json.RawMessage            `json:"body"`      // any JSON value
			BodyText *string                    `json:"body_text"` // or raw text
		} `json:"request"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Rules) == 0 || string(in.Rules) == "null" {
		in.Rules = json.RawMessage(`{}`)
	}
	rules, err := transform.Parse(in.Rules)
	if err != nil {
		s.writeErr(w, validateRules(in.Rules))
		return
	}
	headers := map[string][]string{}
	for k, raw := range in.Request.Headers {
		var one string
		var many []string
		switch {
		case json.Unmarshal(raw, &one) == nil:
			headers[k] = []string{one}
		case json.Unmarshal(raw, &many) == nil:
			headers[k] = many
		default:
			s.writeErr(w, invalid("transform.invalid_sample_headers"))
			return
		}
	}
	body := []byte(in.Request.Body)
	if in.Request.BodyText != nil {
		body = []byte(*in.Request.BodyText)
	}
	res := rules.Apply(transform.Message{Headers: headers, Body: body}, time.Now())
	if res.Err != nil {
		s.writeErr(w, invalid("transform.output_too_large"))
		return
	}
	out := map[string]any{"headers": res.Message.Headers}
	if json.Valid(res.Message.Body) {
		out["body"] = json.RawMessage(res.Message.Body)
	} else {
		out["body_text"] = string(res.Message.Body)
	}
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": out, "warnings": warnings})
}
