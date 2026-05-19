-- 0031_override_time_window.up.sql
-- Adiciona time_start/time_end em distribution_overrides pra que ajustes
-- manuais no grid carreguem faixa horária. Backfill em duas fases: cria
-- colunas nullable, preenche com a faixa da rule mais antiga da célula+dia
-- (fallback 06:00-22:00 quando não há rule), depois aplica NOT NULL.
-- Spec: docs/superpowers/specs/2026-05-19-override-time-window-design.md

BEGIN;

ALTER TABLE distribution_overrides
    ADD COLUMN time_start TIME,
    ADD COLUMN time_end   TIME;

-- Backfill: faixa da rule mais antiga aplicável à célula+dia.
-- DISTINCT ON garante uma linha por (campaign, type, station, for_date).
-- LEFT JOIN + COALESCE cobre o caso de override sem nenhuma rule.
UPDATE distribution_overrides o
SET time_start = COALESCE(r.time_start, TIME '06:00'),
    time_end   = COALESCE(r.time_end,   TIME '22:00')
FROM (
    SELECT DISTINCT ON (o.campaign_id, o.type_id, o.station_id, o.for_date)
        o.campaign_id, o.type_id, o.station_id, o.for_date,
        r.time_start, r.time_end
    FROM distribution_overrides o
    LEFT JOIN distribution_rules r
        ON r.campaign_id = o.campaign_id
       AND r.type_id     = o.type_id
       AND o.station_id  = ANY(r.station_ids)
       AND o.for_date BETWEEN r.start_date AND r.end_date
       AND (1 << EXTRACT(DOW FROM o.for_date)::INT) & r.weekday_mask != 0
    ORDER BY o.campaign_id, o.type_id, o.station_id, o.for_date,
             r.created_at NULLS LAST, r.id NULLS LAST
) r
WHERE o.campaign_id = r.campaign_id
  AND o.type_id     = r.type_id
  AND o.station_id  = r.station_id
  AND o.for_date    = r.for_date;

-- Salvaguarda: se algum override ainda estiver com faixa NULL (caso não
-- previsto pelo backfill), o ALTER abaixo falha e o transaction inteiro
-- faz rollback. Ou aplica tudo, ou nada.
ALTER TABLE distribution_overrides
    ALTER COLUMN time_start SET NOT NULL,
    ALTER COLUMN time_end   SET NOT NULL,
    ADD CONSTRAINT override_times_valid CHECK (time_end > time_start);

COMMIT;
