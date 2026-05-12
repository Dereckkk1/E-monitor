-- 0019_rules_by_type.down.sql
-- Reverte migração 0019. Como a 0019 dropa rules+overrides existentes,
-- esse down reconstrói o schema antigo mas NÃO restaura os dados —
-- vão precisar ser recadastrados.

BEGIN;

DROP VIEW IF EXISTS daily_play_summary;

-- distribution_rules: type_id → material_id
DELETE FROM distribution_rules;
DROP INDEX IF EXISTS idx_distribution_rules_type;
ALTER TABLE distribution_rules DROP COLUMN type_id;
ALTER TABLE distribution_rules ADD COLUMN material_id UUID NOT NULL
    REFERENCES materials(id) ON DELETE CASCADE;
CREATE INDEX idx_distribution_rules_material ON distribution_rules(material_id);

-- distribution_overrides: type_id → material_id
DELETE FROM distribution_overrides;
ALTER TABLE distribution_overrides DROP CONSTRAINT distribution_overrides_pkey;
ALTER TABLE distribution_overrides DROP COLUMN type_id;
ALTER TABLE distribution_overrides ADD COLUMN material_id UUID NOT NULL
    REFERENCES materials(id) ON DELETE CASCADE;
ALTER TABLE distribution_overrides ADD PRIMARY KEY (campaign_id, material_id, station_id, for_date);

-- View daily_play_summary (recria a versão 0018 — por material)
CREATE VIEW daily_play_summary AS
WITH expected AS (
    SELECT
        r.campaign_id,
        r.material_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(r.start_date, r.end_date, INTERVAL '1 day') AS d(for_date)
    WHERE (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.material_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.material_id, e.material_id) AS material_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN distribution_overrides o
        ON e.campaign_id = o.campaign_id
       AND e.material_id = o.material_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        campaign_id,
        commercial_id AS material_id,
        station_id,
        date_trunc('day', detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE category = 'orphan')::int   AS orphan
    FROM detections
    WHERE retracted_at IS NULL
    GROUP BY campaign_id, commercial_id, station_id, for_date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.material_id, a.material_id) AS material_id,
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
   AND e.material_id = a.material_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date;

COMMIT;
