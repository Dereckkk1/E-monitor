-- §11.4 — evidence storage tiering.
-- Adds a per-detection storage tier column so the daily tiering job can move
-- evidence between hot (local SSD), cold (R2 standard) and archive (R2 IA).

ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT 'hot'
        CHECK (tier IN ('hot', 'cold', 'archive'));

-- Composite index used by the tiering job to find candidates cheaply.
-- TODO PROD: em tabela com >10M linhas considere CREATE INDEX CONCURRENTLY
-- (exige migration tool que aceite no-transaction). Aceitável em PoC e Fase 2.
-- Follow-up registrado em docs/backup-and-retention.md §2.7.
CREATE INDEX IF NOT EXISTS detections_tier_created_at_idx
    ON detections (tier, created_at);
