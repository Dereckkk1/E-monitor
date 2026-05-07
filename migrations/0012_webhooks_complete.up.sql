-- 0012_webhooks_complete: completes the webhook subsystem (§13.1.4).
--
-- 1. Adds the missing client-side webhook configuration columns (enabled flag
--    + event filter list). webhook_url and webhook_secret were introduced by
--    0008; we keep them and only add the new columns.
-- 2. Replaces the legacy webhook_failures table with a proper outbox table
--    (webhook_deliveries) that records every attempt, supports the worker's
--    "FOR UPDATE SKIP LOCKED" polling loop, and tracks dead-letter state.
--
-- Idempotent: every step uses IF NOT EXISTS / IF EXISTS so the migration can
-- be replayed against partially-migrated databases.

-- 1) clients: enabled flag + event filter (URL/secret already added in 0008)
ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_events  TEXT[] NOT NULL DEFAULT ARRAY['detection.confirmed']::TEXT[];

-- 2) webhook_deliveries: full outbox + per-attempt audit trail
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id       UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','delivered','failed','dead')),
    attempt_count   INT  NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    response_code   INT,
    response_body   TEXT,
    last_error      TEXT,
    delivered_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Worker polling index: pick pending deliveries whose backoff has elapsed.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_pending
    ON webhook_deliveries (next_attempt_at)
    WHERE status = 'pending';

-- Per-client listing (operational dashboard).
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_client_created
    ON webhook_deliveries (client_id, created_at DESC);

-- Status-filtered listing (e.g. show only DLQ).
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status
    ON webhook_deliveries (status);
