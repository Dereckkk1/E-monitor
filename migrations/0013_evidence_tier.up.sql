-- §11.4 — evidence storage tiering.
-- Adds a per-detection storage tier column so the daily tiering job can move
-- evidence between hot (local SSD), cold (R2 standard) and archive (R2 IA).

ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT 'hot'
        CHECK (tier IN ('hot', 'cold', 'archive'));

-- Composite index used by the tiering job to find candidates cheaply.
CREATE INDEX IF NOT EXISTS detections_tier_created_at_idx
    ON detections (tier, created_at);
