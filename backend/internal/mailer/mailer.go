// Package mailer sends plain-text emails (alerts, account emails).
//
// Production uses Resend over HTTPS (HS_RESEND_API_KEY, see resend.go).
// Without a Resend key, SMTP is used, configured with a URL:
//
//	smtp://user:pass@host:587   STARTTLS when offered (most providers)
//	smtps://user:pass@host:465  implicit TLS
//
// In development both local stacks run Mailpit (smtp://localhost:1025,
// web UI on http://localhost:8025), which catches every message.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Mailer sends plain-text emails.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPMailer sends through an SMTP server configured with a URL (see package doc).
type SMTPMailer struct {
	addr      string
	host      string
	auth      smtp.Auth
	implicit  bool
	from      string
	fromAddr  string
	TLSConfig *tls.Config // optional (tests)
}

// NewSMTP parses an SMTP URL. from is the RFC 5322 sender, e.g. `HookShield <no-reply@example.com>`.
func NewSMTP(rawURL, from string) (*SMTPMailer, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
		return nil, fmt.Errorf("HS_SMTP_URL must look like smtp://user:pass@host:587 or smtps://user:pass@host:465")
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("HS_EMAIL_FROM is not a valid address: %w", err)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"smtp": "587", "smtps": "465"}[u.Scheme]
	}
	m := &SMTPMailer{
		addr: net.JoinHostPort(u.Hostname(), port), host: u.Hostname(),
		implicit: u.Scheme == "smtps", from: addr.String(), fromAddr: addr.Address,
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		m.auth = smtp.PlainAuth("", u.User.Username(), pass, u.Hostname())
	}
	return m, nil
}

// Send implements Mailer.
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("invalid header value")
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsCfg := m.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	}
	var conn net.Conn
	var err error
	if m.implicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", m.addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", m.addr)
	}
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if !m.implicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return err
			}
		}
	}
	if m.auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(m.auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(m.fromAddr); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + m.from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
