package sink

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(h http.Handler, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook?x=1", strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestFailFirstThenStatus(t *testing.T) {
	s := New(Options{FailFirst: 2, Status: 202})
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, post(s, `{}`, nil).Code)
	}
	if codes[0] != 503 || codes[1] != 503 || codes[2] != 202 || codes[3] != 202 {
		t.Fatalf("codes %v", codes)
	}
}

func TestStatsAndPrinting(t *testing.T) {
	var out bytes.Buffer
	s := New(Options{Out: &out})
	post(s, `{"type":"invoice.paid"}`, map[string]string{
		"X-HookShield-Id": "abc", "X-HookShield-Attempt": "2", "X-HookShield-Event": "invoice.paid",
		"Authorization": "Bearer sk_live_very_secret_value", "Content-Type": "application/json",
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_sink/stats", nil))
	var st Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Received != 1 || st.Last[0].ID != "abc" || st.Last[0].Attempt != "2" || st.Last[0].Path != "/webhook?x=1" {
		t.Fatalf("stats %+v", st)
	}

	printed := out.String()
	for _, want := range []string{"#1", "POST /webhook?x=1", "X-Hookshield-Id: abc", `"type": "invoice.paid"`} {
		if !strings.Contains(printed, want) {
			t.Errorf("output missing %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "very_secret_value") {
		t.Error("credentials printed in full")
	}
}

func TestKeepsOnlyRecent(t *testing.T) {
	s := New(Options{Quiet: true})
	for i := 0; i < keepLast+5; i++ {
		post(s, `{}`, nil)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_sink/stats", nil))
	var st Stats
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Received != keepLast+5 || len(st.Last) != keepLast {
		t.Fatalf("received=%d last=%d", st.Received, len(st.Last))
	}
}
