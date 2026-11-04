// Command hookshield is the developer CLI.
//
//	hookshield login      save the API URL and an API key
//	hookshield listen     forward an endpoint's webhooks to your local app
//	hookshield trigger    send a test event to an endpoint
//	hookshield endpoints  list your endpoints
//	hookshield whoami     show the account of the saved key
//	hookshield logout     forget the saved key
//	hookshield sink       run a local webhook receiver that prints deliveries (demo/dev destination)
//	hookshield version    print the version
//
// Docs: docs/CLI.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/sink"
	"github.com/hookshield-hq/hookshield/backend/internal/version"
)

const usage = `HookShield CLI

Usage:
  hookshield <command> [flags]

Commands:
  login      Save your HookShield API URL and API key
  listen     Forward webhooks of an endpoint to your local app
  trigger    Send a test event to an endpoint
  endpoints  List your endpoints
  whoami     Show the account and API URL in use
  logout     Forget the saved API key
  sink       Run a local webhook receiver that prints every delivery
  version    Print the CLI version
  help       Show this help

Examples:
  hookshield login --api-url https://hooks.example.com
  hookshield listen ep_k3j7x2m4q5ab --forward localhost:3000/webhooks
  hookshield trigger ep_k3j7x2m4q5ab --event invoice.paid

Run "hookshield <command> -h" for the flags of a command.
Configuration: HOOKSHIELD_API_URL, HOOKSHIELD_API_KEY and HOOKSHIELD_CONFIG override the saved login.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "hookshield %s\n", version.String())
		return 0
	case "sink":
		return runSink(ctx, args[1:], stdout, stderr)
	case "login":
		return runLogin(ctx, args[1:], stdout, stderr)
	case "logout":
		return runLogout(args[1:], stdout, stderr)
	case "whoami":
		return runWhoami(ctx, args[1:], stdout, stderr)
	case "endpoints":
		return runEndpoints(ctx, args[1:], stdout, stderr)
	case "listen":
		return runListen(ctx, args[1:], stdout, stderr)
	case "trigger":
		return runTrigger(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runSink(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", envOr("HS_SINK_ADDR", ":4000"), "listen address")
	status := fs.Int("status", 200, "HTTP status to answer")
	failFirst := fs.Int("fail-first", 0, "answer 503 to the first N requests (shows retries)")
	quiet := fs.Bool("quiet", false, "do not print requests")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *status < 100 || *status > 599 {
		fmt.Fprintln(stderr, "sink: --status must be a valid HTTP status")
		return 2
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           sink.New(sink.Options{Status: *status, FailFirst: *failFirst, Quiet: *quiet, Out: stdout}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(stdout, "HookShield sink listening on %s (status %d, fail-first %d) — stats at /_sink/stats\n", *addr, *status, *failFirst)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "sink: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
