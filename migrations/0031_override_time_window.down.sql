-- 0031_override_time_window.down.sql
BEGIN;

ALTER TABLE distribution_overrides
    DROP CONSTRAINT IF EXISTS override_times_valid,
    DROP COLUMN IF EXISTS time_end,
    DROP COLUMN IF EXISTS time_start;

COMMIT;
