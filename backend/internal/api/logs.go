package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/internal/dispatcher"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/live"
	"github.com/hookshield-hq/hookshield/backend/internal/processor"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// Limiter applies the ingest limits to replays (rate limit + monthly quota).
type Limiter interface {
	Check(ctx context.Context, endpointID, userID, plan string) (limits.Decision, int64, error)
}

var errLogNotFound = &apiError{Status: http.StatusNotFound, Code: "log.not_found"}

var logStatuses = map[string]bool{
	store.LogQueued: true, "processing": true, store.LogDelivered: true, store.LogRetrying: true,
	store.LogFailed: true, store.LogDead: true,
}

type logJSON struct {
	ID             string          `json:"id"`
	Endpoint       logEndpointJSON `json:"endpoint"`
	ReplayOf       *string         `json:"replay_of"`
	Method         string          `json:"method"`
	EventType      *string         `json:"event_type"`
	ContentType    *string         `json:"content_type"`
	SizeBytes      int             `json:"size_bytes"`
	SourceIP       *string         `json:"source_ip"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	LastStatusCode *int32          `json:"last_status_code"`
	NextAttemptAt  *time.Time      `json:"next_attempt_at"`
	Latency        latencyJSON     `json:"latency"`
	ReceivedAt     time.Time       `json:"received_at"`
	DeliveredAt    *time.Time      `json:"delivered_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
}

type logEndpointJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// latencyJSON is the Visual Flow Tracker: time spent in each gateway stage.
type latencyJSON struct {
	IngestMs    *float64 `json:"ingest_ms"`
	TransformMs *float64 `json:"transform_ms"`
	PersistMs   *float64 `json:"persist_ms"`
	GatewayMs   *float64 `json:"gateway_ms"` // sum of the three
}

func ms(us *int32) *float64 {
	if us == nil {
		return nil
	}
	v := round3(float64(*us) / 1000)
	return &v
}

func toLogJSON(l store.LogSummary) logJSON {
	lat := latencyJSON{IngestMs: ms(l.IngestMicros), TransformMs: ms(l.TransformMs), PersistMs: ms(l.PersistMicros)}
	if l.IngestMicros != nil {
		total := float64(*l.IngestMicros) / 1000
		for _, v := range []*int32{l.TransformMs, l.PersistMicros} {
			if v != nil {
				total += float64(*v) / 1000
			}
		}
		total = round3(total)
		lat.GatewayMs = &total
	}
	return logJSON{
		ID: l.ID, Endpoint: logEndpointJSON{ID: l.EndpointID, Name: l.EndpointName, Slug: l.EndpointSlug},
		ReplayOf: l.ReplayOf, Method: l.HTTPMethod, EventType: l.EventType, ContentType: l.ContentType,
		SizeBytes: l.SizeBytes, SourceIP: l.SourceIP, Status: l.Status, Attempts: l.AttemptCount,
		LastStatusCode: l.LastStatusCode, NextAttemptAt: utcPtr(l.NextAttemptAt), Latency: lat,
		ReceivedAt: l.ReceivedAt.UTC(), DeliveredAt: utcPtr(l.DeliveredAt), ExpiresAt: l.ExpiresAt.UTC(),
	}
}

// cursor encodes the keyset position of the last row of a page.
func encodeCursor(l store.LogSummary) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(l.ReceivedAt.UnixMicro(), 10) + ":" + l.ID))
}

func decodeCursor(c string) (time.Time, string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", false
	}
	ts, id, ok := strings.Cut(string(raw), ":")
	us, err := strconv.ParseInt(ts, 10, 64)
	if !ok || err != nil || !uuidRE.MatchString(id) {
		return time.Time{}, "", false
	}
	return time.UnixMicro(us).UTC(), id, true
}

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.LogFilter{EventType: q.Get("event_type"), Query: q.Get("q")}
	if v := q.Get("endpoint_id"); v != "" {
		if !uuidRE.MatchString(v) {
			s.writeErr(w, invalid("log.invalid_filter"))
			return
		}
		f.EndpointID = v
	}
	if v := q.Get("status"); v != "" {
		for _, st := range strings.Split(v, ",") {
			if !logStatuses[st] {
				s.writeErr(w, invalid("log.invalid_filter"))
				return
			}
			f.Statuses = append(f.Statuses, st)
		}
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				s.writeErr(w, invalid("log.invalid_filter"))
				return
			}
			*dst = &t
		}
	}
	f.Limit = 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			s.writeErr(w, invalid("log.invalid_filter"))
			return
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		t, id, ok := decodeCursor(v)
		if !ok {
			s.writeErr(w, invalid("log.invalid_cursor"))
			return
		}
		f.BeforeTime, f.BeforeID = &t, id
	}
	logs, err := s.opts.Store.ListLogs(r.Context(), userFrom(r).ID, f)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]logJSON, len(logs))
	for i, l := range logs {
		out[i] = toLogJSON(l)
	}
	var next *string
	if len(logs) == f.Limit {
		c := encodeCursor(logs[len(logs)-1])
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": out, "next_cursor": next})
}

