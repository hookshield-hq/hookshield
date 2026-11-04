package alert

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogIsCompleteInEveryLocale(t *testing.T) {
	en := catalog[LocaleEN]
	for _, loc := range []string{LocalePTBR, LocaleES} {
		for k := range en {
			if catalog[loc][k] == "" {
				t.Errorf("%s: missing key %q", loc, k)
			}
			if strings.Count(catalog[loc][k], "%") != strings.Count(en[k], "%") {
				t.Errorf("%s: key %q has different placeholders than en", loc, k)
			}
		}
		if len(catalog[loc]) != len(en) {
			t.Errorf("%s has %d keys, en has %d", loc, len(catalog[loc]), len(en))
		}
	}
}

func TestNormalizeLocale(t *testing.T) {
	for in, want := range map[string]string{"en": "en", "pt-BR": "pt-BR", "pt": "pt-BR", "pt-PT": "pt-BR", "es": "es", "es-MX": "es", "fr": "en", "": "en"} {
		if got := normalizeLocale(in); got != want {
			t.Errorf("normalizeLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	a := Alert{LogID: "log-1", EventType: "invoice.paid", Attempts: 4, LastStatus: 503, Suppressed: 2}
	c := Context{EndpointName: "Stripe prod", EndpointSlug: "ep_stripe"}

	en := Render(a, c, "en", "https://app.hookshield.dev/logs/log-1")
	if en.Title != "⚠️ Webhook deliveries are failing — Stripe prod" {
		t.Fatalf("title %q", en.Title)
	}
	text := en.Text()
	for _, want := range []string{"Event: invoice.paid", "Failed attempts: 4", "HTTP 503", "Webhook ID: log-1",
		"2 more webhook(s)", "keeps retrying", "https://app.hookshield.dev/logs/log-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("en text missing %q:\n%s", want, text)
		}
	}

	pt := Render(Alert{LogID: "x", Attempts: 6, LastError: "timeout", Final: true}, Context{EndpointSlug: "ep_x"}, "pt-BR", "")
	if pt.Title != "❌ Entrega de webhook abandonada — ep_x" || !strings.Contains(pt.Text(), "Último resultado: timeout") ||
		!strings.Contains(pt.Text(), "(desconhecido)") || !strings.Contains(pt.Text(), "Não haverá novas tentativas") {
		t.Fatalf("pt-BR rendering:\n%s", pt.Text())
	}
	if strings.Contains(pt.Text(), "Abrir no HookShield") {
		t.Error("link line must be omitted without a URL")
	}

	es := Render(a, c, "es", "")
	if !strings.HasPrefix(es.Title, "⚠️ Las entregas de webhooks están fallando") {
		t.Fatalf("es title %q", es.Title)
	}
}

func TestEncodeDecode(t *testing.T) {
	in := Alert{LogID: "l", EndpointID: "e", UserID: "u", EventType: "push", Attempts: 4, LastStatus: 500,
		LastError: "", Final: true, At: time.UnixMilli(1_793_726_188_000).UTC(), Suppressed: 3}
	v := map[string]any{}
	for k, x := range encode(in) {
		v[k] = x
	}
	out, err := decode(v)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: %+v %v", out, err)
	}
	if _, err := decode(map[string]any{"endpoint_id": "e"}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
}

func TestPostJSONNeverLeaksURL(t *testing.T) {
	secretURL := "http://127.0.0.1:1/botSECRET-TOKEN/sendMessage" // nothing listens on port 1
	err := postJSON(context.Background(), http.DefaultClient, secretURL, map[string]string{"a": "b"}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error must not contain the URL: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	if err := postJSON(context.Background(), http.DefaultClient, srv.URL, 1, nil); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("non-2xx must fail: %v", err)
	}
}
