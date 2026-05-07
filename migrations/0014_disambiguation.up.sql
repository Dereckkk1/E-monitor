-- §18.2.2 — Version disambiguation (30s vs 60s cuts of the same commercial).
--
-- 1. detections.retracted_at: when a longer cut from the same client is detected
--    within Δ seconds of a previously published shorter cut, the supervisor
--    publishes detection.retracted on NATS and stamps retracted_at = now() on
--    the previously inserted row. Operational queries should filter rows where
--    retracted_at IS NULL; clients consuming via webhook receive both events.
--
-- 2. campaigns.dedup_window_seconds: per-campaign override of the default Δ=5s
--    window. Campaigns where versions confirm in tighter sequence (e.g. 28s vs
--    32s cuts) can widen the window; campaigns where the operator wants strict
--    deduplication can leave the default.

ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS retracted_at TIMESTAMPTZ;

-- Partial index used by operational queries (UI list, reports) that ignore
-- retracted detections. Avoids paying the index-size cost on the long tail
-- of historical retractions.
CREATE INDEX IF NOT EXISTS detections_active_detected_at_idx
    ON detections (detected_at DESC)
    WHERE retracted_at IS NULL;

ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS dedup_window_seconds INT NOT NULL DEFAULT 5
        CHECK (dedup_window_seconds >= 0 AND dedup_window_seconds <= 60);
