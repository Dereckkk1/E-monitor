-- 0044_campaign_cancelled_at.down.sql
BEGIN;

ALTER TABLE campaigns DROP COLUMN cancelled_at;

COMMIT;
