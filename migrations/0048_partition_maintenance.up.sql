-- Manutenção de partições mensais (audit 2026-07-02 E1).
--
-- detections, stream_health_events e detection_campaigns são PARTITION BY RANGE
-- (detected_at / event_at), mas a 0001/0041 criaram só ~12 meses de partições e
-- NENHUM job cria novas. Quando o horizonte esgota (~abr/2027), todo INSERT
-- falha com "no partition of relation found" e 100% das detecções se perdem até
-- intervenção manual.
--
-- ensure_month_partitions(N) cria as partições faltantes de current_month até
-- current_month + N, para as três tabelas, de forma idempotente (to_regclass
-- pula as que já existem). É chamada aqui pra estender o horizonte AGORA, e por
-- um job diário no processo da api (cmd/api) pra manter um buffer rolante.
--
-- Nota de nomes: as partições-filhas de stream_health_events usam o prefixo
-- 'stream_health_' (ver 0001), não o nome do parent — por isso o CASE abaixo.

CREATE OR REPLACE FUNCTION ensure_month_partitions(months_ahead int DEFAULT 6)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    parent   text;
    prefix   text;
    m        date;
    start_m  date := date_trunc('month', CURRENT_DATE)::date;
    partname text;
    i        int;
BEGIN
    FOREACH parent IN ARRAY ARRAY['detections', 'stream_health_events', 'detection_campaigns'] LOOP
        prefix := CASE parent WHEN 'stream_health_events' THEN 'stream_health' ELSE parent END;
        FOR i IN 0..months_ahead LOOP
            m := (start_m + (i || ' months')::interval)::date;
            partname := prefix || '_' || to_char(m, 'YYYY_MM');
            IF to_regclass(partname) IS NULL THEN
                EXECUTE format(
                    'CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                    partname, parent, m, (m + INTERVAL '1 month')::date
                );
            END IF;
        END LOOP;
    END LOOP;
END;
$$;

-- Estende o horizonte imediatamente (18 meses à frente a partir do mês atual).
SELECT ensure_month_partitions(18);
