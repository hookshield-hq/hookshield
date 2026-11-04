//go:build integration

package api_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/dispatcher"
	"github.com/hookshield-hq/hookshield/backend/internal/live"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
)

// destination is the receiving application: answers with a programmable
// sequence of status codes and records what it received.
type destination struct {
	srv      *httptest.Server
	mu       sync.Mutex
	statuses []int
	bodies   []string
}

func newDestination(t *testing.T, statuses ...int) *destination {
	d := &destination{statuses: statuses}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		n := len(d.bodies)
		d.bodies = append(d.bodies, string(b))
		st := 200
		if n < len(d.statuses) {
			st = d.statuses[n]
		}
		d.mu.Unlock()
		w.Header().Set("X-Dest", "yes")
		w.WriteHeader(st)
		if st >= 300 {
			_, _ = w.Write([]byte("boom"))
		} else {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *destination) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.bodies...)
}

// pipeline runs the worker stages synchronously.
type pipeline struct {
	m    *mgmt
	proc *processor.Processor
	disp *dispatcher.Dispatcher
	prod *queue.Producer
}

func (m *mgmt) pipeline() *pipeline {
	ctx := context.Background()
	prod := queue.NewProducer(m.rdb, m.keys)
	events := live.NewPublisher(m.rdb, m.keys)
	pc := queue.NewConsumer(m.rdb, m.keys.IngestStream(), processor.Group, "p1")
	dc := queue.NewConsumer(m.rdb, m.keys.DispatchStream(), dispatcher.Group, "d1")
	_ = pc.EnsureGroup(ctx)
	_ = dc.EnsureGroup(ctx)
	return &pipeline{m: m, prod: prod,
		proc: processor.New(pc, prod, m.store, m.cipher, processor.Options{Block: 20 * time.Millisecond, Events: events}),
		disp: dispatcher.New(dc, prod, m.store, m.cipher, outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 2 * time.Second}), nil,
			dispatcher.Options{Block: 20 * time.Millisecond, JitterPercent: -1, Events: events}),
	}
}

// run processes everything queued, including every scheduled retry.
func (p *pipeline) run() {
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		n1, err := p.proc.ProcessBatch(ctx)
		if err != nil {
			p.m.t.Fatal(err)
		}
		n2, err := p.disp.DispatchBatch(ctx)
		if err != nil {
			p.m.t.Fatal(err)
		}
		moved, _ := p.prod.MoveDueRetries(ctx, time.Now().Add(48*time.Hour), 100)
		if n1 == 0 && n2 == 0 && moved == 0 {
			return
		}
	}
}

// endpointTo creates an endpoint of b pointing at dest and returns (id, slug, token).
func endpointTo(t *testing.T, b *browser, dest *destination, extra map[string]any) (string, string, string) {
	body := map[string]any{"name": "dest", "target_url": dest.srv.URL + "/hook", "retry": map[string]any{"strategy": "fixed", "initial_interval_ms": 1000, "max_retries": 3}}
	for k, v := range extra {
		body[k] = v
	}
	r := b.do("POST", "/v1/endpoints", body)
	if r.status != 201 {
		t.Fatalf("create endpoint: %d %+v", r.status, r.body)
	}
	return str(ep(r)["id"]), str(ep(r)["slug"]), str(secrets(r)["token"])
}

func (m *mgmt) webhook(slug, token, body string, headers map[string]string) {
	h := map[string]string{"X-HookShield-Token": token, "Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	if code := m.send(slug, h, body); code != 202 {
		m.t.Fatalf("ingest: %d", code)
	}
}

