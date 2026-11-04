// Command worker runs the HookShield async pipeline: processors, dispatchers
// (delivery, retries, DLQ), the retry scheduler and the alert sender.
// Several workers can run side by side (Redis consumer groups).
//
// It runs the self-hosted edition (app.Options{}); see package app.
// Contracts: docs/TRANSFORM.md, docs/DELIVERY.md, docs/ALERTS.md.
package main

import (
	"context"

	"github.com/hookshield-hq/hookshield/backend/app"
)

func main() {
	app.Main("worker", func(ctx context.Context) error { return app.Worker(ctx, app.Options{}) })
}
