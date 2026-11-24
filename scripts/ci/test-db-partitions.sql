-- Partições do banco de TESTE (CI e qualquer test DB novo). NÃO roda em prod.
--
-- Por quê: detections, stream_health_events e detection_campaigns são
-- PARTITION BY RANGE mensal, e as migrations (0001, 0041, 0048) criam partição
-- a partir do MÊS EM QUE RODAM (CURRENT_DATE) até +18 meses. Os testes inserem
-- datas fixas do PASSADO (a mais antiga: 2026-01; o grosso jun/2026). Um banco
-- migrado em mai/2026 tem essas partições; um banco novo de nov/2026 não — e 222
-- testes morriam com "no partition of relation "detections" found for row".
--
-- Faixa: do MENOR entre 2026-01 e (mês corrente − 3) até o mês corrente. O
-- futuro as migrations já cobrem. NÃO alargue à toa: os testes do MCP fazem
-- `TRUNCATE users, clients ... CASCADE` a cada teste, o CASCADE trunca TODA
-- partição de detections/detection_campaigns, e com ~213 partições (faixa
-- 2024-01 → +36m, 3º run do CI) cada teste custava ~2,5s e o pacote mcp
-- estourou 25 min. Não cobre 2010 nem 2090 de propósito
-- (TestDetections_HasPartitionFor exige esses meses vazios).
-- Idempotente (to_regclass pula o que já existe). Mesmos nomes da 0048.
DO $$
DECLARE
    parent   text;
    prefix   text;
    m        date;
    inicio   date := LEAST(DATE '2026-01-01',
                           (date_trunc('month', CURRENT_DATE) - INTERVAL '3 months')::date);
    fim      date := (date_trunc('month', CURRENT_DATE) + INTERVAL '1 month')::date;
    partname text;
BEGIN
    FOREACH parent IN ARRAY ARRAY['detections', 'stream_health_events', 'detection_campaigns'] LOOP
        prefix := CASE parent WHEN 'stream_health_events' THEN 'stream_health' ELSE parent END;
        m := inicio;
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
