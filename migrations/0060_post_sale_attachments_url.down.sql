-- 0060_post_sale_attachments_url.down.sql
ALTER TABLE post_sale_reports
    DROP COLUMN IF EXISTS attachments_url;
