# Delivery — what your application receives

After a webhook is accepted and processed, the **dispatcher** sends it to the endpoint's destination URL, retries on failure and alerts you when it keeps failing.

## The request

| Part | Value |
|---|---|
| Method | Same as the original request (`POST`, `PUT` or `PATCH`) |
| URL | The endpoint's destination URL; the original query string is appended (`https://app.example.com/hooks?src=hs` + `?from=stripe` → `…/hooks?src=hs&from=stripe`) |
| Body | The transformed body, or the original byte-for-byte when the endpoint has no body rules ([TRANSFORM.md](TRANSFORM.md)) |
| Headers | Original headers (after header rules) + your outbound custom headers + the HookShield headers below |

Never forwarded: `Host`, `Content-Length`, hop-by-hop headers, `Accept-Encoding`, the inbound HookShield token, and any `X-HookShield-*` header sent by the provider (so they cannot be spoofed).

### HookShield headers

| Header | Example | Use |
|---|---|---|
| `X-HookShield-Id` | `01a1a24e-0bc9-7d8c-b407-8aed75037678` | Unique webhook ID. **Use it to de-duplicate**: the same ID can arrive more than once (see "Guarantees"). |
| `X-HookShield-Attempt` | `2` | Attempt number, starting at 1 |
| `X-HookShield-Timestamp` | `1793718245` | Unix time (seconds) of this attempt |
| `X-HookShield-Event` | `invoice.paid` | Detected event type, when known |
| `User-Agent` | `HookShield/v0.4.0` | |

### Outbound custom headers and secrets

Each endpoint can add headers to every delivery, e.g. to authenticate against your API. Values can reference named secrets:

```json
{"Authorization": "Bearer {{secrets.MY_API_KEY}}", "X-Team": "billing"}
```

Secrets are stored encrypted (AES-256-GCM) per account and only decrypted in memory at delivery time, so they never appear in stored payloads or logs. Names are `UPPER_SNAKE_CASE`. If a referenced secret does not exist, the attempt is recorded as failed with `secret_not_found` **without calling your application**, and retried normally — adding the secret fixes the next attempt.

## Success and failure

| Outcome | Counts as |
|---|---|
| HTTP `2xx` | **Delivered** |
| Any other status (`3xx` redirects are not followed, `4xx`, `5xx`) | Failed attempt |
| No response | Failed attempt with an error code: `timeout`, `connection_refused`, `connection_reset`, `dns`, `tls`, `blocked_destination`, `network`, `secret_not_found`, `invalid_header`, `invalid_target` |

Each attempt is stored with its status code or error code, duration and the destination's response (status, headers and up to 64 KiB of body, encrypted) for the log debugger.

Timeout per attempt: `HS_DISPATCH_TIMEOUT` (default 15 s).

## Retries

Every endpoint has a retry policy (edited in the dashboard):

| Setting | Default | Meaning |
|---|---|---|
| `max_retries` | 5 | Retries **after** the first attempt — 5 means at most 6 attempts. Self-hosted: no cap beyond the schema limit. HookShield Cloud caps it by plan (Developer 3, Pro 10, Business/Enterprise custom). |
| `retry_strategy` | `exponential` | `fixed` (always the initial interval), `exponential` (initial × multiplier^(n−1)) or `custom` (explicit list of delays; the last one repeats) |
| `retry_initial_interval_ms` | 10 000 | First delay |
| `retry_multiplier` | 2 | Growth factor for `exponential` |
| `retry_max_delay_ms` | 3 600 000 | Upper bound for any delay |

Default schedule: 10 s, 20 s, 40 s, 80 s, 160 s. Delays get ±10 % jitter to avoid thundering herds, are never shorter than 1 s, and **honour `Retry-After`** (seconds or HTTP date) from `429`/`503` responses when it asks for a longer wait (still capped by the max delay).

When the last attempt fails the webhook becomes **`dead`** (dead-letter queue enabled — default) or **`failed`**. Both are kept for the retention period (`HS_RETENTION` self-hosted, default 30 days; per plan on HookShield Cloud) and can be replayed from the dashboard.

### Paused endpoints

While an endpoint is paused, pending deliveries are held and re-checked every minute **without consuming attempts**. They resume automatically when the endpoint is reactivated.

## Alerts

You are alerted **once per webhook** when its failed attempts reach the endpoint's `alert_threshold_failures` (default 4), or when delivery is abandoned before reaching it, with at most one alert per endpoint every 10 minutes (the rest are grouped). Channels: webhook, Slack, Telegram and email — see [ALERTS.md](ALERTS.md).

## Lifecycle

```
queued ──► delivered
   │
   └──► retrying ──► (… attempts …) ──► delivered
              │
              └──► dead   (last attempt failed, DLQ on)
              └──► failed (last attempt failed, DLQ off)
```

`payload_logs.status`, `attempt_count`, `last_status_code`, `next_attempt_at` and `delivered_at` always reflect the latest state; `delivery_attempts` keeps one row per attempt.

## Guarantees

- **At-least-once.** A webhook is delivered until a `2xx` is received or attempts run out. In rare failure scenarios (a worker crashing after your app answered but before the result was recorded) the same attempt can be sent twice. De-duplicate with `X-HookShield-Id`.
- **No lost retries.** Retries are scheduled in Redis and moved to the delivery queue atomically; a sweeper also re-schedules any retry whose next attempt is more than 2 minutes overdue in the database.
- **Ordering is not guaranteed** between different webhooks (retries and parallel workers interleave). Use the event's own timestamps when order matters.

## Security: destination restrictions (SSRF)

Destination URLs are chosen by users, so HookShield refuses to connect to non-public addresses — loopback, private networks (10/8, 172.16/12, 192.168/16, fc00::/7), link-local and cloud metadata (169.254.169.254), CGNAT, multicast and reserved ranges. The check runs on the IP actually being connected to, after DNS resolution, so a public hostname pointing to a private IP is blocked as well. Blocked attempts are recorded as `blocked_destination`. IPv6 forms that reach IPv4 or local networks are blocked too (IPv4-mapped, IPv4-compatible `::/96` and IPv4-translated `::ffff:0:0:0/96`, NAT64 `64:ff9b::/96` and `64:ff9b:1::/48`, 6to4 `2002::/16`, Teredo `2001::/32`, discard `100::/64`, site-local `fec0::/10`), as are the documentation ranges, and so are **privileged ports** other than 80 and 443 (SSH, SMTP, … — a webhook has no business there); ports 1024–65535 are allowed.

Self-hosted installs that deliver to services on their own network can set `HS_ALLOW_PRIVATE_TARGETS=true` (the default in development). Do not enable it on a shared, multi-tenant install.
