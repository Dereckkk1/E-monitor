-- diagnose-zeroed-override-outslot.sql — READ-ONLY
--
-- Contexto: um override com plays_expected = 0 (célula "zerada" à mão no step
-- Distribuição) faz o categorizador devolver 'out_slot' pra QUALQUER tocada do
-- dia, ignorando a faixa horária gravada (categorizer.go:93 e
-- distribution_rules.go:263). Como a faixa fica inerte, uma bonificação que
-- tocou DENTRO da janela aparece como "fora da faixa" — e, no
-- daily_play_summary, out_slot não entra em `bonus` nem em `in_slot`, ou seja,
-- a tocada some do financeiro (base = in_slot + bonus).
--
-- Este script mede o tamanho do problema. Não altera nada.
--
-- Uso na VM:
--   docker compose -f infra/docker/docker-compose.yml \
--                  -f infra/docker/docker-compose.override.yml \
--                  --env-file infra/docker/.env \
--     exec -T postgres psql -U radiocheck -d radiocheck -f - < scripts/sql/diagnose-zeroed-override-outslot.sql
--   (ou cole query a query num `psql -At -c "..."`)

\echo '=== Q1 — células zeradas da campanha alvo e o que tocou nelas ==='

WITH camp AS (
    SELECT id, name
    FROM campaigns
    WHERE name ILIKE '%270%'          -- <<< AJUSTE: trecho do nome da campanha
),
zeroed AS (
    SELECT o.campaign_id, o.type_id, o.station_id, o.for_date,
           o.time_start, o.time_end, o.reason
    FROM distribution_overrides o
    JOIN camp c ON c.id = o.campaign_id
    WHERE o.plays_expected = 0
)
SELECT
    c.name                                                      AS campanha,
    st.name || ' ' || st.band || ' ' ||
        COALESCE(st.frequency_mhz::text, '')                    AS emissora,
    z.for_date,
    to_char(z.time_start, 'HH24:MI') || '–' ||
        to_char(z.time_end, 'HH24:MI')                          AS faixa_inerte,
    COUNT(*)                                                    AS tocadas,
    COUNT(*) FILTER (WHERE dc.category = 'out_slot')            AS como_out_slot,
    COUNT(*) FILTER (
        WHERE (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::time
              BETWEEN z.time_start AND z.time_end)              AS dentro_da_faixa_gravada,
    z.reason
FROM zeroed z
JOIN camp     c  ON c.id  = z.campaign_id
JOIN stations st ON st.id = z.station_id
JOIN detection_campaigns dc
      ON dc.campaign_id = z.campaign_id
     AND dc.detected_at >= (z.for_date::timestamp     AT TIME ZONE 'America/Sao_Paulo')
     AND dc.detected_at <  ((z.for_date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
JOIN detections d
      ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
     AND d.station_id = z.station_id
     AND d.retracted_at IS NULL
     AND d.ignored_at IS NULL
     AND d.evidence_status <> 'audit_rejected'
JOIN materials m ON m.id = dc.commercial_id AND m.type_id = z.type_id
GROUP BY c.name, emissora, z.for_date, faixa_inerte, z.reason
ORDER BY z.for_date, emissora;

\echo ''
\echo '=== Q2 — raio de alcance global: toda campanha com célula zerada + tocada ==='

WITH zeroed AS (
    SELECT campaign_id, type_id, station_id, for_date, time_start, time_end
    FROM distribution_overrides
    WHERE plays_expected = 0
),
hits AS (
    SELECT
        z.campaign_id,
        dc.detection_id,
        dc.category,
        ((d.detected_at AT TIME ZONE 'America/Sao_Paulo')::time
             BETWEEN z.time_start AND z.time_end) AS dentro_da_faixa
    FROM zeroed z
    JOIN detection_campaigns dc
          ON dc.campaign_id = z.campaign_id
         AND dc.detected_at >= (z.for_date::timestamp     AT TIME ZONE 'America/Sao_Paulo')
         AND dc.detected_at <  ((z.for_date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
    JOIN detections d
          ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
         AND d.station_id = z.station_id
         AND d.retracted_at IS NULL
         AND d.ignored_at IS NULL
         AND d.evidence_status <> 'audit_rejected'
    JOIN materials m ON m.id = dc.commercial_id AND m.type_id = z.type_id
)
SELECT
    c.name                                              AS campanha,
    c.status,
    cl.name                                             AS cliente,
    COUNT(*)                                            AS tocadas_em_celula_zerada,
    COUNT(*) FILTER (WHERE h.category = 'out_slot')     AS hoje_out_slot,
    COUNT(*) FILTER (WHERE h.dentro_da_faixa)           AS dentro_da_faixa_gravada
FROM hits h
JOIN campaigns c ON c.id = h.campaign_id
JOIN clients  cl ON cl.id = c.client_id
GROUP BY c.name, c.status, cl.name
ORDER BY tocadas_em_celula_zerada DESC;

\echo ''
\echo '=== Q3 — quantas células zeradas existem, e quantas têm tocada ==='

SELECT
    COUNT(*)                                        AS celulas_zeradas,
    COUNT(DISTINCT campaign_id)                     AS campanhas,
    MIN(for_date)                                   AS primeira,
    MAX(for_date)                                   AS ultima
FROM distribution_overrides
WHERE plays_expected = 0;
