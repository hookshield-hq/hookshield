-- Schema constraint tests. Run against a database where all migrations were applied:
--   psql "$HS_DATABASE_URL" -v ON_ERROR_STOP=1 -f backend/migrations/tests/schema_test.sql
-- Everything runs inside a transaction that is rolled back, so it is safe on a dev database.
-- Any failed expectation raises an exception and psql exits non-zero.

\o /dev/null
\set VERBOSITY terse
BEGIN;

-- expect_error(sql, sqlstate): asserts that executing sql fails with the given SQLSTATE.
CREATE FUNCTION pg_temp.expect_error(stmt TEXT, expected_state TEXT, label TEXT) RETURNS void AS $$
BEGIN
    BEGIN
        EXECUTE stmt;
    EXCEPTION WHEN OTHERS THEN
        IF SQLSTATE <> expected_state THEN
            RAISE EXCEPTION 'FAIL %: expected SQLSTATE %, got % (%)', label, expected_state, SQLSTATE, SQLERRM;
        END IF;
        RAISE NOTICE 'ok   %', label;
        RETURN;
    END;
    RAISE EXCEPTION 'FAIL %: statement succeeded but should have failed', label;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION pg_temp.expect_true(cond BOOLEAN, label TEXT) RETURNS void AS $$
BEGIN
    IF cond IS NOT TRUE THEN
        RAISE EXCEPTION 'FAIL %', label;
    END IF;
    RAISE NOTICE 'ok   %', label;
END;
$$ LANGUAGE plpgsql;

-- 23505 unique_violation · 23514 check_violation · 23503 foreign_key_violation · 23502 not_null_violation

-- ── users ────────────────────────────────────────────────
INSERT INTO users (id, email) VALUES ('00000000-0000-0000-0000-000000000001', 'dev@example.com');

SELECT pg_temp.expect_true(
    (SELECT locale = 'en' AND theme_preference = 'dark' AND plan_type = 'developer' FROM users WHERE email = 'dev@example.com'),
    'users defaults: locale en, dark theme, developer plan');
SELECT pg_temp.expect_error($$INSERT INTO users (email) VALUES ('dev@example.com')$$, '23505', 'users email is unique');
SELECT pg_temp.expect_error($$INSERT INTO users (email) VALUES ('Upper@Example.com')$$, '23514', 'users email must be lowercase');
SELECT pg_temp.expect_error($$INSERT INTO users (email) VALUES ('no-at-sign')$$, '23514', 'users email must contain @');
SELECT pg_temp.expect_error($$INSERT INTO users (email, locale) VALUES ('a@b.co', 'fr')$$, '23514', 'users locale limited to en/pt-BR/es');
INSERT INTO users (email, locale) VALUES ('pt@example.com', 'pt-BR'), ('es@example.com', 'es');

UPDATE users SET updated_at = '2000-01-01' WHERE email = 'pt@example.com';
UPDATE users SET name = 'Ana' WHERE email = 'pt@example.com';
SELECT pg_temp.expect_true(
    (SELECT updated_at > '2000-01-01' FROM users WHERE email = 'pt@example.com'),
    'users updated_at trigger refreshes timestamp');

-- ── auth tables ──────────────────────────────────────────
INSERT INTO oauth_accounts (user_id, provider, provider_user_id) VALUES ('00000000-0000-0000-0000-000000000001', 'google', 'g-123');
SELECT pg_temp.expect_error($$INSERT INTO oauth_accounts (user_id, provider, provider_user_id)
    VALUES ('00000000-0000-0000-0000-000000000001', 'google', 'g-123')$$, '23505', 'oauth (provider, provider_user_id) unique');
SELECT pg_temp.expect_error($$INSERT INTO oauth_accounts (user_id, provider, provider_user_id)
    VALUES ('00000000-0000-0000-0000-000000000001', 'github', 'x')$$, '23514', 'oauth provider limited to google');
SELECT pg_temp.expect_error($$INSERT INTO sessions (user_id, token_hash)
    VALUES ('00000000-0000-0000-0000-000000000001', '\x01')$$, '23502', 'sessions require expires_at');
SELECT pg_temp.expect_error($$INSERT INTO email_tokens (user_id, purpose, token_hash, expires_at)
    VALUES ('00000000-0000-0000-0000-000000000001', 'magic_link', '\x02', now())$$, '23514', 'email_tokens purpose restricted');

