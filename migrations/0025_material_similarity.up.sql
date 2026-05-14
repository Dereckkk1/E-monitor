-- 0025_material_similarity.up.sql
-- Per-client similarity warning feature (see docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md).
-- Adds 4 columns to materials so we can persist the result of a one-shot
-- post-fingerprint scan against the client's other materials, plus a partial
-- index for fast polling of unfinished checks.

BEGIN;

ALTER TABLE materials
    ADD COLUMN similarity_check_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (similarity_check_status IN ('pending', 'ready', 'skipped', 'failed')),
    ADD COLUMN most_similar_material_id UUID REFERENCES materials(id) ON DELETE SET NULL,
    ADD COLUMN similarity_score REAL
        CHECK (similarity_score IS NULL OR (similarity_score >= 0 AND similarity_score <= 1)),
    ADD COLUMN similarity_acknowledged_at TIMESTAMPTZ;

-- Partial index makes the polling query fast even as the table grows.
-- Frontend polls every 3s while any material is pending — without this
-- index, that's a sequential scan per poll.
CREATE INDEX idx_materials_similarity_pending
    ON materials (id) WHERE similarity_check_status = 'pending';

-- Backfilled materials (those that existed before this migration) shouldn't
-- block forever. Mark them all as skipped — they pre-date the feature, no
-- user expectation of a warning. The unconditional UPDATE is safe because
-- new inserts (post-migration) get DEFAULT='pending' from the ALTER above.
UPDATE materials SET similarity_check_status = 'skipped';

COMMIT;
