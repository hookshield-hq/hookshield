-- 0001 — Users and dashboard authentication (guide D6: email + password and Google).
-- gen_random_uuid() is built into PostgreSQL 13+, so no extension is required.

CREATE OR REPLACE FUNCTION hs_set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE users (
    id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                          TEXT NOT NULL CHECK (email = lower(email) AND position('@' IN email) > 1),
    email_verified_at              TIMESTAMPTZ,
    -- argon2id PHC string; NULL for accounts that only sign in with Google.
    password_hash                  TEXT,
    name                           TEXT,
    locale                         TEXT NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'pt-BR', 'es')),
    theme_preference               TEXT NOT NULL DEFAULT 'dark' CHECK (theme_preference IN ('dark', 'light', 'system')),
    plan_type                      TEXT NOT NULL DEFAULT 'developer' CHECK (plan_type IN ('developer', 'pro', 'business', 'enterprise')),
    lemon_squeezy_customer_id      TEXT,
    lemon_squeezy_subscription_id  TEXT,
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_key ON users (email);

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION hs_set_updated_at();

-- External identity providers linked to a user (Google today).
CREATE TABLE oauth_accounts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider          TEXT NOT NULL CHECK (provider IN ('google')),
    provider_user_id  TEXT NOT NULL,
    email             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX oauth_accounts_user_id_idx ON oauth_accounts (user_id);

-- Dashboard sessions. Only the SHA-256 of the cookie token is stored.
CREATE TABLE sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash    BYTEA NOT NULL UNIQUE,
    ip            INET,
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- One-time tokens sent by email (verification, password reset). Hash only.
CREATE TABLE email_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    token_hash  BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX email_tokens_user_id_idx ON email_tokens (user_id);
