package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// DefaultResendURL is the Resend email API endpoint.
const DefaultResendURL = "https://api.resend.com/emails"

// ResendMailer sends through the Resend HTTP API (https://resend.com/docs).
// It is the production mailer: HTTPS works on hosts that block outbound SMTP ports.
type ResendMailer struct {
	apiKey string
	url    string
	from   string
	client *http.Client
	// RetryDelay is the pause before the single retry on 429/5xx (default 1s).
	RetryDelay time.Duration
}

// NewResend builds a Resend mailer. apiURL may be empty (DefaultResendURL).
func NewResend(apiKey, apiURL, from string) (*ResendMailer, error) {
	if !strings.HasPrefix(apiKey, "re_") {
		return nil, errors.New("HS_RESEND_API_KEY must be a Resend API key (starts with re_)")
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("HS_EMAIL_FROM is not a valid address: %w", err)
	}
	if apiURL == "" {
		apiURL = DefaultResendURL
	}
	return &ResendMailer{apiKey: apiKey, url: apiURL, from: addr.String(),
		client: &http.Client{Timeout: 15 * time.Second}, RetryDelay: time.Second}, nil
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// Send implements Mailer. 429 and 5xx responses are retried once.
func (m *ResendMailer) Send(ctx context.Context, to, subject, body string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("invalid header value")
	}
	payload, err := json.Marshal(resendRequest{From: m.from, To: []string{to}, Subject: subject, Text: body})
	if err != nil {
		return err
	}
	// Same key on the retry, so Resend never sends the message twice.
	idem := "hs-" + uuid.NewV7()

	for attempt := 0; ; attempt++ {
		status, msg, err := m.post(ctx, payload, idem)
		if err == nil && status >= 200 && status < 300 {
			return nil
		}
		retryable := err != nil || status == http.StatusTooManyRequests || status >= 500
		if attempt == 0 && retryable {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(m.RetryDelay):
			}
			continue
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("resend: HTTP %d: %s", status, msg)
	}
}

func (m *ResendMailer) post(ctx context.Context, payload []byte, idem string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.url, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idem)
	res, err := m.client.Do(req)
	if err != nil {
		var uerr interface{ Unwrap() error }
		if errors.As(err, &uerr) && uerr.Unwrap() != nil {
			err = uerr.Unwrap() // drop the URL wrapper; keep the cause
		}
		return 0, "", fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var e struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		msg = e.Message
		if e.Name != "" {
			msg = e.Name + ": " + e.Message
		}
	}
	return res.StatusCode, msg, nil
}

// FromConfig picks the mailer for the environment: Resend when resendKey is
// set, SMTP when smtpURL is set, nil (emails disabled) otherwise.
// kind is "resend", "smtp" or "" for logging.
func FromConfig(resendKey, resendURL, smtpURL, from string) (m Mailer, kind string, err error) {
	switch {
	case resendKey != "":
		r, err := NewResend(resendKey, resendURL, from)
		if err != nil {
			return nil, "", err
		}
		return r, "resend", nil
	case smtpURL != "":
		s, err := NewSMTP(smtpURL, from)
		if err != nil {
			return nil, "", err
		}
		return s, "smtp", nil
	default:
		return nil, "", nil
	}
}
