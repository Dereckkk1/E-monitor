BEGIN;

-- Reverte indexes/constraints novos
DROP INDEX IF EXISTS idx_users_email_active;
DROP INDEX IF EXISTS idx_users_active;
DROP INDEX IF EXISTS idx_users_client_id;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_client_role_consistency;

-- Restaura UNIQUE original em email (sem case-fold; pode falhar se houver
-- emails duplicados case-insensitive — verificar antes de rodar down em prod)
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);

-- Drop colunas
ALTER TABLE users
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS last_login_at,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS is_active,
  DROP COLUMN IF EXISTS phone,
  DROP COLUMN IF EXISTS name,
  DROP COLUMN IF EXISTS client_id;

COMMIT;
