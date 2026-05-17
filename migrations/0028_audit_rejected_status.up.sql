-- Migration 0028 — adiciona 'audit_rejected' ao CHECK constraint de
-- detections.evidence_status + backfill das pending órfãs.
--
-- Contexto: a PR 8f8f3ee (feat(audit) §9.9) shipou o código usando o valor
-- 'audit_rejected' em UpdateEvidence() mas NÃO atualizou o CHECK constraint
-- declarado em 0001_initial. Resultado em prod entre 15/05 e 17/05: toda
-- detecção que o audit decidia rejeitar (UPDATE com status='audit_rejected')
-- batia em SQLSTATE 23514 — a função logava o erro e retornava early,
-- deixando 104 rows órfãs em 'pending' (sem evidence_key). Detail page e
-- modal devolvendo 404 no play porque o handler exige status='available'.
-- Postmortem: docs/incidents/incident-2026-05-17-audit-status-constraint.md.
--
-- Esta migration faz duas coisas atômicas:
--   1. Recria o CHECK incluindo 'audit_rejected'. Como detections é
--      RANGE-particionada por detected_at, o DROP/ADD na parent cascateia
--      pras partitions (PG11+) — todas com o mesmo nome de constraint.
--   2. Backfill: as 104 'pending' antigas (>5min, sem evidence_key) viram
--      'missing' — mesma semântica de detection manual sem áudio, conta
--      como veiculação válida nos agregados, áudio indisponível.

BEGIN;

SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- 1. Constraint update. IF EXISTS para idempotência em caso de re-run.
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

-- 2. Backfill das órfãs. Filtro `created_at < now() - 5 min` evita pegar
-- detection ainda em janela de processamento normal (sleep + extract +
-- audit + encode + upload completa em <2min em condição saudável).
UPDATE detections
SET evidence_status = 'missing'
WHERE evidence_status = 'pending'
  AND evidence_key IS NULL
  AND created_at < now() - interval '5 minutes';

COMMIT;
