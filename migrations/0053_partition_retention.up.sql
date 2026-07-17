-- Retenção de partições de stream_health_events (auditoria 2026-07-17):
-- ensure_month_partitions (0048) cria mas nunca dropa — telemetria de health
-- cresce para sempre. drop_old_health_partitions remove partições cujo mês
-- terminou há mais de retention_months. SÓ stream_health_events:
-- detections/detection_campaigns são dado de negócio (veiculações), nunca entram.

CREATE OR REPLACE FUNCTION drop_old_health_partitions(retention_months int DEFAULT 6)
RETURNS int
LANGUAGE plpgsql
AS $$
DECLARE
    cutoff  date := date_trunc('month', CURRENT_DATE)::date
                    - (retention_months || ' months')::interval;
    part    record;
    dropped int := 0;
BEGIN
    FOR part IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'stream_health_events'
          AND c.relname ~ '^stream_health_[0-9]{4}_[0-9]{2}$'
          AND to_date(right(c.relname, 7), 'YYYY_MM') < cutoff
    LOOP
        EXECUTE format('DROP TABLE %I', part.relname);
        dropped := dropped + 1;
    END LOOP;
    RETURN dropped;
END;
$$;
