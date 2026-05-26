-- 0033_migrate_audience_age_ranges.up.sql
-- Converte o texto livre legado em stations.metadata.audience_profile.ageRange
-- ("XX% YY+") para o novo objeto estruturado ageRanges (range18to24, range25to49,
-- range50plus), preservando o texto original em ageRangeLegado.
--
-- Fórmula (definida pelo dono do produto em 2026-05-26):
--   range18to24 = XX / 2
--   range25to49 = XX / 2
--   range50plus = 100 - XX
-- O limite de idade YY do texto legado é intencionalmente ignorado — a heurística
-- assume que XX é a fatia "adulta" e o complemento vai pra 50+.
--
-- Três caminhos por station:
--   1. ageRange casa "XX% YY+"            → escreve ageRanges + ageRangeLegado + dropa ageRange
--   2. ageRange é texto livre não-parseável → move pra ageRangeLegado + dropa ageRange (sem ageRanges)
--   3. ageRange é vazio / só whitespace    → só dropa ageRange (nada a preservar)
--
-- Stations já migradas (ageRanges presente) ou com ageRange JSON-null / ausente
-- são puladas → re-runnable e seguro.
--
-- Spec: docs/features/station-audience-age-ranges.md

BEGIN;

WITH targets AS (
    SELECT
        id,
        metadata #>  '{audience_profile,ageRange}' AS legacy_json,
        metadata #>> '{audience_profile,ageRange}' AS legacy_text,
        (regexp_match(
            metadata #>> '{audience_profile,ageRange}',
            '^\s*([0-9]+(?:\.[0-9]+)?)\s*%\s*[0-9]+\s*\+\s*$'
        ))[1]::numeric AS xx
    FROM stations
    WHERE metadata #> '{audience_profile,ageRange}' IS NOT NULL
      AND jsonb_typeof(metadata #> '{audience_profile,ageRange}') = 'string'
      AND NOT (metadata #> '{audience_profile}' ? 'ageRanges')
)
UPDATE stations s
SET metadata =
        CASE
            WHEN t.xx IS NOT NULL THEN
                jsonb_set(
                    jsonb_set(
                        (s.metadata #- '{audience_profile,ageRange}'),
                        '{audience_profile,ageRanges}',
                        jsonb_build_object(
                            'range18to24', t.xx / 2,
                            'range25to49', t.xx / 2,
                            'range50plus', 100 - t.xx
                        )
                    ),
                    '{audience_profile,ageRangeLegado}',
                    t.legacy_json
                )
            WHEN length(trim(t.legacy_text)) > 0 THEN
                jsonb_set(
                    (s.metadata #- '{audience_profile,ageRange}'),
                    '{audience_profile,ageRangeLegado}',
                    t.legacy_json
                )
            ELSE
                (s.metadata #- '{audience_profile,ageRange}')
        END,
    updated_at = NOW()
FROM targets t
WHERE s.id = t.id;

COMMIT;
