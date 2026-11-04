package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestHealthz(t *testing.T) {
	mux := http.NewServeMux()
	Health(mux, "ingest", nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["service"] != "ingest" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		name   string
		checks map[string]ReadyCheck
		want   int
	}{
		{"all up", map[string]ReadyCheck{"db": func(context.Context) error { return nil }}, http.StatusOK},
		{"one down", map[string]ReadyCheck{
			"db":    func(context.Context) error { return nil },
			"redis": func(context.Context) error { return errors.New("boom") },
		}, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			Health(mux, "api", tc.checks)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestErrorUsesCode(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusNotFound, "endpoint.not_found")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || body.Error.Code != "endpoint.not_found" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct{ peer, xff, want string }{
		{"203.0.113.1:1234", "", "203.0.113.1"},                      // direct client
		{"203.0.113.1:1234", "198.51.100.9", "203.0.113.1"},          // untrusted peer: XFF ignored
		{"127.0.0.1:1234", "198.51.100.9", "198.51.100.9"},           // behind a trusted proxy
		{"127.0.0.1:1234", "6.6.6.6, 198.51.100.9", "198.51.100.9"},  // spoofed first hop is ignored
		{"127.0.0.1:1234", "198.51.100.9, 10.1.2.3", "198.51.100.9"}, // two trusted proxies
		{"127.0.0.1:1234", "10.9.9.9", "10.9.9.9"},                   // client inside the trusted network
		{"127.0.0.1:1234", "", "127.0.0.1"},                          // no header
		{"127.0.0.1:1234", "garbage, 10.1.2.3", "10.1.2.3"},          // unparsable hop stops the walk
		{"[::ffff:127.0.0.1]:1234", "198.51.100.9", "198.51.100.9"},  // IPv4-mapped peer
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.peer
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := ClientIP(r, trusted).String(); got != tc.want {
			t.Errorf("peer %s xff %q: got %s, want %s", tc.peer, tc.xff, got, tc.want)
		}
	}
}
