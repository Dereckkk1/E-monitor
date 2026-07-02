-- 0050_evidence_status_expired.up.sql
-- Adiciona 'expired' aos valores válidos de detections.evidence_status.
-- Estado da retenção local (§11.4 variante de prod, incidente 2026-07-02): o
-- clipe de áudio foi apagado do MinIO pelo prune após N dias, mas a detecção
-- continua contando como veiculação. Sem este valor no CHECK, MarkEvidenceExpired
-- falharia o constraint (mesma pegadinha do incidente 2026-05-17 com
-- 'audit_rejected'). detections é RANGE-particionada: o DROP/ADD cascateia pras
-- partitions.
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
        'ambiguous',
        'expired'
    ));

COMMIT;
