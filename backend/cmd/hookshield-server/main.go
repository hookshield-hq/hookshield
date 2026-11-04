// Command hookshield-server runs all of HookShield in one process behind one
// public port: API (/v1), ingest (/in/v1/{slug}), worker and the dashboard.
// It is the entrypoint of the all-in-one image used by the one-click deploys
// (Render, Railway, Coolify); larger installs run api, ingest, worker and web
// as separate services instead (deploy/docker-compose.yml).
//
// Configuration: the usual HS_* variables (docs/CONFIGURATION.md) plus PORT
// (default 8000), HS_PUBLIC_URL, HS_WEB_DIR and HS_WEB_UPSTREAM — see
// app.Server and docs/SELF_HOSTING.md.
package main

import (
	"context"

	"github.com/hookshield-hq/hookshield/backend/app"
)

func main() {
	app.Main("hookshield-server", func(ctx context.Context) error { return app.Server(ctx, app.Options{}) })
}
