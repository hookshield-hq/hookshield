# Ingest API — receiving webhooks

This is the public contract of the HookShield **ingest** service: the URL you paste into Stripe, GitHub, Shopify or any other webhook provider.

```
POST https://in.hookshield.dev/v1/{slug}
```

`{slug}` identifies your endpoint (e.g. `ep_demo`). Self-hosted installs use their own `HS_PUBLIC_INGEST_URL` — behind one domain (production compose, one-click deploys) that is `https://your.domain/in`, so webhook URLs look like `https://your.domain/in/v1/{slug}` ([SELF_HOSTING.md](SELF_HOSTING.md)).

## Lifecycle of a request

```
method → endpoint lookup → IP allow-list → token → paused? → body (size cap)
       → HMAC → paused? (HMAC mode) → rate limit / monthly quota → encrypt → enqueue → 202 Accepted
```

The response is sent as soon as the request is **encrypted and queued**; transformation, storage and delivery to your application happen asynchronously in the worker. Typical response time is a few milliseconds (p99 budget: 20 ms).

## Methods

`POST`, `PUT` and `PATCH` are accepted. Anything else returns `405 method_not_allowed` with an `Allow` header.

## Authentication (zero-trust)

Every endpoint has exactly one inbound authentication mode; there is no unauthenticated mode. Requests that fail authentication are dropped **before anything is stored**.

| Mode | How the sender proves itself | Notes |
|---|---|---|
| `token` (default) | Header `X-HookShield-Token: hs_in_…` (header name configurable per endpoint) | The token is shown once at creation; HookShield stores only its SHA-256 and compares in constant time. The token header is **removed** before the request is stored or forwarded. |
| `hmac` | Header with an HMAC-SHA256 of the raw body, keyed with the endpoint secret (header name configurable, e.g. `X-Hub-Signature-256`) | Accepted formats: `<hex>`, `sha256=<hex>` (GitHub), `<base64>` (Shopify), compared with `hmac.Equal`. The secret is stored encrypted (AES-256-GCM). There is no timestamp, so a captured request can be replayed; schemes that sign a timestamp too (Stripe's `t=…,v1=…`) are not supported — use `token` mode and verify them in your app. |
| `ip_allowlist` | Request comes from an allowed IP/CIDR | Behind a reverse proxy, set `HS_TRUSTED_PROXIES` so the real client IP is taken from `X-Forwarded-For` (rightmost untrusted hop). Spoofed `X-Forwarded-For` from untrusted peers is ignored. |

> **Why not tokens in the query string?** URLs end up in logs, browser history and provider dashboards. Tokens are only accepted in headers.

Unknown endpoints and invalid credentials return the **same** `401 unauthorized`, so endpoint slugs cannot be enumerated.

## Responses

All responses are JSON. Errors use a stable machine-readable code (the dashboard translates codes into English, Português and Español):

```json
{"error": {"code": "unauthorized"}}
```

| Status | Code | Meaning | Should the sender retry? |
|---|---|---|---|
| `202` | — | Accepted and queued. Body: `{"id": "<request id>", "status": "queued"}` | No |
| `400` | `bad_request` | The body could not be read | No |
| `401` | `unauthorized` | Unknown endpoint, missing or invalid credentials | No |
| `405` | `method_not_allowed` | Method other than POST/PUT/PATCH | No |
| `413` | `payload_too_large` | Body larger than `HS_MAX_BODY_BYTES` (default 1 MiB) | No |
| `429` | `rate_limited` | Endpoint above `HS_INGEST_RATE_LIMIT` requests/second. `Retry-After: 1` | Yes |
| `429` | `quota_exceeded` | Monthly request quota of the plan used up — HookShield Cloud only (Developer plan: 5,000); self-hosted installs have no quota | Next month / after upgrade |
| `503` | `endpoint_paused` | The endpoint is paused in the dashboard. `Retry-After: 60` | Yes — providers' own retries will succeed once it is resumed |
| `503` | `unavailable` | Temporary internal problem (queue or database unreachable). `Retry-After: 5` | Yes |

Successful responses also carry the request ID in the `X-HookShield-Request-Id` header. Use it to find the request in the dashboard's log debugger.

### Order of checks

Authentication is evaluated **before** the paused state and limits (and before the size cap, except in `hmac` mode, where the signature covers the body). A paused endpoint therefore still answers `401` to bad credentials, and requests that fail authentication never count against your quota.

## What is stored

| Kept (encrypted) | Dropped |
|---|---|
| Method, query string, body, all request headers | The inbound token header, hop-by-hop headers (`Connection`, `Transfer-Encoding`, …) |

The request is sealed with AES-256-GCM **in the ingest service**, so the payload is encrypted in the queue (Redis) as well as in PostgreSQL. Only metadata stays in plain text: method, content type, size, source IP and timestamps, plus the event type the worker detects (e.g. `invoice.paid`, so logs can be filtered by it) — keep secrets out of event-type fields.

## Limits

| Limit | Default | Configure with |
|---|---|---|
| Body size | 1 MiB | `HS_MAX_BODY_BYTES` |
| Requests per second, per endpoint | 100 | `HS_INGEST_RATE_LIMIT` (`0` = off) |
| Requests per month, per account | Self-hosted: unlimited (still counted). HookShield Cloud, by plan — Developer 5,000 (hard limit); Pro 100,000, Business 1,000,000 (overage billed, never rejected); Enterprise unlimited | `core.Plans` (see [ARCHITECTURE.md](ARCHITECTURE.md#editions-and-extension-points-open-core)) |

Rejected requests are not counted towards the monthly quota.

## Example

```bash
curl -i -X POST https://in.hookshield.dev/v1/ep_demo \
  -H 'X-HookShield-Token: hs_in_…' \
  -H 'Content-Type: application/json' \
  -d '{"type":"invoice.paid"}'

HTTP/1.1 202 Accepted
Content-Type: application/json; charset=utf-8
X-Hookshield-Request-Id: 01a1a237-a069-7f34-ad12-d8833bba3bce

{"id":"01a1a237-a069-7f34-ad12-d8833bba3bce","status":"queued"}
```

Locally, `make seed` prints a ready-to-run command for the demo endpoint.

## Health

| Route | Meaning |
|---|---|
| `GET /healthz` | Process is alive |
| `GET /readyz` | PostgreSQL and Redis reachable (`503` otherwise) |
