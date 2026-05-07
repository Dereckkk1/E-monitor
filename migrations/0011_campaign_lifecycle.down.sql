-- 0011_campaign_lifecycle.down.sql
-- Reverte 0011: volta para o modelo em inglês ('planned','active','paused','ended').
--
-- Best-effort: não há como recuperar a distinção original entre 'paused'
-- e 'ended' que foi colapsada em 'cancelada' (semântica perdida).
-- Convenção do down: 'cancelada' → 'ended'.

BEGIN;

ALTER TABLE campaigns
    DROP CONSTRAINT IF EXISTS campaigns_status_check;

UPDATE campaigns SET status = 'planned' WHERE status = 'programada';
UPDATE campaigns SET status = 'active'  WHERE status = 'ativa';
UPDATE campaigns SET status = 'ended'   WHERE status = 'concluida';
UPDATE campaigns SET status = 'ended'   WHERE status = 'cancelada';

ALTER TABLE campaigns
    ALTER COLUMN status SET DEFAULT 'planned';

ALTER TABLE campaigns
    ADD CONSTRAINT campaigns_status_check
    CHECK (status IN ('planned','active','paused','ended'));

COMMIT;
