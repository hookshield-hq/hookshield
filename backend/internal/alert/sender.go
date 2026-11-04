package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/mailer"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

// Group is the consumer group name on the alert stream.
const Group = "alerts"

// Store is what the sender reads (implemented by *store.Store).
type Store interface {
	GetAlertContext(ctx context.Context, endpointID string) (store.AlertContext, error)
	ListAlertChannels(ctx context.Context, userID, endpointID string) ([]store.AlertChannel, error)
}

// Source is the alert stream consumer.
type Source interface {
	EnsureGroup(ctx context.Context) error
	Read(ctx context.Context, count int64, block time.Duration) ([]redis.XMessage, error)
	Claim(ctx context.Context, minIdle time.Duration, count int64) ([]redis.XMessage, error)
	Done(ctx context.Context, ids ...string) error
}

// SenderOptions configure a Sender.
type SenderOptions struct {
	AppURL      string          // dashboard base URL for "open in HookShield" links
	TelegramAPI string          // default https://api.telegram.org
	Mailer      mailer.Mailer   // nil = email channels are skipped (logged)
	RetryDelays []time.Duration // waits between attempts per channel (default 1s, 5s → 3 attempts)
	Block       time.Duration   // default 2s
	Logger      *slog.Logger
	// AllowEmail is asked before each alert email of an account (rate limit);
	// nil = no limit.
	AllowEmail func(ctx context.Context, userID string) bool
}

// Sender delivers queued alerts to every channel that applies.
type Sender struct {
	src    Source
	store  Store
	cipher *crypto.Cipher
	client *http.Client // SSRF-protected (outbound.NewClient)
	opts   SenderOptions
}

// NewSender builds a Sender.
func NewSender(src Source, st Store, cipher *crypto.Cipher, client *http.Client, opts SenderOptions) *Sender {
	if opts.TelegramAPI == "" {
		opts.TelegramAPI = "https://api.telegram.org"
	}
	if opts.RetryDelays == nil {
		opts.RetryDelays = []time.Duration{time.Second, 5 * time.Second}
	}
	if opts.Block <= 0 {
		opts.Block = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	opts.AppURL = strings.TrimRight(opts.AppURL, "/")
	return &Sender{src: src, store: st, cipher: cipher, client: client, opts: opts}
}

// Run sends alerts until ctx is cancelled.
func (s *Sender) Run(ctx context.Context) error {
	if err := s.src.EnsureGroup(ctx); err != nil {
		return err
	}
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) > time.Minute {
			lastClaim = time.Now()
			if msgs, err := s.src.Claim(ctx, 5*time.Minute, 16); err == nil {
				s.handleAll(ctx, msgs)
			}
		}
		if _, err := s.SendBatch(ctx); err != nil && ctx.Err() == nil {
			s.opts.Logger.Warn("alert batch failed", "err", err)
			time.Sleep(time.Second)
		}
	}
	return nil
}

// SendBatch handles one batch of queued alerts and returns how many were processed.
func (s *Sender) SendBatch(ctx context.Context) (int, error) {
	msgs, err := s.src.Read(ctx, 16, s.opts.Block)
	if err != nil {
		return 0, err
	}
	return s.handleAll(ctx, msgs)
}

func (s *Sender) handleAll(ctx context.Context, msgs []redis.XMessage) (int, error) {
	var done []string
	for _, m := range msgs {
		if s.handle(ctx, m) {
			done = append(done, m.ID)
		}
	}
	return len(done), s.src.Done(ctx, done...)
}

// ChannelResult reports one channel delivery (for logs and tests).
type ChannelResult struct {
	Type string
	Err  error
}

// handle sends one alert; it returns false only on transient store errors.
func (s *Sender) handle(ctx context.Context, m redis.XMessage) bool {
	a, err := decode(m.Values)
	if err != nil {
		s.opts.Logger.Error("dropping malformed alert", "entry", m.ID)
		return true
	}
	results, err := s.Deliver(ctx, a)
	if err != nil {
		s.opts.Logger.Warn("alert context unavailable; will retry", "err", err, "endpoint_id", a.EndpointID)
		return false
	}
	for _, r := range results {
		if r.Err != nil {
			s.opts.Logger.Warn("alert channel failed", "type", r.Type, "endpoint_id", a.EndpointID, "err", r.Err)
		}
	}
	return true
}

