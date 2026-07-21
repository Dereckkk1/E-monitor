-- 0055_client_target_label.up.sql
-- Rótulo do público-alvo por cliente (ex.: "Homens 25-49, classe AB").
--
-- Decisões de design:
--
-- 1. Texto livre, propriedade do CLIENTE (mesma granularidade do
--    client_station_pmm da 0054): o target é do cliente e vale para todas as
--    campanhas dele. As telas usam o rótulo só como sufixo descritivo dos
--    números já existentes ("Impactos no target (Homens 25-49)").
--
-- 2. NULL (ou string vazia) = cliente sem rótulo → as telas continuam
--    dizendo apenas "no target". Por isso não há DEFAULT nem NOT NULL:
--    nenhum cliente existente ganha rótulo com esta migration.
--
-- 3. TEXT sem CHECK de tamanho — o limite (200 chars) é validado na API,
--    onde a mensagem de erro é acionável; o banco não precisa recusar.
--
-- 4. Sem versionamento temporal, igual ao pmm_target da 0054: corrigir o
--    rótulo muda a legenda de relatórios passados.

BEGIN;

ALTER TABLE clients ADD COLUMN IF NOT EXISTS target_label TEXT;

COMMIT;
