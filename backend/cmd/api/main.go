// Command api is the HookShield control-plane REST API consumed by the
// dashboard (auth, endpoints, logs, metrics).
//
// Usage:
//
//	api                  serve the API (applies pending migrations first unless HS_AUTO_MIGRATE=false)
//	api migrate up       apply pending migrations and exit
//	api migrate status   list applied and pending migrations and exit
//	api seed [--env]     create/refresh demo data (development only) and print a test token
//	                     (--env prints KEY=value lines for scripts; HS_SEED_TARGET sets the destination)
//
// It runs the self-hosted edition (app.Options{}); see package app.
// Contract: docs/API.md.
package main

import (
	"context"
	"os"

	"github.com/hookshield-hq/hookshield/backend/app"
)

func main() {
	app.Main("api", func(ctx context.Context) error { return app.API(ctx, os.Args[1:], os.Stdout, app.Options{}) })
}
