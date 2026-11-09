-- Reversão da 0072. Remove o trigger/função em `users` e as 3 tabelas do MCP
-- (credenciais e clientes OAuth: quem estiver conectado precisa reconectar).
--
-- lock_timeout: mesma proteção do up — DROP TRIGGER em `users` pede lock
-- conflitante com DML e não pode enfileirar tráfego real atrás de si.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP TRIGGER IF EXISTS users_revoga_mcp ON users;
DROP FUNCTION IF EXISTS mcp_revogar_credenciais_do_usuario();
DROP TABLE IF EXISTS mcp_oauth_codes;
DROP TABLE IF EXISTS mcp_tokens;
DROP TABLE IF EXISTS mcp_oauth_clients;

COMMIT;
