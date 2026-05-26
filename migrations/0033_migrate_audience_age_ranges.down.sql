-- 0033_migrate_audience_age_ranges.down.sql
-- Reverte a migração 0033: restaura ageRange a partir de ageRangeLegado e
-- remove ageRanges + ageRangeLegado.
--
-- AVISO: qualquer ageRanges preenchido MANUALMENTE depois da migration up é
-- perdido nesta reversão. Stations sem ageRangeLegado (novas, criadas pós-up)
-- ficam intactas — não havia texto legado pra restaurar.

BEGIN;

UPDATE stations
SET metadata = (
        jsonb_set(
            (metadata #- '{audience_profile,ageRanges}'),
            '{audience_profile,ageRange}',
            metadata #> '{audience_profile,ageRangeLegado}'
        )
    ) #- '{audience_profile,ageRangeLegado}',
    updated_at = NOW()
WHERE metadata #> '{audience_profile,ageRangeLegado}' IS NOT NULL;

COMMIT;
