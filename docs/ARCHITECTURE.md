# Architecture

> Living document: updated in the same change as the code it describes.

## Overview

```
                ┌──────────────┐   XADD    ┌──────────────────────┐
 Sender ──────► │   ingest     │ ────────► │ hs:stream:ingest     │
  (Stripe,      │ token check  │           └──────────┬───────────┘
   GitHub…)     │ → 202 / 401  │                      │ XREADGROUP
                └──────────────┘                      ▼
                                           ┌──────────────────────┐
                                           │ worker: processor    │
                                           │ transform → encrypt  │──► PostgreSQL
                                           │ → persist            │    (payload_logs)
                                           └──────────┬───────────┘
                                                      │ XADD
                                                      ▼
                                           ┌──────────────────────┐
                                           │ worker: dispatcher   │──► Destination app
                                           │ headers → deliver    │
                                           └───┬──────────────┬───┘
                                    fail       │              │ max retries / threshold
                                               ▼              ▼
                                        hs:zset:retry     DLQ + alerts
                                        (scheduled)

 Dashboard (Next.js) ──HTTP/SSE──► api (Go control plane) ──► PostgreSQL / Redis
 CLI (hookshield listen) ──SSE────► api (live feed + raw logs, no tunnel)
```

## Components

| Binary | Responsibility |
|---|---|
| `cmd/ingest` | Edge HTTP: resolve endpoint by slug (Redis cache), authenticate (token / HMAC / IP allow-list), enforce rate limit + monthly quota, **encrypt the request**, enqueue, answer `202`. Never touches Postgres on the hot path when the cache is warm. Measured p99 ≈ 6 ms with 16 concurrent senders (budget 20 ms). Contract: [INGEST.md](INGEST.md). |
| `cmd/worker` | **Processor**: consumes the ingest stream with a consumer group (`HS_PROCESSOR_CONCURRENCY` goroutines), decrypts, applies [transform rules](TRANSFORM.md), detects the event type, stores original + transformed requests encrypted in `payload_logs` (idempotent on the request ID), hands the log to the dispatch stream, then `XACK` + `XDEL`. Poison messages go to the dead-letter stream with their original (still encrypted) fields so they can be replayed (capped at about 100,000 entries); transient errors (database or Redis down) stay pending, are reclaimed with `XAUTOCLAIM` and never count toward the poison limit, however long the outage. **Dispatcher**: consumes the dispatch stream (`HS_DISPATCHER_CONCURRENCY` goroutines, each with up to 4 deliveries in flight, acknowledged one by one; in-flight entries are kept fresh with `XCLAIM` so they are never reclaimed while being delivered; at most 4 in-flight deliveries per endpoint per worker — excess messages are re-checked a second later without consuming an attempt, so a slow destination cannot stall the others), decrypts the transformed (or original) request, resolves `{{secrets.NAME}}` in outbound headers, delivers through the SSRF-protected client, records every attempt (encrypted response) and applies the retry policy; final failures become `dead` (+ DLQ entry) or `failed`; alerts once per webhook. **Scheduler**: atomic Lua move of due retries from `hs:zset:retry` to the dispatch stream + sweeper for lost retries. **Retention**: every minute, deletes logs past `expires_at` in batches of 1,000 (`FOR UPDATE SKIP LOCKED`, safe with several workers). **Auth cleanup** (hourly job): deletes sessions that expired or passed `HS_SESSION_MAX_AGE`, and used or expired email tokens, so no stale IP address or user agent is kept. **Live events**: the processor and dispatcher publish `log.created` / `log.updated` on `hs:live:{user}` for the dashboard feed. Contract: [DELIVERY.md](DELIVERY.md). |
| `cmd/worker` (alerts) | **Alert sender**: the dispatcher's `alert.Service` applies a per-endpoint cooldown and enqueues to `hs:stream:alerts`; the sender delivers to webhook, Slack, Telegram and email channels (Resend in production, SMTP/Mailpit in development) with retries, localized to the account language. Contract: [ALERTS.md](ALERTS.md). |
| `cmd/api` | Control-plane REST API for the dashboard. **Auth**: email/password (argon2id) with email verification and password reset, Google sign-in (state + PKCE), sliding sessions in an `HttpOnly` cookie, CSRF via `Origin` check, CORS for the dashboard, login throttling in Redis; account emails via Resend (SMTP/Mailpit in development). **Resources**: endpoints (token shown once, rotation, ingest cache invalidated on every change), encrypted secrets, alert channels (masked targets), API keys with scopes (`Authorization: Bearer hs_live_…`), transform preview. **Observability**: logs with filters and keyset pagination, request debugger (decrypted request, transformed request, every attempt with the destination response), replay (as received or edited, through the whole pipeline), test events from the dashboard (`POST /v1/endpoints/{id}/test`, same pipeline, inbound auth skipped for the signed-in caller), metrics, cluster status, global search, SSE live feed. Contract: [API.md](API.md) + OpenAPI 3.1 (`GET /v1/openapi.json`, kept in sync with the routes by a test). |
| `cmd/hookshield-server` | **All-in-one**: runs the API, ingest and worker in one process (`app.Server`), starts the dashboard (`node server.js`) as a child, and serves everything on one port: `/v1` → API, `/in/v1/{slug}` → ingest, the rest → dashboard; `/readyz` checks every part. Image `ghcr.io/hookshield-hq/hookshield` (root `Dockerfile`) for the one-click deploys. Contract: [SELF_HOSTING.md](SELF_HOSTING.md). |
| `cmd/hookshield` | **Developer CLI** (`internal/cli`): `login` (API key, saved 0600 in the user config dir), `listen` (follows `GET /v1/live`, reads each new log with `GET /v1/logs/{id}?raw=true` and forwards it to localhost the way the dispatcher would — byte-exact body, static outbound headers, never secrets; catches up after reconnecting), `trigger` (test events), `endpoints`, `whoami`, `sink`. Released for 6 platforms by GoReleaser. Contract: [CLI.md](CLI.md). |
| `web/` | Next.js dashboard; pure frontend that talks to `cmd/api` (the billing page appears only when `GET /v1/features` says so). **Foundation**: locale routing (`/en`, `/pt-BR`, `/es`; every string from `messages/*.json`, enforced by an i18n checker), dark/light themes from design tokens, authenticated shell (sidebar, cluster status, ⌘K command palette with global search, user menu), sign-in/sign-up with email or Google, password recovery and email verification, API client typed from the OpenAPI document. The browser calls the API directly with the session cookie (CORS allows the dashboard origin); the API URL is read at runtime from `GET /api/config` (`HS_API_URL`), so one image serves every deployment. **Screens**: dashboard (cards, canvas traffic chart, SSE live feed, onboarding), endpoints (create, security, visual retry policy, outbound headers), logs & request debugger (timeline, replay, edit & replay), payload transformer with live preview, sandbox (test events through the real pipeline, code snippets), alerts, secrets, API keys, account settings. Details: [web/README.md](../web/README.md). |

