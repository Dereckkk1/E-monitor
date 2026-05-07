DROP TABLE IF EXISTS webhook_failures;
ALTER TABLE clients DROP COLUMN IF EXISTS webhook_url;
ALTER TABLE clients DROP COLUMN IF EXISTS webhook_secret;
