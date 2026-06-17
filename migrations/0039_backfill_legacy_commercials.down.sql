-- 0039_backfill_legacy_commercials.down.sql
--
-- No-op intencional. O up é um backfill de dados (commercials→materials) que
-- NÃO é reversível com segurança: depois de aplicado, os materials/links
-- criados aqui são indistinguíveis por dado dos mirrors criados pela migration
-- 0016 ou dos materiais legitimamente reaproveitados em outras campanhas.
-- Apagar "materials cujo id existe em commercials" derrubaria também os mirrors
-- de 0016 e quebraria detections.commercial_id que já apontam pra eles.
--
-- Conforme docs/operations/migrations.md, DOWN é guard-rail de dev e em produção
-- evitamos rollback automático — a reversão correta, se algum dia necessária,
-- é uma migration corretiva cirúrgica, não este down genérico.

SELECT 1;
