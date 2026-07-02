-- Remove a função de manutenção. NÃO dropa as partições criadas por ela — elas
-- guardam dado (detections/detection_campaigns/stream_health_events).
DROP FUNCTION IF EXISTS ensure_month_partitions(int);
