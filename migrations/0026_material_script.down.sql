-- 0026_material_script.down.sql
-- Reverse of 0026_material_script.up.sql.

BEGIN;

ALTER TABLE materials DROP COLUMN IF EXISTS script;

COMMIT;
