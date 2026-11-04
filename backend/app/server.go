package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/config"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

// Internal addresses of the all-in-one server (loopback only).
const (
	serverIngestAddr = "127.0.0.1:18080"
	serverAPIAddr    = "127.0.0.1:18081"
	serverWorkerAddr = "127.0.0.1:18082"
	serverWebAddr    = "127.0.0.1:18083"
)

// IngestPathPrefix is where the all-in-one server (and the production
// Caddyfile) exposes ingest: {public URL}/in/v1/{slug}.
const IngestPathPrefix = "/in"

// Proxies trusted to set X-Forwarded-For by default in the all-in-one server
// (HS_TRUSTED_PROXIES overrides): loopback — its own proxy — and, only on
// platforms whose load balancer reaches the container from a private address
// (Render, Railway), private networks. Trusting private networks everywhere
// would let any host on the same private network (a VPC neighbour, a container
// on a shared Docker network) claim any client IP and bypass IP allow-lists.
const (
	loopbackProxies = "127.0.0.0/8,::1/128"
	platformProxies = loopbackProxies + ",10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fc00::/7"
)

func defaultTrustedProxies(getenv func(string) string) string {
	if getenv("RENDER") != "" || getenv("RAILWAY_ENVIRONMENT_NAME") != "" || getenv("RAILWAY_ENVIRONMENT") != "" || getenv("RAILWAY_PUBLIC_DOMAIN") != "" {
		return platformProxies
	}
	return loopbackProxies
}

