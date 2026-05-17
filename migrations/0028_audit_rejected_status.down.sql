-- Reverte 0028: remove 'audit_rejected' do CHECK e devolve o constraint
-- ao estado de 0001_initial. Antes de dropar o valor é preciso garantir
-- que nenhuma row está nele (caso contrário o ADD falha).
--
-- O backfill de 'pending' → 'missing' NÃO é desfeito — uma vez que o áudio
-- foi limpo do disco não há como reconstruir a row anterior, e re-trazer
-- pra 'pending' geraria as mesmas órfãs do bug original.

BEGIN;

SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Move rows audit_rejected pra 'failed' antes de remover o valor do CHECK.
UPDATE detections
SET evidence_status = 'failed'
WHERE evidence_status = 'audit_rejected';

ALTER TABLE detections
    DROP CONSTRAINT IF EXISTS detections_evidence_status_check;

ALTER TABLE detections
    ADD CONSTRAINT detections_evidence_status_check
    CHECK (evidence_status IN (
        'pending',
        'generating',
        'available',
        'missing',
        'failed'
    ));

COMMIT;
