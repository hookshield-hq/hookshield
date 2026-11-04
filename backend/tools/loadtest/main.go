// Command loadtest measures HookShield under load: how fast ingest accepts
// webhooks (the latency senders see) and, with -sink, how long a webhook takes
// from the sender to the destination (end to end, through processor and
// dispatcher).
//
//	go run ./tools/loadtest -url http://localhost:8080/v1/ep_x -token hs_in_… -c 16 -d 30s
//	go run ./tools/loadtest -url … -token … -sink :4300     # endpoint target = http://<this host>:4300/
//
// It is a development tool (not shipped in the images). Results and method:
// docs/PERFORMANCE.md.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "", "ingest URL of the endpoint, e.g. http://localhost:8080/v1/ep_x (required)")
	token := flag.String("token", "", "endpoint token (hs_in_…)")
	header := flag.String("header", "X-HookShield-Token", "header that carries the token")
	conc := flag.Int("c", 16, "concurrent senders")
	dur := flag.Duration("d", 20*time.Second, "test duration")
	rps := flag.Int("rps", 0, "total requests per second (0 = as fast as possible)")
	size := flag.Int("size", 512, "approximate body size in bytes")
	sinkAddr := flag.String("sink", "", "also run a destination on this address and measure end-to-end latency (the endpoint must target it)")
	drain := flag.Duration("drain", 30*time.Second, "with -sink: how long to wait for deliveries after sending")
	warmup := flag.Int("warmup", 50, "requests sent (and not measured) before the test")
	flag.Parse()
	if *target == "" {
		fmt.Fprintln(os.Stderr, "loadtest: -url is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var sink *sinkServer
	if *sinkAddr != "" {
		sink = startSink(*sinkAddr)
	}

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		MaxIdleConns: *conc * 2, MaxIdleConnsPerHost: *conc * 2, IdleConnTimeout: 90 * time.Second,
	}}
	padding := strings.Repeat("x", max(*size-120, 0))
	var seq atomic.Int64
	send := func(measured bool) (time.Duration, int, error) {
		n := seq.Add(1)
		body := fmt.Sprintf(`{"type":"load.test","seq":%d,"measured":%t,"sent_ns":%d,"pad":%q}`, n, measured, time.Now().UnixNano(), padding)
		req, _ := http.NewRequest(http.MethodPost, *target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if *token != "" {
			req.Header.Set(*header, *token)
		}
		start := time.Now()
		res, err := client.Do(req)
		if err != nil {
			return 0, 0, err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return time.Since(start), res.StatusCode, nil
	}

	for i := 0; i < *warmup; i++ {
		if _, code, err := send(false); err != nil || code != http.StatusAccepted {
			fmt.Fprintf(os.Stderr, "loadtest: warm-up request failed (status %d, %v) — check -url and -token\n", code, err)
			os.Exit(1)
		}
	}
	if sink != nil {
		time.Sleep(2 * time.Second) // let warm-up deliveries land
		sink.reset()
	}

	var (
		mu        sync.Mutex
		latencies = make([]time.Duration, 0, 1<<16)
		statuses  = map[int]int{}
		errs      = map[string]int{}
	)
	var tick <-chan time.Time
	if *rps > 0 {
		t := time.NewTicker(time.Second / time.Duration(*rps))
		defer t.Stop()
		tick = t.C
	}
	runCtx, cancel := context.WithTimeout(ctx, *dur)
	defer cancel()
	began := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for runCtx.Err() == nil {
				if tick != nil {
					select {
					case <-tick:
					case <-runCtx.Done():
						return
					}
				}
				d, code, err := send(true)
				mu.Lock()
				if err != nil {
					errs[shorten(err.Error())]++
				} else {
					statuses[code]++
					if code == http.StatusAccepted {
						latencies = append(latencies, d)
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(began)

	total := len(latencies)
	for _, n := range errs {
		total += n
	}
	for code, n := range statuses {
		if code != http.StatusAccepted {
			total += n
		}
	}
	fmt.Printf("\nIngest — %d senders, %s, body ≈ %d B\n", *conc, elapsed.Round(time.Millisecond), *size)
	fmt.Printf("  requests   %d (%.0f/s)\n", total, float64(total)/elapsed.Seconds())
	fmt.Printf("  accepted   %d   statuses %v   errors %v\n", len(latencies), statuses, errs)
	printPercentiles("  latency   ", latencies)

	if sink != nil {
		accepted := len(latencies)
		fmt.Printf("\nEnd to end — waiting up to %s for %d deliveries…\n", *drain, accepted)
		deadline := time.Now().Add(*drain)
		for sink.count() < accepted && time.Now().Before(deadline) && ctx.Err() == nil {
			time.Sleep(200 * time.Millisecond)
		}
		e2e := sink.latencies()
		fmt.Printf("  delivered  %d of %d (duplicates %d)\n", len(e2e), accepted, sink.duplicates())
		printPercentiles("  latency   ", e2e)
	}
}

func printPercentiles(prefix string, ds []time.Duration) {
	if len(ds) == 0 {
		fmt.Println(prefix + "no samples")
		return
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	p := func(q float64) time.Duration {
		i := int(q * float64(len(ds)-1))
		return ds[i]
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000) }
	fmt.Printf("%s p50 %s · p90 %s · p99 %s · p99.9 %s · max %s\n", prefix, ms(p(0.50)), ms(p(0.90)), ms(p(0.99)), ms(p(0.999)), ms(ds[len(ds)-1]))
}

func shorten(s string) string {
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// sinkServer is the destination: it records how long each measured webhook
// took from the sender to here.
type sinkServer struct {
	mu   sync.Mutex
	seen map[int64]bool
	lat  []time.Duration
	dups int
}

func startSink(addr string) *sinkServer {
	s := &sinkServer{seen: map[int64]bool{}}
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		var m struct {
			Seq      int64 `json:"seq"`
			Measured bool  `json:"measured"`
			SentNS   int64 `json:"sent_ns"`
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err := json.NewDecoder(bytes.NewReader(b)).Decode(&m); err == nil && m.Measured {
			s.mu.Lock()
			if s.seen[m.Seq] {
				s.dups++
			} else {
				s.seen[m.Seq] = true
				s.lat = append(s.lat, now.Sub(time.Unix(0, m.SentNS)))
			}
			s.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "loadtest: sink: %v\n", err)
			os.Exit(1)
		}
	}()
	return s
}

func (s *sinkServer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen, s.lat, s.dups = map[int64]bool{}, nil, 0
}

func (s *sinkServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lat)
}

func (s *sinkServer) duplicates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dups
}

func (s *sinkServer) latencies() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.lat...)
}
