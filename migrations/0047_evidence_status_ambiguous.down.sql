-- 0047_evidence_status_ambiguous.down.sql
BEGIN;
SET lock_timeout = '5s';
SET statement_timeout = '60s';

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
        'audit_rejected'
    ));

COMMIT;
