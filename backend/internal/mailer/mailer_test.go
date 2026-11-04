package mailer

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewSMTP(t *testing.T) {
	m, err := NewSMTP("smtp://user:pa%24s@mail.example.com", `HookShield <no-reply@hookshield.dev>`)
	if err != nil || m.addr != "mail.example.com:587" || m.implicit || m.auth == nil || m.fromAddr != "no-reply@hookshield.dev" {
		t.Fatalf("%+v %v", m, err)
	}
	m, err = NewSMTP("smtps://mail.example.com", "a@b.co")
	if err != nil || m.addr != "mail.example.com:465" || !m.implicit || m.auth != nil {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{"http://x", "smtp://", "::"} {
		if _, err := NewSMTP(bad, "a@b.co"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := NewSMTP("smtp://h", "not an address"); err == nil {
		t.Error("invalid from accepted")
	}
}

// fakeSMTP is a minimal SMTP server (no TLS, no AUTH) recording one message.
type fakeSMTP struct {
	addr string
	mu   sync.Mutex
	from string
	to   []string
	data string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{addr: l.Addr().String()}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250-fake")
			w("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.TrimSpace(line[10:])
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.to = append(f.to, strings.TrimSpace(line[8:]))
			f.mu.Unlock()
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPMailerSends(t *testing.T) {
	srv := startFakeSMTP(t)
	m, err := NewSMTP("smtp://"+srv.addr, `HookShield <no-reply@hookshield.dev>`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Send(ctx, "ops@example.com", "❌ Entrega abandonada — Stripe", "linha 1\nlinha 2"); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.HasPrefix(srv.from, "<no-reply@hookshield.dev>") || len(srv.to) != 1 || srv.to[0] != "<ops@example.com>" {
		t.Fatalf("envelope from=%q to=%v", srv.from, srv.to)
	}
	for _, want := range []string{"From: \"HookShield\" <no-reply@hookshield.dev>", "To: ops@example.com", "Subject: =?utf-8?q?", "charset=utf-8", "linha 1\r\nlinha 2"} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message missing %q:\n%s", want, srv.data)
		}
	}

	if err := m.Send(ctx, "bad\r\nBcc: x@y.z", "s", "b"); err == nil {
		t.Error("header injection in recipient accepted")
	}
}