// Deliver sends a to every applicable channel. The error is non-nil only when
// the endpoint context cannot be loaded (deleted endpoints yield no error and no results).
func (s *Sender) Deliver(ctx context.Context, a Alert) ([]ChannelResult, error) {
	ac, err := s.store.GetAlertContext(ctx, a.EndpointID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	channels, err := s.store.ListAlertChannels(ctx, ac.UserID, a.EndpointID)
	if err != nil {
		return nil, err
	}

	logURL := ""
	if s.opts.AppURL != "" && a.LogID != "" {
		logURL = s.opts.AppURL + "/logs/" + a.LogID
	}
	msg := Render(a, Context{EndpointName: ac.EndpointName, EndpointSlug: ac.EndpointSlug}, ac.UserLocale, logURL)
	payload := Payload{
		Type: "hookshield.alert", Final: a.Final,
		Endpoint:  PayloadEndpoint{ID: ac.EndpointID, Name: ac.EndpointName, Slug: ac.EndpointSlug},
		WebhookID: a.LogID, EventType: a.EventType, Attempts: a.Attempts, LastStatus: a.LastStatus,
		LastError: a.LastError, Suppressed: a.Suppressed, At: a.At, URL: logURL, Message: msg.Title,
	}

	var results []ChannelResult
	send := func(typ string, fn func() error) {
		results = append(results, ChannelResult{Type: typ, Err: s.withRetries(ctx, fn)})
	}

	if ac.AlertWebhookURL != nil && *ac.AlertWebhookURL != "" {
		u := *ac.AlertWebhookURL
		send(store.ChannelWebhook, func() error { return s.postWebhook(ctx, u, payload) })
	}
	for _, ch := range channels {
		raw, err := s.openTarget(ch)
		if err != nil {
			results = append(results, ChannelResult{Type: ch.Type, Err: err})
			continue
		}
		switch ch.Type {
		case store.ChannelWebhook:
			var t WebhookTarget
			if err := json.Unmarshal(raw, &t); err != nil || t.URL == "" {
				results = append(results, ChannelResult{Type: ch.Type, Err: errors.New("invalid webhook target")})
				continue
			}
			send(ch.Type, func() error { return s.postWebhook(ctx, t.URL, payload) })
		case store.ChannelSlack:
			var t SlackTarget
			if err := json.Unmarshal(raw, &t); err != nil || t.WebhookURL == "" {
				results = append(results, ChannelResult{Type: ch.Type, Err: errors.New("invalid slack target")})
				continue
			}
			body := map[string]string{"text": "*" + slackEscape(msg.Title) + "*\n" + slackEscape(strings.Join(msg.Lines, "\n"))}
			send(ch.Type, func() error { return postJSON(ctx, s.client, t.WebhookURL, body, nil) })
		case store.ChannelTelegram:
			var t TelegramTarget
			if err := json.Unmarshal(raw, &t); err != nil || t.BotToken == "" || t.ChatID == "" {
				results = append(results, ChannelResult{Type: ch.Type, Err: errors.New("invalid telegram target")})
				continue
			}
			endpoint := s.opts.TelegramAPI + "/bot" + t.BotToken + "/sendMessage"
			body := map[string]any{"chat_id": t.ChatID, "text": msg.Text(), "disable_web_page_preview": true}
			send(ch.Type, func() error { return postJSON(ctx, s.client, endpoint, body, nil) })
		case store.ChannelEmail:
			var t EmailTarget
			_ = json.Unmarshal(raw, &t)
			to := t.To
			if to == "" {
				to = ac.UserEmail
			}
			if s.opts.Mailer == nil {
				results = append(results, ChannelResult{Type: ch.Type, Err: errors.New("email not configured (HS_SMTP_URL)")})
				continue
			}
			if s.opts.AllowEmail != nil && !s.opts.AllowEmail(ctx, ac.UserID) {
				results = append(results, ChannelResult{Type: ch.Type, Err: errors.New("hourly alert email limit of the account reached")})
				continue
			}
			send(ch.Type, func() error { return s.opts.Mailer.Send(ctx, to, msg.Title, msg.Text()) })
		default:
			results = append(results, ChannelResult{Type: ch.Type, Err: fmt.Errorf("unknown channel type %q", ch.Type)})
		}
	}
	return results, nil
}

// slackEscape keeps text from senders (event types, endpoint names) from
// turning into Slack links or mentions (<https://evil|click>, <!channel>).
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (s *Sender) openTarget(ch store.AlertChannel) ([]byte, error) {
	if ch.KeyVersion != s.cipher.KeyVersion() {
		return nil, fmt.Errorf("unknown key version %d", ch.KeyVersion)
	}
	return s.cipher.Open(ch.TargetEncrypted, ch.TargetNonce, store.AlertChannelAAD(ch.ID))
}

func (s *Sender) postWebhook(ctx context.Context, url string, p Payload) error {
	return postJSON(ctx, s.client, url, p, map[string]string{
		"User-Agent":         "HookShield/" + version.Version,
		"X-HookShield-Alert": "1",
	})
}

func (s *Sender) withRetries(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; ; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i >= len(s.opts.RetryDelays) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.opts.RetryDelays[i]):
		}
	}
}

// EncryptTarget seals a channel target for storage (helper for the API and tests).
func EncryptTarget(c *crypto.Cipher, channelID string, target any) (ct, nonce []byte, err error) {
	b, err := json.Marshal(target)
	if err != nil {
		return nil, nil, err
	}
	return c.Seal(b, store.AlertChannelAAD(channelID))
}
