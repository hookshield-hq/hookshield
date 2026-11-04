package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

// ParseForward accepts "3000", ":3000", "localhost:3000/hooks" or a full URL.
func ParseForward(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("--forward is required (e.g. --forward localhost:3000/webhooks)")
	}
	// "3000", ":3000", "3000/webhooks" → localhost:3000…
	first, rest, _ := strings.Cut(strings.TrimPrefix(s, ":"), "/")
	if _, err := strconv.Atoi(first); err == nil && !strings.Contains(s, "://") {
		s = "localhost:" + first
		if rest != "" {
			s += "/" + rest
		}
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("--forward %q is not an http(s) URL", s)
	}
	return u, nil
}

// Listener forwards the webhooks of an account (or one endpoint) to a local URL.
//
// It follows the API's live feed (GET /v1/live, Server-Sent Events). For each
// new log it fetches the stored request (GET /v1/logs/{id}?raw=true — the
// body byte for byte) and sends it to Forward the way the dispatcher would:
// same method, query, headers and body, plus the X-HookShield-* headers. The
// feed is best-effort, so after every (re)connection it also reads the logs
// received since the last one it saw. Webhooks are forwarded one at a time,
// in order.
type Listener struct {
	Client     *Client
	Endpoint   *Endpoint // nil = every endpoint of the account
	Forward    *url.URL
	Original   bool // forward the request as received instead of after transform rules
	PrintBody  bool // print the local response body
	SkipVerify bool // accept self-signed certificates of the local app
	Out        io.Writer
	Color      bool

	// Now and the retry delays are overridable in tests.
	Now        func() time.Time
	RetryDelay func(attempt int) time.Duration

	http      *http.Client
	stream    *http.Client
	seen      map[string]bool
	seenOrder []string
	lastAt    time.Time
	eps       map[string]epCache
	warned    map[string]bool
	mu        sync.Mutex
}

type epCache struct {
	ep  Endpoint
	at  time.Time
	err error
}

// Run forwards webhooks until ctx is done.
func (l *Listener) Run(ctx context.Context) error {
	l.init()
	// Start after the newest existing log: only webhooks from now on are forwarded.
	recent, err := l.recentLogs(ctx, time.Time{}, 1)
	if err != nil {
		return err
	}
	if len(recent) > 0 {
		l.markSeen(recent[0].ID)
		l.lastAt = recent[0].ReceivedAt
	} else {
		l.lastAt = l.Now().Add(-time.Second)
	}

	failures := 0
	for reconnecting := false; ctx.Err() == nil; reconnecting = true {
		connected, err := l.follow(ctx, reconnecting)
		if ctx.Err() != nil {
			return nil
		}
		if IsUnauthorized(err) {
			return err
		}
		if connected {
			failures = 0
		}
		delay := l.RetryDelay(failures)
		failures++
		l.printf("%s  %s\n", l.dim(l.clock()), l.yellow(fmt.Sprintf("connection lost (%v) — reconnecting in %s", shortErr(err), delay)))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
	return nil
}

func (l *Listener) init() {
	if l.Now == nil {
		l.Now = time.Now
	}
	if l.RetryDelay == nil {
		l.RetryDelay = func(attempt int) time.Duration {
			d := time.Second << min(attempt, 5) // 1s … 32s
			return min(d, 30*time.Second)
		}
	}
	l.http = &http.Client{
		Timeout: 30 * time.Second,
		// Like the dispatcher: redirects are answers, not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if l.SkipVerify {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for local apps with self-signed certificates
		l.http.Transport = tr
	}
	l.stream = &http.Client{} // no timeout: the feed stays open
	l.seen = map[string]bool{}
	l.eps = map[string]epCache{}
	l.warned = map[string]bool{}
}

// follow opens the live feed and handles events until it ends.
func (l *Listener) follow(ctx context.Context, reconnect bool) (connected bool, err error) {
	q := url.Values{}
	if l.Endpoint != nil {
		q.Set("endpoint_id", l.Endpoint.ID)
	}
	u := l.Client.BaseURL + "/v1/live"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	l.Client.authorize(req)
	req.Header.Set("Accept", "text/event-stream")
	res, err := l.stream.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, decode(res, nil)
	}
	if reconnect {
		l.printf("%s  %s\n", l.dim(l.clock()), l.green("reconnected"))
	}

	// Pick up what arrived while the feed was not connected (and the gap before the first connection).
	if err := l.catchUp(ctx); err != nil {
		return true, err
	}

	r := bufio.NewReader(res.Body)
	var event, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				err = errors.New("feed closed by the server")
			}
			return true, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if event == "log.created" && data != "" {
				var ev struct {
					LogID string `json:"log_id"`
				}
				if json.Unmarshal([]byte(data), &ev) == nil && ev.LogID != "" {
					l.handle(ctx, ev.LogID)
				}
			}
			event, data = "", ""
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}

