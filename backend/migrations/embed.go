// Package migrations embeds the versioned SQL migrations so every binary
// ships with its schema and self-hosted installs can migrate on boot.
//
// Naming: NNNN_short_description.sql (4-digit version, applied in order).
// Migrations are forward-only and each one runs in its own transaction.
// Never edit a migration that was already released — add a new one.
package migrations

import "embed"

// FS contains every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