// Server runs HookShield as one process behind one public port — API,
// ingest, worker and the dashboard — for one-click platforms (Render,
// Railway, Coolify) where wiring four services together is the hard part.
//
//	{public URL}/v1/…      → API
//	{public URL}/in/v1/…   → ingest
//	{public URL}/…         → dashboard (Next.js, started as a child process)
//	{public URL}/healthz   → liveness · /readyz → readiness of every part
//
// The public URL is HS_PUBLIC_URL, else the platform's own URL
// (RENDER_EXTERNAL_URL, RAILWAY_PUBLIC_DOMAIN), else http://localhost:{port}.
// It listens on $PORT (default 8000). The dashboard is the Next.js
// standalone build in HS_WEB_DIR (default /app/web), or HS_WEB_UPSTREAM when
// it runs elsewhere (development).
func Server(ctx context.Context, opts Options) error {
	port := envOr("PORT", "8000")
	public := resolvePublicURL(os.Getenv, port)
	setDefault := func(k, v string) {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	os.Setenv("HS_PUBLIC_URL", public)
	os.Setenv("HS_INGEST_ADDR", serverIngestAddr)
	os.Setenv("HS_API_ADDR", serverAPIAddr)
	os.Setenv("HS_WORKER_HEALTH_ADDR", serverWorkerAddr)
	setDefault("HS_TRUSTED_PROXIES", defaultTrustedProxies(os.Getenv))

	cfg, err := config.Load(config.Requirement{Database: true, Redis: true})
	if err != nil {
		return err
	}
	log := cfg.Logger("server")
	if cfg.EncryptionKey == nil && cfg.EncryptionKeyFile == "" {
		return errors.New("HS_ENCRYPTION_KEY is required (generate with: openssl rand -base64 32), or set HS_ENCRYPTION_KEY_FILE to have it generated on first start")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	fail := func(part string, err error) {
		mu.Lock()
		if firstErr == nil && err != nil && ctx.Err() == nil {
			firstErr = fmt.Errorf("%s: %w", part, err)
		}
		mu.Unlock()
		cancel() // one part down = the whole server restarts (the platform restarts it)
	}
	run := func(part string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fn(ctx)
			if err == nil && ctx.Err() == nil {
				err = errors.New("stopped unexpectedly")
			}
			fail(part, err)
		}()
	}

	run("api", func(ctx context.Context) error { return API(ctx, nil, os.Stdout, opts) })
	run("ingest", func(ctx context.Context) error { return Ingest(ctx, opts) })
	run("worker", func(ctx context.Context) error { return Worker(ctx, opts) })

	webTarget := os.Getenv("HS_WEB_UPSTREAM")
	if webTarget == "" {
		dir := envOr("HS_WEB_DIR", "/app/web")
		if _, err := os.Stat(filepath.Join(dir, "server.js")); err != nil {
			cancel()
			wg.Wait()
			return fmt.Errorf("dashboard not found: %s/server.js (set HS_WEB_DIR to the Next.js standalone build, or HS_WEB_UPSTREAM to a running dashboard)", dir)
		}
		webTarget = "http://" + serverWebAddr
		run("web", func(ctx context.Context) error { return runWeb(ctx, dir, public, log) })
	}

	handler, err := newServerHandler(serverRoutes{
		API:    "http://" + serverAPIAddr,
		Ingest: "http://" + serverIngestAddr,
		Worker: "http://" + serverWorkerAddr,
		Web:    webTarget,
	}, log)
	if err != nil {
		cancel()
		wg.Wait()
		return err
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// No WriteTimeout: the live feed (SSE) streams for as long as the dashboard is open.
		IdleTimeout:    90 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	log.Info("HookShield starting", "url", public, "port", port, "ingest_url", public+IngestPathPrefix+"/v1/{slug}", "version", version.String())
	run("http", func(ctx context.Context) error { return httpx.Serve(ctx, srv, log) })

	wg.Wait()
	return firstErr
}

// resolvePublicURL picks the URL users reach this install at.
func resolvePublicURL(getenv func(string) string, port string) string {
	for _, v := range []string{getenv("HS_PUBLIC_URL"), getenv("RENDER_EXTERNAL_URL")} {
		if v = strings.TrimRight(strings.TrimSpace(v), "/"); v != "" {
			return v
		}
	}
	if d := strings.TrimSpace(getenv("RAILWAY_PUBLIC_DOMAIN")); d != "" {
		return "https://" + strings.TrimRight(d, "/")
	}
	return "http://localhost:" + port
}

type serverRoutes struct{ API, Ingest, Worker, Web string }

// newServerHandler routes the public port to the parts of the server.
func newServerHandler(r serverRoutes, log *slog.Logger) (http.Handler, error) {
	proxy := func(target string, stripPrefix string) (*httputil.ReverseProxy, error) {
		u, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		return &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(u)
				// Keep the chain added by the platform's load balancer; the
				// services trust it only through HS_TRUSTED_PROXIES.
				if xff := pr.In.Header.Values("X-Forwarded-For"); len(xff) > 0 {
					pr.Out.Header["X-Forwarded-For"] = xff
				}
				pr.SetXForwarded()
				if stripPrefix != "" {
					pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, stripPrefix)
					pr.Out.URL.RawPath = strings.TrimPrefix(pr.In.URL.RawPath, stripPrefix)
				}
			},
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				log.Warn("upstream unavailable", "upstream", target, "path", req.URL.Path, "err", err)
				httpx.Error(w, http.StatusBadGateway, "server.starting")
			},
		}, nil
	}
	api, err := proxy(r.API, "")
	if err != nil {
		return nil, err
	}
	ingest, err := proxy(r.Ingest, IngestPathPrefix)
	if err != nil {
		return nil, err
	}
	web, err := proxy(r.Web, "")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "server", "version": version.String()})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		checks := map[string]string{
			"api":    r.API + "/readyz",
			"ingest": r.Ingest + "/readyz",
			"worker": r.Worker + "/readyz",
			"web":    r.Web + "/api/config",
		}
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		status, results := http.StatusOK, make(map[string]string, len(checks))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, u := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				state := "up"
				if !probe(ctx, u) {
					state = "down"
				}
				mu.Lock()
				results[name] = state
				if state != "up" {
					status = http.StatusServiceUnavailable
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		httpx.JSON(w, status, map[string]any{"service": "server", "checks": results})
	})
	mux.Handle("/v1/", api)
	mux.Handle(IngestPathPrefix+"/", ingest)
	mux.Handle("/", web)
	return mux, nil
}

func probe(ctx context.Context, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// runWeb runs the dashboard's Next.js server until ctx is done.
func runWeb(ctx context.Context, dir, publicURL string, log *slog.Logger) error {
	host, port, _ := net.SplitHostPort(serverWebAddr)
	cmd := exec.Command(envOr("HS_NODE", "node"), "server.js")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"PORT="+port, "HOSTNAME="+host, "NODE_ENV=production",
		"HS_API_URL="+publicURL, // the browser calls the API on the same origin
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dashboard: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return fmt.Errorf("dashboard exited: %v", err)
	case <-ctx.Done():
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Warn("dashboard did not stop in 10s, killing it")
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
