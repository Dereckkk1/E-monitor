-- 0042_manual_proof_batches.down.sql
BEGIN;
DROP INDEX IF EXISTS detections_proof_batch_idx;
ALTER TABLE detections DROP COLUMN IF EXISTS proof_batch_id;
DROP TABLE IF EXISTS manual_proof_batches;
COMMIT;
