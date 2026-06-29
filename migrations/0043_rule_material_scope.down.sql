-- 0043_rule_material_scope.down.sql
BEGIN;

ALTER TABLE distribution_rules DROP COLUMN material_ids;

COMMIT;
