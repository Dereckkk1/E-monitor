-- 0020_detections_ignored.down.sql
-- Reverte o soft-delete operacional. View volta à definição sem o filtro
-- `ignored_at`; coluna e índice são removidos. Veiculações que estavam
-- desconsideradas voltam a contar nos agregados.

BEGIN;

CREATE OR REPLACE VIEW daily_play_summary AS
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
        d.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE d.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE d.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE d.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE d.category = 'orphan')::int   AS orphan
    FROM detections d
    JOIN materials m ON m.id = d.commercial_id
    WHERE d.retracted_at IS NULL
      AND m.type_id IS NOT NULL
    GROUP BY d.campaign_id, m.type_id, d.station_id, for_date
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

DROP INDEX IF EXISTS detections_not_ignored_idx;

ALTER TABLE detections
    DROP COLUMN IF EXISTS ignored_by,
    DROP COLUMN IF EXISTS ignored_at;

COMMIT;
