CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_id    UUID,
    actor_type  TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    resource_id UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON audit_log (created_at);
CREATE INDEX ON audit_log (actor_id) WHERE actor_id IS NOT NULL;
