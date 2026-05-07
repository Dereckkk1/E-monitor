-- 0011_campaign_lifecycle.up.sql
-- §18.2.1 — Ciclo de vida automático de campanha.
--
-- Migra os valores de campaigns.status do modelo manual em inglês
-- ('planned','active','paused','ended') para o modelo automático em
-- português ('programada','ativa','concluida','cancelada').
--
-- Mapeamento de dados existentes (§18.2.1, item 1):
--   planned → programada
--   active  → ativa  (ou concluida se end_date < hoje em America/Sao_Paulo)
--   paused  → cancelada (semântica mais próxima do uso atual de pause como
--                        "parar antes do fim")
--   ended   → concluida
--
-- Datas comparadas em TZ America/Sao_Paulo (R-C). DDL transacional.

BEGIN;

-- 1. Remover constraint antiga (CHECK inline criado via CREATE TABLE).
--    O constraint name segue a convenção pg padrão: campaigns_status_check.
ALTER TABLE campaigns
    DROP CONSTRAINT IF EXISTS campaigns_status_check;

-- 2. Migrar dados existentes para os novos valores.
UPDATE campaigns SET status = 'programada' WHERE status = 'planned';
UPDATE campaigns SET status = 'concluida'  WHERE status = 'ended';
UPDATE campaigns SET status = 'cancelada'  WHERE status = 'paused';
UPDATE campaigns
   SET status = CASE
        WHEN end_date < (now() AT TIME ZONE 'America/Sao_Paulo')::date
             THEN 'concluida'
        ELSE 'ativa'
    END
 WHERE status = 'active';

-- 3. Atualizar default da coluna.
ALTER TABLE campaigns
    ALTER COLUMN status SET DEFAULT 'programada';

-- 4. Reaplicar CHECK com os novos valores.
ALTER TABLE campaigns
    ADD CONSTRAINT campaigns_status_check
    CHECK (status IN ('programada','ativa','concluida','cancelada'));

COMMIT;
