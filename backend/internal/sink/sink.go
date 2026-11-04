// Package sink is a tiny webhook receiver for local development and demos
// (`hookshield sink`). It prints every delivery, answers with a configurable
// status and can fail the first N requests to show retries in action.
//
// It also exposes GET /_sink/stats so scripts (make demo / make smoke) can
// check that a webhook really arrived.
package sink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configure the sink.
type Options struct {
	Status    int // response status after the first FailFirst requests (default 200)
	FailFirst int // answer 503 to the first N requests
	Quiet     bool
	Out       io.Writer
	Now       func() time.Time
}

// Received is a summary of one request.
type Received struct {
	At      time.Time `json:"at"`
	Method  string    `json:"method"`
	Path    string    `json:"path"`
	ID      string    `json:"id"`
	Attempt string    `json:"attempt"`
	Event   string    `json:"event"`
	Status  int       `json:"status"`
	Body    string    `json:"body"`
}

// Stats is returned by GET /_sink/stats.
type Stats struct {
	Received int        `json:"received"`
	Last     []Received `json:"last"`
}

// Sink is an http.Handler.
type Sink struct {
	opts Options
	mu   sync.Mutex
	n    int
	last []Received
}

// New returns a Sink.
func New(opts Options) *Sink {
	if opts.Status == 0 {
		opts.Status = http.StatusOK
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Sink{opts: opts}
}

const keepLast = 20

func (s *Sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/_sink/stats" {
		s.mu.Lock()
		st := Stats{Received: s.n, Last: append([]Received(nil), s.last...)}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))

	s.mu.Lock()
	s.n++
	n := s.n
	status := s.opts.Status
	if n <= s.opts.FailFirst {
		status = http.StatusServiceUnavailable
	}
	rec := Received{
		At: s.opts.Now(), Method: r.Method, Path: r.URL.RequestURI(),
		ID: r.Header.Get("X-HookShield-Id"), Attempt: r.Header.Get("X-HookShield-Attempt"),
		Event: r.Header.Get("X-HookShield-Event"), Status: status, Body: string(body),
	}
	s.last = append(s.last, rec)
	if len(s.last) > keepLast {
		s.last = s.last[len(s.last)-keepLast:]
	}
	s.mu.Unlock()

	if !s.opts.Quiet {
		s.print(n, rec, r.Header)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"received":true,"n":%d}`, n)
}

func (s *Sink) print(n int, rec Received, h http.Header) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n── #%d  %s  %s %s  → %d\n", n, rec.At.Format("15:04:05.000"), rec.Method, rec.Path, rec.Status)
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "X-Hookshield-") || k == "Content-Type" || k == "Authorization" {
			v := strings.Join(h[k], ", ")
			if k == "Authorization" && len(v) > 12 {
				v = v[:12] + "…" // never print full credentials
			}
			fmt.Fprintf(&b, "   %s: %s\n", k, v)
		}
	}
	body := rec.Body
	var pretty bytes.Buffer
	if json.Indent(&pretty, []byte(body), "   ", "  ") == nil {
		body = pretty.String()
	}
	if len(body) > 2000 {
		body = body[:2000] + "…"
	}
	fmt.Fprintf(&b, "   %s\n", body)
	fmt.Fprint(s.opts.Out, b.String())
}
