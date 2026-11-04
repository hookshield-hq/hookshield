-- 0006 — Install-wide settings. Today: a check value encrypted with the
-- encryption key, so a service started with a different key (or a lost key
-- file that got regenerated) refuses to boot instead of mixing ciphertexts.

CREATE TABLE system_settings (
    name        TEXT PRIMARY KEY CHECK (name ~ '^[a-z0-9_.]+$'),
    value       BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