Shared code lives in `backend/internal/` (see [backend/README.md](../backend/README.md) for the full list). The binaries are thin wrappers around the exported package `backend/app` (see below).

## Editions and extension points (open-core)

HookShield is open-core. This repository is the complete gateway and dashboard, **self-hosted without commercial limits**. HookShield Cloud lives in a separate private repository that imports this one and adds billing, plans and multi-tenant limits. The core never imports the cloud.

Go forbids importing `internal/` packages from another module, so every extension point is in an exported package:

| Package | What it holds |
|---|---|
| `backend/core` | Contracts with self-hosted defaults: `Plans` (quota, retry cap and retention per account — default `SelfHosted`: no quota, no retry cap, retention `HS_RETENTION`), `Limits`, `StaticPlans`, `User`; **billing**: `Billing` (catalog, checkout, customer portal, provider webhooks — default none), `Offer`, `Subscription` (+ `EffectivePlan`), `Accounts` (what a provider may change: save a subscription, which also sets the plan and drops cached endpoint configuration), `UsageStore` (monthly usage to report), sentinel errors mapped to API codes. |
| `backend/app` | The services as a library: `app.Ingest`, `app.Worker`, `app.API` and `app.Main`. `app.Options{}` (zero value) is the self-hosted edition used by `cmd/*`. `Options.Plans` injects a plan table; `Options.Billing` mounts the `/v1/billing` routes (the core owns HTTP, sessions, errors and subscription records; the provider only talks to its API); `Options.Jobs` adds periodic worker tasks (Redis lock: one run per interval across replicas); `Options.APIRoutes` adds other API routes with the same middleware plus `RequireUser` / `User`. |

