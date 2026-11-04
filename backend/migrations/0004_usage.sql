-- 0004 — Monthly usage metering per user (plan quotas and Lemon Squeezy overage billing).

CREATE TABLE usage_counters (
    user_id            UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    period_start       DATE NOT NULL CHECK (extract(day FROM period_start) = 1),
    requests           BIGINT NOT NULL DEFAULT 0 CHECK (requests >= 0),
    delivery_attempts  BIGINT NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    overage_reported   BIGINT NOT NULL DEFAULT 0 CHECK (overage_reported >= 0),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, period_start)
);
