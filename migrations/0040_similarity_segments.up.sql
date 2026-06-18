-- 0040_similarity_segments.up.sql
-- Persiste os trechos iguais (segmentos conectados) entre um material e seu
-- top match, pra desenhar a timeline de sobreposição no upload (wizard).
-- Ver docs/superpowers/specs/2026-06-18-similarity-overlap-timeline-design.md.

BEGIN;

ALTER TABLE materials ADD COLUMN similarity_segments JSONB;

COMMIT;
