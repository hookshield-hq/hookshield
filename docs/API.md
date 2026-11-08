# Control-plane API

The `api` binary serves the REST API used by the dashboard (`web/`). Base URL: `HS_API_URL` (default `http://localhost:8081`). All bodies are JSON (`Content-Type: application/json`, max 64 KiB). Unknown fields are rejected.

> Status: complete for the dashboard — **6a** (authentication, profile), **6b** (endpoints, secrets, alert channels, API keys, transform preview), **6c** (logs, request debugger, replay, metrics, status, search, live feed) and **6d** (OpenAPI).

**OpenAPI 3.1:** [`backend/internal/api/openapi.json`](../backend/internal/api/openapi.json), also served by every API at `GET /v1/openapi.json`. Import it in Postman/Insomnia, open it in any OpenAPI viewer, or generate a typed client (the dashboard does). Each operation declares `x-access` (`public`, `read`, `operate`, `admin`, `session`); `TestOpenAPIMatchesRoutes` fails the build when a route and the document disagree (path, method or access level). This page is the narrative guide; the OpenAPI file is the exact contract.

## Conventions

**Errors** always have the same shape:

```json
{"error": {"code": "auth.invalid_credentials"}}
```

`code` is stable and translatable: the dashboard maps it to a message in English, Portuguese and Spanish. Never show raw server text to users. Validation errors from the password policy use the `password.*` namespace.

| HTTP | Meaning |
|---|---|
| `400` | Malformed JSON, unknown field (`validation.invalid_json`) or invalid/expired token (`auth.invalid_token`) |
| `401` | Not signed in (`auth.unauthorized`) or wrong credentials (`auth.invalid_credentials`) |
| `403` | Cross-site request blocked (`auth.csrf`) |
| `409` | Email already registered (`auth.email_taken`) |
| `422` | Invalid field value (`auth.invalid_*`, `auth.wrong_password`, `password.*`) |
| `429` | Too many failed logins (`auth.too_many_attempts`) |

Some errors add `details` (e.g. the list of problems of a transform rules document):

```json
{"error": {"code": "transform.invalid", "details": [{"section": "body", "index": 0, "code": "transform.unknown_op"}]}}
```

Every response carries `X-Request-Id` (reused from the request when present), which also appears in the server logs.

**Timestamps** are RFC 3339 in UTC.

## Sessions, cookies and browser security

