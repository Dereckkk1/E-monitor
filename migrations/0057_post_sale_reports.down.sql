-- 0057_post_sale_reports.down.sql
-- Ordem inversa das FKs: filhos primeiro.
BEGIN;
DROP TABLE IF EXISTS post_sale_report_recipients;
DROP TABLE IF EXISTS post_sale_report_campaigns;
DROP TABLE IF EXISTS post_sale_reports;
COMMIT;