-- ── endpoints ────────────────────────────────────────────
INSERT INTO endpoints (id, user_id, name, slug, target_url, auth_token_hash)
VALUES ('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-000000000001',
        'Stripe', 'ep_stripe1', 'https://api.example.com/hooks', '\xaa');

SELECT pg_temp.expect_true(
    (SELECT max_retries = 5 AND alert_threshold_failures = 4 AND retry_strategy = 'exponential'
            AND status = 'active' AND dlq_enabled AND auth_mode = 'token'
     FROM endpoints WHERE slug = 'ep_stripe1'),
    'endpoints defaults match blueprint (5 retries, alert after 4)');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash)
    VALUES ('00000000-0000-0000-0000-000000000001', 'dup', 'ep_stripe1', 'https://x.io', '\x01')$$, '23505', 'endpoints slug unique');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash)
    VALUES ('00000000-0000-0000-0000-000000000001', 'bad', 'Bad Slug!', 'https://x.io', '\x01')$$, '23514', 'endpoints slug format');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash)
    VALUES ('00000000-0000-0000-0000-000000000001', 'bad', 'ep_ftp', 'ftp://x.io', '\x01')$$, '23514', 'endpoints target_url must be http(s)');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url)
    VALUES ('00000000-0000-0000-0000-000000000001', 'notoken', 'ep_notoken', 'https://x.io')$$, '23514', 'token mode requires token hash');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_mode)
    VALUES ('00000000-0000-0000-0000-000000000001', 'hmac', 'ep_hmac', 'https://x.io', 'hmac')$$, '23514', 'hmac mode requires secret');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_mode)
    VALUES ('00000000-0000-0000-0000-000000000001', 'ips', 'ep_ips', 'https://x.io', 'ip_allowlist')$$, '23514', 'ip_allowlist mode requires IPs');
INSERT INTO endpoints (user_id, name, slug, target_url, auth_mode, ip_allowlist)
VALUES ('00000000-0000-0000-0000-000000000001', 'ips', 'ep_ips', 'https://x.io', 'ip_allowlist', '{10.0.0.0/8}');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash, retry_strategy)
    VALUES ('00000000-0000-0000-0000-000000000001', 'c', 'ep_custom', 'https://x.io', '\x01', 'custom')$$, '23514', 'custom retry requires schedule');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash, max_retries)
    VALUES ('00000000-0000-0000-0000-000000000001', 'r', 'ep_retries', 'https://x.io', '\x01', 101)$$, '23514', 'max_retries upper bound');
SELECT pg_temp.expect_error($$INSERT INTO endpoints (user_id, name, slug, target_url, auth_token_hash, status)
    VALUES ('00000000-0000-0000-0000-000000000001', 's', 'ep_status', 'https://x.io', '\x01', 'deleted')$$, '23514', 'endpoint status restricted');

-- ── keys, secrets, alerts ────────────────────────────────
SELECT pg_temp.expect_error($$INSERT INTO api_keys (user_id, name, prefix, key_hash, scope)
    VALUES ('00000000-0000-0000-0000-000000000001', 'k', 'hs_live_1', '\x01', 'root')$$, '23514', 'api_keys scope restricted');
SELECT pg_temp.expect_error($$INSERT INTO user_secrets (user_id, name, value_encrypted, value_nonce)
    VALUES ('00000000-0000-0000-0000-000000000001', 'my-key', '\x01', '\x02')$$, '23514', 'secret names are UPPER_SNAKE');
INSERT INTO user_secrets (user_id, name, value_encrypted, value_nonce)
VALUES ('00000000-0000-0000-0000-000000000001', 'MY_API_KEY', '\x01', '\x02');
SELECT pg_temp.expect_error($$INSERT INTO alert_channels (user_id, type, target_encrypted, target_nonce)
    VALUES ('00000000-0000-0000-0000-000000000001', 'sms', '\x01', '\x02')$$, '23514', 'alert channel type restricted');

-- ── payload logs ─────────────────────────────────────────
INSERT INTO payload_logs (id, endpoint_id, http_method, original_encrypted, original_nonce, expires_at)
VALUES ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000e1', 'POST', '\x01', '\x02', now() + interval '1 day');
SELECT pg_temp.expect_true(
    (SELECT status = 'queued' AND attempt_count = 0 FROM payload_logs WHERE id = '00000000-0000-0000-0000-0000000000a1'),
    'payload_logs start queued with 0 attempts');
