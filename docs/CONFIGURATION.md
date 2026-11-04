# Configuration reference

All HookShield binaries read environment variables. A template lives in [`.env.example`](../.env.example).
Each binary validates its settings on boot and reports **every** problem at once.

| Variable | Used by | Default | Required | Description |
|---|---|---|---|---|
| `HS_ENV` | all | `development` (Docker images: `production`) | no | `production`: JSON logs, deliveries to private addresses refused (`HS_ALLOW_PRIVATE_TARGETS=false`), `Secure` cookies. `development`: text logs and the opposite defaults — the services warn when it runs on a public URL. |
| `HS_LOG_LEVEL` | all | `info` | no | `debug`, `info`, `warn` or `error`. |
| `HS_INGEST_ADDR` | ingest | `:8080` | no | Listen address of the ingest service. |
| `HS_API_ADDR` | api | `:8081` | no | Listen address of the control-plane API. |
| `HS_WORKER_HEALTH_ADDR` | worker | `:8082` | no | Listen address of the worker health endpoints. |
| `HS_PUBLIC_URL` | all | — | no | Single public origin of an install that serves everything behind one proxy (production compose, `hookshield-server`): dashboard at `/`, API at `/v1`, ingest at `/in/v1/{slug}`. When set it is the default of `HS_APP_URL` and `HS_API_URL`, and `$HS_PUBLIC_URL/in` the default of `HS_PUBLIC_INGEST_URL`. See [SELF_HOSTING.md](SELF_HOSTING.md). |
| `HS_PUBLIC_INGEST_URL` | api, web | `http://localhost:8080` (`$HS_PUBLIC_URL/in` when set) | no | Public base URL shown to users (e.g. `https://in.hookshield.dev`). Trailing slash is removed. |
| `HS_APP_URL` | api, worker | `http://localhost:3000` (`HS_PUBLIC_URL` when set) | no | Public dashboard URL (email links, redirect after Google sign-in, default CORS origin). |
| `HS_API_URL` | api, web | `http://localhost:8081` (`HS_PUBLIC_URL` when set) | no | Public URL of the API. The Google redirect URI is `$HS_API_URL/v1/auth/google/callback`. The dashboard reads it at runtime (`GET /api/config`) and the browser calls the API at this address, so it must be reachable from users' browsers. |
| `HS_CORS_ORIGINS` | api | `HS_APP_URL` | no | Comma-separated browser origins allowed to call the API with cookies (CORS + CSRF check). No wildcards. |
| `HS_SESSION_TTL` | api | `720h` | no | Sliding lifetime of a dashboard session (1h–8760h). |
| `HS_SESSION_MAX_AGE` | api, worker | `2160h` (90 days) | no | Absolute session lifetime: a session ends this long after sign-in, however active (1h–8760h). The worker deletes ended sessions hourly, so it reads this too. |
| `HS_COOKIE_SECURE` | api | `true` in production, `false` in development | no | `Secure` flag on cookies. Needs HTTPS. |
| `HS_DATABASE_URL` | api, ingest, worker | — | **yes** | PostgreSQL connection string. Pool size can be tuned in the URL (`?pool_max_conns=20`). |
| `HS_AUTO_MIGRATE` | api | `true` | no | Apply pending migrations on boot. Set `false` to run `api migrate up` manually (e.g. in a release step). |
| `HS_TEST_DATABASE_URL` | tests | — | integration tests | Admin URL used by integration tests to create and drop throw-away databases (needs `CREATEDB`). Tests are skipped when unset. |
| `HS_TEST_REDIS_URL` | tests | — | integration tests | Redis used by integration tests (each test gets a random key namespace that is deleted afterwards). Suggested: `redis://localhost:6379/15`. |
| `HS_SEED_TARGET` | api (`seed`) | `http://localhost:4000/webhook` | no | Destination of the demo endpoint created by `api seed` (the Docker stack sets `http://sink:4000/webhook`). Re-seeding updates it. |
| `HS_SINK_ADDR` | `hookshield sink` | `:4000` | no | Listen address of the demo destination. |
| `HS_SINK_FAIL_FIRST` | `make dev`, Docker stack | `0` | no | Makes the demo destination answer `503` to the first N requests, to watch retries. |
| `HS_WEB_PORT` / `HS_INGEST_PORT` / `HS_API_PORT` / `HS_SINK_PORT` / `HS_MAILPIT_PORT` | Docker stack (`compose.yaml`) | `3000` / `8080` / `8081` / `4000` / `8025` | no | Host ports published by `make up`. Changing `HS_WEB_PORT` also changes the dashboard origin the API allows. |
| `HS_PERF_TEST` | tests | — | perf test | Set to `1` to run the ingest latency budget test (`make test-perf`). |
| `HS_REDIS_URL` | ingest, worker, api | — | **yes** (`api seed` works without it) | Redis connection string (`redis://` or `rediss://`). The api keeps login throttling and OAuth state there and invalidates the endpoint cache. |
| `HS_REDIS_PREFIX` | ingest, worker, api | `hs:` | no | Namespace for every Redis key (useful when Redis is shared). Key list: `internal/redisx`. |
| `HS_MAX_BODY_BYTES` | ingest | `1048576` | no | Largest accepted webhook body; bigger requests get `413 payload_too_large`. |
| `HS_INGEST_RATE_LIMIT` | ingest | `100` | no | Requests per second per endpoint; above it `429 rate_limited`. `0` disables it. |
| `HS_SIGNUP_RATE_LIMIT` | api | `10` in production, `1000` in development | no | Sign-ups per IP (IPv6: per /64) per 15 minutes; above it `429`. Development is generous because the end-to-end tests create many accounts. |
| `HS_PROCESSOR_CONCURRENCY` | worker | `4` | no | Processor goroutines per worker (1–256). Each is a member of the Redis consumer group; run more workers to scale horizontally. |
| `HS_DISPATCHER_CONCURRENCY` | worker | `8` | no | Dispatcher loops per worker (1–1024); each has up to 4 deliveries in flight, and one endpoint never has more than 4 in flight per worker. Deliveries wait on the network, so this can be much higher than the CPU count. |
| `HS_DISPATCH_TIMEOUT` | worker | `15s` | no | Timeout of one delivery attempt (1s–5m), Go duration syntax. |
| `HS_ALLOW_PRIVATE_TARGETS` | worker, api | `true` in development, `false` in production | no | Allow deliveries to private/loopback/link-local addresses. Keep `false` on shared installs (SSRF protection, see [DELIVERY.md](DELIVERY.md#security-destination-restrictions-ssrf)). |
| `HS_TRUSTED_PROXIES` | ingest, api | — (`hookshield-server`: loopback; on Render and Railway also private networks) | no | Comma-separated IPs/CIDRs of reverse proxies whose `X-Forwarded-For` is trusted (needed for IP allow-lists behind Caddy). Empty = use the TCP peer address. |
| `HS_ENCRYPTION_KEY` | ingest, worker, api | — | **yes**, unless `HS_ENCRYPTION_KEY_FILE` is set — all services must use the same key | AES-256 master key: 32 random bytes as base64 (`openssl rand -base64 32`) or 64 hex characters (`openssl rand -hex 32`). **Losing it makes stored payloads unreadable.** The database keeps a value encrypted with the key: a service started with another key refuses to boot (`encryption key does not match this database`). |
| `HS_ENCRYPTION_KEY_FILE` | ingest, worker, api | — (Docker images: `/var/lib/hookshield/encryption.key`, all-in-one `/data/encryption.key`) | no | File holding the key when `HS_ENCRYPTION_KEY` is empty. On the first start of a fresh install (no account yet) the key is **generated** there (mode 0600) and a warning asks you to back it up; services starting together share one key. If the database already has data and the file is missing, the services refuse to start instead of creating a new key. |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | api | — | no | Google sign-in. Set both (or neither: Google is disabled and `/v1/auth/providers` reports it). Create a "Web application" OAuth client and register the redirect URI above. |
| `HS_GOOGLE_AUTH_URL` / `HS_GOOGLE_TOKEN_URL` / `HS_GOOGLE_USERINFO_URL` | api | Google's endpoints | no | Overrides for tests only. |
| `HS_RESEND_API_KEY` | worker, api | — | **in production** | [Resend](https://resend.com) API key (`re_…`). When set, every email (verification, password reset, alerts) goes through the Resend HTTP API and `HS_SMTP_URL` is ignored. Verify the domain of `HS_EMAIL_FROM` in Resend first. |
| `HS_RESEND_API_URL` | worker, api | `https://api.resend.com/emails` | no | Override for tests. |
| `HS_SMTP_URL` | worker, api | — (`.env.example`: `smtp://localhost:1025`, Mailpit) | development | Used when no Resend key is set: `smtp://user:pass@host:587` (STARTTLS when offered) or `smtps://user:pass@host:465`. With neither, account emails are only logged and email alert channels are skipped. |
| `HS_EMAIL_FROM` | worker, api | `HookShield <no-reply@hookshield.dev>` | no | Sender of every email. Quote it in `.env`: `HS_EMAIL_FROM="HookShield <no-reply@example.com>"`. |
| `HS_RETENTION` | worker | `720h` (30 days) | no | How long payload logs are kept on self-hosted installs (≥ 1h). Plan-based editions (HookShield Cloud) set retention per plan instead. |
| `HS_ALERT_COOLDOWN` | worker | `10m` | no | Minimum time between two alerts of the same endpoint; alerts in between are grouped. `0` disables grouping. See [ALERTS.md](ALERTS.md). |
| `HS_TELEGRAM_API_URL` | worker | `https://api.telegram.org` | no | Telegram Bot API base URL (override for tests or a local Bot API server). |

The `hookshield` CLI has its own variables (`HOOKSHIELD_API_URL`, `HOOKSHIELD_API_KEY`, `HOOKSHIELD_CONFIG`): see [CLI.md](CLI.md).

Billing (Paddle) is part of HookShield Cloud and configured in that repository; self-hosted installs have no billing settings.

### All-in-one server (`hookshield-server`)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8000` | Public port (set by Render and Railway). |
| `HS_PUBLIC_URL` | `RENDER_EXTERNAL_URL`, else `https://$RAILWAY_PUBLIC_DOMAIN`, else `http://localhost:$PORT` | Public origin; set it when you add a custom domain. |
| `HS_WEB_DIR` | `/app/web` | Next.js standalone build of the dashboard (started with `node server.js`). |
| `HS_WEB_UPSTREAM` | — | URL of a dashboard running elsewhere instead (development). |
| `HS_NODE` | `node` | Node.js binary. |

`HS_INGEST_ADDR`, `HS_API_ADDR` and `HS_WORKER_HEALTH_ADDR` are ignored: the parts listen on loopback ports 18080–18083.

\* Exact requirements per binary are enforced in code via `config.Requirement` and updated here as each phase lands.
