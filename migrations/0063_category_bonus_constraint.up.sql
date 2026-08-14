-- 0063: CHECK admite 'bonus' além de 'orphan' (spec 2026-08-14 D4).
--
-- Esta migration é SÓ os ALTER TABLE, isolados de propósito num arquivo próprio
-- para que o ACCESS EXCLUSIVE lock (parent + toda partição de detections e
-- detection_campaigns) dure milissegundos — não o tempo de reescrever linhas.
-- A renomeação de dado 'orphan' -> 'bonus' está em 0064_category_bonus_rename,
-- que só toma ROW locks e não bloqueia leitores/escritores da tabela. Juntar as
-- duas coisas numa transação faria o ALTER segurar o lock exclusivo durante o
-- UPDATE inteiro (medido: ~4,9s de bloqueio num UPDATE de 375k linhas) — o
-- NOT VALID não evita isso, só evita o scan de validação.
--
-- 'orphan' CONTINUA aceito de propósito: durante a janela de deploy o binário
-- antigo da API ainda pode gravar 'orphan', e um CHECK sem ele derrubaria o
-- insert. Uma migration futura remove 'orphan' do CHECK depois que o backfill
-- global tiver rodado e nenhum produtor escrever mais o valor.
--
-- NOT VALID pra não travar as tabelas particionadas com um scan em ACCESS
-- EXCLUSIVE. Como o conjunto novo é um SUPERSET do antigo, toda linha existente
-- já satisfaz — validar é formalidade e pode rodar depois.

BEGIN;

ALTER TABLE detections DROP CONSTRAINT IF EXISTS detections_category_check;
ALTER TABLE detections ADD CONSTRAINT detections_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan','bonus')) NOT VALID;

ALTER TABLE detection_campaigns DROP CONSTRAINT IF EXISTS detection_campaigns_category_check;
ALTER TABLE detection_campaigns ADD CONSTRAINT detection_campaigns_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan','bonus')) NOT VALID;

COMMIT;
