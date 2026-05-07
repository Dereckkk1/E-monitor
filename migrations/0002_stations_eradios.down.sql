DROP INDEX IF EXISTS idx_stations_metadata;
DROP INDEX IF EXISTS idx_stations_eradios_id;
ALTER TABLE stations
    DROP COLUMN IF EXISTS logo_url,
    DROP COLUMN IF EXISTS pmm,
    DROP COLUMN IF EXISTS eradios_id,
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS longitude;