func TestLogDebuggerAndReplay(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	dest := newDestination(t, 500) // first attempt fails, then 200
	id, slug, tok := endpointTo(t, b, dest, map[string]any{
		"transform_rules": map[string]any{"version": 1, "body": []any{map[string]any{"op": "rename", "from": "user_id", "to": "customerId"}}}})
	p := m.pipeline()

	m.webhook(slug, tok, `{"type":"invoice.paid","user_id":1}`, map[string]string{"X-Custom": "abc"})
	p.run()

	l := b.do("GET", "/v1/logs", nil)
	logs := list(l.body["logs"])
	if l.status != 200 || len(logs) != 1 {
		t.Fatalf("list: %d %+v", l.status, l.body)
	}
	lg := obj(logs[0])
	logID := str(lg["id"])
	if str(lg["status"]) != "delivered" || lg["attempts"] != float64(2) || lg["last_status_code"] != float64(200) ||
		str(lg["event_type"]) != "invoice.paid" || str(obj(lg["endpoint"])["id"]) != id || obj(lg["latency"])["gateway_ms"] == nil {
		t.Fatalf("log summary: %+v", lg)
	}

	d := b.do("GET", "/v1/logs/"+logID, nil)
	req := obj(d.body["request"])
	orig, tr := obj(req["original"]), obj(req["transformed"])
	if d.status != 200 || mustJSON(orig["body"]) != `{"type":"invoice.paid","user_id":1}` || mustJSON(tr["body"]) != `{"customerId":1,"type":"invoice.paid"}` {
		t.Fatalf("debugger request: %d %+v", d.status, d.body)
	}
	if hs := obj(orig["headers"]); hs["X-Custom"] == nil || hs["X-Hookshield-Token"] != nil {
		t.Fatalf("stored headers must keep X-Custom and drop the inbound token: %+v", hs)
	}
	// raw=true: every body as base64, byte for byte (the CLI forwards exactly what was received).
	rawD := b.do("GET", "/v1/logs/"+logID+"?raw=true", nil)
	rawOrig := obj(obj(rawD.body["request"])["original"])
	if got, _ := base64.StdEncoding.DecodeString(str(rawOrig["body_base64"])); string(got) != `{"type":"invoice.paid","user_id":1}` || rawOrig["body"] != nil {
		t.Fatalf("raw original body: %+v", rawOrig)
	}
	if a := obj(list(rawD.body["attempts"])[0]); obj(a["response"])["body_base64"] != "Ym9vbQ==" {
		t.Fatalf("raw attempt response: %+v", a)
	}

	atts := list(d.body["attempts"])
	if len(atts) != 2 {
		t.Fatalf("attempts: %+v", d.body["attempts"])
	}
	a1, a2 := obj(atts[0]), obj(atts[1])
	if a1["status_code"] != float64(500) || str(obj(a1["response"])["body_text"]) != "boom" ||
		a2["status_code"] != float64(200) || mustJSON(obj(a2["response"])["body"]) != `{"ok":true}` || a1["duration_ms"] == nil {
		t.Fatalf("attempt timeline: %+v", atts)
	}

	// Replay as received: a new log, linked to the original, through the whole pipeline.
	rp := b.do("POST", "/v1/logs/"+logID+"/replay", nil)
	replayID := str(rp.body["id"])
	if rp.status != 202 || str(rp.body["replay_of"]) != logID || replayID == "" || replayID == logID {
		t.Fatalf("replay: %d %+v", rp.status, rp.body)
	}
	p.run()
	rl := obj(b.do("GET", "/v1/logs/"+replayID, nil).body["log"])
	if str(rl["status"]) != "delivered" || str(rl["replay_of"]) != logID || rl["source_ip"] != nil {
		t.Fatalf("replayed log: %+v", rl)
	}
	if got := dest.received(); len(got) != 3 || got[2] != `{"customerId":1,"type":"invoice.paid"}` {
		t.Fatalf("destination after replay: %q", got)
	}

	// Replay with an edited body: the current transform rules still apply.
	ed := b.do("POST", "/v1/logs/"+logID+"/replay", map[string]any{"body": map[string]any{"type": "invoice.paid", "user_id": 2}})
	if ed.status != 202 {
		t.Fatalf("edited replay: %d %+v", ed.status, ed.body)
	}
	p.run()
	if got := dest.received(); got[len(got)-1] != `{"customerId":2,"type":"invoice.paid"}` {
		t.Fatalf("edited replay delivered: %q", got[len(got)-1])
	}
	if reps := list(b.do("GET", "/v1/logs/"+logID, nil).body["replays"]); len(reps) != 2 || str(reps[0]) != replayID {
		t.Fatalf("replays of the original: %+v", reps)
	}
	if bad := b.do("POST", "/v1/logs/"+logID+"/replay", map[string]any{"headers": map[string][]string{"X-Bad": {"a\r\nb"}}}); bad.code != "log.invalid_replay_header" {
		t.Fatalf("header injection on replay: %d %s", bad.status, bad.code)
	}

	// Other accounts see nothing; scopes apply.
	other := m.signedIn("other@example.com")
	if r := other.do("GET", "/v1/logs/"+logID, nil); r.status != 404 || r.code != "log.not_found" {
		t.Fatalf("other account GET: %d %s", r.status, r.code)
	}
	if r := other.do("POST", "/v1/logs/"+logID+"/replay", nil); r.status != 404 {
		t.Fatalf("other account replay: %d", r.status)
	}
	if l := other.do("GET", "/v1/logs", nil); len(list(l.body["logs"])) != 0 {
		t.Fatal("other account lists foreign logs")
	}
	readKey := str(b.do("POST", "/v1/api-keys", map[string]any{"name": "r", "scope": "read_only"}).body["key"])
	ingestKey := str(b.do("POST", "/v1/api-keys", map[string]any{"name": "i", "scope": "ingest"}).body["key"])
	for key, want := range map[string]int{readKey: 403, ingestKey: 202} {
		req, _ := http.NewRequest("POST", m.srv.URL+"/v1/logs/"+logID+"/replay", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("replay with %s key: %d, want %d", key[:12], res.StatusCode, want)
		}
	}
}

