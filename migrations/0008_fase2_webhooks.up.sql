ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_url TEXT;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_secret TEXT;

CREATE TABLE webhook_failures (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    detection_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ NOT NULL,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON webhook_failures (next_retry_at) WHERE attempt <= 5;
