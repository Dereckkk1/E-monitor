-- 0021_detections_manual.up.sql
-- Permite que um admin adicione veiculações retroativas via /detections —
-- casos de uso: comerciais que de fato tocaram mas o áudio caiu / streaming
-- deu glitch / a impressão veio offline da emissora, ou ajustes finos
-- combinados fora do sistema.
--
-- Uma "manual detection" é uma linha em `detections` indistinguível das demais
-- pra todos os agregados (in_slot/out_slot/out_date/orphan saem do mesmo
-- categorizador), com 3 metadados novos pra rastreabilidade:
--   manual_at   — quando foi criada
--   manual_by   — qual admin criou (FK users)
--   manual_note — descrição livre que aparece na detail page
--
-- O CHECK garante que manual_at e manual_by sempre andam juntos (uma sem a
-- outra é estado inválido). manual_note é opcional.
--
-- Detections manuais herdam evidence_status='missing' (sem áudio) e
-- confidence=1.0 (declarado pelo admin, treat as ground-truth pra fins de
-- agregado). Continuam susceptíveis a `ignored_at` (admin pode desconsiderar)
-- e a `retracted_at` (engine de disambiguation não vai mexer em manual, mas
-- por consistência do schema o campo continua presente).

BEGIN;

ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS manual_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS manual_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS manual_note TEXT;

ALTER TABLE detections
    ADD CONSTRAINT detections_manual_pair_chk
    CHECK (
        (manual_at IS NULL AND manual_by IS NULL)
        OR (manual_at IS NOT NULL AND manual_by IS NOT NULL)
    );

-- Índice parcial pra acelerar queries de auditoria do tipo "todas as
-- veiculações inseridas manualmente esse mês". Não pesa no índice principal
-- de detected_at porque a esmagadora maioria das linhas tem manual_at NULL.
CREATE INDEX IF NOT EXISTS detections_manual_at_idx
    ON detections (manual_at DESC, campaign_id)
    WHERE manual_at IS NOT NULL;

COMMIT;
