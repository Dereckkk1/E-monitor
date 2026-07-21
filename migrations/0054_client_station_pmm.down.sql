BEGIN;
DROP TRIGGER IF EXISTS trg_cspmm_touch ON client_station_pmm;
DROP TABLE IF EXISTS client_station_pmm;
COMMIT;
