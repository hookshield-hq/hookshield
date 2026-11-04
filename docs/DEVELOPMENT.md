# Development guide

## Two ways to run HookShield locally

| | **Docker stack** (recommended) | **Native** (for backend contributors) |
|---|---|---|
| Needs | Docker with Compose v2, Make | Go 1.24+, Make, Docker *or* your own PostgreSQL 15+ / Redis 7; Node 22+ for the dashboard |
| Start | `make up` | `make dev-up` then `make dev` (+ `make web-dev` for the dashboard) |
| Check it works | `make demo` (+ `make demo-alert`) | `make smoke` (+ `make smoke-alert`) in another terminal |
| Logs | `make logs` | printed in the `make dev` terminal, one color per service |
| Stop | `make down` (`make reset` also deletes data) | Ctrl+C (and `make dev-down` for PostgreSQL/Redis) |
| Code changes | `make up` again (rebuilds the images) | Ctrl+C + `make dev` (rebuilds in seconds); the dashboard hot-reloads |

Both run **api** (applies migrations), **ingest**, **worker**, a **demo destination** (`hookshield sink`) that prints every webhook it receives, and **Mailpit**, which catches every email — sign-up, password reset, alerts (http://localhost:8025) — on the same ports, so stop one mode before starting the other. The Docker stack also serves the **dashboard** at http://localhost:3000; natively you start it with `make web-dev`.

`.env` is created automatically from `.env.example` on the first run, with a freshly generated `HS_ENCRYPTION_KEY`. Back it up if you keep real data: without it stored payloads cannot be decrypted.

### Docker stack

```bash
make up      # builds the images, starts the dashboard, api, ingest, worker, postgres, redis, sink, Mailpit; waits until all are healthy
make demo    # seeds a demo endpoint, sends a webhook, waits until the sink received it with a 2xx
make logs    # follow everything — the sink prints each delivery with its X-HookShield-* headers
make demo-alert   # sends a webhook to an always-failing endpoint → alert email at http://localhost:8025
```

PostgreSQL and Redis are **not** published on the host (no clash with databases you already run); use `make psql` to open a shell. Host ports can be changed with `HS_WEB_PORT`, `HS_INGEST_PORT`, `HS_API_PORT` and `HS_SINK_PORT`.

Open http://localhost:3000 and create an account (the verification email is in Mailpit; the account works before you verify).

### Native

```bash
make dev-up   # PostgreSQL 17, Redis 7 and Mailpit in Docker (5432 / 6379 / SMTP 1025, mail UI 8025)
make dev      # builds and runs api, ingest, worker and sink in this terminal
make smoke    # in another terminal: seed + webhook + delivery check
make smoke-alert   # failing endpoint → alert email in Mailpit

make web-install   # once: dashboard dependencies (Node 22+)
make web-dev       # dashboard with hot reload on http://localhost:3000, talking to the api on :8081
```

Dashboard details — i18n rules, generated API types, tests, conventions: [web/README.md](../web/README.md).

### See alerts

`make seed` also creates an account-wide **email alert channel** and an endpoint **`ep_failing`** whose destination always refuses connections (no retries, alert after the first failure). `make demo-alert` / `make smoke-alert` send it a webhook and wait for the alert email in Mailpit. Details and the other channels: [ALERTS.md](ALERTS.md).

### Try sign-up and login

`make demo` / `make smoke` already create an account and check the session. By hand (works in both modes):

```bash
A=http://localhost:8081; O='Origin: http://localhost:3000'; J='Content-Type: application/json'
curl -c jar -H "$O" -H "$J" -d '{"email":"me@example.com","password":"a long enough passphrase","locale":"pt-BR"}' $A/v1/auth/register
curl -b jar $A/v1/me
```

The confirmation email lands in Mailpit (http://localhost:8025); open the link's token with `POST /v1/auth/verify-email`. Google sign-in needs `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` in `.env` (the redirect URI is `http://localhost:8081/v1/auth/google/callback`). Every route: [API.md](API.md).

### See retries in action

Make the demo destination fail the first N requests:

```bash
HS_SINK_FAIL_FIRST=2 make dev        # native
HS_SINK_FAIL_FIRST=2 make up         # Docker
make seed                            # prints a token and a curl command; send it and watch the logs
```

The default retry policy waits 10 s, then 20 s. To make it faster for a demo, lower the demo endpoint's initial interval:
`make psql` (Docker) or `psql "$HS_DATABASE_URL"` (native) → `UPDATE endpoints SET retry_initial_interval_ms = 1000 WHERE slug = 'ep_demo';`

### Send webhooks by hand

```bash
make seed     # (native) prints the token and a ready-to-run curl command
docker compose exec api api seed      # (Docker) same
```

Then inspect what was stored:

```sql
SELECT id, status, attempt_count, event_type, ingest_us, transform_us, persist_us FROM payload_logs ORDER BY received_at DESC LIMIT 5;
SELECT payload_log_id, attempt_number, status_code, error_code, duration_us FROM delivery_attempts ORDER BY started_at DESC LIMIT 5;
```

## Commands

`make help` prints the up-to-date list, grouped as below.

| Command | What it does |
|---|---|
| **Docker stack** | |
| `make up` / `make down` / `make reset` | Start (build + migrate + wait healthy) / stop / stop and delete data |
| `make demo` / `make demo-alert` | End-to-end delivery check / alert email check against the Docker stack |
| `make logs` / `make ps` / `make psql` | Follow logs / service status / database shell |
| **Native** | |
| `make dev-up` / `make dev-down` / `make dev-logs` | PostgreSQL + Redis in Docker for native mode |
| `make dev` | Run api, ingest, worker and sink in one terminal |
| `make smoke` / `make smoke-alert` | Same checks against `make dev` |
| `make seed` | Create/refresh the demo user + endpoint and print a token + curl command |
| `make migrate` / `make migrate-status` | Apply / list database migrations |
| `make run-api` / `run-ingest` / `run-worker` / `run-sink` | Run a single service (`make run-sink ARGS="--fail-first 2"`) |
| **Build & quality** | |
| `make test` | Unit tests with the race detector (no external services) |
| `make test-integration` | Unit + integration tests against PostgreSQL + Redis (`make dev-up` first) |
| `make test-perf` | Ingest latency budget test (p99 < 20 ms) — no `-race` |
| `make lint` / `make fmt` | `gofmt` check + `go vet` / format |
| `make build` | Build all binaries into `./bin` (version injected from git) |
| `make cover` | Unit tests + total coverage |

Native commands load `.env` through `scripts/run-native.sh`, which uses the shell's own parser (quoted values work as expected).

### Scripts

| Script | Purpose |
|---|---|
| `scripts/ensure-env.sh` | Create `.env` and generate `HS_ENCRYPTION_KEY` if missing (never overwrites) |
| `scripts/dev.sh` | Native mode runner behind `make dev` |
| `scripts/smoke.sh` | End-to-end check behind `make demo` / `make smoke` (sign-up + session, endpoint created through the API, webhook delivered with the token the API returned, decrypted log checked in the request debugger); exits non-zero on failure (used in CI) |
| `scripts/alert-demo.sh` | Alert check behind `make demo-alert` / `make smoke-alert` (waits for the email in Mailpit) |
| `scripts/run-native.sh` | Run one backend binary with `.env` loaded |

All scripts run on macOS (bash 3.2) and Linux.

### The demo destination (`hookshield sink`)

A tiny receiver for development: prints each request (method, path, `X-HookShield-*` headers, pretty JSON body; credentials truncated), answers `200` (or `--status`), can fail the first N requests (`--fail-first`) and exposes `GET /_sink/stats` with the last 20 deliveries for scripts.

## Services and ports

| Service | Default port | Health | Readiness |
|---|---|---|---|
| ingest | 8080 | `GET /healthz` | `GET /readyz` |
| api | 8081 | `GET /healthz` | `GET /readyz` |
| worker | 8082 | `GET /healthz` | `GET /readyz` |
| demo sink | 4000 | `GET /_sink/stats` | — |
| dashboard (web) | 3000 | `GET /api/config` | — |
| all-in-one (`hookshield-server`) | 8000 (`PORT`) | `GET /healthz` | `GET /readyz` (every part) |
| Postgres | 5432 (native mode only) | — | — |
| Redis | 6379 (native mode only) | — | — |

Try the all-in-one server the way one-click deploys run it (needs `make dev-up` and the dashboard on :3000, e.g. `make web-dev`):

```bash
cd backend && set -a && . ../.env && set +a
HS_WEB_UPSTREAM=http://localhost:3000 go run ./cmd/hookshield-server   # → http://localhost:8000
```

Or the image: `docker build -t hookshield .` at the repository root.

Try the CLI against `make dev` (create an API key in the dashboard first):

```bash
cd backend && go build -o ../bin/ ./cmd/hookshield
../bin/hookshield login --api-url localhost:8081
../bin/hookshield listen <slug> --forward 4000      # the demo sink prints what arrives
../bin/hookshield trigger <slug> --event demo.ping
```

`/healthz` = the process is alive. `/readyz` = its dependencies (database, Redis) are reachable; returns `503` otherwise.

## Tests

| Kind | Where | Run |
|---|---|---|
| Unit | `*_test.go` next to the code | `make test` |
| End-to-end | `scripts/smoke.sh` | `make demo` (Docker) / `make smoke` (native) |
| Integration (Postgres) | `*_integration_test.go` with `//go:build integration` | `make dev-up && make test-integration` |
| Performance | `TestLatencyBudget` (opt-in via `HS_PERF_TEST=1`) | `make test-perf` |
| Load (manual) | `backend/tools/loadtest` — ingest latency and end-to-end delivery under load | see [PERFORMANCE.md](PERFORMANCE.md) |
| Schema constraints | `backend/migrations/tests/schema_test.sql` | runs inside the integration suite; also `psql -f` (see [DATABASE.md](DATABASE.md#testing-the-schema)) |
| Dashboard checks | type-check, ESLint, i18n checker | `make web-check` |
| Dashboard unit | `web/src/**/*.test.ts` (Vitest) | `make web-test` |
| Dashboard end-to-end | `web/tests/e2e` (Playwright, against the real API) | `make dev` (or `make up`) then `make web-e2e` |
| Accessibility | `web/tests/e2e/a11y.spec.ts` — axe-core, WCAG 2.1 A + AA, every screen, dialogs and the command palette in the dark and light themes | part of `make web-e2e` |

Coverage of the Go code (unit + integration, measured 2026-11-04): **74%** of statements overall; the core packages are 78–100% (`api` 82%, `ingest` 91%, `transform` 87%, `store` 83%, `config` 97%). Measure it with:

```bash
cd backend && go test -tags integration -coverpkg=./... -coverprofile=/tmp/cover.out ./... && go tool cover -func=/tmp/cover.out | tail -1
```

Some e2e tests read emails from Mailpit (`HS_MAILPIT_URL`, default `http://localhost:8025`) to confirm the account's address, so run them against a stack with Mailpit (`make dev-up` / `make up`).

Integration tests call `testdb.New(t)`, which creates an isolated database (`hs_test_<random>`) and drops it afterwards, so tests never share state and can run in parallel. Redis-backed tests call `testredis.New(t)`, which returns a random key namespace that is deleted afterwards. Without `HS_TEST_DATABASE_URL` / `HS_TEST_REDIS_URL` they are skipped. CI (`.github/workflows/ci.yml`) runs unit and integration tests, the latency budget, the native end-to-end check (`dev.sh` + `smoke.sh`) the full Docker Compose stack with `make up` + `make demo`, and the dashboard (generated API types up to date, type-check, lint, i18n, unit tests, build, Playwright against the native stack).

## Database migrations

Every service (api, ingest, worker) applies pending migrations on boot (`HS_AUTO_MIGRATE=true`; an advisory lock serialises them). Manual control:

```bash
make migrate-status    # native: api migrate status
make migrate           # native: api migrate up
docker compose exec api api migrate status   # Docker stack
```

Rules for writing migrations: [DATABASE.md](DATABASE.md#migrations).

## Configuration

All configuration comes from environment variables; see [`.env.example`](../.env.example). Every binary validates its config on boot and exits with a clear error listing **all** problems at once.

## Conventions

- Go: standard library first; add a dependency only when it clearly pays off (and note why in the PR).
- Logs: structured (`log/slog`), text in development and JSON in production. Never log payloads, tokens or headers that may contain secrets.
- Errors returned to the dashboard are codes (`{"error":{"code":"endpoint.not_found"}}`); the UI translates them (`errors.<code>` in `web/messages/*.json`; the i18n checker fails if a backend code has no translation).
- Changed an API route or schema? Update `backend/internal/api/openapi.json`, then `make gen-api` so the dashboard types follow (CI checks both).
- See [CONTRIBUTING.md](../CONTRIBUTING.md) for the Definition of Done.
