# Database

PostgreSQL 15+ (developed and tested on 16/17). The schema lives in versioned, forward-only SQL files under [`backend/migrations/`](../backend/migrations), embedded into every Go binary.

## Migrations

| Rule | Why |
|---|---|
| File name `NNNN_short_description.sql`, versions contiguous from `0001` | Deterministic order; enforced by `internal/migrate` tests |
| Each migration runs in **one transaction** | A failure leaves the schema untouched |
| **Never edit a released migration** — add a new one | Applied files are checksummed (SHA-256); an edited file stops the boot with `applied migration was modified` |
| Forward-only (no down migrations) | Rollbacks are done with a new migration; keeps self-hosted upgrades simple |
| Concurrent runners are safe | A PostgreSQL advisory lock serialises replicas booting at the same time |
| Binary older than the database refuses to run | `database has a migration unknown to this binary` |

Applied migrations are recorded in `schema_migrations (version, name, checksum, applied_at)`.

### Running them

- **Automatically:** `api`, `ingest` and `worker` apply pending migrations on boot (`HS_AUTO_MIGRATE=true`, default; the advisory lock serialises them), so services can start in any order. This is what makes Docker/1-click installs work without extra steps.
- **Manually:** `api migrate up` and `api migrate status` (`make migrate`, `make migrate-status`). Set `HS_AUTO_MIGRATE=false` if you prefer a separate release step.

### Testing the schema

[`backend/migrations/tests/schema_test.sql`](../backend/migrations/tests/schema_test.sql) asserts every constraint, default, trigger and cascade. It runs in a rolled-back transaction, so it is safe on a dev database:

```bash
# after applying the migrations
psql "$HS_DATABASE_URL" -v ON_ERROR_STOP=1 -f backend/migrations/tests/schema_test.sql
```

Each check prints `ok   <description>`; the first failure aborts with `FAIL <description>` and a non-zero exit code. **Every new constraint gets a test here.**

The Go integration suite (`internal/db`) also runs this file on a freshly migrated database, and additionally verifies: idempotent re-runs, 6 concurrent runners applying each migration exactly once, detection of edited migrations, and rollback of a failing migration.

## Conventions

- Primary keys: `UUID DEFAULT gen_random_uuid()` (built into PostgreSQL 13+, no extension).
- Timestamps: `TIMESTAMPTZ`, `created_at`/`updated_at`; `updated_at` is maintained by the `hs_set_updated_at()` trigger.
- Enumerations: `TEXT` + `CHECK (... IN (...))` (easier to evolve than PostgreSQL enums).
- **Secrets are never stored in plain text.** Tokens and keys we only need to verify are stored as SHA-256 hashes (`*_hash`). Values we must read back are encrypted with AES-256-GCM in the application: `*_encrypted` (ciphertext + tag), `*_nonce` (12 random bytes, unique per encryption) and `key_version` (master key generation, for rotation).
- Foreign keys cascade on user/endpoint deletion, so deleting an account removes all of its data.

## Tables

```
users ─┬─< oauth_accounts
       ├─< sessions
       ├─< email_tokens
       ├─< api_keys
       ├─< user_secrets
       ├─< alert_channels >─┐ (optional, per endpoint)
       ├─< usage_counters   │
       ├── billing_subscriptions (0..1, HookShield Cloud)
       └─< endpoints ───────┴─< payload_logs ─< delivery_attempts
```

### Identity & auth — `0001_users_auth.sql`

| Table | Purpose | Notes |
|---|---|---|
| `users` | Accounts | Email stored lowercase and unique. `password_hash` (argon2id) is NULL for Google-only accounts. `locale` ∈ `en`, `pt-BR`, `es` (default `en`); `theme_preference` default `dark`; `plan_type` ∈ `developer`, `pro`, `business`, `enterprise`. Lemon Squeezy customer/subscription IDs. |
| `oauth_accounts` | Linked Google identities | Unique `(provider, provider_user_id)`. |
| `sessions` | Dashboard sessions | Only the SHA-256 of the cookie token is stored; `expires_at` required. |
| `email_tokens` | Email verification / password reset | Hash only, single use (`used_at`). |

### Configuration — `0002_endpoints_keys_secrets.sql`

