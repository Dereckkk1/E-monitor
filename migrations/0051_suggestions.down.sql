-- 0051_suggestions.down.sql
BEGIN;
DROP TABLE IF EXISTS suggestion_reads;
DROP TABLE IF EXISTS suggestion_events;
DROP TABLE IF EXISTS suggestion_attachments;
DROP TABLE IF EXISTS suggestion_comments;
DROP TABLE IF EXISTS suggestions;
COMMIT;
