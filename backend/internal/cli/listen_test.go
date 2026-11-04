package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI is the part of the control API the listener uses.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	logs     []fakeLog // newest last
	events   chan string
	feeds    int // live feed connections so far
	dropFeed chan struct{}
	failOnce map[string]bool // GET /v1/logs/{id} answers 503 once for these
	srv      *httptest.Server
}

type fakeLog struct {
	id, endpoint, event string
	at                  time.Time
	original            storedRequest
	transformed         *storedRequest
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, events: make(chan string, 16), dropFeed: make(chan struct{}, 1), failOnce: map[string]bool{}}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer hs_live_test" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":{"code":"auth.unauthorized"}}`)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/logs", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var from time.Time
		if v := r.URL.Query().Get("from"); v != "" {
			from, _ = time.Parse(time.RFC3339Nano, v)
		}
		out := []map[string]any{}
		for i := len(f.logs) - 1; i >= 0; i-- {
			lg := f.logs[i]
			if lg.at.Before(from) || (r.URL.Query().Get("endpoint_id") != "" && r.URL.Query().Get("endpoint_id") != lg.endpoint) {
				continue
			}
			out = append(out, f.summary(lg))
		}
		if v := r.URL.Query().Get("limit"); v == "1" && len(out) > 1 {
			out = out[:1]
		}
		json.NewEncoder(w).Encode(map[string]any{"logs": out})
	}))
	mux.HandleFunc("GET /v1/logs/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("raw") != "true" {
			t.Errorf("listener must ask for raw bodies")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failOnce[r.PathValue("id")] {
			delete(f.failOnce, r.PathValue("id"))
			w.WriteHeader(503)
			return
		}
		for _, lg := range f.logs {
			if lg.id == r.PathValue("id") {
				json.NewEncoder(w).Encode(map[string]any{"log": f.summary(lg), "request": map[string]any{"original": lg.original, "transformed": lg.transformed}})
				return
			}
		}
		w.WriteHeader(404)
	}))
	mux.HandleFunc("GET /v1/endpoints/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"endpoint": map[string]any{
			"id": r.PathValue("id"), "slug": "ep_test", "name": "Test",
			"outbound_headers": map[string]string{"X-Static": "yes", "Authorization": "Bearer {{secrets.API_TOKEN}}"},
		}})
	}))
	mux.HandleFunc("GET /v1/live", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.feeds++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "retry: 3000\n: connected\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-f.dropFeed:
				return
			case id := <-f.events:
				fmt.Fprintf(w, "event: log.created\nid: %s\ndata: {\"type\":\"log.created\",\"log_id\":%q}\n\n", id, id)
				w.(http.Flusher).Flush()
			}
		}
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) summary(lg fakeLog) map[string]any {
	return map[string]any{"id": lg.id, "endpoint": map[string]any{"id": lg.endpoint, "slug": "ep_test", "name": "Test"},
		"event_type": lg.event, "method": lg.original.Method, "received_at": lg.at}
}

func (f *fakeAPI) add(lg fakeLog) {
	f.mu.Lock()
	f.logs = append(f.logs, lg)
	f.mu.Unlock()
}

func (f *fakeAPI) feedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.feeds
}

type received struct {
	method, uri string
	header      http.Header
	body        []byte
}

func localApp(t *testing.T, status int) (*httptest.Server, chan received) {
	ch := make(chan received, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- received{r.Method, r.URL.RequestURI(), r.Header.Clone(), b}
		w.WriteHeader(status)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func req(method, query, body string, headers map[string][]string) storedRequest {
	return storedRequest{Method: method, Query: query, Headers: headers, BodyBase64: base64.StdEncoding.EncodeToString([]byte(body))}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func next(t *testing.T, ch chan received) received {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the local app received nothing")
		return received{}
	}
}

func startListener(t *testing.T, api *fakeAPI, forward string, configure func(*Listener)) (*syncBuf, context.CancelFunc, chan error) {
	t.Helper()
	target, err := ParseForward(forward)
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	l := &Listener{
		Client:     &Client{BaseURL: api.srv.URL, APIKey: "hs_live_test", HTTP: http.DefaultClient},
		Endpoint:   &Endpoint{ID: "ep-1", Slug: "ep_test"},
		Forward:    target,
		Out:        out,
		RetryDelay: func(int) time.Duration { return 20 * time.Millisecond },
	}
	if configure != nil {
		configure(l)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return out, cancel, done
}

func TestListenForwardsExactRequest(t *testing.T) {
	api := newFakeAPI(t)
	old := time.Now().Add(-time.Hour)
	api.add(fakeLog{id: "log-old", endpoint: "ep-1", at: old, original: req("POST", "", "{}", nil)}) // before listen: not forwarded
	app, got := localApp(t, 200)
	out, _, _ := startListener(t, api, app.URL+"/hooks?src=cli", nil)
	waitFor(t, "live feed", func() bool { return api.feedCount() == 1 })

	body := "{\n  \"type\": \"invoice.paid\",\n  \"amount\": 10\n}" // whitespace kept: signatures over the body still verify
	api.add(fakeLog{id: "log-1", endpoint: "ep-1", event: "invoice.paid", at: time.Now(),
		original: req("POST", "a=1", `{"raw":true}`, nil),
		transformed: ptr(req("PUT", "a=1", body, map[string][]string{
			"Content-Type": {"application/json"}, "Stripe-Signature": {"t=1,v1=abc"}, "X-Hookshield-Id": {"spoofed"},
			"Content-Length": {"999"}, "Host": {"evil.example"},
		}))})
	api.events <- "log-1"

	r := next(t, got)
	if r.method != "PUT" || r.uri != "/hooks?src=cli&a=1" || string(r.body) != body {
		t.Fatalf("request: %s %s %q", r.method, r.uri, r.body)
	}
	h := r.header
	if h.Get("Stripe-Signature") != "t=1,v1=abc" || h.Get("Content-Type") != "application/json" || h.Get("X-Static") != "yes" ||
		h.Get("Authorization") != "" || h.Get("X-Hookshield-Id") != "log-1" || h.Get("X-Hookshield-Event") != "invoice.paid" ||
		h.Get("X-Hookshield-Forwarded-By") != "hookshield-cli" || !strings.HasPrefix(h.Get("User-Agent"), "HookShield-CLI/") {
		t.Fatalf("headers: %v", h)
	}
	waitFor(t, "output", func() bool { return strings.Contains(out.String(), "<-- 200 OK") })
	o := out.String()
	if !strings.Contains(o, "--> invoice.paid") || !strings.Contains(o, "Authorization of ep_test uses a secret") || strings.Contains(o, "log-old") {
		t.Fatalf("output:\n%s", o)
	}

	// The same log announced again is not forwarded twice.
	api.events <- "log-1"
	select {
	case r := <-got:
		t.Fatalf("forwarded twice: %+v", r)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestListenOriginalAndCatchUpAfterReconnect(t *testing.T) {
	api := newFakeAPI(t)
	app, got := localApp(t, 500)
	out, _, _ := startListener(t, api, app.URL, func(l *Listener) { l.Original = true; l.PrintBody = true })
	waitFor(t, "live feed", func() bool { return api.feedCount() == 1 })

	// The feed drops; two webhooks arrive while the listener is disconnected.
	api.dropFeed <- struct{}{}
	base := time.Now()
	for i, id := range []string{"log-a", "log-b"} {
		api.add(fakeLog{id: id, endpoint: "ep-1", at: base.Add(time.Duration(i) * time.Millisecond),
			original:    req("POST", "", fmt.Sprintf(`{"n":%d}`, i), nil),
			transformed: ptr(req("POST", "", `{"transformed":true}`, nil))})
	}
	for i := range 2 {
		r := next(t, got)
		if want := fmt.Sprintf(`{"n":%d}`, i); string(r.body) != want {
			t.Fatalf("catch-up #%d: got %s, want %s (original, in order)", i, r.body, want)
		}
	}
	waitFor(t, "reconnect", func() bool { return api.feedCount() >= 2 })
	waitFor(t, "output", func() bool { return strings.Contains(out.String(), "500 Internal Server Error") })
	o := out.String()
	if !strings.Contains(o, "connection lost") || !strings.Contains(o, "reconnected") || !strings.Contains(o, `{"ok":true}`) {
		t.Fatalf("output:\n%s", o)
	}
}

func TestListenRetriesAWebhookItCouldNotRead(t *testing.T) {
	api := newFakeAPI(t)
	app, got := localApp(t, 200)
	out, _, _ := startListener(t, api, app.URL, nil)
	waitFor(t, "live feed", func() bool { return api.feedCount() == 1 })

	api.mu.Lock()
	api.failOnce["log-flaky"] = true
	api.mu.Unlock()
	api.add(fakeLog{id: "log-flaky", endpoint: "ep-1", at: time.Now(), original: req("POST", "", `{"flaky":true}`, nil)})
	api.events <- "log-flaky"
	waitFor(t, "read error", func() bool { return strings.Contains(out.String(), "could not read webhook log-flaky") })

	api.dropFeed <- struct{}{} // the next connection catches up
	if r := next(t, got); string(r.body) != `{"flaky":true}` {
		t.Fatalf("retried webhook: %s", r.body)
	}
}

func TestListenReportsLocalAppDown(t *testing.T) {
	api := newFakeAPI(t)
	app, _ := localApp(t, 200)
	appURL := app.URL
	app.Close() // nothing listens there anymore
	out, _, _ := startListener(t, api, appURL, nil)
	waitFor(t, "live feed", func() bool { return api.feedCount() == 1 })
	api.add(fakeLog{id: "log-x", endpoint: "ep-1", at: time.Now(), original: req("POST", "", "{}", nil)})
	api.events <- "log-x"
	u, _ := url.Parse(appURL)
	waitFor(t, "error line", func() bool {
		return strings.Contains(out.String(), "connection refused — is your app running on "+u.Host)
	})
}

func TestListenRejectsInvalidKey(t *testing.T) {
	api := newFakeAPI(t)
	target, _ := ParseForward("3000")
	l := &Listener{Client: &Client{BaseURL: api.srv.URL, APIKey: "hs_live_wrong", HTTP: http.DefaultClient}, Forward: target, Out: io.Discard}
	err := l.Run(context.Background())
	if !IsUnauthorized(err) || !strings.Contains(err.Error(), "hookshield login") {
		t.Fatalf("want unauthorized, got %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
