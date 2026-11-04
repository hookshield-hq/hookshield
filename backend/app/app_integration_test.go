//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/app"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestAPIRoutesExtension runs the API the way HookShield Cloud does: through
// app.API with an extra route guarded by the dashboard session.
func TestAPIRoutesExtension(t *testing.T) {
	pool := testdb.New(t)
	_, keys := testredis.New(t)
	addr := freeAddr(t)
	t.Setenv("HS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("HS_REDIS_URL", os.Getenv(testredis.EnvVar))
	t.Setenv("HS_REDIS_PREFIX", keys.Prefix())
	t.Setenv("HS_API_ADDR", addr)
	t.Setenv("HS_APP_URL", "http://localhost:3000")
	t.Setenv("HS_SMTP_URL", "")
	t.Setenv("HS_RESEND_API_KEY", "")
	t.Setenv("HS_LOG_LEVEL", "error")
	t.Setenv("HS_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")

	opts := app.Options{APIRoutes: func(mux *http.ServeMux, x app.APIContext) {
		if x.DB == nil || x.Redis == nil || x.Logger == nil {
			t.Error("APIContext is incomplete")
		}
		mux.Handle("GET /v1/billing/whoami", x.RequireUser(func(w http.ResponseWriter, r *http.Request) {
			u := x.User(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"email": u.Email, "plan": u.PlanType, "locale": u.Locale})
		}))
		mux.HandleFunc("POST /v1/billing/webhook", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent) // e.g. Paddle: no cookie, no Origin
		})
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.API(ctx, nil, io.Discard, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("API did not stop")
		}
	})

	base := "http://" + addr
	for i := 0; ; i++ {
		if res, err := http.Get(base + "/readyz"); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		if i > 100 {
			t.Fatal("API did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	do := func(method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if res := do("GET", "/v1/billing/whoami", ""); res.StatusCode != 401 {
		t.Fatalf("extra route without session: %d", res.StatusCode)
	}
	if res := do("POST", "/v1/auth/register", `{"email":"cloud@example.com","password":"a long enough passphrase","locale":"es"}`); res.StatusCode != 201 {
		t.Fatalf("register: %d", res.StatusCode)
	}
	res := do("GET", "/v1/billing/whoami", "")
	var got map[string]string
	_ = json.NewDecoder(res.Body).Decode(&got)
	res.Body.Close()
	if res.StatusCode != 200 || got["email"] != "cloud@example.com" || got["plan"] != "developer" || got["locale"] != "es" {
		t.Fatalf("extra route with session: %d %v", res.StatusCode, got)
	}
	if res := do("GET", "/v1/me", ""); res.StatusCode != 200 || res.Header.Get("X-Request-Id") == "" {
		t.Fatalf("built-in routes must keep working: %d", res.StatusCode)
	}
	// Server-to-server webhooks (no cookie, no Origin) pass the CSRF check.
	req, _ := http.NewRequest("POST", base+"/v1/billing/webhook", strings.NewReader("{}"))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 204 {
		t.Fatalf("billing webhook: %v %v", res, err)
	}
}
