-- 0051_suggestions.up.sql
-- Central de Sugestões — demandas internas do dev (spec 2026-07-09).
-- 5 plain tables (não particionadas), estilo da casa: uuid_generate_v4(),
-- TIMESTAMPTZ DEFAULT NOW(), FKs pra users(id), CHECK pra enums.
BEGIN;

CREATE TABLE suggestions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    ref_num             SERIAL UNIQUE,   -- número humano (#42), convenção da casa (short_id)
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    title               TEXT NOT NULL,
    description         TEXT NOT NULL,
    type                TEXT NOT NULL CHECK (type IN ('bug','melhoria','feature','duvida')),
    target_screen       TEXT,
    requester_priority  TEXT NOT NULL CHECK (requester_priority IN ('baixa','media','alta')),
    status              TEXT NOT NULL DEFAULT 'nova'
                        CHECK (status IN ('nova','em_analise','aceita','em_progresso','concluida','recusada')),
    dev_priority        TEXT CHECK (dev_priority IN ('urgente','alta','media','baixa')),
    effort              TEXT CHECK (effort IN ('P','M','G')),
    dev_feedback        TEXT,
    dev_notes           TEXT,
    awaiting_author     BOOLEAN NOT NULL DEFAULT false,
    resolved_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestions_created_by_idx ON suggestions (created_by);
CREATE INDEX suggestions_status_idx ON suggestions (status);
CREATE INDEX suggestions_updated_at_idx ON suggestions (updated_at DESC);

CREATE TABLE suggestion_comments (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    author_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    body           TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_comments_sid_idx ON suggestion_comments (suggestion_id, created_at);

CREATE TABLE suggestion_attachments (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    comment_id     UUID REFERENCES suggestion_comments(id) ON DELETE CASCADE,
    storage_key    TEXT NOT NULL,
    content_type   TEXT NOT NULL,
    size_bytes     BIGINT NOT NULL,
    uploaded_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_attachments_sid_idx ON suggestion_attachments (suggestion_id);

CREATE TABLE suggestion_events (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    actor_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN
                   ('created','status_changed','priority_changed','feedback_given','commented','attachment_added','reopened')),
    from_value     TEXT,
    to_value       TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_events_sid_idx ON suggestion_events (suggestion_id, created_at);

CREATE TABLE suggestion_reads (
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    last_read_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, suggestion_id)
);

COMMIT;
