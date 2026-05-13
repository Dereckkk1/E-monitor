-- 0022_pricing.down.sql
-- Reverte a estrutura de pricing por (campaign × station). Não tenta
-- preservar valores cadastrados — se estiver revertendo, é porque algo deu
-- errado e os dados serão recadastrados depois.

BEGIN;

DROP TRIGGER IF EXISTS trg_cstp_touch ON campaign_station_type_pricing;
DROP TRIGGER IF EXISTS trg_csp_touch  ON campaign_station_pricing;
DROP FUNCTION IF EXISTS touch_updated_at();

DROP TABLE IF EXISTS campaign_station_type_pricing;
DROP TABLE IF EXISTS campaign_station_pricing;

COMMIT;
