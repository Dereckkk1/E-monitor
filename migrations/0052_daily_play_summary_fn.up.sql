-- daily_play_summary_for: versão parametrizada da view daily_play_summary
-- (0041) com pushdown manual dos filtros. Semântica IDÊNTICA à view para o
-- recorte (p_from..p_to, p_campaigns); p_campaigns NULL = todas as campanhas.
-- A view segue existindo — os consumidores migram gradualmente (Tasks 11-13).
--
-- Por que a view não dá pushdown: o SELECT final é um FULL OUTER JOIN cujas
-- colunas de saída são COALESCE das duas pernas. Um WHERE externo em
-- campaign_id/for_date não atravessa isso — o planner materializa as duas CTEs
-- inteiras (generate_series de TODAS as regras + agregado do histórico INTEIRO
-- de detection_campaigns) e só então filtra. Aqui os filtros entram DENTRO das
-- CTEs.
--
-- Equivalência (validada por scripts/sql/paridade-dps-function.sql):
--   SELECT * FROM daily_play_summary WHERE for_date BETWEEN f AND t
--     [AND campaign_id = ANY(c)]
-- ≡ SELECT * FROM daily_play_summary_for(f, t, c)
--
-- O bound em detected_at usa [meia-noite local de p_from, meia-noite local de
-- p_to+1) — exatamente as linhas cujo dia local cai em [p_from, p_to], igual ao
-- date_trunc da view, mas SARGÁVEL (poda partições de detections/detection_campaigns).
--
-- Contrato de NULL: p_from/p_to são OBRIGATÓRIOS (NOT NULL) — GREATEST/LEAST
-- ignoram NULL silenciosamente (a CTE expected continuaria produzindo linhas
-- reais), mas `p_from::timestamp AT TIME ZONE ...` vira NULL e zera a CTE
-- actual, o que produziria "100% de déficit" plausível e ERRADO em vez de um
-- erro visível. Por isso a CTE expected também é blindada com
-- `p_from IS NOT NULL AND p_to IS NOT NULL`: com qualquer um dos dois NULL a
-- função devolve VAZIO (obviamente quebrado), nunca um número fabricado.
-- Não usar STRICT/RETURNS NULL ON NULL INPUT: mataria o `p_campaigns IS NULL`
-- legítimo (= "todas as campanhas"). p_campaigns = '{}' (array vazio, não
-- NULL) é "zero campanhas" via semântica de `= ANY('{}')` — não é bug, mas
-- callers devem passar NULL, nunca array vazio, para "todas".

CREATE OR REPLACE FUNCTION daily_play_summary_for(p_from date, p_to date, p_campaigns uuid[] DEFAULT NULL)
RETURNS TABLE (
    campaign_id uuid, type_id uuid, station_id uuid, for_date date,
    expected int, in_slot int, deficit int, bonus int, out_slot int, out_date int)
LANGUAGE sql STABLE AS $$
WITH expected AS (
    SELECT
        r.campaign_id,
        r.type_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(
        GREATEST(r.start_date, p_from),
        LEAST(r.end_date, p_to),
        INTERVAL '1 day') AS d(for_date)
    WHERE p_from IS NOT NULL AND p_to IS NOT NULL
      AND (p_campaigns IS NULL OR r.campaign_id = ANY(p_campaigns))
      AND (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.type_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.type_id,     e.type_id)     AS type_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN (
        SELECT * FROM distribution_overrides ov
        WHERE ov.for_date BETWEEN p_from AND p_to
          AND (p_campaigns IS NULL OR ov.campaign_id = ANY(p_campaigns))
    ) o
        ON e.campaign_id = o.campaign_id
       AND e.type_id     = o.type_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        dc.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE dc.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE dc.category = 'orphan')::int   AS orphan
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials   m ON m.id = dc.commercial_id
    WHERE (p_campaigns IS NULL OR dc.campaign_id = ANY(p_campaigns))
      AND dc.detected_at >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND dc.detected_at <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.retracted_at IS NULL
      AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id,
             date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.type_id,     a.type_id)     AS type_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int AS expected,
    COALESCE(a.in_slot,  0)::int AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0) - COALESCE(a.out_slot,0))::int AS deficit,
    (GREATEST(0, COALESCE(a.in_slot,0) - COALESCE(e.expected,0)) + COALESCE(a.orphan,0))::int AS bonus,
    COALESCE(a.out_slot, 0)::int AS out_slot,
    COALESCE(a.out_date, 0)::int AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.type_id     = a.type_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date
$$;
