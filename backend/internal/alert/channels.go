package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Targets are stored encrypted as JSON in alert_channels.target_encrypted.

// WebhookTarget posts a JSON alert to any URL.
type WebhookTarget struct {
	URL string `json:"url"`
}

// SlackTarget posts to a Slack incoming webhook.
type SlackTarget struct {
	WebhookURL string `json:"webhook_url"`
}

// TelegramTarget sends a message through a Telegram bot.
type TelegramTarget struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// EmailTarget sends an email; an empty To means the account's email.
type EmailTarget struct {
	To string `json:"to,omitempty"`
}

// Payload is the JSON body sent to webhook channels (stable, documented in docs/ALERTS.md).
type Payload struct {
	Type       string          `json:"type"` // always "hookshield.alert"
	Final      bool            `json:"final"`
	Endpoint   PayloadEndpoint `json:"endpoint"`
	WebhookID  string          `json:"webhook_id"`
	EventType  string          `json:"event_type,omitempty"`
	Attempts   int             `json:"attempts"`
	LastStatus int             `json:"last_status,omitempty"`
	LastError  string          `json:"last_error,omitempty"`
	Suppressed int             `json:"suppressed"`
	At         time.Time       `json:"at"`
	URL        string          `json:"url,omitempty"`
	Message    string          `json:"message"` // localized title
}

// PayloadEndpoint identifies the endpoint in a webhook alert.
type PayloadEndpoint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func postJSON(ctx context.Context, client *http.Client, target string, body any, headers map[string]string) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error embeds the full URL, which for Slack and Telegram contains
		// the secret (webhook path, bot token). Never let it reach the logs.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("%s request failed: %w", ue.Op, ue.Err)
		}
		return errors.New("request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("alert channel answered HTTP %d", resp.StatusCode)
	}
	return nil
}
