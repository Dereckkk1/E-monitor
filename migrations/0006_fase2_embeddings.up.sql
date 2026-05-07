CREATE TABLE commercial_embeddings (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    commercial_id   UUID NOT NULL REFERENCES commercials(id) ON DELETE CASCADE,
    variant_id      SMALLINT NOT NULL,
    rate_id         SMALLINT NOT NULL DEFAULT 0,
    window_offset_ms INT NOT NULL,
    embedding       FLOAT[] NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_embeddings_lookup
    ON commercial_embeddings (commercial_id, variant_id, rate_id, window_offset_ms);