// renderBody shows a payload as JSON when it is JSON, as text when it is
// UTF-8, and as base64 otherwise.
func renderBody(b []byte) map[string]any { return renderBodyAs(b, false) }

// renderBodyAs renders a body; raw = always base64, byte-for-byte (the CLI
// forwards exactly what was received, so signatures over the body still verify).
func renderBodyAs(b []byte, raw bool) map[string]any {
	switch {
	case raw:
		return map[string]any{"body_base64": base64.StdEncoding.EncodeToString(b)}
	case len(b) == 0:
		return map[string]any{"body_text": ""}
	case json.Valid(b):
		return map[string]any{"body": json.RawMessage(b)}
	case utf8.Valid(b):
		return map[string]any{"body_text": string(b)}
	default:
		return map[string]any{"body_base64": base64.StdEncoding.EncodeToString(b)}
	}
}

func renderEnvelope(e queue.Envelope, raw bool) map[string]any {
	out := renderBodyAs(e.Body, raw)
	out["method"] = e.Method
	out["query"] = e.Query
	headers := e.Headers
	if headers == nil {
		headers = map[string][]string{}
	}
	out["headers"] = headers
	return out
}

func (s *Server) loadLog(r *http.Request) (store.LogDetail, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return store.LogDetail{}, errLogNotFound
	}
	d, err := s.opts.Store.GetLogDetail(r.Context(), userFrom(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return d, errLogNotFound
	}
	return d, err
}

func (s *Server) openEnvelope(ct, nonce, aad []byte) (queue.Envelope, error) {
	plain, err := s.opts.Cipher.Open(ct, nonce, aad)
	if err != nil {
		return queue.Envelope{}, err
	}
	return queue.UnmarshalEnvelope(plain)
}

// getLog is the request debugger: the original request, the request after the
// transform rules, and every delivery attempt with the destination's response.
func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadLog(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	raw := r.URL.Query().Get("raw") == "true"
	if d.KeyVersion != s.opts.Cipher.KeyVersion() {
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "log.key_unavailable"})
		return
	}
	orig, err := s.openEnvelope(d.OriginalEncrypted, d.OriginalNonce, processor.OriginalAAD(d.ID))
	if err != nil {
		s.fail(w, fmt.Errorf("decrypt original %s: %w", d.ID, err))
		return
	}
	var transformed map[string]any
	if d.TransformedEncrypted != nil {
		t, err := s.openEnvelope(d.TransformedEncrypted, d.TransformedNonce, processor.TransformedAAD(d.ID))
		if err != nil {
			s.fail(w, fmt.Errorf("decrypt transformed %s: %w", d.ID, err))
			return
		}
		transformed = renderEnvelope(t, raw)
	}

	attempts, err := s.opts.Store.ListDeliveryAttempts(r.Context(), d.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	outAttempts := make([]map[string]any, len(attempts))
	for i, a := range attempts {
		item := map[string]any{
			"number": a.AttemptNumber, "started_at": a.StartedAt.UTC(), "duration_ms": ms(a.DurationMicros),
			"status_code": a.StatusCode, "error_code": a.ErrorCode, "response": nil,
		}
		if a.ResponseEncrypted != nil && a.KeyVersion == s.opts.Cipher.KeyVersion() {
			if plain, err := s.opts.Cipher.Open(a.ResponseEncrypted, a.ResponseNonce, dispatcher.ResponseAAD(d.ID, a.AttemptNumber)); err == nil {
				var resp struct {
					Status  int                 `json:"status"`
					Headers map[string][]string `json:"headers"`
					Body    []byte              `json:"body"`
				}
				if json.Unmarshal(plain, &resp) == nil {
					rendered := renderBodyAs(resp.Body, raw)
					rendered["status"], rendered["headers"] = resp.Status, resp.Headers
					item["response"] = rendered
				}
			}
		}
		outAttempts[i] = item
	}
	replays, err := s.opts.Store.ListReplays(r.Context(), d.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"log":      toLogJSON(d.LogSummary),
		"request":  map[string]any{"original": renderEnvelope(orig, raw), "transformed": transformed},
		"attempts": outAttempts,
		"replays":  replays,
	})
}

