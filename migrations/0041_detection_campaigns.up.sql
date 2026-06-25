-- 0041_detection_campaigns.up.sql
-- F-119 multi-atribuição (fase 1: modelo de dados).
-- Spec: docs/superpowers/specs/2026-06-25-multi-attribution-f119-design.md
--
-- detections continua 1 linha por tocada física (dona de evidência/audit/dedup/
-- retração, INTOCADA). Esta tabela carrega N projeções por campanha (uma por
-- campanha que roda o mesmo áudio na emissora). Os 3 predicados do conjunto
-- "aprovado" (retracted/ignored/audit_rejected) ficam na tocada base e valem
-- para todas as projeções.

BEGIN;

CREATE TABLE detection_campaigns (
    detection_id  UUID        NOT NULL,
    detected_at   TIMESTAMPTZ NOT NULL,
    campaign_id   UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    commercial_id UUID        NOT NULL,
    category      TEXT        NOT NULL DEFAULT 'orphan'
                  CHECK (category IN ('in_slot','out_slot','out_date','orphan')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (detection_id, detected_at, campaign_id),
    FOREIGN KEY (detection_id, detected_at)
        REFERENCES detections(id, detected_at) ON DELETE CASCADE
) PARTITION BY RANGE (detected_at);

-- Espelha EXATAMENTE as partições existentes de detections (histórico + futuro
-- já criado). Range fixo "do mês corrente +12" não cobriria detecções antigas
-- em prod e o backfill abaixo cairia em "no partition of relation found for row".
DO $$
DECLARE
    part RECORD;
    suffix TEXT;
BEGIN
    FOR part IN
        SELECT c.relname AS relname, pg_get_expr(c.relpartbound, c.oid) AS bound
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'detections'::regclass
    LOOP
        suffix := regexp_replace(part.relname, '^detections_', '');
        EXECUTE format('CREATE TABLE detection_campaigns_%s PARTITION OF detection_campaigns %s',
                       suffix, part.bound);
    END LOOP;
END $$;

CREATE INDEX idx_detcamp_campaign_time   ON detection_campaigns (campaign_id, detected_at DESC);
CREATE INDEX idx_detcamp_commercial_time ON detection_campaigns (commercial_id, detected_at DESC);

-- Backfill 1:1: cada detecção existente vira sua projeção canônica. Com isso o
-- read path (que passa a ler a view) fica idêntico ao de hoje com a flag OFF.
INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
SELECT id, detected_at, campaign_id, commercial_id, category
FROM detections
ON CONFLICT DO NOTHING;

-- View de compatibilidade: leituras por-campanha trocam FROM detections d ->
-- FROM detection_attributions d. Expõe os campos da tocada base (retracted_at,
-- ignored_at, evidence_status) pra o ApprovedDetectionsFilter seguir válido.
CREATE VIEW detection_attributions AS
SELECT d.id, d.station_id, d.detected_at, d.evidence_status, d.evidence_key,
       d.retracted_at, d.ignored_at, d.confidence, d.hash_count, d.audit_coverage,
       d.match_start_offset_ms, d.match_end_offset_ms, d.temporal_coverage,
       d.variant_used, d.rate_used, d.created_at,
       dc.campaign_id, dc.commercial_id, dc.category
FROM detections d
JOIN detection_campaigns dc
  ON dc.detection_id = d.id AND dc.detected_at = d.detected_at;

-- daily_play_summary: o CTE `actual` passa a agregar as projeções (categoria
-- por-campanha em detection_campaigns), com o gate aprovado vindo do JOIN na
-- tocada base. expected / expected_with_override / SELECT final = verbatim 0029.
DROP VIEW IF EXISTS daily_play_summary;
CREATE VIEW daily_play_summary AS
WITH expected AS (
    SELECT
        r.campaign_id,
        r.type_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(r.start_date, r.end_date, INTERVAL '1 day') AS d(for_date)
    WHERE (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
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
    FULL OUTER JOIN distribution_overrides o
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
    WHERE d.retracted_at IS NULL
      AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id, for_date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.type_id,     a.type_id)     AS type_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int                                                    AS expected,
    COALESCE(a.in_slot,  0)::int                                                    AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0) - COALESCE(a.out_slot,0))::int  AS deficit,
    (GREATEST(0, COALESCE(a.in_slot,0) - COALESCE(e.expected,0)) + COALESCE(a.orphan,0))::int  AS bonus,
    COALESCE(a.out_slot, 0)::int                                                    AS out_slot,
    COALESCE(a.out_date, 0)::int                                                    AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.type_id     = a.type_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date;

COMMIT;
