-- Down de 0063: restaura o CHECK de 4 valores (sem 'bonus').
--
-- Só é seguro rodar depois que 0064_category_bonus_rename.down.sql já tiver
-- revertido qualquer linha 'bonus' de volta pra 'orphan' — senão essas linhas
-- ficam violando o CHECK restaurado (o CHECK entra NOT VALID, então não barra
-- o schema em si, mas fica com dado inconsistente até validar/corrigir).

BEGIN;

ALTER TABLE detections DROP CONSTRAINT IF EXISTS detections_category_check;
ALTER TABLE detections ADD CONSTRAINT detections_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan')) NOT VALID;

ALTER TABLE detection_campaigns DROP CONSTRAINT IF EXISTS detection_campaigns_category_check;
ALTER TABLE detection_campaigns ADD CONSTRAINT detection_campaigns_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan')) NOT VALID;

COMMIT;
