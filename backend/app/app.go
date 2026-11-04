// Package app runs the HookShield services as a library.
//
// The binaries in cmd/ (ingest, worker, api) are thin wrappers around Ingest,
// Worker and API with zero Options, which is the self-hosted edition: no plan
// quotas, no retry caps, no extra routes. HookShield Cloud (a separate,
// private repository) calls the same functions with its own Options, so both
// editions share one code path. Extension contracts live in package core.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
)

// Options customize a service. The zero value is the self-hosted edition.
type Options struct {
	// Plans decides quotas, retry caps and retention per account.
	// nil = core.SelfHosted with HS_RETENTION.
	Plans core.Plans

	// APIRoutes adds routes to the control-plane API (e.g. billing checkout and
	// billing webhooks in the cloud). They share the API's middleware: request
	// IDs, access logs, CORS and the CSRF Origin check.
	APIRoutes func(mux *http.ServeMux, api APIContext)

	// Billing mounts the /v1/billing routes (plans, checkout, customer
	// portal, provider webhooks) and shows the billing page in the
	// dashboard. nil = self-hosted: no billing.
	Billing core.Billing

	// Jobs are extra periodic tasks of the worker (e.g. usage reporting to
	// the billing provider).
	Jobs []Job
}

// APIContext is what extra API routes may use.
type APIContext struct {
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Logger *slog.Logger
	// RequireUser runs h only for a signed-in dashboard user; others get 401 auth.unauthorized.
	RequireUser func(h http.HandlerFunc) http.Handler
	// User returns the signed-in user inside a RequireUser handler.
	User func(r *http.Request) core.User
}

// Main runs fn until SIGINT/SIGTERM and exits with status 1 on error.
// Typical use: app.Main("worker", func(ctx context.Context) error { return app.Worker(ctx, app.Options{}) }).
func Main(service string, fn func(ctx context.Context) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := fn(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", service, err)
		os.Exit(1)
	}
}