// replayLog sends a stored webhook again, as received or with an edited body
// and/or headers. The replay is a new log (replay_of = the original) that goes
// through the whole pipeline with the endpoint's current settings: transform
// rules, outbound headers, retry policy and alerts.
func (s *Server) replayLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body     json.RawMessage      `json:"body"`
		BodyText *string              `json:"body_text"`
		Headers  *map[string][]string `json:"headers"` // full replacement
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
	}
	d, err := s.loadLog(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if d.KeyVersion != s.opts.Cipher.KeyVersion() {
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "log.key_unavailable"})
		return
	}
	env, err := s.openEnvelope(d.OriginalEncrypted, d.OriginalNonce, processor.OriginalAAD(d.ID))
	if err != nil {
		s.fail(w, fmt.Errorf("decrypt original %s: %w", d.ID, err))
		return
	}
	switch {
	case in.Body != nil && in.BodyText != nil:
		s.writeErr(w, invalid("log.invalid_replay_body"))
		return
	case in.Body != nil:
		env.Body = []byte(in.Body)
	case in.BodyText != nil:
		env.Body = []byte(*in.BodyText)
	}
	if len(env.Body) > 1<<20 {
		s.writeErr(w, invalid("log.replay_body_too_large"))
		return
	}
	if in.Headers != nil {
		for k, vs := range *in.Headers {
			for _, v := range vs {
				if !headerRE.MatchString(k) || strings.ContainsAny(v, "\r\n\x00") {
					s.writeErr(w, &apiError{Status: http.StatusUnprocessableEntity, Code: "log.invalid_replay_header", Details: map[string]string{"header": k}})
					return
				}
			}
		}
		env.Headers = *in.Headers
	}

	contentType := ""
	if v := firstHeader(env.Headers, "Content-Type"); v != "" {
		contentType = v
	} else if d.ContentType != nil {
		contentType = *d.ContentType
	}
	id, ok := s.enqueueWebhook(w, r, d.EndpointID, env, contentType, d.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "replay_of": d.ID, "status": store.LogQueued})
}

// enqueueWebhook checks the rate limit and the quota, encrypts the request and
// publishes it to the ingest stream, exactly like the ingest service does, so
// replays and test events go through the whole pipeline. It writes the error
// response itself and reports whether the webhook was queued.
func (s *Server) enqueueWebhook(w http.ResponseWriter, r *http.Request, endpointID string, env queue.Envelope, contentType, replayOf string) (string, bool) {
	u := userFrom(r)
	if s.opts.Limiter != nil {
		decision, _, err := s.opts.Limiter.Check(r.Context(), endpointID, u.ID, u.PlanType)
		if err != nil {
			s.fail(w, err)
			return "", false
		}
		switch decision {
		case limits.RateLimited:
			w.Header().Set("Retry-After", "1")
			s.writeErr(w, &apiError{Status: http.StatusTooManyRequests, Code: "rate_limited"})
			return "", false
		case limits.QuotaExceeded:
			s.writeErr(w, &apiError{Status: http.StatusTooManyRequests, Code: "quota_exceeded"})
			return "", false
		}
	}

	id := uuid.NewV7()
	plain, err := env.Marshal()
	if err != nil {
		s.fail(w, err)
		return "", false
	}
	ct, nonce, err := s.opts.Cipher.Seal(plain, queue.IngestAAD(id))
	if err != nil {
		s.fail(w, err)
		return "", false
	}
	if _, err := queue.NewProducer(s.opts.Redis, s.opts.Keys).PublishIngest(r.Context(), queue.IngestMessage{
		ID: id, EndpointID: endpointID, UserID: u.ID, KeyVersion: s.opts.Cipher.KeyVersion(),
		Ciphertext: ct, Nonce: nonce, ReceivedAt: time.Now().UTC(), SizeBytes: len(env.Body),
		ContentType: contentType, Method: env.Method, ReplayOf: replayOf,
	}); err != nil {
		s.fail(w, err)
		return "", false
	}
	return id, true
}

