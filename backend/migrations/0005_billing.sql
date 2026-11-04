-- 0005 — Subscriptions of HookShield Cloud (provider-agnostic) and usage reporting.
-- Self-hosted installs never write here: their plans have no limits.

CREATE TABLE billing_subscriptions (
    user_id              UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    provider             TEXT NOT NULL CHECK (provider <> ''),
    customer_id          TEXT NOT NULL DEFAULT '',
    subscription_id      TEXT NOT NULL CHECK (subscription_id <> ''),
    item_id              TEXT NOT NULL DEFAULT '',            -- metered line usage is reported to
    plan                 TEXT NOT NULL CHECK (plan IN ('developer', 'pro', 'business', 'enterprise')),
    status               TEXT NOT NULL CHECK (status IN ('active', 'trialing', 'past_due', 'cancelled', 'paused', 'unpaid', 'expired')),
    renews_at            TIMESTAMPTZ,
    ends_at              TIMESTAMPTZ,
    test_mode            BOOLEAN NOT NULL DEFAULT false,
    provider_updated_at  TIMESTAMPTZ NOT NULL,                -- older notifications are ignored
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX billing_subscriptions_provider_id ON billing_subscriptions (provider, subscription_id);

CREATE TRIGGER billing_subscriptions_set_updated_at BEFORE UPDATE ON billing_subscriptions
    FOR EACH ROW EXECUTE FUNCTION hs_set_updated_at();

-- Usage reported to the billing provider, in the provider's units (e.g. blocks
-- of 10,000 requests), so each period is reported exactly once.
ALTER TABLE usage_counters RENAME COLUMN overage_reported TO units_reported;
