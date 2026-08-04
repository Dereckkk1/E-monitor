-- Reversão da 0062. users.client_id (o principal) sobrevive intacto, então
-- nenhum vínculo primário é perdido — só os secundários, que são exatamente
-- o que a feature adicionou.
--
-- lock_timeout/statement_timeout: DROP TRIGGER em `users` pede o mesmo tipo
-- de lock conflitante que o CREATE TRIGGER do up (ver comentário lá) —
-- mesma proteção contra enfileirar tráfego real de `users` atrás da DDL.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP TRIGGER IF EXISTS trg_sync_user_primary_client ON users;
DROP FUNCTION IF EXISTS sync_user_primary_client();
DROP INDEX IF EXISTS idx_user_clients_client;
DROP TABLE IF EXISTS user_clients;

COMMIT;