```go
// HookShield Cloud (private repository)
provider := billing.New(cfg, log) // implements core.Billing with Paddle
opts := app.Options{Plans: plans.Cloud, Billing: provider,
	Jobs: []app.Job{{Name: "paddle-overage", Every: 10 * time.Minute, Run: reportUsage}}}
app.Main("cloud-ingest", func(ctx context.Context) error { return app.Ingest(ctx, opts) })
```

Rules: anything the cloud may restrict or bill is an interface in `core` with a self-hosted default — never an import of a payment provider in this repository. Usage is counted in both editions (`hs:usage:{user}:{YYYYMM}`), so the dashboard can show it; only a plan table turns it into a quota. The plan column (`users.plan_type`) exists in both and is ignored by the self-hosted defaults. Tests of plan-dependent behaviour use `internal/testplans`, a copy of the commercial tiers that ships only in tests.

**Billing flow (cloud).** The dashboard asks `GET /v1/features`; with `billing: true` it shows the billing page (`GET /v1/billing`: plan, subscription, usage of the month, catalog). Checkout returns the provider's hosted page (or switches an existing subscription in place); the provider's signed webhook (`POST /v1/billing/webhook`) reaches the provider implementation, which saves the subscription through `core.Accounts`: the plan changes and the ingest/worker cache of the account's endpoints is dropped, so quota, retry cap and retention follow at once. The worker copies the Redis usage counters to `usage_counters` every minute (both editions); the cloud's job reports metered usage from there. Contract tests run in the core with a fake provider (`internal/api/billing_integration_test.go`).

## Queues (Redis Streams)

All key names live in `internal/redisx` and are prefixed with `HS_REDIS_PREFIX` (default `hs:`).

**Payloads are encrypted before they enter Redis.** The ingest service seals `{method, query, headers, body}` with AES-256-GCM (AAD = `ingest:<request id>`), so webhook bodies never reach Redis memory, AOF or RDB files in plain text. The inbound token header is stripped before sealing.

| Key | Purpose |
|---|---|
| `hs:stream:ingest` | Processing queue |
| `hs:stream:dispatch` | Delivery queue |
| `hs:zset:retry` | Scheduled retries; score = due time (unix ms), member = `log_id|endpoint_id|attempt` (scheduling twice is a no-op) |
| `hs:stream:alerts` | Alerts waiting for the sender |
| `hs:alert:cooldown:{endpoint}` / `hs:alert:suppressed:{endpoint}` | Alert cooldown marker (TTL = `HS_ALERT_COOLDOWN`) and count of grouped alerts |
| `hs:stream:dlq` | Dead-letter stream: poison messages (malformed, undecryptable, crashing workers) with source, entry ID and reason, plus the original stream fields unchanged (payload still sealed with AES-256-GCM, never plaintext) so they can be replayed |
| `hs:auth:fail:{email or ip}` | Failed-login counters (TTL = throttling window, 15 min) |
| `hs:auth:mail:{purpose}:{user}` | Cooldown between two account emails of one kind |
| `hs:oauth:{state}` | Google sign-in state + PKCE verifier (TTL 10 min, single use) |
| `hs:cache:ep:{slug}` | Endpoint ingest config (hashes/ciphertexts only), TTL 10 min; `!` = unknown slug, TTL 30 s |
| `hs:cache:epgen:{slug}` | Invalidation counter of the entry above (TTL 24 h): a cache miss only writes back what it loaded if no invalidation happened meanwhile |
| `hs:live:{user}` | Pub/sub channel of the live feed (log created / attempt recorded; no payload data) |
| `hs:usage:{user}:{YYYYMM}` | Accepted requests this month (quota), TTL 40 days |
| `hs:rl:{endpoint}:{unix second}` | Per-second rate window, TTL 2 s |