func TestReplayRespectsQuota(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com") // developer plan in the test plan table: 5,000/month, hard limit
	dest := newDestination(t)
	_, slug, tok := endpointTo(t, b, dest, nil)
	m.webhook(slug, tok, `{}`, nil)
	m.pipeline().run()
	logID := str(obj(list(b.do("GET", "/v1/logs", nil).body["logs"])[0])["id"])

	me := user(b.do("GET", "/v1/me", nil))
	m.rdb.Set(context.Background(), m.keys.MonthlyUsage(str(me["id"]), time.Now().UTC().Format("200601")), 5000, time.Hour)
	if r := b.do("POST", "/v1/logs/"+logID+"/replay", nil); r.status != 429 || r.code != "quota_exceeded" {
		t.Fatalf("replay over quota: %d %s", r.status, r.code)
	}
}

func TestLogFiltersAndPagination(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	ok, failing := newDestination(t), newDestination(t, 500, 500, 500, 500, 500)
	_, slugOK, tokOK := endpointTo(t, b, ok, nil)
	failID, slugFail, tokFail := endpointTo(t, b, failing, map[string]any{"retry": map[string]any{"max_retries": 0}})
	p := m.pipeline()
	for i, typ := range []string{"a.created", "b.created", "a.created", "c.deleted"} {
		m.webhook(slugOK, tokOK, `{"type":"`+typ+`","n":`+string(rune('0'+i))+`}`, nil)
		time.Sleep(2 * time.Millisecond) // distinct received_at
	}
	m.webhook(slugFail, tokFail, `{"type":"x.failed"}`, nil)
	p.run()

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/v1/logs?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := b.do("GET", path, nil)
		for _, l := range list(r.body["logs"]) {
			id := str(obj(l)["id"])
			if seen[id] {
				t.Fatalf("duplicate %s across pages", id)
			}
			seen[id] = true
		}
		pages++
		if r.body["next_cursor"] == nil {
			break
		}
		cursor = str(r.body["next_cursor"])
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("pagination: %d logs in %d pages", len(seen), pages)
	}

	count := func(q string) int { return len(list(b.do("GET", "/v1/logs?"+q, nil).body["logs"])) }
	if n := count("status=dead"); n != 1 {
		t.Fatalf("status=dead: %d", n)
	}
	if n := count("status=delivered,dead"); n != 5 {
		t.Fatalf("status list: %d", n)
	}
	if n := count("event_type=a.created"); n != 2 {
		t.Fatalf("event_type: %d", n)
	}
	if n := count("endpoint_id=" + failID); n != 1 {
		t.Fatalf("endpoint filter: %d", n)
	}
	if n := count("q=created"); n != 3 {
		t.Fatalf("q by event type: %d", n)
	}
	some := str(obj(list(b.do("GET", "/v1/logs?limit=1", nil).body["logs"])[0])["id"])
	if n := count("q=" + some[:13]); n < 1 {
		t.Fatalf("q by ID prefix: %d", n)
	}
	if n := count("from=" + time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); n != 0 {
		t.Fatalf("from in the future: %d", n)
	}
	for _, bad := range []string{"status=weird", "limit=0", "from=yesterday", "endpoint_id=x", "cursor=not-a-cursor!"} {
		if r := b.do("GET", "/v1/logs?"+bad, nil); r.status != 422 {
			t.Errorf("%s accepted: %d", bad, r.status)
		}
	}
}

