package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolvePublicURL(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{nil, "http://localhost:8000"},
		{map[string]string{"HS_PUBLIC_URL": "https://hooks.example.com/"}, "https://hooks.example.com"},
		{map[string]string{"RENDER_EXTERNAL_URL": "https://hookshield-x.onrender.com"}, "https://hookshield-x.onrender.com"},
		{map[string]string{"RAILWAY_PUBLIC_DOMAIN": "hookshield.up.railway.app"}, "https://hookshield.up.railway.app"},
		{map[string]string{"HS_PUBLIC_URL": "https://mine.example.com", "RENDER_EXTERNAL_URL": "https://x.onrender.com"}, "https://mine.example.com"},
	} {
		if got := resolvePublicURL(func(k string) string { return tc.env[k] }, "8000"); got != tc.want {
			t.Errorf("%v: got %s want %s", tc.env, got, tc.want)
		}
	}
}

func TestDefaultTrustedProxies(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := defaultTrustedProxies(env(nil)); got != loopbackProxies {
		t.Fatalf("plain docker run must trust loopback only, got %s", got)
	}
	for _, m := range []map[string]string{{"RENDER": "true"}, {"RAILWAY_ENVIRONMENT": "production"}} {
		if got := defaultTrustedProxies(env(m)); got != platformProxies {
			t.Fatalf("%v: got %s", m, got)
		}
	}
}

func TestServerRouting(t *testing.T) {
	upstream := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream", name)
			io.WriteString(w, name+" "+r.Method+" "+r.URL.RequestURI()+" xff="+r.Header.Get("X-Forwarded-For"))
		}))
	}
	api, ingest, worker, web := upstream("api"), upstream("ingest"), upstream("worker"), upstream("web")
	for _, s := range []*httptest.Server{api, ingest, worker, web} {
		defer s.Close()
	}
	h, err := newServerHandler(serverRoutes{API: api.URL, Ingest: ingest.URL, Worker: worker.URL, Web: web.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(h)
	defer front.Close()

	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/v1/me", "api GET /v1/me"},
		{"POST", "/v1/endpoints?x=1", "api POST /v1/endpoints?x=1"},
		{"POST", "/in/v1/ep_abc", "ingest POST /v1/ep_abc"},
		{"PUT", "/in/v1/ep%2Fodd", "ingest PUT /v1/ep%2Fodd"},
		{"GET", "/", "web GET /"},
		{"GET", "/pt-BR/logs", "web GET /pt-BR/logs"},
		{"GET", "/api/config", "web GET /api/config"},
		{"GET", "/inbox", "web GET /inbox"},
	} {
		req, _ := http.NewRequest(tc.method, front.URL+tc.path, strings.NewReader("{}"))
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.HasPrefix(string(body), tc.want+" ") {
			t.Errorf("%s %s → %q, want prefix %q", tc.method, tc.path, body, tc.want)
		}
		if !strings.Contains(string(body), "xff=203.0.113.7, 127.0.0.1") {
			t.Errorf("%s: X-Forwarded-For not set: %q", tc.path, body)
		}
	}

	res, err := http.Get(front.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("readyz with every part up = %d", res.StatusCode)
	}
	worker.Close()
	res, _ = http.Get(front.URL + "/readyz")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"worker":"down"`) {
		t.Fatalf("readyz with the worker down = %d %s", res.StatusCode, body)
	}

	api.Close()
	res, _ = http.Get(front.URL + "/v1/me")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "server.starting") {
		t.Fatalf("api down = %d %s", res.StatusCode, body)
	}
}
