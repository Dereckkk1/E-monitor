-- Persiste a cobertura do §9.9 audit (frames do master casados no clipe de
-- evidência / total de frames do master) por detection. Necessária pra:
--   (a) a desambiguação por cobertura (§18.2.2 v2): um corte irmão (15s vs 30s do
--       mesmo cliente, mesmo break) compara quanto de CADA master o clipe cobriu,
--       e o mais coberto é o que de fato tocou — em vez de "o mais longo ganha";
--   (b) o display rico do /detections/:id.
-- NULL = ainda não auditado (ou audit não rodou). NUMERIC(5,4) cobre 0.0000..1.0000.
ALTER TABLE detections
  ADD COLUMN IF NOT EXISTS audit_coverage NUMERIC(5,4);

COMMENT ON COLUMN detections.audit_coverage IS
  'Cobertura do §9.9 audit (frames do master casados no clipe / total). Usada pela desambiguação por cobertura (§18.2.2 v2) e pelo /detections/:id. NULL quando não auditado.';
