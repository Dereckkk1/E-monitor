-- 0024_unify_short_id_drop_detections_fk.down.sql
-- Reverse of 0024. Restores the FK and reverts both tables to their original
-- per-table SERIAL sequences. Existing short_id values are preserved.

BEGIN;

-- ────── 1. Restore per-table sequence defaults ──────

ALTER TABLE commercials
    ALTER COLUMN short_id SET DEFAULT nextval('commercials_short_id_seq');

ALTER TABLE materials
    ALTER COLUMN short_id SET DEFAULT nextval('materials_short_id_seq');

-- Sync the per-table sequences forward so subsequent inserts don't reuse
-- a value that may have been issued from the shared sequence while it was
-- active. The 3-arg setval(seq, value, is_called) form preserves the
-- correct semantics on empty tables:
--   - Empty table: setval(seq, 1, false) → next nextval() returns 1
--   - Populated:   setval(seq, MAX, true) → next nextval() returns MAX+1
SELECT setval(
    pg_get_serial_sequence('commercials', 'short_id'),
    COALESCE((SELECT MAX(short_id) FROM commercials), 1),
    (SELECT MAX(short_id) FROM commercials) IS NOT NULL
);

SELECT setval(
    pg_get_serial_sequence('materials', 'short_id'),
    COALESCE((SELECT MAX(short_id) FROM materials), 1),
    (SELECT MAX(short_id) FROM materials) IS NOT NULL
);

DROP SEQUENCE catalog_short_id_seq;

-- ────── 2. Re-add detections.commercial_id FK ──────

-- This will FAIL if there are detections with commercial_id pointing at
-- a materials.id that doesn't also exist in commercials. Operators should
-- delete or migrate those rows before running the down.
--
-- Diagnostic query to find offending rows:
--   SELECT d.id, d.commercial_id, d.detected_at FROM detections d
--   LEFT JOIN commercials c ON c.id = d.commercial_id
--   WHERE c.id IS NULL;
ALTER TABLE detections
    ADD CONSTRAINT detections_commercial_id_fkey
    FOREIGN KEY (commercial_id) REFERENCES commercials(id);

COMMIT;