| Table | Purpose | Notes |
|---|---|---|
| `endpoints` | Ingestion endpoints | `slug` is the public ID in `/v1/{slug}`. `status` ∈ `active`, `paused`, `debug`. Inbound auth `auth_mode` ∈ `token` (hash required), `hmac` (encrypted secret + header required), `ip_allowlist` (CIDRs required) — no unauthenticated mode. Retry policy: `max_retries` (default 5, 0–100), `retry_strategy` ∈ `fixed`, `exponential`, `custom` (custom requires `retry_custom_schedule_ms`), initial interval, multiplier, max delay, `dlq_enabled`. Alerts: `alert_threshold_failures` (default 4). `outbound_custom_headers` and `transform_rules` are JSONB. |
| `api_keys` | Control-plane API keys | Visible `prefix`, hashed key, `scope` ∈ `read_only`, `ingest`, `admin`, revocation/expiry. |
| `user_secrets` | Values for `{{secrets.NAME}}` templates | Names are `UPPER_SNAKE_CASE`, unique per user; values encrypted. |
| `alert_channels` | Alert destinations | `type` ∈ `webhook`, `email`, `slack`, `telegram`; destination encrypted; `endpoint_id` NULL = all endpoints. |

### Traffic — `0003_payload_logs.sql`

| Table | Purpose | Notes |
|---|---|---|
| `payload_logs` | One row per received webhook | Original and transformed `{headers, body}` envelopes, each encrypted with its own nonce. `status` ∈ `queued`, `processing`, `delivered`, `retrying`, `failed`, `dead` (sent to DLQ). Per-stage latency (`ingest_us`, `transform_us`, `persist_us`). `expires_at` is set from the plan's retention (24 h / 30 d / 90 d) and drives the cleanup job. `replay_of` links replays to the original. Written by the processor with `INSERT … ON CONFLICT (id) DO NOTHING` (the ID is the ingest request ID, so redeliveries never duplicate rows). `transformed_*` is NULL when the endpoint has no transform rules (the dispatcher then sends the original). `persist_us` is filled by the dispatcher. |
| `delivery_attempts` | One row per outbound attempt | Status code, duration, error class (never secrets), encrypted destination response (≤ 64 KiB body). Unique `(payload_log_id, attempt_number)` — the dispatcher inserts the attempt and updates the log in one transaction, and the unique key makes duplicate dispatch messages harmless. |

**Status lifecycle** (`payload_logs.status`): `queued` → `delivered`, or `queued` → `retrying` → … → `delivered` / `dead` (last attempt failed, DLQ on) / `failed` (DLQ off). `processing` is reserved. See [DELIVERY.md](DELIVERY.md#lifecycle).

Indexes cover the dashboard filters (endpoint + status + time, endpoint + event type), the retention job (`expires_at`; the worker deletes expired logs every minute — attempts cascade, `replay_of` links become `NULL`) and retry recovery (`next_attempt_at WHERE status = 'retrying'`).

> Scale note: `payload_logs` is the hot table. Time-based partitioning (monthly) is a planned optimisation once volume requires it; the schema avoids features that would block it.

### Billing — `0004_usage.sql`, `0005_billing.sql`, `0007_usage_included.sql`, `0008_billing_interval.sql`

| Table | Purpose | Notes |
|---|---|---|
| `usage_counters` | Monthly usage per user | `period_start` is always day 1 of the month. `requests` = accepted requests (webhooks, replays and test events; retries are not counted), copied every minute from the live Redis counter by the worker and never lowered. `units_reported` (renamed from `overage_reported` in 0005) is how much of the month was already reported to the billing provider, in the provider's unit; it is claimed by compare-and-set before billing and given back by subtraction when the provider refuses, so a unit is billed once. `included_requests` (0007) is the most generous monthly quota the account held during the month (raised by the billing job, never lowered), so a downgrade never turns included requests into overage. `delivery_attempts` is reserved. |
| `billing_subscriptions` | Subscription of an account (HookShield Cloud only) | Provider-agnostic: `provider`, `customer_id`, `subscription_id` (unique per provider), `item_id` (what usage is billed against: Paddle's overage price), `plan`, `billing_interval` (0008: `month` or `year`, default `month`; quotas stay monthly), `status` (`active`, `trialing`, `past_due`, `cancelled`, `paused`, `unpaid`, `expired`), `renews_at`, `ends_at`, `test_mode` (sandbox). `provider_updated_at` makes out-of-order notifications harmless (older ones are ignored). Saving a subscription sets `users.plan_type` to the plan it grants in the same transaction. The legacy `users.lemon_squeezy_*` columns are not used. |

### Install — `0006_system_settings.sql`

| Table | Purpose | Notes |
|---|---|---|
| `system_settings` | Install-wide values, by `name` | `encryption_key_check`: a constant encrypted with the encryption key (nonce ‖ ciphertext, AAD `system:encryption_key_check`), written by the first service to start. Every service opens it on boot and refuses to run with another key — see [SELF_HOSTING.md](SELF_HOSTING.md#the-encryption-key). |
