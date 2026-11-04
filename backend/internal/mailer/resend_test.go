package mailer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type resendStub struct {
	mu       sync.Mutex
	statuses []int // response status per call; last one repeats
	calls    []*http.Request
	bodies   []resendRequest
}

func (s *resendStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body resendRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.calls = append(s.calls, r)
	s.bodies = append(s.bodies, body)
	st := s.statuses[min(len(s.calls)-1, len(s.statuses)-1)]
	w.WriteHeader(st)
	if st >= 300 {
		_, _ = w.Write([]byte(`{"statusCode":422,"name":"validation_error","message":"The from domain is not verified"}`))
		return
	}
	_, _ = w.Write([]byte(`{"id":"49a3999c-0ce1-4ea6-ab68-afcd6dc2e794"}`))
}

func newResendTest(t *testing.T, statuses ...int) (*ResendMailer, *resendStub) {
	t.Helper()
	stub := &resendStub{statuses: statuses}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	m, err := NewResend("re_test_123", srv.URL, "HookShield <no-reply@hookshield.dev>")
	if err != nil {
		t.Fatal(err)
	}
	m.RetryDelay = time.Millisecond
	return m, stub
}

func TestResendSend(t *testing.T) {
	m, stub := newResendTest(t, 200)
	if err := m.Send(context.Background(), "ana@example.com", "Olá, entrega abandonada", "corpo\nlinha 2"); err != nil {
		t.Fatal(err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("calls = %d", len(stub.calls))
	}
	r, b := stub.calls[0], stub.bodies[0]
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer re_test_123" ||
		r.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(r.Header.Get("Idempotency-Key"), "hs-") {
		t.Fatalf("request headers: %v", r.Header)
	}
	if b.From != `"HookShield" <no-reply@hookshield.dev>` || len(b.To) != 1 || b.To[0] != "ana@example.com" ||
		b.Subject != "Olá, entrega abandonada" || b.Text != "corpo\nlinha 2" {
		t.Fatalf("body: %+v", b)
	}
}

func TestResendRetriesOnceWithSameIdempotencyKey(t *testing.T) {
	m, stub := newResendTest(t, 503, 200)
	if err := m.Send(context.Background(), "ana@example.com", "s", "b"); err != nil {
		t.Fatal(err)
	}
	if len(stub.calls) != 2 || stub.calls[0].Header.Get("Idempotency-Key") != stub.calls[1].Header.Get("Idempotency-Key") {
		t.Fatalf("retry: %d calls", len(stub.calls))
	}

	m, stub = newResendTest(t, 429)
	if err := m.Send(context.Background(), "ana@example.com", "s", "b"); err == nil || len(stub.calls) != 2 {
		t.Fatalf("persistent 429: err=%v calls=%d", err, len(stub.calls))
	}
}

func TestResendClientErrorIsNotRetriedAndHidesKey(t *testing.T) {
	m, stub := newResendTest(t, 422)
	err := m.Send(context.Background(), "ana@example.com", "s", "b")
	if err == nil || len(stub.calls) != 1 {
		t.Fatalf("err=%v calls=%d", err, len(stub.calls))
	}
	if !strings.Contains(err.Error(), "validation_error: The from domain is not verified") || strings.Contains(err.Error(), "re_test_123") {
		t.Fatalf("error message: %v", err)
	}
}

func TestResendValidation(t *testing.T) {
	if _, err := NewResend("sk_live_x", "", "a@b.com"); err == nil {
		t.Fatal("non-Resend key accepted")
	}
	if _, err := NewResend("re_x", "", "not an address"); err == nil {
		t.Fatal("bad from accepted")
	}
	m, stub := newResendTest(t, 200)
	for _, to := range []string{"nope", "a@b.com\r\nBcc: x@y.com"} {
		if err := m.Send(context.Background(), to, "s", "b"); err == nil {
			t.Errorf("recipient %q accepted", to)
		}
	}
	if err := m.Send(context.Background(), "a@b.com", "s\r\nBcc: x@y.com", "b"); err == nil {
		t.Error("subject injection accepted")
	}
	if len(stub.calls) != 0 {
		t.Fatal("invalid messages reached the API")
	}
}

func TestFromConfig(t *testing.T) {
	from := "HookShield <no-reply@hookshield.dev>"
	if m, kind, err := FromConfig("re_x", "", "smtp://localhost:1025", from); err != nil || kind != "resend" || m == nil {
		t.Fatalf("resend wins: %v %q", err, kind)
	}
	if m, kind, err := FromConfig("", "", "smtp://localhost:1025", from); err != nil || kind != "smtp" || m == nil {
		t.Fatalf("smtp: %v %q", err, kind)
	}
	if m, kind, err := FromConfig("", "", "", from); err != nil || kind != "" || m != nil {
		t.Fatalf("disabled: %v %q", err, kind)
	}
	if _, _, err := FromConfig("bad", "", "", from); err == nil {
		t.Fatal("bad key accepted")
	}
}
