-- 0002 — Endpoints (ingestion + retry policy + alerts), API keys, user secrets, alert channels.
-- Encrypted columns use AES-256-GCM in the application: *_encrypted holds ciphertext+tag,
-- *_nonce the 12-byte nonce and key_version the master key generation (for rotation).

CREATE TABLE endpoints (
    id                         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name                       VARCHAR(100) NOT NULL,
    description                TEXT,
    -- Public identifier in the ingest URL: https://in.hookshield.dev/v1/{slug}
    slug                       VARCHAR(100) NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9_-]{2,99}$'),
    target_url                 TEXT NOT NULL CHECK (target_url ~* '^https?://'),
    status                     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'debug')),

    -- Inbound authentication (zero-trust: there is no "none" mode).
    auth_mode                  TEXT NOT NULL DEFAULT 'token' CHECK (auth_mode IN ('token', 'hmac', 'ip_allowlist')),
    auth_header_key            VARCHAR(100) NOT NULL DEFAULT 'X-HookShield-Token',
    auth_token_hash            BYTEA,          -- SHA-256 of the inbound token (shown to the user once)
    auth_token_prefix          VARCHAR(16),    -- visible prefix for the UI, e.g. "hs_in_8f3a"
    hmac_header                VARCHAR(100),
    hmac_secret_encrypted      BYTEA,
    hmac_secret_nonce          BYTEA,
    hmac_key_version           SMALLINT NOT NULL DEFAULT 1,
    ip_allowlist               CIDR[] NOT NULL DEFAULT '{}',

    -- Pipeline
    outbound_custom_headers    JSONB NOT NULL DEFAULT '{}'::jsonb,  -- values may reference {{secrets.NAME}}
    transform_rules            JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Retry policy (guide §3.5)
    max_retries                INT NOT NULL DEFAULT 5 CHECK (max_retries BETWEEN 0 AND 100),
    retry_strategy             TEXT NOT NULL DEFAULT 'exponential' CHECK (retry_strategy IN ('fixed', 'exponential', 'custom')),
    retry_initial_interval_ms  INT NOT NULL DEFAULT 10000 CHECK (retry_initial_interval_ms > 0),
    retry_multiplier           NUMERIC(4, 2) NOT NULL DEFAULT 2.00 CHECK (retry_multiplier >= 1),
    retry_max_delay_ms         INT NOT NULL DEFAULT 3600000 CHECK (retry_max_delay_ms > 0),
    retry_custom_schedule_ms   INT[],          -- explicit delays when retry_strategy = 'custom'
    dlq_enabled                BOOLEAN NOT NULL DEFAULT TRUE,

    -- Alerts
    alert_threshold_failures   INT NOT NULL DEFAULT 4 CHECK (alert_threshold_failures >= 1),
    alert_webhook_url          TEXT,

    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT endpoints_token_mode_requires_hash
        CHECK (auth_mode <> 'token' OR auth_token_hash IS NOT NULL),
    CONSTRAINT endpoints_hmac_mode_requires_secret
        CHECK (auth_mode <> 'hmac' OR (hmac_secret_encrypted IS NOT NULL AND hmac_secret_nonce IS NOT NULL AND hmac_header IS NOT NULL)),
    CONSTRAINT endpoints_allowlist_mode_requires_ips
        CHECK (auth_mode <> 'ip_allowlist' OR cardinality(ip_allowlist) > 0),
    CONSTRAINT endpoints_custom_retry_requires_schedule
        CHECK (retry_strategy <> 'custom' OR coalesce(cardinality(retry_custom_schedule_ms), 0) > 0)
);

CREATE INDEX endpoints_user_id_idx ON endpoints (user_id);

CREATE TRIGGER endpoints_set_updated_at BEFORE UPDATE ON endpoints
    FOR EACH ROW EXECUTE FUNCTION hs_set_updated_at();

-- API keys for the control-plane API (guide §3.7). Only the hash is stored.
CREATE TABLE api_keys (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          VARCHAR(100) NOT NULL,
    prefix        VARCHAR(16) NOT NULL,        -- e.g. "hs_live_8f3a"
    key_hash      BYTEA NOT NULL UNIQUE,
    scope         TEXT NOT NULL CHECK (scope IN ('read_only', 'ingest', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

-- Named secrets referenced as {{secrets.NAME}} in outbound headers.
CREATE TABLE user_secrets (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name             VARCHAR(64) NOT NULL CHECK (name ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    value_encrypted  BYTEA NOT NULL,
    value_nonce      BYTEA NOT NULL,
    key_version      SMALLINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE TRIGGER user_secrets_set_updated_at BEFORE UPDATE ON user_secrets
    FOR EACH ROW EXECUTE FUNCTION hs_set_updated_at();

-- Where failure alerts go. endpoint_id NULL = applies to every endpoint of the user.
-- The destination (webhook URL, Slack URL, Telegram chat, email) may be sensitive, so it is encrypted.
CREATE TABLE alert_channels (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    endpoint_id          UUID REFERENCES endpoints (id) ON DELETE CASCADE,
    type                 TEXT NOT NULL CHECK (type IN ('webhook', 'email', 'slack', 'telegram')),
    target_encrypted     BYTEA NOT NULL,
    target_nonce         BYTEA NOT NULL,
    key_version          SMALLINT NOT NULL DEFAULT 1,
    is_enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX alert_channels_user_id_idx ON alert_channels (user_id);
CREATE INDEX alert_channels_endpoint_id_idx ON alert_channels (endpoint_id);
