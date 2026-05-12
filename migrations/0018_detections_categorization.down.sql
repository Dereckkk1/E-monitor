-- 0018_detections_categorization.down.sql

BEGIN;

DROP VIEW IF EXISTS daily_play_summary;

DROP INDEX IF EXISTS idx_detections_category;
ALTER TABLE detections DROP COLUMN IF EXISTS category;

COMMIT;
