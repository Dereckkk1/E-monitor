-- 0025_material_similarity.down.sql

BEGIN;

DROP INDEX IF EXISTS idx_materials_similarity_pending;

ALTER TABLE materials
    DROP COLUMN IF EXISTS similarity_acknowledged_at,
    DROP COLUMN IF EXISTS similarity_score,
    DROP COLUMN IF EXISTS most_similar_material_id,
    DROP COLUMN IF EXISTS similarity_check_status;

COMMIT;
