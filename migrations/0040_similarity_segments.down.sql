-- 0040_similarity_segments.down.sql
BEGIN;

ALTER TABLE materials DROP COLUMN IF EXISTS similarity_segments;

COMMIT;