func TestMetricsSearchAndStatus(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	ok, failing := newDestination(t), newDestination(t, 500, 500)
	okID, slugOK, tokOK := endpointTo(t, b, ok, map[string]any{"name": "Stripe payments"})
	failID, slugFail, tokFail := endpointTo(t, b, failing, map[string]any{"retry": map[string]any{"max_retries": 0}})
	for i := 0; i < 3; i++ {
		m.webhook(slugOK, tokOK, `{"type":"paid"}`, nil)
	}
	m.webhook(slugFail, tokFail, `{"type":"lost"}`, nil)
	m.pipeline().run()

	s := b.do("GET", "/v1/metrics/summary?range=24h", nil).body
	if s["total_received"] != float64(4) || s["delivered"] != float64(3) || s["failed"] != float64(1) ||
		s["delivery_rate"] != float64(75) || s["active_retries"] != float64(0) || s["avg_latency_ms"] == float64(0) {
		t.Fatalf("summary: %+v", s)
	}
	if one := b.do("GET", "/v1/metrics/summary?endpoint_id="+okID, nil).body; one["total_received"] != float64(3) || one["delivery_rate"] != float64(100) {
		t.Fatalf("summary of one endpoint: %+v", one)
	}
	series := b.do("GET", "/v1/metrics/series?range=24h", nil).body
	pts := list(series["points"])
	var sum float64
	for _, p := range pts {
		sum += obj(p)["received"].(float64)
	}
	if series["bucket_seconds"] != float64(3600) || len(pts) < 24 || len(pts) > 26 || sum != 4 {
		t.Fatalf("series: %d points, sum %v, %+v", len(pts), sum, series["bucket_seconds"])
	}
	stats := list(b.do("GET", "/v1/metrics/endpoints", nil).body["endpoints"])
	for _, st := range stats {
		o := obj(st)
		if str(o["endpoint_id"]) == failID && (o["failed"] != float64(1) || o["success_rate"] != float64(0)) {
			t.Fatalf("failing endpoint stats: %+v", o)
		}
	}
	if r := b.do("GET", "/v1/metrics/summary?range=1y", nil); r.code != "metrics.invalid_range" {
		t.Fatalf("bad range: %s", r.code)
	}

	b.do("PUT", "/v1/secrets/STRIPE_KEY", map[string]string{"value": "x"})
	sr := b.do("GET", "/v1/search?q=stripe", nil).body
	if len(list(sr["endpoints"])) != 1 || len(list(sr["secrets"])) != 1 {
		t.Fatalf("search: %+v", sr)
	}
	if r := b.do("GET", "/v1/search?q=lost", nil); len(list(r.body["logs"])) != 1 {
		t.Fatalf("search logs by event type: %+v", r.body)
	}
	if r := b.do("GET", "/v1/search?q=x", nil); r.code != "search.invalid_query" {
		t.Fatalf("short query: %s", r.code)
	}

	st := b.do("GET", "/v1/status", nil).body
	q := obj(st["queues"])
	if str(st["status"]) != "operational" || str(st["database"]) != "up" || obj(q["ingest"]) == nil || q["dead_letters"] != float64(1) {
		t.Fatalf("status: %+v", st)
	}
}

func TestLiveFeed(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	other := m.signedIn("other@example.com")
	dest := newDestination(t)
	_, slug, tok := endpointTo(t, b, dest, nil)
	_, otherSlug, otherTok := endpointTo(t, other, dest, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", m.srv.URL+"/v1/live", nil)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("live: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	type sse struct{ event, data string }
	events := make(chan sse, 16)
	pings := make(chan struct{}, 16)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		sc := bufio.NewScanner(res.Body)
		var cur sse
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ": ping"):
				select {
				case pings <- struct{}{}:
				default:
				}
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.event != "":
				events <- cur
				cur = sse{}
			}
		}
	}()
	select {
	case <-pings:
	case <-ctx.Done():
		t.Fatal("no keep-alive ping")
	}

	m.webhook(otherSlug, otherTok, `{"type":"not.mine"}`, nil) // must not show up
	m.webhook(slug, tok, `{"type":"mine"}`, nil)
	m.pipeline().run()

	got := map[string]map[string]any{}
	for len(got) < 2 {
		select {
		case e := <-events:
			var ev map[string]any
			if err := json.Unmarshal([]byte(e.data), &ev); err != nil {
				t.Fatal(err)
			}
			if str(ev["event_type"]) == "not.mine" {
				t.Fatal("received another account's event")
			}
			got[e.event] = ev
		case <-ctx.Done():
			t.Fatalf("events received: %+v", got)
		}
	}
	c, u := got["log.created"], got["log.updated"]
	if str(c["event_type"]) != "mine" || str(c["status"]) != "queued" || str(u["status"]) != "delivered" ||
		u["status_code"] != float64(200) || str(u["log_id"]) != str(c["log_id"]) {
		t.Fatalf("live events: %+v", got)
	}

	// Shutting the server down ends open feeds at once (clients reconnect elsewhere)
	// instead of holding the graceful shutdown until its timeout.
	m.api.CloseStreams()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the live feed stayed open after CloseStreams")
	}
}

