-- Rollback for 0012_webhooks_complete.
DROP INDEX IF EXISTS idx_webhook_deliveries_status;
DROP INDEX IF EXISTS idx_webhook_deliveries_client_created;
DROP INDEX IF EXISTS idx_webhook_deliveries_pending;
DROP TABLE IF EXISTS webhook_deliveries;

ALTER TABLE clients DROP COLUMN IF EXISTS webhook_events;
ALTER TABLE clients DROP COLUMN IF EXISTS webhook_enabled;
