// Command ingest is the HookShield edge service: it authenticates incoming
// webhooks on POST/PUT/PATCH /v1/{slug}, applies limits, encrypts each request
// and enqueues it for the worker (target: < 20 ms response).
//
// It runs the self-hosted edition (app.Options{}); see package app.
// Contract: docs/INGEST.md.
package main

import (
	"context"

	"github.com/hookshield-hq/hookshield/backend/app"
)

func main() {
	app.Main("ingest", func(ctx context.Context) error { return app.Ingest(ctx, app.Options{}) })
}