type logSummary struct {
	ID       string `json:"id"`
	Endpoint struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"endpoint"`
	ReplayOf   *string   `json:"replay_of"`
	Method     string    `json:"method"`
	EventType  *string   `json:"event_type"`
	ReceivedAt time.Time `json:"received_at"`
}

func (l *Listener) recentLogs(ctx context.Context, from time.Time, limit int) ([]logSummary, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if l.Endpoint != nil {
		q.Set("endpoint_id", l.Endpoint.ID)
	}
	if !from.IsZero() {
		q.Set("from", from.UTC().Format(time.RFC3339Nano))
	}
	var out struct {
		Logs []logSummary `json:"logs"`
	}
	err := l.Client.Do(ctx, http.MethodGet, "/v1/logs", q, nil, &out)
	return out.Logs, err
}

// catchUp forwards the logs received since the last one seen, oldest first.
func (l *Listener) catchUp(ctx context.Context) error {
	logs, err := l.recentLogs(ctx, l.lastAt, 200)
	if err != nil {
		return err
	}
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].ReceivedAt.Equal(logs[j].ReceivedAt) {
			return logs[i].ID < logs[j].ID
		}
		return logs[i].ReceivedAt.Before(logs[j].ReceivedAt)
	})
	for _, lg := range logs {
		l.handle(ctx, lg.ID)
	}
	return nil
}

func (l *Listener) markSeen(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[id] {
		return false
	}
	l.seen[id] = true
	l.seenOrder = append(l.seenOrder, id)
	if len(l.seenOrder) > 2000 {
		delete(l.seen, l.seenOrder[0])
		l.seenOrder = l.seenOrder[1:]
	}
	return true
}

func (l *Listener) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, id)
}

type storedRequest struct {
	Method     string              `json:"method"`
	Query      string              `json:"query"`
	Headers    map[string][]string `json:"headers"`
	BodyBase64 string              `json:"body_base64"`
}

// handle forwards one log (once).
func (l *Listener) handle(ctx context.Context, logID string) {
	if !l.markSeen(logID) {
		return
	}
	var d struct {
		Log     logSummary `json:"log"`
		Request struct {
			Original    storedRequest  `json:"original"`
			Transformed *storedRequest `json:"transformed"`
		} `json:"request"`
	}
	if err := l.Client.Do(ctx, http.MethodGet, "/v1/logs/"+url.PathEscape(logID), url.Values{"raw": {"true"}}, nil, &d); err != nil {
		l.forget(logID) // the catch-up after the next reconnection tries again
		if ctx.Err() == nil {
			l.printf("%s  %s %s\n", l.dim(l.clock()), l.red("✗"), fmt.Sprintf("could not read webhook %s: %v", logID, err))
		}
		return
	}
	if d.Log.ReceivedAt.After(l.lastAt) {
		l.lastAt = d.Log.ReceivedAt
	}
	sr := d.Request.Original
	if !l.Original && d.Request.Transformed != nil {
		sr = *d.Request.Transformed
	}

	label := "webhook"
	if d.Log.EventType != nil && *d.Log.EventType != "" {
		label = *d.Log.EventType
	}
	if d.Log.ReplayOf != nil {
		label += " (replay)"
	}
	prefix := fmt.Sprintf("%s  %s %s %s", l.dim(l.clock()), l.cyan("-->"), l.bold(label), l.dim("["+d.Log.Endpoint.Slug+" "+shortID(logID)+"]"))

	req, err := l.buildRequest(ctx, d.Log, sr)
	if err != nil {
		l.printf("%s  %s %v\n", prefix, l.red("✗"), err)
		return
	}
	start := l.Now()
	res, err := l.http.Do(req.WithContext(ctx))
	elapsed := l.Now().Sub(start)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.printf("%s  %s %s\n", prefix, l.red("<-- error:"), l.localError(err))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	status := fmt.Sprintf("%d %s", res.StatusCode, http.StatusText(res.StatusCode))
	color := l.green
	if res.StatusCode >= 300 {
		color = l.yellow
	}
	if res.StatusCode >= 400 {
		color = l.red
	}
	l.printf("%s  %s %s\n", prefix, color("<-- "+status), l.dim(fmt.Sprintf("(%d ms)", elapsed.Milliseconds())))
	if l.PrintBody && len(body) > 0 {
		b := strings.TrimSpace(string(body))
		if len(b) > 2048 {
			b = b[:2048] + "…"
		}
		l.printf("%s\n", indent(b, "      "))
	}
}

// hopHeaders are never forwarded (the same list as the dispatcher's).
var hopHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Keep-Alive": true, "Te": true,
	"Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Proxy-Authorization": true,
	"Proxy-Connection": true, "Accept-Encoding": true,
}

func (l *Listener) buildRequest(ctx context.Context, lg logSummary, sr storedRequest) (*http.Request, error) {
	body, err := base64.StdEncoding.DecodeString(sr.BodyBase64)
	if err != nil {
		return nil, fmt.Errorf("unreadable body: %w", err)
	}
	target := *l.Forward
	if sr.Query != "" {
		if target.RawQuery == "" {
			target.RawQuery = sr.Query
		} else {
			target.RawQuery += "&" + sr.Query
		}
	}
	method := sr.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequest(method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range sr.Headers {
		ck := http.CanonicalHeaderKey(k)
		if hopHeaders[ck] || strings.HasPrefix(ck, "X-Hookshield-") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(ck, v)
		}
	}
	// Static outbound headers of the endpoint. Values with {{secrets.…}} stay on
	// the server: secrets are never sent to the CLI.
	if ep, err := l.endpoint(ctx, lg.Endpoint.ID); err == nil {
		for k, v := range ep.OutboundHeaders {
			if strings.Contains(v, "{{") {
				if !l.warned[ep.ID+k] {
					l.warned[ep.ID+k] = true
					l.printf("%s  %s\n", l.dim(l.clock()), l.yellow(fmt.Sprintf("note: outbound header %s of %s uses a secret — not sent by the CLI", k, ep.Slug)))
				}
				continue
			}
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("User-Agent", "HookShield-CLI/"+version.Version)
	req.Header.Set("X-HookShield-Id", lg.ID)
	req.Header.Set("X-HookShield-Timestamp", strconv.FormatInt(l.Now().Unix(), 10))
	req.Header.Set("X-HookShield-Forwarded-By", "hookshield-cli")
	if lg.EventType != nil && *lg.EventType != "" {
		req.Header.Set("X-HookShield-Event", *lg.EventType)
	}
	return req, nil
}

// endpoint returns the endpoint configuration, cached for 30 s.
func (l *Listener) endpoint(ctx context.Context, id string) (Endpoint, error) {
	if c, ok := l.eps[id]; ok && l.Now().Sub(c.at) < 30*time.Second {
		return c.ep, c.err
	}
	ep, err := l.Client.Endpoint(ctx, id)
	l.eps[id] = epCache{ep: ep, at: l.Now(), err: err}
	return ep, err
}

func (l *Listener) localError(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("connection refused — is your app running on %s?", l.Forward.Host)
	case isTimeout(err):
		return "no answer within 30 s"
	}
	return shortErr(err)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func shortErr(err error) string {
	if err == nil {
		return "closed"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func shortID(id string) string {
	if len(id) > 13 {
		return id[:8] + "…" + id[len(id)-4:]
	}
	return id
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

func (l *Listener) clock() string { return l.Now().Format("15:04:05") }

func (l *Listener) printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.Out, format, a...)
}

func (l *Listener) paint(code, s string) string {
	if !l.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (l *Listener) dim(s string) string    { return l.paint("2", s) }
func (l *Listener) bold(s string) string   { return l.paint("1", s) }
func (l *Listener) red(s string) string    { return l.paint("31", s) }
func (l *Listener) green(s string) string  { return l.paint("32", s) }
func (l *Listener) yellow(s string) string { return l.paint("33", s) }
func (l *Listener) cyan(s string) string   { return l.paint("36", s) }
