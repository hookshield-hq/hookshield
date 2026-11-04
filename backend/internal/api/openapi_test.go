package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/redisx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// fullServer registers every route (no I/O happens during registration).
func fullServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	cipher, err := crypto.New(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = rdb.Close() })
	keys := redisx.NewKeys("hs:")
	srv := New(auth.New(store.New(nil), rdb, keys, auth.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}),
		Options{Store: store.New(nil), Cipher: cipher, Redis: rdb, Keys: keys, Billing: stubBilling{}})
	mux := http.NewServeMux()
	srv.Register(mux)
	return srv, mux
}

// stubBilling makes the billing routes register (HookShield Cloud's are documented too).
type stubBilling struct{}

func (stubBilling) Offers() []core.Offer { return nil }
func (stubBilling) Checkout(context.Context, core.CheckoutRequest) (core.CheckoutResult, error) {
	return core.CheckoutResult{}, nil
}
func (stubBilling) Portal(context.Context, core.User, core.Subscription) (string, error) {
	return "", nil
}
func (stubBilling) HandleWebhook(context.Context, []byte, http.Header, core.Accounts) error {
	return nil
}

func TestBillingRoutesOnlyWithAProvider(t *testing.T) {
	srv := New(auth.New(store.New(nil), nil, redisx.NewKeys("hs:"), auth.Options{}), Options{Store: store.New(nil)})
	srv.Register(http.NewServeMux())
	for _, r := range srv.Routes() {
		if strings.HasPrefix(r.Pattern, "GET /v1/billing") || strings.HasPrefix(r.Pattern, "POST /v1/billing") {
			t.Errorf("self-hosted server mounts %s", r.Pattern)
		}
	}
	rec := httptest.NewRecorder()
	srv.features(rec, httptest.NewRequest("GET", "/v1/features", nil))
	if rec.Body.String() != "{\"billing\":false}\n" {
		t.Fatalf("features = %s", rec.Body.String())
	}
}

type openapiDoc struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components json.RawMessage                       `json:"components"`
}

type operation struct {
	OperationID string            `json:"operationId"`
	Access      string            `json:"x-access"`
	Security    []json.RawMessage `json:"security"`
	Responses   map[string]any    `json:"responses"`
}

// TestOpenAPIMatchesRoutes fails when a route is added, removed or changes its
// access level without the same change in openapi.json (and vice versa).
func TestOpenAPIMatchesRoutes(t *testing.T) {
	srv, _ := fullServer(t)
	code := map[string]string{"GET /healthz": "public", "GET /readyz": "public"} // httpx.Health, every service
	for _, r := range srv.Routes() {
		if _, dup := code[r.Pattern]; dup {
			t.Errorf("route registered twice: %s", r.Pattern)
		}
		code[r.Pattern] = r.Access
	}

	var doc openapiDoc
	if err := json.Unmarshal(OpenAPI, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	spec := map[string]string{}
	ids := map[string]bool{}
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if method == "parameters" {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			key := strings.ToUpper(method) + " " + path
			spec[key] = op.Access
			if op.OperationID == "" || ids[op.OperationID] {
				t.Errorf("%s: missing or duplicate operationId %q", key, op.OperationID)
			}
			ids[op.OperationID] = true
			if len(op.Responses) == 0 {
				t.Errorf("%s: no responses", key)
			}
			if (op.Access == "public") != (len(op.Security) == 0) {
				t.Errorf("%s: security does not match x-access %q", key, op.Access)
			}
		}
	}

	var missing, extra, wrong []string
	for k, a := range code {
		switch sa, ok := spec[k]; {
		case !ok:
			missing = append(missing, k)
		case sa != a:
			wrong = append(wrong, k+" (code "+a+", spec "+sa+")")
		}
	}
	for k := range spec {
		if _, ok := code[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(wrong)
	for _, k := range missing {
		t.Errorf("route not documented in openapi.json: %s", k)
	}
	for _, k := range extra {
		t.Errorf("openapi.json documents a route that does not exist: %s", k)
	}
	for _, k := range wrong {
		t.Errorf("x-access differs: %s", k)
	}
}

// TestOpenAPIRefsResolve checks that every $ref points at an existing component.
func TestOpenAPIRefsResolve(t *testing.T) {
	var all map[string]any
	if err := json.Unmarshal(OpenAPI, &all); err != nil {
		t.Fatal(err)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				node := any(all)
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					m, _ := node.(map[string]any)
					node = m[part]
				}
				if node == nil {
					t.Errorf("unresolved $ref %s", ref)
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(all)
}

func TestOpenAPIIsServed(t *testing.T) {
	_, mux := fullServer(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != string(OpenAPI) {
		t.Fatalf("GET /v1/openapi.json: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
