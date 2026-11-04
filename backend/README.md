# backend — HookShield Go services

One Go module, five binaries. `cmd/ingest`, `cmd/worker` and `cmd/api` are thin wrappers around the exported package `app` (self-hosted edition); HookShield Cloud reuses `app` and `core` from its private repository — see [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md#editions-and-extension-points-open-core).

| Path | Binary | Purpose |
|---|---|---|
| `cmd/ingest` | `ingest` | Edge webhook receiver (token guard + enqueue) |
| `cmd/worker` | `worker` | Processor + outbound dispatcher |
| `cmd/api` | `api` | Control-plane REST API for the dashboard; also `api migrate up/status` and `api seed` |
| `cmd/hookshield-server` | `hookshield-server` | All-in-one: api + ingest + worker in one process plus the dashboard, on one port (one-click deploys) — see [docs/SELF_HOSTING.md](../docs/SELF_HOSTING.md) |
| `cmd/hookshield` | `hookshield` | Developer CLI: `login`, `listen`, `trigger`, `endpoints`, `whoami`, `logout`, `sink` (demo destination), `version` — see [docs/CLI.md](../docs/CLI.md) |

| Package | Purpose |
|---|---|
| `core` (exported) | Extension contracts with self-hosted defaults: `Plans`/`Limits` (quota, retry cap, retention), `User` |
| `app` (exported) | The services as a library: `Ingest`, `Worker`, `API`, `Main`; `Options{Plans, APIRoutes}` |
| `internal/config` | Environment loading and validation |
| `internal/api` | Control-plane HTTP API: routes with access levels, session cookie and API keys, CORS + CSRF (Origin check), error mapping; `openapi.json` (embedded, served at `/v1/openapi.json`) — see [docs/API.md](../docs/API.md) |
| `internal/auth` | Account use cases: sign-up, login + throttling, sessions, email verification, password reset/change, Google OAuth (state + PKCE) |
| `internal/password` | argon2id hashing (PHC strings, rehash detection) and the password policy |
| `internal/emails` | Account email templates (verify, reset, password changed) in en, pt-BR and es |
| `internal/mailer` | Email transport: Resend HTTP API (production) and SMTP (development, Mailpit) |
| `internal/httpx` | Health/readiness endpoints, JSON helpers, graceful server |
| `internal/version` | Build metadata injected via `-ldflags` |
| `internal/ingest` | Edge HTTP handler (auth, limits, encryption, enqueue) — see [docs/INGEST.md](../docs/INGEST.md) |
| `internal/dispatcher` | Worker stage 2: delivery, secrets in headers, attempt recording, retries, DLQ, alerts, retry scheduler + sweeper — see [docs/DELIVERY.md](../docs/DELIVERY.md) |
| `internal/retrypolicy` | Backoff strategies (fixed / exponential / custom), jitter, Retry-After, retry caps |
| `internal/outbound` | SSRF-protected HTTP client for deliveries, error classification |
| `internal/alert` | Alerts: cooldown + queue (`Service`), channel delivery with retries (`Sender`: webhook, Slack, Telegram, email), localized messages — see [docs/ALERTS.md](../docs/ALERTS.md) |
| `internal/processor` | Worker stage 1: ingest stream → transform → encrypt → `payload_logs` → dispatch stream |
| `internal/transform` | Declarative transform rules (parse, validate, apply) — see [docs/TRANSFORM.md](../docs/TRANSFORM.md) |
| `internal/eventtype` | Event name detection (provider headers, JSON `type`/`event`) |
| `internal/endpointcache` | Read-through Redis cache of endpoint ingest configs (negative caching, singleflight) |
| `internal/limits` | Per-endpoint rate limit and per-user monthly quota (one Redis round trip) |
| `internal/queue` | Stream messages (encrypted envelopes), producers, consumer-group reader (read, claim, ack+delete), dead letters, retry schedule (atomic Lua move) |
| `internal/live` | Real-time log events (Redis pub/sub per account) for the SSE live feed |
| `internal/redisx` | Redis client + the single list of Redis keys |
| `internal/store` | Hand-written PostgreSQL queries (users, sessions, email tokens, OAuth links, endpoints, payload logs, delivery attempts, user secrets, alert channels) |
| `internal/crypto` | AES-256-GCM with AAD (encryption at rest) |
| `internal/keyboot` | Encryption key on boot: generated on a fresh install (`HS_ENCRYPTION_KEY_FILE`), checked against the database by every service |
| `internal/token` | Token generation, SHA-256 hashing, constant-time verification |
| `internal/testplans` | Commercial plan tiers for tests of plan-dependent behaviour (not used at runtime) |
| `internal/uuid` | Time-ordered UUIDv7 request IDs |
| `internal/seed` | Development demo data (`api seed`, `api seed --env` for scripts) |
| `internal/cli` | The CLI's account commands: config file, API client (API keys), `listen` (live feed → local app, catch-up after reconnects), `trigger` |
| `internal/sink` | Demo webhook destination behind `hookshield sink` |
| `internal/testredis` | Isolated Redis namespaces for integration tests |
| `internal/db` | PostgreSQL pool (pgx), readiness probe, boot-time migrations |
| `internal/testdb` | Throw-away databases for integration tests |
| `tools/loadtest` | Load generator: ingest latency and end-to-end delivery under load (not shipped) — see [docs/PERFORMANCE.md](../docs/PERFORMANCE.md) |
| `internal/migrate` | Applies the embedded migrations (ordering, advisory lock, checksums) |
| `migrations/` | Versioned SQL migrations, embedded via `migrations.FS`; schema tests in `migrations/tests/` — see [docs/DATABASE.md](../docs/DATABASE.md) |

Commands are run from the repo root via `make` — see [docs/DEVELOPMENT.md](../docs/DEVELOPMENT.md).

The [`Dockerfile`](Dockerfile) builds one image with every binary; the process is chosen with the entrypoint (`api`, `ingest`, `worker`, `hookshield sink`), as in the root [`compose.yaml`](../compose.yaml).
