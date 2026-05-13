-- 0021_detections_manual.down.sql
-- Remove o suporte a veiculações inseridas manualmente. Linhas com
-- manual_at != NULL ficam órfãs dessa coluna; reverter pode perder o
-- histórico de auditoria, então só rode em dev/staging.

BEGIN;

DROP INDEX IF EXISTS detections_manual_at_idx;

ALTER TABLE detections
    DROP CONSTRAINT IF EXISTS detections_manual_pair_chk;

ALTER TABLE detections
    DROP COLUMN IF EXISTS manual_note,
    DROP COLUMN IF EXISTS manual_by,
    DROP COLUMN IF EXISTS manual_at;

COMMIT;
