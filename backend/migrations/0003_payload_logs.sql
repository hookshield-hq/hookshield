-- 0003 — Webhook logs and delivery attempts.
-- Headers and bodies are never stored in plain text: each envelope ({headers, body})
-- is encrypted with AES-256-GCM using its own random 12-byte nonce.

CREATE TABLE payload_logs (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id             UUID NOT NULL REFERENCES endpoints (id) ON DELETE CASCADE,
    replay_of               UUID REFERENCES payload_logs (id) ON DELETE SET NULL,

    -- Non-sensitive metadata (searchable)
    http_method             VARCHAR(10) NOT NULL,
    event_type              VARCHAR(200),
    content_type            VARCHAR(200),
    size_bytes              INT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    source_ip               INET,

    -- Encrypted envelopes
    key_version             SMALLINT NOT NULL DEFAULT 1,
    original_encrypted      BYTEA NOT NULL,
    original_nonce          BYTEA NOT NULL,
    transformed_encrypted   BYTEA,
    transformed_nonce       BYTEA,

    -- Delivery state
    status                  TEXT NOT NULL DEFAULT 'queued'
                            CHECK (status IN ('queued', 'processing', 'delivered', 'retrying', 'failed', 'dead')),
    attempt_count           INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_status_code        INT,
    next_attempt_at         TIMESTAMPTZ,
    alerted_at              TIMESTAMPTZ,

    -- Per-stage latency for the Visual Flow Tracker (microseconds)
    ingest_us               INT,
    transform_us            INT,
    persist_us              INT,

    received_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at            TIMESTAMPTZ,
    delivered_at            TIMESTAMPTZ,
    -- Retention deadline computed from the owner's plan at insert time (24h / 30d / 90d).
    expires_at              TIMESTAMPTZ NOT NULL,

    CONSTRAINT payload_logs_transformed_pair
        CHECK ((transformed_encrypted IS NULL) = (transformed_nonce IS NULL))
);

-- Dashboard listing / filtering (adapted from the blueprint's idx_payloads_endpoint_status).
CREATE INDEX payload_logs_endpoint_status_idx ON payload_logs (endpoint_id, status, received_at DESC);
CREATE INDEX payload_logs_endpoint_received_idx ON payload_logs (endpoint_id, received_at DESC);
CREATE INDEX payload_logs_event_type_idx ON payload_logs (endpoint_id, event_type) WHERE event_type IS NOT NULL;
-- Retention job.
CREATE INDEX payload_logs_expires_at_idx ON payload_logs (expires_at);
-- Pending retries (recovery if Redis loses scheduled retries).
CREATE INDEX payload_logs_next_attempt_idx ON payload_logs (next_attempt_at) WHERE status = 'retrying';

-- One row per outbound delivery attempt (timeline + "Destination Response" tab).
CREATE TABLE delivery_attempts (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payload_log_id       UUID NOT NULL REFERENCES payload_logs (id) ON DELETE CASCADE,
    attempt_number       INT NOT NULL CHECK (attempt_number >= 1),
    started_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    duration_us          INT,
    status_code          INT,
    -- Error class only (e.g. "timeout", "connection_refused", "tls"), never secrets.
    error_code           VARCHAR(100),
    key_version          SMALLINT NOT NULL DEFAULT 1,
    response_encrypted   BYTEA,
    response_nonce       BYTEA,
    UNIQUE (payload_log_id, attempt_number),
    CONSTRAINT delivery_attempts_response_pair
        CHECK ((response_encrypted IS NULL) = (response_nonce IS NULL))
);
