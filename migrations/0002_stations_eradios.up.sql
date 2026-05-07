ALTER TABLE stations
    ADD COLUMN logo_url    TEXT,
    ADD COLUMN pmm         NUMERIC(10,2),
    ADD COLUMN eradios_id  TEXT,
    ADD COLUMN latitude    NUMERIC(9,6),
    ADD COLUMN longitude   NUMERIC(9,6);

-- Partial unique index: only one row per E-radios broadcaster, but manually-created rows (NULL) are unrestricted
CREATE UNIQUE INDEX idx_stations_eradios_id ON stations(eradios_id) WHERE eradios_id IS NOT NULL;

-- GIN index on metadata for future category/audience queries
CREATE INDEX idx_stations_metadata ON stations USING GIN (metadata);
