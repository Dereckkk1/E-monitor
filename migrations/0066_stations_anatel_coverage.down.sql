-- Reversão da 0066. Todo o dado removido aqui é DERIVADO dos Planos Básicos
-- embutidos no binário (workers/internal/anatel/data) — nada é entrada manual,
-- então `backfill-anatel` reconstrói tudo. Nenhum dado de cadastro é tocado:
-- stations.latitude/longitude (centroide do município, migration 0002)
-- permanecem intactos.
--
-- lock_timeout: mesma proteção do up — DROP COLUMN em `stations` pega
-- AccessExclusive e não pode enfileirar tráfego real atrás de si.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP INDEX IF EXISTS idx_stations_anatel_unmatched;
DROP INDEX IF EXISTS idx_station_coverage_cities_uf_city;
DROP INDEX IF EXISTS idx_station_coverage_cities_ibge;
DROP TABLE IF EXISTS station_coverage_cities;

ALTER TABLE stations
    DROP CONSTRAINT IF EXISTS stations_anatel_match_tier_check;

ALTER TABLE stations
    DROP COLUMN IF EXISTS anatel_class,
    DROP COLUMN IF EXISTS anatel_coverage_km,
    DROP COLUMN IF EXISTS anatel_reach_km,
    DROP COLUMN IF EXISTS anatel_erp_kw,
    DROP COLUMN IF EXISTS anatel_latitude,
    DROP COLUMN IF EXISTS anatel_longitude,
    DROP COLUMN IF EXISTS anatel_match_tier,
    DROP COLUMN IF EXISTS anatel_match_distance_km,
    DROP COLUMN IF EXISTS anatel_plan_city,
    DROP COLUMN IF EXISTS anatel_plan_state,
    DROP COLUMN IF EXISTS anatel_matched_at;

COMMIT;
