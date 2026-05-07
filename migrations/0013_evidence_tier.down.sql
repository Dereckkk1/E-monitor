DROP INDEX IF EXISTS detections_tier_created_at_idx;
ALTER TABLE detections DROP COLUMN IF EXISTS tier;
