-- 0047_evidence_status_ambiguous.up.sql
-- Adiciona 'ambiguous' aos valores válidos de detections.evidence_status.
-- Detecção de gêmeo acústico cujo trecho discriminante não sobreviveu → vai
-- pra revisão manual, NÃO conta como confirmada (spec 2026-07-01).
-- detections é RANGE-particionada: o DROP/ADD cascateia pras partitions.
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
        'audit_rejected',
        'ambiguous'
    ));

COMMIT;
