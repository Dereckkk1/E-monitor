-- Reverse §18.2.2 disambiguation schema additions.
DROP INDEX IF EXISTS detections_active_detected_at_idx;

ALTER TABLE detections
    DROP COLUMN IF EXISTS retracted_at;

ALTER TABLE campaigns
    DROP COLUMN IF EXISTS dedup_window_seconds;
