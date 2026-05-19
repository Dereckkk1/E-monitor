-- Reverte 0030_admin_monitoring.up.sql. Drop em ordem reversa de dependência.
-- Nenhuma das três tabelas é referenciada por FKs, então CASCADE não é
-- necessário.

BEGIN;

DROP TABLE IF EXISTS web_vitals;
DROP TABLE IF EXISTS blocked_ips;
DROP TABLE IF EXISTS system_metrics;

COMMIT;
