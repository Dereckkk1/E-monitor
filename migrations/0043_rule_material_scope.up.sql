-- 0043_rule_material_scope.up.sql
-- Escopo opcional de regra a materiais específicos dentro de um tipo.
-- material_ids vazio ('{}') = regra vale pra TODOS os materiais do tipo
-- (comportamento da migration 0019, inalterado). Preenchido = vale só pra
-- esses materiais, com precedência (carve-out) no categorizador.
-- Spec: docs/superpowers/specs/2026-06-29-material-specific-distribution-rules-design.md

BEGIN;

ALTER TABLE distribution_rules
  ADD COLUMN material_ids UUID[] NOT NULL DEFAULT '{}';

COMMIT;
