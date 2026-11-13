-- Reversão da 0070.
--
-- Perde-se o username de todo mundo que tinha um; o e-mail e a senha de todos
-- seguem intactos, então ninguém fica sem acesso — só deixa de entrar pelo
-- username.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP INDEX IF EXISTS users_username_active;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_format;
ALTER TABLE users DROP COLUMN IF EXISTS username;

COMMIT;
