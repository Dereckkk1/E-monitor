-- 0045_distribution_rule_name.down.sql
BEGIN;

ALTER TABLE distribution_rules DROP COLUMN name;

COMMIT;
