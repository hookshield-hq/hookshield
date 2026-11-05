package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigRoundTripAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	env := map[string]string{EnvConfig: path}
	getenv := func(k string) string { return env[k] }

	cfg, err := LoadConfig(getenv)
	if err != nil || cfg.APIURL != DefaultAPIURL || cfg.APIKey != "" {
		t.Fatalf("empty config: %+v %v", cfg, err)
	}
	if _, err := NewClient(cfg); err != ErrNotLoggedIn {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
	if _, err := SaveConfig(getenv, Config{APIURL: "https://hooks.example.com", APIKey: "hs_live_abc"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode: %v %v", st.Mode(), err)
	}
	cfg, _ = LoadConfig(getenv)
	if cfg.APIURL != "https://hooks.example.com" || cfg.APIKey != "hs_live_abc" {
		t.Fatalf("saved: %+v", cfg)
	}
	env[EnvAPIKey], env[EnvAPIURL] = "hs_live_env", "http://localhost:8000/"
	cfg, _ = LoadConfig(getenv)
	if cfg.APIURL != "http://localhost:8000" || cfg.APIKey != "hs_live_env" {
		t.Fatalf("env overrides: %+v", cfg)
	}
	if _, err := RemoveConfig(getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveConfig(getenv); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestNormalizeAPIURL(t *testing.T) {
	for in, want := range map[string]string{
		"hooks.example.com":       "https://hooks.example.com",
		"http://localhost:8000/":  "http://localhost:8000",
		"localhost:8081":          "http://localhost:8081",
		"127.0.0.1:8000/":         "http://127.0.0.1:8000",
		" https://x.example.com ": "https://x.example.com",
	} {
		if got, err := NormalizeAPIURL(in); err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x"} {
		if _, err := NormalizeAPIURL(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseForward(t *testing.T) {
	for in, want := range map[string]string{
		"3000":                          "http://localhost:3000",
		":3000":                         "http://localhost:3000",
		"3000/webhooks":                 "http://localhost:3000/webhooks",
		":4000/a?b=c":                   "http://localhost:4000/a?b=c",
		"localhost:3000/webhooks":       "http://localhost:3000/webhooks",
		"https://app.test/hooks?x=1":    "https://app.test/hooks?x=1",
		"http://127.0.0.1:8080/a/b?c=d": "http://127.0.0.1:8080/a/b?c=d",
	} {
		u, err := ParseForward(in)
		if err != nil || u.String() != want {
			t.Errorf("%q → %v %v, want %s", in, u, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://"} {
		if _, err := ParseForward(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestFindEndpoint(t *testing.T) {
	eps := []Endpoint{{ID: "id-1", Slug: "ep_a", Name: "Stripe"}, {ID: "id-2", Slug: "ep_b", Name: "Dup"}, {ID: "id-3", Slug: "ep_c", Name: "Dup"}}
	for ref, want := range map[string]string{"ep_a": "id-1", "id-2": "id-2", "Stripe": "id-1"} {
		if e, err := FindEndpoint(eps, ref); err != nil || e.ID != want {
			t.Errorf("%s → %+v %v", ref, e, err)
		}
	}
	if _, err := FindEndpoint(eps, "Dup"); err == nil || !strings.Contains(err.Error(), "slug") {
		t.Errorf("ambiguous name: %v", err)
	}
	if _, err := FindEndpoint(eps, "nope"); err == nil {
		t.Error("unknown endpoint should fail")
	}
}

func TestBuildTrigger(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	tr, err := BuildTrigger("", "invoice.paid", "", nil, now)
	if err != nil || tr.Method != "POST" || tr.Headers != nil {
		t.Fatalf("event only: %+v %v", tr, err)
	}
	var body map[string]any
	_ = json.Unmarshal(tr.Body, &body)
	if body["type"] != "invoice.paid" || body["sent_at"] != "2026-11-03T12:00:00Z" {
		t.Fatalf("event body: %s", tr.Body)
	}

	tr, _ = BuildTrigger("put", "", `{"a":1}`, []string{"X-Test: 1", "x-test: 2"}, now)
	if tr.Method != "PUT" || string(tr.Body) != `{"a":1}` || len(tr.Headers["X-Test"]) != 2 || tr.Headers["Content-Type"][0] != "application/json" {
		t.Fatalf("json data + headers: %+v", tr)
	}
	tr, _ = BuildTrigger("POST", "", "a=1&b=2", []string{"Content-Type: application/x-www-form-urlencoded"}, now)
	if tr.BodyText == nil || *tr.BodyText != "a=1&b=2" || tr.Body != nil || tr.Headers["Content-Type"][0] != "application/x-www-form-urlencoded" {
		t.Fatalf("text data: %+v", tr)
	}
	file := filepath.Join(t.TempDir(), "evt.json")
	_ = os.WriteFile(file, []byte(`{"from":"file"}`), 0o600)
	if tr, _ = BuildTrigger("", "", "@"+file, nil, now); string(tr.Body) != `{"from":"file"}` {
		t.Fatalf("@file: %+v", tr)
	}
	if tr, _ = BuildTrigger("", "", "", nil, now); tr.Body != nil || tr.BodyText != nil {
		t.Fatalf("no data: server default expected, got %+v", tr)
	}
	if _, err := BuildTrigger("", "", "", []string{"bad"}, now); err == nil {
		t.Fatal("header without colon should fail")
	}
}

// docs/CLI.md: the saved login is "readable only by you", even when an old
// file with wider permissions (or a leftover temporary file) is in the way.
func TestSaveConfigIsAlwaysPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	for _, p := range []string{path, path + ".tmp"} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	getenv := func(k string) string {
		if k == EnvConfig {
			return path
		}
		return ""
	}
	if _, err := SaveConfig(getenv, Config{APIURL: "https://hooks.example.com", APIKey: "hs_live_secret"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode %v (%v), want 0600", st.Mode(), err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}
