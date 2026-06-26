-- 0042_manual_proof_batches.up.sql
-- Veiculações manuais em lote + comprovante PDF.
-- Spec: docs/superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md
--
-- Um PDF comprovante (1 linha aqui) cobre N veiculações manuais (materiais
-- mistos). Cada detecção do lote referencia o batch via detections.proof_batch_id.
-- A censura (áudio) continua nas colunas evidence_* de detections (subida depois).

BEGIN;

CREATE TABLE manual_proof_batches (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id    UUID NOT NULL REFERENCES campaigns(id),
    station_id     UUID NOT NULL REFERENCES stations(id),
    proof_pdf_key  TEXT   NOT NULL,
    proof_pdf_size BIGINT NOT NULL,
    note           TEXT,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- detections é particionada por detected_at; FK de tabela particionada -> tabela
-- comum é suportado (PG 12+). Coluna nasce nullable, sem reescrita.
ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS proof_batch_id UUID
        REFERENCES manual_proof_batches(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS detections_proof_batch_idx
    ON detections (proof_batch_id) WHERE proof_batch_id IS NOT NULL;

COMMIT;
