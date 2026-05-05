CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ===== CLIENTS =====
CREATE TABLE clients (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    contact_email TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===== STATIONS =====
CREATE TABLE stations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    name TEXT NOT NULL,
    band TEXT NOT NULL CHECK (band IN ('AM', 'FM')),
    frequency_mhz NUMERIC(6,2),
    city TEXT,
    state CHAR(2),
    stream_url TEXT NOT NULL,
    monitoring_status TEXT NOT NULL DEFAULT 'paused'
        CHECK (monitoring_status IN ('active','paused','calibrating','error')),
    last_health_check TIMESTAMPTZ,
    health_status TEXT,
    consecutive_failures INT NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stations_status ON stations(monitoring_status);
CREATE INDEX idx_stations_short_id ON stations(short_id);

-- ===== CAMPAIGNS =====
CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id UUID NOT NULL REFERENCES clients(id),
    name TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned','active','paused','ended')),
    target_stations UUID[] NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT campaign_dates_valid CHECK (end_date >= start_date)
);

CREATE INDEX idx_campaigns_status_dates ON campaigns(status, start_date, end_date);
CREATE INDEX idx_campaigns_client ON campaigns(client_id);

-- ===== COMMERCIALS =====
CREATE TABLE commercials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    title TEXT NOT NULL,
    cut_label TEXT,
    duration_seconds NUMERIC(6,3) NOT NULL,
    master_storage_path TEXT NOT NULL,
    master_sha256 TEXT NOT NULL,
    fingerprint_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (fingerprint_status IN ('pending','generating','ready','failed')),
    fingerprint_generated_at TIMESTAMPTZ,
    fingerprint_hash_count INT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_commercials_campaign ON commercials(campaign_id);
CREATE INDEX idx_commercials_status ON commercials(fingerprint_status);
CREATE INDEX idx_commercials_short_id ON commercials(short_id);

-- ===== FINGERPRINT HASHES =====
CREATE TABLE fingerprint_hashes (
    commercial_id UUID NOT NULL,
    variant_id SMALLINT NOT NULL,
    rate_id SMALLINT NOT NULL DEFAULT 0,
    hash_value BIGINT NOT NULL,
    time_frame INT NOT NULL,
    PRIMARY KEY (commercial_id, variant_id, rate_id, hash_value, time_frame)
) PARTITION BY HASH (commercial_id);

DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('CREATE TABLE fingerprint_hashes_p%s PARTITION OF fingerprint_hashes FOR VALUES WITH (MODULUS 16, REMAINDER %s)', i, i);
        EXECUTE format('CREATE INDEX idx_fph_p%s_hash ON fingerprint_hashes_p%s(hash_value)', i, i);
    END LOOP;
END $$;

-- ===== DETECTIONS =====
CREATE TABLE detections (
    id UUID NOT NULL DEFAULT uuid_generate_v4(),
    station_id UUID NOT NULL REFERENCES stations(id),
    commercial_id UUID NOT NULL REFERENCES commercials(id),
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    detected_at TIMESTAMPTZ NOT NULL,
    match_start_offset_ms INT NOT NULL,
    match_end_offset_ms INT NOT NULL,
    confidence NUMERIC(5,4) NOT NULL,
    hash_count INT NOT NULL,
    temporal_coverage NUMERIC(4,3),
    variant_used SMALLINT,
    rate_used SMALLINT,
    evidence_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (evidence_status IN ('pending','generating','available','missing','failed')),
    evidence_key TEXT,
    evidence_size_bytes BIGINT,
    notes JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, detected_at)
) PARTITION BY RANGE (detected_at);

DO $$
DECLARE
    start_d DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_d DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_d := start_d + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE detections_%s PARTITION OF detections FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_d, 'YYYY_MM'), start_d, end_d);
        start_d := end_d;
    END LOOP;
END $$;

CREATE INDEX idx_detections_station_time ON detections(station_id, detected_at DESC);
CREATE INDEX idx_detections_campaign_time ON detections(campaign_id, detected_at DESC);
CREATE INDEX idx_detections_commercial_time ON detections(commercial_id, detected_at DESC);

-- ===== STREAM HEALTH EVENTS =====
CREATE TABLE stream_health_events (
    id BIGSERIAL,
    station_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_seconds INT,
    details JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, event_at)
) PARTITION BY RANGE (event_at);

DO $$
DECLARE
    start_d DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_d DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_d := start_d + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE stream_health_%s PARTITION OF stream_health_events FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_d, 'YYYY_MM'), start_d, end_d);
        start_d := end_d;
    END LOOP;
END $$;

CREATE INDEX idx_health_station_time ON stream_health_events(station_id, event_at DESC);

-- ===== TRIGGERS =====
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_clients_updated BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_stations_updated BEFORE UPDATE ON stations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_campaigns_updated BEFORE UPDATE ON campaigns
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_commercials_updated BEFORE UPDATE ON commercials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
