-- 0050_evidence_status_expired.down.sql
-- Reverte 0050: move rows 'expired' pra 'missing' (o clipe já foi apagado, então
-- 'missing' é o estado honesto) antes de remover o valor do CHECK.
BEGIN;
SET lock_timeout = '5s';
SET statement_timeout = '60s';

UPDATE detections
SET evidence_status = 'missing'
WHERE evidence_status = 'expired';

ALTER TABLE detections
    DROP CONSTRAINT IF EXISTS detections_evidence_status_check;

ALTER TABLE detections
    ADD CONSTRAINT detections_evidence_status_check
    CHECK (evidence_status IN (
        'pending',
        'generating',
        'available',
        'missing',
        'failed',
        'audit_rejected',
        'ambiguous'
    ));

COMMIT;