Consumer groups give at-least-once delivery; stuck messages are reclaimed with `XAUTOCLAIM`. Each delivery carries an idempotency header with the log ID.

## Boot sequence

Every service (`api`, `ingest`, `worker`, and the parts of `hookshield-server`) starts the same way, so they can start in any order and in parallel:

1. Load and validate the configuration (every problem reported at once).
2. Apply pending migrations (`HS_AUTO_MIGRATE`; advisory lock — one runner at a time).
3. Resolve the encryption key (`internal/keyboot`): `HS_ENCRYPTION_KEY`, else `HS_ENCRYPTION_KEY_FILE`; on a fresh database (no account, no check value) the first service generates the file atomically (temp file + hard link) and warns to back it up. The key must open `system_settings.encryption_key_check`, otherwise the service stops — a wrong or regenerated key never writes data the others cannot read.
4. Serve.

Deployment topologies: separate services behind Caddy (`deploy/docker-compose.yml`, workers scale horizontally) or the all-in-one server (one-click platforms). Both expose one origin: `/`, `/v1`, `/in/v1/{slug}` ([SELF_HOSTING.md](SELF_HOSTING.md)).

## Encryption contexts (AAD)

Every ciphertext is bound to *where* it lives through AES-GCM additional authenticated data, so a ciphertext copied to another row or field fails to decrypt:

| Data | AAD |
|---|---|
| Request in the ingest stream | `ingest:<request id>` |
| Original request in `payload_logs` | `payload:<log id>:original` |
| Transformed request in `payload_logs` | `payload:<log id>:transformed` |
| Endpoint HMAC secret | `endpoint:<endpoint id>:hmac` |
| User secret (`{{secrets.NAME}}`) | `secret:<user id>:<NAME>` |
| Alert channel target | `alert_channel:<channel id>:target` |
| Destination response of an attempt | `attempt:<log id>:<n>:response` |
| Encryption key check | `system:encryption_key_check` |

## Security model

Summary; the full list, the reporting policy and the known limitations are in [SECURITY.md](../SECURITY.md).

- **Payloads at rest:** AES-256-GCM in the application (Go `crypto/cipher`), 12-byte random nonce per record, AAD bound to the record, master key from `HS_ENCRYPTION_KEY` (checked against the database on boot), `key_version` for rotation.
- **Inbound tokens:** high-entropy, shown once, stored as SHA-256 hash, compared in constant time; HMAC signatures with `hmac.Equal`.
- **Dashboard auth:** argon2id passwords (concurrency-capped), Google OIDC (state + PKCE; an unverified account is taken over cleanly), opaque sessions (sliding + absolute lifetime) in an `HttpOnly`/`Secure`/`SameSite=Lax` cookie, CSRF by `Origin` check, atomic login throttling (IPv6 by /64), API keys with scopes revoked on account recovery.
- **Outbound:** SSRF protection in the dialer (resolved IP; private, loopback, link-local/metadata, CGNAT, NAT64/6to4/Teredo, multicast and reserved ranges; privileged ports except 80/443; no redirects). Alert targets follow the same rules.
- **Abuse limits:** transform size budget (no exponential blow-up), poison messages dead-lettered after 5 deliveries, alert emails only from verified accounts and capped per hour.
- **Browser:** CSP, `frame-ancestors 'none'`, `nosniff` and a strict referrer policy on the dashboard; the API sends a deny-all CSP and `no-store`.

## Database

Schema, migration rules and table reference: [DATABASE.md](DATABASE.md).

## Configuration

See [`.env.example`](../.env.example) and [DEVELOPMENT.md](DEVELOPMENT.md#configuration).
