CREATE TABLE station_thresholds (
    station_id          UUID PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
    calibration_mode    BOOLEAN NOT NULL DEFAULT true,
    calibration_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    noise_samples       FLOAT[] NOT NULL DEFAULT '{}',
    noise_p99           FLOAT,
    min_hashes          INT NOT NULL DEFAULT 5,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