SELECT pg_temp.expect_error($$INSERT INTO payload_logs (endpoint_id, http_method, original_encrypted, original_nonce)
    VALUES ('00000000-0000-0000-0000-0000000000e1', 'POST', '\x01', '\x02')$$, '23502', 'payload_logs require expires_at (retention)');
SELECT pg_temp.expect_error($$INSERT INTO payload_logs (endpoint_id, http_method, original_encrypted, original_nonce, expires_at, transformed_encrypted)
    VALUES ('00000000-0000-0000-0000-0000000000e1', 'POST', '\x01', '\x02', now(), '\x03')$$, '23514', 'transformed ciphertext requires nonce');
SELECT pg_temp.expect_error($$UPDATE payload_logs SET status = 'lost' WHERE id = '00000000-0000-0000-0000-0000000000a1'$$, '23514', 'payload status restricted');

INSERT INTO delivery_attempts (payload_log_id, attempt_number, status_code) VALUES ('00000000-0000-0000-0000-0000000000a1', 1, 503);
SELECT pg_temp.expect_error($$INSERT INTO delivery_attempts (payload_log_id, attempt_number)
    VALUES ('00000000-0000-0000-0000-0000000000a1', 1)$$, '23505', 'attempt numbers unique per log');
SELECT pg_temp.expect_error($$INSERT INTO delivery_attempts (payload_log_id, attempt_number)
    VALUES ('00000000-0000-0000-0000-0000000000a1', 0)$$, '23514', 'attempt numbers start at 1');

-- ── usage ────────────────────────────────────────────────
INSERT INTO usage_counters (user_id, period_start) VALUES ('00000000-0000-0000-0000-000000000001', '2026-11-01');
SELECT pg_temp.expect_error($$INSERT INTO usage_counters (user_id, period_start)
    VALUES ('00000000-0000-0000-0000-000000000001', '2026-11-15')$$, '23514', 'usage period starts on day 1');

-- ── billing ──────────────────────────────────────────────
INSERT INTO billing_subscriptions (user_id, provider, subscription_id, plan, status, provider_updated_at)
    VALUES ('00000000-0000-0000-0000-000000000001', 'lemonsqueezy', 'sub_1', 'pro', 'active', now());
SELECT pg_temp.expect_error($$INSERT INTO billing_subscriptions (user_id, provider, subscription_id, plan, status, provider_updated_at)
    VALUES ('00000000-0000-0000-0000-000000000001', 'lemonsqueezy', 'sub_2', 'pro', 'active', now())$$, '23505', 'one subscription per user');
SELECT pg_temp.expect_error($$UPDATE billing_subscriptions SET plan = 'gold'$$, '23514', 'subscription plan restricted');
SELECT pg_temp.expect_error($$UPDATE billing_subscriptions SET status = 'on_trial'$$, '23514', 'subscription status restricted');
UPDATE usage_counters SET units_reported = 3 WHERE user_id = '00000000-0000-0000-0000-000000000001';
SELECT pg_temp.expect_error($$UPDATE usage_counters SET included_requests = -1$$, '23514', 'included requests are not negative');

-- ── system settings ──────────────────────────────────────
SELECT pg_temp.expect_error($$INSERT INTO system_settings (name, value) VALUES ('Bad Name', '\x00')$$, '23514', 'system setting names are lowercase identifiers');
INSERT INTO system_settings (name, value) VALUES ('test.setting', '\x01');
SELECT pg_temp.expect_error($$INSERT INTO system_settings (name, value) VALUES ('test.setting', '\x02')$$, '23505', 'system setting names are unique');

-- ── cascades ─────────────────────────────────────────────
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
SELECT pg_temp.expect_true(
    (SELECT count(*) = 0 FROM endpoints WHERE user_id = '00000000-0000-0000-0000-000000000001')
    AND (SELECT count(*) = 0 FROM payload_logs WHERE id = '00000000-0000-0000-0000-0000000000a1')
    AND (SELECT count(*) = 0 FROM delivery_attempts WHERE payload_log_id = '00000000-0000-0000-0000-0000000000a1')
    AND (SELECT count(*) = 0 FROM usage_counters WHERE user_id = '00000000-0000-0000-0000-000000000001')
    AND (SELECT count(*) = 0 FROM billing_subscriptions WHERE user_id = '00000000-0000-0000-0000-000000000001'),
    'deleting a user cascades to endpoints, logs, attempts, usage and subscriptions');

ROLLBACK;
