-- 0059_post_sale_overrides.down.sql
BEGIN;
ALTER TABLE post_sale_report_campaigns
    DROP COLUMN IF EXISTS checking_edited,
    DROP COLUMN IF EXISTS kpi_overrides;
COMMIT;
