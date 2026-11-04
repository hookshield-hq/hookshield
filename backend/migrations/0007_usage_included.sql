-- 0007 — Quota included in each billed month (HookShield Cloud).
-- The most generous monthly quota an account held during the period, so a
-- downgrade never turns requests that were inside the old plan's quota into
-- overage. Raised by the billing job while the month runs; never lowered.

ALTER TABLE usage_counters
    ADD COLUMN included_requests BIGINT NOT NULL DEFAULT 0 CHECK (included_requests >= 0);