- Signing in (register, login, Google) sets the **`hs_session`** cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `HS_COOKIE_SECURE=true` (default in production). The token (`hs_sess_…`) is stored only as a SHA-256 hash.
- Sessions are **sliding**: valid for `HS_SESSION_TTL` (default 30 days) since the last use, refreshed at most once an hour (the cookie's `Max-Age` with it) — and never longer than `HS_SESSION_MAX_AGE` (default 90 days) after sign-in.
- **Security headers** on every API response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store` (the OpenAPI document is cacheable).
- **CSRF:** every state-changing request (`POST`, `PATCH`, `PUT`, `DELETE`) that carries a cookie must send an `Origin` listed in `HS_CORS_ORIGINS` (default `HS_APP_URL`); otherwise `403 auth.csrf`. Browsers send `Origin` automatically. Clients without cookies (curl, servers) may call the public auth endpoints without it.
- **CORS:** only the allowed origins get `Access-Control-Allow-Origin` with `Access-Control-Allow-Credentials: true`. The dashboard must call the API with `credentials: "include"`.
- **Login throttling:** every attempt counts before the password is checked (parallel guesses cannot slip past the limit). After 10 attempts for one email, or 50 from one IP (IPv6: one /64), within 15 minutes, logins answer `429 auth.too_many_attempts` (even with the right password) until the window passes; a successful login resets the email's counter and does not count against the IP. Unknown emails and wrong passwords get the same answer and take the same time. Password hashing (argon2id, 64 MiB) runs at most a few at a time per server, so a burst of logins queues instead of exhausting memory.
- **Sign-up throttling:** at most `HS_SIGNUP_RATE_LIMIT` sign-up attempts per IP (IPv6: one /64) per 15 minutes — 10 in production, 1000 in development (`429 auth.too_many_attempts`).
- **Email throttling:** at most one verification and one reset email per account per minute.
- Changing or resetting the password **signs out every other session** and emails a "password changed" notice. **Resetting** (account recovery) also **revokes every API key**. Open live feeds re-check their credentials every ping and end after a sign-out, a reset or a key revocation.
- **Google sign-in on an unverified account:** when someone registered the email with a password but never verified it, the first Google sign-in with that (Google-verified) address — or a password reset through a link sent to it — takes the account over and it starts clean: the password, every session and API key, every endpoint (with its inbound token, HMAC secret and logs), alert channel and secret are deleted, so a squatter who registered first keeps no access and no foothold. The account itself (ID, plan) is kept.

## User object

```json
{
  "id": "0193…",
  "email": "ana@example.com",
  "name": "Ana",
  "email_verified": true,
  "has_password": true,
  "locale": "pt-BR",
  "theme_preference": "dark",
  "plan_type": "developer",
  "created_at": "2026-11-03T18:02:57.754Z"
}
```

`has_password` is `false` for accounts created with Google (they can set one through "forgot password"). `locale` is `en`, `pt-BR` or `es` and drives emails and alerts. `theme_preference` is `dark`, `light` or `system`.

## Access: dashboard sessions and API keys

Every route below `/v1` except the public auth routes needs a caller:

- a **dashboard session** (`hs_session` cookie), which may do everything; or
- an **API key**: `Authorization: Bearer hs_live_…`, limited by its scope.

| Route kind | Session | `read_only` key | `ingest` key | `admin` key |
|---|---|---|---|---|
| Read (every `GET`, transform preview) — includes decrypted payloads in the debugger | ✅ | ✅ | ✅ | ✅ |
| Operate (replays) | ✅ | — | ✅ | ✅ |
| Configure (create/change/delete endpoints, secrets, channels) | ✅ | — | — | ✅ |
| Account (password, profile, sessions, **API keys**) | ✅ | — | — | — |

A key without the needed scope gets `403 auth.insufficient_scope`; account routes answer API keys with `403 auth.session_required` (a leaked key can never mint more keys or change the password). Unknown, revoked and expired keys get `401 auth.unauthorized`. Bearer requests carry no cookie, so the CSRF `Origin` check does not apply to them.

## Auth routes

### `GET /v1/auth/providers`

Which sign-in methods the server offers. → `200 {"password": true, "google": false}`

### `POST /v1/auth/register`

```json
{"email": "ana@example.com", "password": "a long passphrase", "name": "Ana", "locale": "pt-BR"}
```

`name` and `locale` are optional (`locale` defaults to `en`). The email is lowercased. Password policy: 10–256 characters, not equal to the email. → `201 {"user": …}` plus the session cookie, and a verification email (link to `HS_APP_URL/verify-email?token=…`, valid 48 h). Errors: `auth.email_taken`, `auth.invalid_email`, `auth.invalid_locale`, `password.too_short`, `password.too_long`, `password.same_as_email`, `auth.too_many_attempts` (more than `HS_SIGNUP_RATE_LIMIT` sign-ups — 10 in production — from one IP, IPv6: one /64, within 15 minutes).

### `POST /v1/auth/login`

`{"email": "...", "password": "..."}` → `200 {"user": …}` plus the session cookie. Errors: `auth.invalid_credentials`, `auth.too_many_attempts`.

### `POST /v1/auth/logout`

Deletes the current session and clears the cookie. → `204`

### `POST /v1/auth/verify-email`

`{"token": "..."}` (from the email link) → `204`. Tokens are single-use. Error: `auth.invalid_token`.

### `POST /v1/auth/resend-verification` (signed in)

Sends a new verification email (older links stop working). → `202`

### `POST /v1/auth/forgot-password`

`{"email": "..."}` → always `202`, at once, whether or not the account exists: the account lookup and the email happen in the background, so neither the answer nor its timing reveals whether the address is registered. Sends a reset link (`HS_APP_URL/reset-password?token=…`, valid 1 h) when it is.

### `POST /v1/auth/reset-password`

`{"token": "...", "password": "new passphrase"}` → `204`. The new password is checked before the token is used, so after a `password.*` error the same link still works; it is single-use once a reset succeeds. Signs out every session and revokes every API key. If the account's email was never verified, the reset is a takeover by the proven owner of the address and the account is emptied first (as for Google sign-in below). Errors: `auth.invalid_token`, `password.*`.

### `POST /v1/auth/change-password` (signed in)

`{"current_password": "...", "new_password": "..."}` → `204`. The current session stays signed in; others are signed out. Errors: `auth.wrong_password`, `password.*`.

### `GET /v1/me` (signed in)

→ `200 {"user": …}`

### `PATCH /v1/me` (signed in)

Any subset of `{"name": "Ana", "locale": "es", "theme_preference": "light"}` → `200 {"user": …}`. The plan and the email cannot be changed here. Errors: `auth.invalid_name` (over 100 characters), `auth.invalid_locale`, `auth.invalid_theme`.

## Endpoints (webhook entry points)

An endpoint receives webhooks at `ingest_url` (`HS_PUBLIC_INGEST_URL/v1/{slug}`) and forwards them to `target_url`. Every change is visible to the ingest service immediately (its cache entry is dropped).

```json
{
  "id": "0193…", "name": "Stripe", "description": "", "slug": "ep_k3j7x2m4q5ab",
  "ingest_url": "https://in.hookshield.dev/v1/ep_k3j7x2m4q5ab",
  "target_url": "https://api.example.com/webhooks/stripe", "status": "active",
  "auth": {"mode": "token", "header": "X-HookShield-Token", "token_prefix": "hs_in_8f3a", "ip_allowlist": []},
  "outbound_headers": {"Authorization": "Bearer {{secrets.API_KEY}}"},
  "transform_rules": {},
  "retry": {"strategy": "exponential", "max_retries": 5, "initial_interval_ms": 10000, "multiplier": 2, "max_delay_ms": 3600000, "custom_schedule_ms": []},
  "dlq_enabled": true,
  "alert": {"threshold_failures": 4, "webhook_url": null},
  "created_at": "…", "updated_at": "…"
}
```

| Route | Access | Result |
|---|---|---|
| `GET /v1/endpoints` | read | `200 {"endpoints": [...]}` newest first |
| `POST /v1/endpoints` | configure | `201 {"endpoint", "secrets", "warnings"}` |
| `GET /v1/endpoints/{id}` | read | `200 {"endpoint", "warnings"}` |
| `PATCH /v1/endpoints/{id}` | configure | `200 {"endpoint", "secrets", "warnings"}` — only the fields sent change |
| `POST /v1/endpoints/{id}/rotate-secret` | configure | `200 {"endpoint", "secrets"}` — new token (token mode) or generated HMAC secret (hmac mode); the old one stops working at once |
| `DELETE /v1/endpoints/{id}` | configure | `204` — its logs and alert channels go with it |
| `POST /v1/endpoints/{id}/test` | operate | `202 {"id", "status": "queued"}` — sends a test webhook (see below) |

Endpoints of other accounts answer `404 endpoint.not_found`, exactly like missing ones.

**Create/update body** — every field is optional on `PATCH`; on `POST` only `name` and `target_url` are required (the rest gets the defaults shown above):

| Field | Rules |
|---|---|
| `name` | 1–100 characters |
| `description` | up to 1,000 characters |
| `slug` | `POST` only: `^[a-z0-9][a-z0-9_-]{2,99}$`, unique (`409 endpoint.slug_taken`). Default: random `ep_…`. Never changes afterwards (`endpoint.slug_immutable`): senders use it |
| `target_url` | `http(s)://`, no credentials. When `HS_ALLOW_PRIVATE_TARGETS=false` (production default), `localhost`, `*.internal` and private/loopback/link-local/metadata IPs are refused (`endpoint.private_target`); the dispatcher re-checks resolved addresses at delivery time |
| `status` | `active`, `paused` (ingest answers `503` + `Retry-After`, senders retry) or `debug` |
| `auth.mode` | `token` (default), `hmac` or `ip_allowlist` — there is no unauthenticated mode |
| `auth.header` | header carrying the token (default `X-HookShield-Token`) |
| `auth.hmac_header` / `auth.hmac_secret` | `hmac` mode: header with the signature (default `X-HookShield-Signature`) and the provider's signing secret (8–512 bytes, e.g. a GitHub webhook secret). Without `hmac_secret` a secret is generated. Only an HMAC-SHA256 of the raw body is checked ([INGEST.md](INGEST.md#authentication-zero-trust)): timestamped schemes such as Stripe's `Stripe-Signature` cannot be verified here — use `token` mode and let your app check them |
| `auth.ip_allowlist` | `ip_allowlist` mode: up to 100 IPs or CIDRs |
| `outbound_headers` | up to 50 headers added to deliveries; values may reference `{{secrets.NAME}}`. `Host`, `Content-Length`, hop-by-hop headers and `X-HookShield-*` are refused |
| `transform_rules` | a rules document ([TRANSFORM.md](TRANSFORM.md)); problems come back as `422 transform.invalid` with `details` |
| `retry` | `strategy` `fixed`/`exponential`/`custom`; `max_retries` 0–100; `initial_interval_ms` 1 s–7 days; `multiplier` 1–10; `max_delay_ms` ≥ initial, ≤ 7 days; `custom_schedule_ms` 1–100 delays (required for `custom`). See [DELIVERY.md](DELIVERY.md) |
| `dlq_enabled` | keep failed webhooks as `dead` (replayable) instead of `failed` |
| `alert.threshold_failures` / `alert.webhook_url` | consecutive failed attempts before an alert (1–100); legacy plain-text alert URL (`""` clears it) — prefer alert channels |

**Secrets are shown once.** `secrets.token` (`hs_in_…`) appears when token auth is switched on (creation, switching modes, rotation); `secrets.hmac_secret` only when HookShield generated it. They are stored as a SHA-256 hash (token) or encrypted (HMAC secret) and are never returned again — `auth.token_prefix` helps recognize which token is configured. Switching auth mode discards the previous mode's credentials.

**Warnings** list `{{secrets.NAME}}` references that do not exist yet (`{"code": "endpoint.secret_missing", "secret": "API_KEY"}`): the endpoint is saved, but deliveries fail with `secret_not_found` until the secret is created.

Other codes: `endpoint.invalid_name`, `endpoint.invalid_description`, `endpoint.invalid_slug`, `endpoint.invalid_target_url`, `endpoint.invalid_status`, `endpoint.invalid_auth_mode`, `endpoint.invalid_header`, `endpoint.invalid_hmac_header`, `endpoint.invalid_hmac_secret`, `endpoint.hmac_fields_need_hmac_mode`, `endpoint.invalid_ip_allowlist` (with the bad `value`), `endpoint.ip_allowlist_required`, `endpoint.too_many_outbound_headers`, `endpoint.invalid_outbound_header` / `_value` (with the `header`), `transform.invalid_json`, `retry.invalid_strategy`, `retry.invalid_max_retries`, `retry.invalid_initial_interval`, `retry.invalid_multiplier`, `retry.invalid_max_delay`, `retry.invalid_custom_schedule`, `alert.invalid_threshold`, `alert.invalid_webhook_url`, `alert.private_target`, `endpoint.no_secret_to_rotate` (409, `ip_allowlist` mode).

**Test events** (`POST /v1/endpoints/{id}/test`) — the dashboard's sandbox and onboarding. The webhook goes through the whole pipeline (transform rules, outbound headers, retries, alerts) and becomes a normal log; only the inbound authentication is skipped, because the caller is already signed in. It counts towards the rate limit and the quota like any webhook. Every field is optional:

```json
{"method": "POST", "headers": {"Content-Type": ["application/json"]}, "body": {"type": "order.created"}}
```

`method` is `POST` (default), `PUT` or `PATCH`; `headers` replaces the defaults (`Content-Type: application/json`, `User-Agent: HookShield-Test/1`); `body` (JSON) or `body_text` (anything else); like every API request, the JSON sent must fit in 64 KiB (`validation.invalid_json` otherwise). Without a body it sends `{"type": "hookshield.test", "message": "Hello from HookShield", "sent_at": "…"}`. Errors: `endpoint.not_found`, `endpoint.paused` (409), `endpoint.invalid_test_method`, `endpoint.invalid_test_body`, `endpoint.invalid_test_header`, `endpoint.test_body_too_large`, `rate_limited`, `quota_exceeded`.

## Secrets

Named values used as `{{secrets.NAME}}` in outbound headers (e.g. the API key of the destination). Encrypted with AES-256-GCM, bound to the account and name; **values can never be read back**.

| Route | Access | Result |
|---|---|---|
| `GET /v1/secrets` | read | `200 {"secrets": [{"name", "reference", "created_at", "updated_at"}]}` |
| `PUT /v1/secrets/{NAME}` | configure | body `{"value": "…"}` → `200 {"secret": …}` (creates or replaces) |
| `DELETE /v1/secrets/{NAME}` | configure | `204` (`404 secret.not_found`) |

`NAME`: `^[A-Z][A-Z0-9_]{0,63}$` (`secret.invalid_name`). Value: 1 byte–8 KiB, no line breaks (`secret.invalid_value`). `reference` is the exact text to paste: `{{secrets.STRIPE_KEY}}`.

## Alert channels

Where failure alerts go ([ALERTS.md](ALERTS.md)). Targets are encrypted; responses show them **masked** (URL host only, last 4 characters of Slack URLs and bot tokens).

| Route | Access | Result |
|---|---|---|
| `GET /v1/alert-channels` | read | `200 {"alert_channels": [{"id", "type", "endpoint_id", "enabled", "target", "created_at"}]}` |
| `POST /v1/alert-channels` | configure | body `{"type", "target", "endpoint_id"?, "enabled"?}` → `201 {"alert_channel"}` |
| `PATCH /v1/alert-channels/{id}` | configure | any of `endpoint_id` (`null` = every endpoint), `enabled`, `target` (full replacement; the type never changes) |
| `DELETE /v1/alert-channels/{id}` | configure | `204` |

| `type` | `target` |
|---|---|
| `webhook` | `{"url": "https://…"}` — same private-address rules as `target_url` |
| `slack` | `{"webhook_url": "https://hooks.slack.com/services/…"}` |
| `telegram` | `{"bot_token": "123456:AA…", "chat_id": "-100123…"}` (or `@channel`) |
| `email` | `{"to": "ops@example.com"}` — empty `to` = the account email |

`endpoint_id` limits the channel to one of your endpoints (`alert.invalid_endpoint` otherwise); `null` applies it to all. **Email channels need a verified account email** (`403 alert.email_unverified`), and one account sends at most 100 alert emails an hour. Errors: `alert.invalid_type`, `alert.invalid_target`, `alert.private_target`, `alert.email_unverified`, `alert.channel_not_found`.

## API keys

For scripts, CI and infrastructure as code. Dashboard session only.

| Route | Result |
|---|---|
| `GET /v1/api-keys` | `200 {"api_keys": [{"id", "name", "prefix", "scope", "created_at", "last_used_at", "expires_at", "revoked_at"}]}` |
| `POST /v1/api-keys` | body `{"name", "scope", "expires_in_days"?}` → `201 {"api_key", "key"}` — **`key` (`hs_live_…`) is shown once** |
| `DELETE /v1/api-keys/{id}` | `204` — revoked immediately (kept in the list with `revoked_at`) |

`scope`: `read_only`, `ingest` or `admin` (see the access table). `expires_in_days`: 1–3650 (default: never). Only a SHA-256 hash is stored; `last_used_at` is updated at most once a minute. Errors: `api_key.invalid_name`, `api_key.invalid_scope`, `api_key.invalid_expiry`, `api_key.not_found`.

## Transform preview

`POST /v1/transform/preview` (read) runs rules over a sample request without saving anything — the live preview of the pipeline editor.

```json
{
  "rules": {"version": 1, "body": [{"op": "rename", "from": "user_id", "to": "customerId"}],
            "headers": [{"op": "set", "name": "X-Env", "value": "prod"}]},
  "request": {"headers": {"X-Debug": "1", "Accept": ["a", "b"]}, "body": {"user_id": 42}}
}
```

→ `200 {"request": {"headers": {...}, "body": {"customerId": 42}}, "warnings": []}`. Use `body_text` instead of `body` for non-JSON payloads (body rules are then skipped, with a warning). Invalid rules → `422 transform.invalid` with `details`. Rules whose copies would grow the body past the input size + 64 KiB → `422 transform.output_too_large` ([TRANSFORM.md](TRANSFORM.md#limits)).

## Logs (webhooks received)

Every webhook becomes a log: metadata in clear, request and responses encrypted. Logs are kept until `expires_at` (`HS_RETENTION` self-hosted; per plan on HookShield Cloud); the worker deletes expired logs every minute.

### `GET /v1/logs` (read)

Newest first. Filters, all optional and combinable:

| Parameter | Meaning |
|---|---|
| `endpoint_id` | one endpoint |
| `status` | comma-separated: `queued`, `processing`, `delivered`, `retrying`, `failed`, `dead` |
| `event_type` | exact event type (e.g. `invoice.paid`) |
| `q` | log ID prefix or part of the event type |
| `from`, `to` | RFC 3339 times (`from` inclusive, `to` exclusive) |
| `limit` | 1–200 (default 50) |
| `cursor` | `next_cursor` of the previous page |

→ `200 {"logs": [...], "next_cursor": "…" | null}`. Pagination is keyset-based: pages never skip or repeat rows while new webhooks arrive. Invalid filters → `422 log.invalid_filter` / `log.invalid_cursor`.

```json
{
  "id": "01a1a2…", "endpoint": {"id": "…", "name": "Stripe", "slug": "ep_k3j9…"}, "replay_of": null,
  "method": "POST", "event_type": "invoice.paid", "content_type": "application/json", "size_bytes": 35,
  "source_ip": "203.0.113.7", "status": "delivered", "attempts": 2, "last_status_code": 200, "next_attempt_at": null,
  "latency": {"ingest_ms": 1.19, "transform_ms": 0.06, "persist_ms": 1.43, "gateway_ms": 2.68},
  "received_at": "…", "delivered_at": "…", "expires_at": "…"
}
```

`latency` is the Visual Flow Tracker: time spent in each gateway stage (delivery times are per attempt, below).

### `GET /v1/logs/{id}` (read) — request debugger

```json
{
  "log": { …same as the list… },
  "request": {
    "original":    {"method": "POST", "query": "", "headers": {"Content-Type": ["application/json"]}, "body": {"user_id": 1}},
    "transformed": {"method": "POST", "query": "", "headers": {...}, "body": {"customerId": 1}}
  },
  "attempts": [
    {"number": 1, "started_at": "…", "duration_ms": 0.42, "status_code": 500, "error_code": null,
     "response": {"status": 500, "headers": {...}, "body_text": "boom"}},
    {"number": 2, "started_at": "…", "duration_ms": 0.24, "status_code": 200, "error_code": null,
     "response": {"status": 200, "headers": {...}, "body": {"ok": true}}}
  ],
  "replays": ["01a1a2…"]
}
```

Bodies are shown as `body` (JSON), `body_text` (other UTF-8) or `body_base64` (binary). With `?raw=true` every body (requests and responses) is `body_base64`, byte for byte — what `hookshield listen` uses to forward a webhook exactly as received (JSON in `body` is re-encoded and loses its original whitespace). `transformed` is `null` when the endpoint had no transform rules. The inbound auth token is never stored, so it never appears. `error_code` classifies attempts that got no HTTP response (`timeout`, `connection_refused`, `dns`, `tls`, `blocked_destination`, `secret_not_found`, …; see [DELIVERY.md](DELIVERY.md)). A log encrypted with a key this server does not have → `409 log.key_unavailable`.

### `POST /v1/logs/{id}/replay` (operate)

Sends a stored webhook again. Without a body it is replayed exactly as received; optionally:

```json
{"body": {"user_id": 2}}            // or "body_text": "…" for non-JSON payloads
{"headers": {"X-Custom": ["v"]}}    // replaces all request headers
```

→ `202 {"id": "<new log>", "replay_of": "<original>", "status": "queued"}`. The replay is a **new log** that goes through the whole pipeline with the endpoint's **current** settings (transform rules, outbound headers and secrets, retry policy, alerts) — handy after fixing a destination or a rule. It counts towards the rate limit and the monthly quota like any webhook (`429 rate_limited` / `quota_exceeded`). Errors: `log.not_found`, `log.invalid_replay_body`, `log.invalid_replay_header`. Without an edited body the stored webhook is replayed whatever its size; an edited one must fit in the 64 KiB API request limit (`validation.invalid_json` otherwise).

## Metrics

All take `range` = `1h`, `24h` (default), `7d` or `30d` (`422 metrics.invalid_range`) and, except `/endpoints`, an optional `endpoint_id`.

| Route (read) | Result |
|---|---|
| `GET /v1/metrics/summary` | Dashboard cards: `total_received`, `delivered`, `failed` (failed + dead), `in_flight`, `active_retries` (waiting now, any age), `delivery_rate` (% of finished webhooks delivered; `null` when none finished), `avg_latency_ms` / `p95_latency_ms` (gateway time), `avg_delivery_ms` (destination response time) |
| `GET /v1/metrics/series` | Traffic chart: `{"bucket_seconds", "points": [{"at", "received", "delivered", "failed", "error_rate", "avg_latency_ms"}]}`. Buckets: 1 min (1h), 1 h (24h), 6 h (7d), 1 day (30d); empty buckets included |
| `GET /v1/metrics/endpoints` | Per endpoint: `received`, `delivered`, `failed`, `success_rate`, `last_received_at` (every endpoint listed, idle ones too) |

### `GET /v1/status` (read)

Cluster status: `{"status": "operational" | "degraded", "database": "up", "redis": "up", "queues": {"ingest": {"length", "waiting", "pending", "consumers"}, "dispatch": {…}, "alerts": {…}, "retry_scheduled": 0, "dead_letters": 0}, "checked_at"}`. `waiting` = not yet read by a worker; `pending` = being processed; `consumers` = worker goroutines attached. On multi-tenant installs (HookShield Cloud) `queues` is empty: install-wide numbers would reveal other accounts' traffic.

## Search

`GET /v1/search?q=` (read) — the ⌘K palette. `q`: 2–100 characters. Returns up to 5 of each: `{"endpoints": [{"id", "name", "slug", "status"}], "logs": [...], "secrets": [...]}` — endpoints by name, slug or target URL; logs by ID prefix or event type; secrets by name.

## Live feed (Server-Sent Events)

`GET /v1/live` (read; optional `endpoint_id`) streams the account's log events:

```
event: log.created
id: 01a1a2…
data: {"type":"log.created","log_id":"01a1a2…","endpoint_id":"…","status":"queued","method":"POST","event_type":"invoice.paid","latency_ms":2.68,"at":"…"}

event: log.updated
id: 01a1a2…
data: {"type":"log.updated","log_id":"01a1a2…","endpoint_id":"…","status":"delivered","attempt":1,"status_code":200,"latency_ms":120.5,"at":"…"}
```

`log.created` is sent when a webhook is stored (or replayed), `log.updated` after every delivery attempt (`status` = `delivered`, `retrying`, `failed` or `dead`). Events never contain payloads. A `: ping` comment keeps the connection open every 15 s. In the browser: `new EventSource(API + "/v1/live", {withCredentials: true})`. Delivery is best-effort (Redis pub/sub): after a reconnect, refresh the list with `GET /v1/logs`. When an API instance shuts down it ends its open feeds at once; clients reconnect (`retry: 3000`). The `hookshield listen` command ([CLI.md](CLI.md)) is built on this feed.

## Features

`GET /v1/features` (public) → `{"billing": false}` on self-hosted installs. The dashboard shows the billing page only when `billing` is `true` (HookShield Cloud).

## Billing (HookShield Cloud)

Mounted only on servers with a billing provider (`app.Options.Billing`); self-hosted installs answer `404`. Dashboard session only (API keys get `403 auth.session_required`), except the provider's webhook.

| Route | Result |
|---|---|
| `GET /v1/billing` | `200 {"plan", "subscription", "usage", "offers"}` — see below |
| `POST /v1/billing/checkout` | body `{"plan": "pro" \| "business", "interval": "month" \| "year"}` (`interval` defaults to `month`; `year` only for offers with `yearly_price_cents` > 0) → `200 {"url", "subscription"}`. Without a usable subscription: `url` is the provider's hosted checkout (it returns to `HS_APP_URL/{locale}/billing?checkout=success`), `subscription` is `null`. With one: the plan or the interval is switched in place (prorated), `url` is `null` and `subscription` is the updated one |
| `POST /v1/billing/portal` | `200 {"url"}` — the provider's customer portal (payment method, invoices, cancellation) |
| `POST /v1/billing/webhook` | Provider notifications, authenticated by signature (public, no cookie, max 256 KiB) → `200 {"ok": true}` |

```json
{
  "plan": "pro",
  "subscription": {"plan": "pro", "interval": "month", "status": "active", "renews_at": "2026-12-03T18:00:00Z", "ends_at": null, "test_mode": false},
  "usage": {"period_start": "2026-11-01T00:00:00Z", "period_end": "2026-12-01T00:00:00Z", "requests": 1234, "included": 100000, "hard_limit": false},
  "offers": [{"id": "pro", "monthly_price_cents": 2900, "yearly_price_cents": 29000, "currency": "USD", "overage_price_cents": 100, "overage_unit": 10000,
              "purchasable": true, "contact_url": "", "limits": {"monthly_requests": 100000, "hard_limit": false, "max_retries": 10, "retention_hours": 720}}]
}
```

`usage.requests` is live (Redis): accepted webhooks, replays and test events of the calendar month (UTC); retries do not count. `null` limits mean unlimited. Quotas are monthly even on a yearly subscription (`interval: "year"`); `yearly_price_cents` is the price of a year paid at once (`0` = not sold yearly). Subscription `status`: `active`, `trialing`, `past_due` (payment being retried — the plan still applies), `cancelled` (applies until `ends_at`), `paused`, `unpaid`, `expired` (free plan). Notifications older than the stored one are ignored, so out-of-order delivery is harmless.

Errors: `billing.invalid_plan` (422, plan not sold online), `billing.same_plan` (422, already on that plan and interval), `billing.invalid_interval` (422), `billing.no_subscription` (409), `billing.provider_error` (502), and for the webhook `billing.invalid_signature`, `billing.invalid_payload` (400), `billing.not_found` (404, unknown subscription), `billing.subscription_conflict` (409, the provider ID belongs to another account), `billing.payload_too_large` (413).

## Google sign-in

A browser flow (full-page navigation, not `fetch`):

1. The dashboard links to `GET /v1/auth/google/start?redirect=/endpoints`.
2. The API stores a random `state` plus a PKCE verifier (S256) in Redis for 10 minutes, sets a short-lived `hs_oauth_state` cookie bound to the browser, and redirects (`302`) to Google with scopes `openid email profile`.
3. Google redirects to `GET /v1/auth/google/callback?code=…&state=…`. The API checks the state against the cookie (login-CSRF protection), exchanges the code with the PKCE verifier, and reads the profile.
4. Account resolution:
   - the Google account is already linked → sign in;
   - otherwise an account with the same email exists → the Google account is linked to it (only if Google says the email is verified);
   - otherwise a new account is created (email verified, no password, locale from Google when supported).
5. The API sets `hs_session` and redirects to `HS_APP_URL` + `redirect`. Only same-site relative paths are accepted (`/x`), so `//evil.com` or `https://evil.com` fall back to `/`.

On failure it redirects to `HS_APP_URL/login?error=<code>`: `auth.google_failed` (cancelled or Google error), `auth.google_email_unverified`, `auth.invalid_token` (expired or foreign state), `auth.google_disabled`.

**Setup:** in Google Cloud Console create an OAuth client of type *Web application*, add the redirect URI `HS_API_URL/v1/auth/google/callback`, and set `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`.

## Emails

Account emails (verification, reset, password changed) are plain text in the account's language. Production sends them through **Resend** (`HS_RESEND_API_KEY`); development uses SMTP to Mailpit (`http://localhost:8025`). Without either, the API logs that the email was skipped, so local sign-up still works. See [CONFIGURATION.md](CONFIGURATION.md).

## Try it with curl

```bash
A=http://localhost:8081; O='Origin: http://localhost:3000'; J='Content-Type: application/json'
curl -c jar -H "$O" -H "$J" -d '{"email":"ana@example.com","password":"a long enough passphrase"}' $A/v1/auth/register
curl -b jar $A/v1/me
# open http://localhost:8025 to see the verification email
# create an endpoint (the token is printed once)
curl -b jar -H "$O" -H "$J" -d '{"name":"Demo","target_url":"http://localhost:4000/webhook"}' $A/v1/endpoints
# an API key for scripts, then use it
curl -b jar -H "$O" -H "$J" -d '{"name":"CI","scope":"read_only"}' $A/v1/api-keys
curl -H "Authorization: Bearer hs_live_…" $A/v1/endpoints
curl -b jar -c jar -X POST -H "$O" $A/v1/auth/logout
```

## Code

`internal/api` (HTTP: routing, cookies, CORS/CSRF, error mapping), `internal/auth` (use cases, Google OAuth, throttling), `internal/password` (argon2id + policy), `internal/emails` (localized templates), `internal/mailer` (Resend + SMTP), `internal/store/auth.go` and `internal/store/manage.go` (queries, always scoped by account). Handlers: `internal/api/endpoints.go`, `internal/api/resources.go`, `internal/api/logs.go` (logs, replay, metrics, status, search, live feed); access levels in `internal/api/middleware.go`; queries in `internal/store/logs.go`; live events in `internal/live`. Tests: `internal/api/auth_integration_test.go` (every auth flow, fake Google server that checks PKCE) and `internal/api/manage_integration_test.go` (every resource, with a real ingest service sharing the endpoint cache: tokens, rotation, pausing and HMAC changes are checked from the sender's side; account isolation; API key scopes) and `internal/api/logs_integration_test.go` (the real pipeline — ingest, processor, dispatcher and a destination — behind the debugger, replays, pagination, metrics, search and the SSE feed).