func TestSendTestEvent(t *testing.T) {
	m := setupMgmt(t, true)
	b := m.signedIn("dev@example.com")
	dest := newDestination(t)
	id, _, _ := endpointTo(t, b, dest, map[string]any{
		"transform_rules": map[string]any{"version": 1, "body": []any{map[string]any{"op": "set", "path": "via", "value": "test"}}}})
	p := m.pipeline()

	// Default sample: JSON, through the whole pipeline (transform rules included).
	r := b.do("POST", "/v1/endpoints/"+id+"/test", nil)
	testID := str(r.body["id"])
	if r.status != 202 || testID == "" || str(r.body["status"]) != "queued" {
		t.Fatalf("test event: %d %+v", r.status, r.body)
	}
	p.run()
	got := dest.received()
	if len(got) != 1 || !strings.Contains(got[0], `"type":"hookshield.test"`) || !strings.Contains(got[0], `"via":"test"`) {
		t.Fatalf("destination got %q", got)
	}
	lg := obj(b.do("GET", "/v1/logs/"+testID, nil).body["log"])
	if str(lg["status"]) != "delivered" || str(lg["event_type"]) != "hookshield.test" || lg["replay_of"] != nil || lg["source_ip"] != nil {
		t.Fatalf("test log: %+v", lg)
	}

	// Custom method, headers and body.
	r = b.do("POST", "/v1/endpoints/"+id+"/test", map[string]any{
		"method": "PUT", "headers": map[string][]string{"Content-Type": {"application/json"}, "X-Sandbox": {"1"}}, "body": map[string]any{"type": "order.created", "n": 7}})
	if r.status != 202 {
		t.Fatalf("custom test event: %d %+v", r.status, r.body)
	}
	p.run()
	d := b.do("GET", "/v1/logs/"+str(r.body["id"]), nil)
	orig := obj(obj(d.body["request"])["original"])
	if str(orig["method"]) != "PUT" || obj(orig["headers"])["X-Sandbox"] == nil || str(obj(d.body["log"])["event_type"]) != "order.created" {
		t.Fatalf("custom test log: %+v", d.body)
	}

	// Validation, pause, other accounts, scopes.
	for body, code := range map[string]map[string]any{
		"endpoint.invalid_test_method": {"method": "GET"},
		"endpoint.invalid_test_body":   {"body": map[string]any{}, "body_text": "x"},
		"endpoint.invalid_test_header": {"headers": map[string][]string{"X-Bad": {"a\r\nb"}}},
	} {
		if res := b.do("POST", "/v1/endpoints/"+id+"/test", code); res.status != 422 || res.code != body {
			t.Errorf("%v: %d %s, want 422 %s", code, res.status, res.code, body)
		}
	}
	if res := b.do("PATCH", "/v1/endpoints/"+id, map[string]any{"status": "paused"}); res.status != 200 {
		t.Fatalf("pause: %d", res.status)
	}
	if res := b.do("POST", "/v1/endpoints/"+id+"/test", nil); res.status != 409 || res.code != "endpoint.paused" {
		t.Fatalf("test on paused endpoint: %d %s", res.status, res.code)
	}
	b.do("PATCH", "/v1/endpoints/"+id, map[string]any{"status": "active"})
	if res := m.signedIn("other@example.com").do("POST", "/v1/endpoints/"+id+"/test", nil); res.status != 404 || res.code != "endpoint.not_found" {
		t.Fatalf("other account: %d %s", res.status, res.code)
	}
	readKey := str(b.do("POST", "/v1/api-keys", map[string]any{"name": "r", "scope": "read_only"}).body["key"])
	ingestKey := str(b.do("POST", "/v1/api-keys", map[string]any{"name": "i", "scope": "ingest"}).body["key"])
	for key, want := range map[string]int{readKey: 403, ingestKey: 202} {
		req, _ := http.NewRequest("POST", m.srv.URL+"/v1/endpoints/"+id+"/test", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("test event with %s key: %d, want %d", key[:12], res.StatusCode, want)
		}
	}
}
