-- 0008 — Billing interval of a subscription (HookShield Cloud: monthly or yearly).
-- Quotas stay monthly whatever the interval; the interval only changes how
-- often the plan is paid and when overage is charged.

ALTER TABLE billing_subscriptions
    ADD COLUMN billing_interval TEXT NOT NULL DEFAULT 'month' CHECK (billing_interval IN ('month', 'year'));