// testEndpoint sends a test webhook to an endpoint from the dashboard (the
// sandbox and the onboarding): it skips the inbound authentication — the caller
// is already signed in — and otherwise goes through the whole pipeline like a
// real webhook. Without a body it sends a small JSON sample.
func (s *Server) testEndpoint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method   string               `json:"method"`
		Body     json.RawMessage      `json:"body"`
		BodyText *string              `json:"body_text"`
		Headers  *map[string][]string `json:"headers"`
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
	}
	e, err := s.loadEndpoint(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if e.Status == store.StatusPaused {
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "endpoint.paused"})
		return
	}
	env := queue.Envelope{Method: http.MethodPost, Headers: map[string][]string{
		"Content-Type": {"application/json"},
		"User-Agent":   {"HookShield-Test/1"},
	}}
	switch in.Method {
	case "":
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		env.Method = in.Method
	default:
		s.writeErr(w, invalid("endpoint.invalid_test_method"))
		return
	}
	switch {
	case in.Body != nil && in.BodyText != nil:
		s.writeErr(w, invalid("endpoint.invalid_test_body"))
		return
	case in.Body != nil:
		env.Body = []byte(in.Body)
	case in.BodyText != nil:
		env.Body = []byte(*in.BodyText)
	default:
		env.Body, _ = json.Marshal(map[string]any{
			"type": "hookshield.test", "message": "Hello from HookShield", "sent_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
	if len(env.Body) > 1<<20 {
		s.writeErr(w, invalid("endpoint.test_body_too_large"))
		return
	}
	if in.Headers != nil {
		for k, vs := range *in.Headers {
			for _, v := range vs {
				if !headerRE.MatchString(k) || strings.ContainsAny(v, "\r\n\x00") {
					s.writeErr(w, &apiError{Status: http.StatusUnprocessableEntity, Code: "endpoint.invalid_test_header", Details: map[string]string{"header": k}})
					return
				}
			}
		}
		env.Headers = *in.Headers
	}
	id, ok := s.enqueueWebhook(w, r, e.ID, env, firstHeader(env.Headers, "Content-Type"), "")
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": store.LogQueued})
}

func firstHeader(h map[string][]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// ── metrics ───────────────────────────────────────────────

// metricRanges maps ?range= to the window and the chart bucket.
var metricRanges = map[string]struct{ window, bucket time.Duration }{
	"1h":  {time.Hour, time.Minute},
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"30d": {30 * 24 * time.Hour, 24 * time.Hour},
}

func (s *Server) metricsWindow(r *http.Request) (string, time.Time, time.Duration, string, error) {
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "24h"
	}
	rg, ok := metricRanges[name]
	if !ok {
		return "", time.Time{}, 0, "", invalid("metrics.invalid_range")
	}
	ep := r.URL.Query().Get("endpoint_id")
	if ep != "" && !uuidRE.MatchString(ep) {
		return "", time.Time{}, 0, "", invalid("metrics.invalid_endpoint")
	}
	return name, time.Now().Add(-rg.window), rg.bucket, ep, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func (s *Server) metricsSummary(w http.ResponseWriter, r *http.Request) {
	name, since, _, ep, err := s.metricsWindow(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	m, err := s.opts.Store.GetMetricsSummary(r.Context(), userFrom(r).ID, ep, since)
	if err != nil {
		s.fail(w, err)
		return
	}
	var rate *float64
	if done := m.Delivered + m.Failed; done > 0 {
		v := round2(float64(m.Delivered) * 100 / float64(done))
		rate = &v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range": name, "since": since.UTC(),
		"total_received": m.Received, "delivered": m.Delivered, "failed": m.Failed, "in_flight": m.InFlight,
		"active_retries": m.Retrying, "delivery_rate": rate,
		"avg_latency_ms": round2(m.AvgGatewayUs / 1000), "p95_latency_ms": round2(m.P95GatewayUs / 1000),
		"avg_delivery_ms": round2(m.AvgDeliverUs / 1000),
	})
}

func (s *Server) metricsSeries(w http.ResponseWriter, r *http.Request) {
	name, since, bucket, ep, err := s.metricsWindow(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	pts, err := s.opts.Store.GetMetricsSeries(r.Context(), userFrom(r).ID, ep, since, bucket)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, len(pts))
	for i, p := range pts {
		var errRate *float64
		if done := p.Delivered + p.Failed; done > 0 {
			v := round2(float64(p.Failed) * 100 / float64(done))
			errRate = &v
		}
		out[i] = map[string]any{"at": p.Bucket.UTC(), "received": p.Received, "delivered": p.Delivered,
			"failed": p.Failed, "error_rate": errRate, "avg_latency_ms": round2(p.AvgGatewayUs / 1000)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": name, "bucket_seconds": int(bucket.Seconds()), "points": out})
}

func (s *Server) metricsEndpoints(w http.ResponseWriter, r *http.Request) {
	name, since, _, _, err := s.metricsWindow(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	stats, err := s.opts.Store.GetEndpointStats(r.Context(), userFrom(r).ID, since)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, len(stats))
	for i, st := range stats {
		var rate *float64
		if done := st.Delivered + st.Failed; done > 0 {
			v := round2(float64(st.Delivered) * 100 / float64(done))
			rate = &v
		}
		out[i] = map[string]any{"endpoint_id": st.EndpointID, "received": st.Received, "delivered": st.Delivered,
			"failed": st.Failed, "success_rate": rate, "last_received_at": utcPtr(st.LastReceived)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": name, "endpoints": out})
}

// status reports the health of the shared infrastructure (cluster status widget).
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	up := func(err error) string {
		if err != nil {
			return "down"
		}
		return "up"
	}
	dbErr := s.opts.Store.Pool().Ping(ctx)
	redisErr := s.opts.Redis.Ping(ctx).Err()
	queues := map[string]any{}
	if redisErr == nil && !s.opts.MultiTenant {
		for name, stream := range map[string]string{
			"ingest": s.opts.Keys.IngestStream(), "dispatch": s.opts.Keys.DispatchStream(), "alerts": s.opts.Keys.AlertStream(),
		} {
			queues[name] = streamStats(ctx, s.opts.Redis, stream)
		}
		queues["retry_scheduled"] = s.opts.Redis.ZCard(ctx, s.opts.Keys.RetrySchedule()).Val()
		queues["dead_letters"] = s.opts.Redis.XLen(ctx, s.opts.Keys.DeadLetterStream()).Val()
	}
	overall := "operational"
	if dbErr != nil || redisErr != nil {
		overall = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": overall, "database": up(dbErr), "redis": up(redisErr), "queues": queues, "checked_at": time.Now().UTC(),
	})
}

// streamStats: entries waiting (lag), being processed (pending) and active consumers.
func streamStats(ctx context.Context, rdb *redis.Client, stream string) map[string]int64 {
	out := map[string]int64{"length": 0, "waiting": 0, "pending": 0, "consumers": 0}
	out["length"] = rdb.XLen(ctx, stream).Val()
	groups, err := rdb.XInfoGroups(ctx, stream).Result()
	if err != nil {
		return out
	}
	for _, g := range groups {
		out["pending"] += g.Pending
		out["consumers"] += g.Consumers
		if g.Lag > 0 {
			out["waiting"] += g.Lag
		}
	}
	return out
}

// ── search ────────────────────────────────────────────────

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		s.writeErr(w, invalid("search.invalid_query"))
		return
	}
	res, err := s.opts.Store.Search(r.Context(), userFrom(r).ID, q, 5)
	if err != nil {
		s.fail(w, err)
		return
	}
	eps := make([]map[string]any, len(res.Endpoints))
	for i, e := range res.Endpoints {
		eps[i] = map[string]any{"id": e.ID, "name": e.Name, "slug": e.Slug, "status": e.Status}
	}
	logs := make([]logJSON, len(res.Logs))
	for i, l := range res.Logs {
		logs[i] = toLogJSON(l)
	}
	secrets := make([]secretJSON, len(res.Secrets))
	for i, sec := range res.Secrets {
		secrets[i] = toSecretJSON(sec)
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": eps, "logs": logs, "secrets": secrets})
}

// ── live feed (Server-Sent Events) ────────────────────────

// liveFeed streams log events of the account: "log.created" when a webhook is
// stored, "log.updated" after each delivery attempt. Optional ?endpoint_id=.
// Events carry no payload data; the client fetches details when needed.
func (s *Server) liveFeed(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("endpoint_id")
	if filter != "" && !uuidRE.MatchString(filter) {
		s.writeErr(w, invalid("live.invalid_endpoint"))
		return
	}
	ctx := r.Context()
	events, err := live.Subscribe(ctx, s.opts.Redis, s.opts.Keys, userFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the stream outlives the server's write timeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // nginx/Caddy: do not buffer
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	_ = rc.Flush()

	ping := time.NewTicker(s.opts.LivePing)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closing:
			return
		case <-ping.C:
			if !s.stillAuthorized(r) {
				return // signed out, password reset or key revoked since the feed opened
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case msg, ok := <-events:
			if !ok {
				return
			}
			var ev live.Event
			if json.Unmarshal(msg, &ev) != nil || (filter != "" && ev.EndpointID != filter) {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", ev.Type, ev.LogID, msg); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}
