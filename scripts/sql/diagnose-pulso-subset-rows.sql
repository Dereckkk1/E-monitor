-- diagnose-pulso-subset-rows.sql — READ-ONLY.
-- Candidatas a "tocada do pulso contada como spot" no histórico: rows do spot
-- com audit_coverage baixo (assinatura de false-confirm, medido 0.183 no E2E)
-- + supressões do pulso registradas. Rodar na VM via:
--   $DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < este_arquivo
-- Reparo (reatribuir/retratar/manual) é decisão do dono a partir deste relatório.
-- Contexto: docs/incidents/incident-2026-07-24-pulso-milium-nao-detectado.md

\echo '=== 1. Rows do catálogo Milium com audit_coverage < 0.30 (últimos 30 dias) ==='
SELECT to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, m.short_id, left(m.title,40) AS material,
       m.duration_seconds AS dur,
       d.audit_coverage, d.evidence_status, (d.retracted_at IS NOT NULL) AS retratada
FROM detections d
JOIN stations s ON s.id=d.station_id
JOIN materials m ON m.id=d.commercial_id
JOIN clients c ON c.id=m.client_id
WHERE c.name ILIKE '%MILIUM%'
  AND d.audit_coverage IS NOT NULL AND d.audit_coverage < 0.30
  AND d.detected_at > now() - interval '30 days'
ORDER BY d.detected_at;

\echo ''
\echo '=== 2. Supressões do pulso (tocadas reais mortas sem row) ==='
SELECT to_char(ds.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando_brt,
       s.name AS emissora, ds.suppressed_short_id, ds.kept_short_id,
       ds.suppressed_duration AS dur_supr, ds.kept_duration AS dur_kept,
       round(ds.suppressed_confidence::numeric,3) AS conf_supr,
       round(ds.kept_confidence::numeric,3) AS conf_kept,
       (ds.suppressed_confidence >= ds.kept_confidence + 0.25) AS suspeita,
       ds.reason
FROM dedup_suppressions ds JOIN stations s ON s.id=ds.station_id
WHERE ds.detected_at > now() - interval '30 days'
  AND ds.suppressed_short_id IN (SELECT short_id FROM materials m
                                 JOIN clients c ON c.id=m.client_id
                                 WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12)
ORDER BY ds.detected_at;

\echo ''
\echo '=== 3. Cruzamento por dia/emissora: rows suspeitas do spot × supressões do pulso ==='
\echo '    (dia com AS DUAS colunas > 0 = tocada do pulso provavelmente recuperável'
\echo '     por reatribuição da row do spot; só supressão e nenhuma row = perda seca, F-122)'
WITH rows_suspeitas AS (
    SELECT date(d.detected_at AT TIME ZONE 'America/Sao_Paulo') AS dia,
           d.station_id, count(*) AS n
    FROM detections d
    JOIN materials m ON m.id=d.commercial_id
    JOIN clients c ON c.id=m.client_id
    WHERE c.name ILIKE '%MILIUM%'
      AND d.audit_coverage IS NOT NULL AND d.audit_coverage < 0.30
      AND d.detected_at > now() - interval '30 days'
    GROUP BY 1, 2
),
supressoes AS (
    SELECT date(ds.detected_at AT TIME ZONE 'America/Sao_Paulo') AS dia,
           ds.station_id, count(*) AS n
    FROM dedup_suppressions ds
    WHERE ds.detected_at > now() - interval '30 days'
      AND ds.suppressed_short_id IN (SELECT short_id FROM materials m
                                     JOIN clients c ON c.id=m.client_id
                                     WHERE c.name ILIKE '%MILIUM%' AND m.duration_seconds < 12)
    GROUP BY 1, 2
)
SELECT COALESCE(r.dia, p.dia) AS dia,
       s.name AS emissora,
       COALESCE(r.n, 0) AS rows_spot_suspeitas,
       COALESCE(p.n, 0) AS supressoes_do_pulso
FROM rows_suspeitas r
FULL OUTER JOIN supressoes p ON p.dia = r.dia AND p.station_id = r.station_id
JOIN stations s ON s.id = COALESCE(r.station_id, p.station_id)
ORDER BY 1, 2;
