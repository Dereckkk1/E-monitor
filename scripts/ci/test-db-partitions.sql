-- Partições do banco de TESTE (CI e qualquer test DB novo). NÃO roda em prod.
--
-- Por quê: detections, stream_health_events e detection_campaigns são
-- PARTITION BY RANGE mensal, e as migrations (0001, 0041, 0048) criam partição
-- a partir do MÊS EM QUE RODAM (CURRENT_DATE). Os testes inserem com datas
-- fixas (a maioria jun/2026). Um banco migrado em mai/2026 tem essas partições;
-- um banco novo de nov/2026 não — e 222 testes morrem com "no partition of
-- relation "detections" found for row" (1º run do CI, 2026-11-24).
--
-- Faixa: 2024-01 até o mês corrente + 36 meses. NÃO cobre 2010 nem 2090 de
-- propósito: TestDetections_HasPartitionFor exige que esses meses NÃO tenham.
-- Idempotente (to_regclass pula o que já existe). Mesmos nomes da 0048.
DO $$
DECLARE
    parent   text;
    prefix   text;
    m        date;
    fim      date := (date_trunc('month', CURRENT_DATE) + INTERVAL '36 months')::date;
    partname text;
BEGIN
    FOREACH parent IN ARRAY ARRAY['detections', 'stream_health_events', 'detection_campaigns'] LOOP
        prefix := CASE parent WHEN 'stream_health_events' THEN 'stream_health' ELSE parent END;
        m := DATE '2024-01-01';
        WHILE m < fim LOOP
            partname := prefix || '_' || to_char(m, 'YYYY_MM');
            IF to_regclass(partname) IS NULL THEN
                EXECUTE format(
                    'CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                    partname, parent, m, (m + INTERVAL '1 month')::date
                );
            END IF;
            m := (m + INTERVAL '1 month')::date;
        END LOOP;
    END LOOP;
END $$;
