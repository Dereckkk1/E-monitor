-- Migration 0027 — ROLLBACK.
--
-- ⚠ DESTRUTIVO: este rollback DROPA colunas que podem conter dado real
--   (client_id, name, phone, is_active, deleted_at, last_login_at,
--    updated_at). Uma vez dropadas, o dado SOMEM. NÃO HÁ recuperação
--   sem backup.
--
-- SAFE em DEV pra resetar schema. **NUNCA em PROD** sem:
--   1. Backup verificado nos últimos 5 minutos.
--   2. Aprovação explícita do user.
--   3. Confirmação de que nenhum tráfego de produção depende das colunas
--      novas (admin/users API, scope filtering, etc.).
--
-- REFUSAL CHECK: o down ABORTA se qualquer user tem dado não-default nas
--   colunas a serem dropadas. Pra forçar (apenas em DEV, ciente do risco):
--
--     SET radiocheck.force_destructive_down = 'yes';
--     -- depois re-rodar a migração

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ═══ Refusal check ════════════════════════════════════════════════════
DO $$
DECLARE
  dirty_count int := 0;
  override    text;
BEGIN
  BEGIN
    override := current_setting('radiocheck.force_destructive_down', true);
  EXCEPTION WHEN OTHERS THEN
    override := NULL;
  END;

  IF override = 'yes' THEN
    RAISE NOTICE '[0027 DOWN] destructive override ativo — prosseguindo';
  ELSE
    -- Soma todas as evidências de "dado real" nas colunas a serem dropadas.
    SELECT count(*) INTO dirty_count FROM users
    WHERE client_id     IS NOT NULL
       OR phone         IS NOT NULL
       OR deleted_at    IS NOT NULL
       OR last_login_at IS NOT NULL
       OR is_active     = false
       OR name          <> '';

    IF dirty_count > 0 THEN
      RAISE EXCEPTION
        E'\n\n========================================\nMIGRATION 0027 DOWN ABORTED — DADO REAL DETECTADO\n========================================\n\n% user(s) têm dado não-default em colunas que este rollback DROPARIA permanentemente.\n\nPra proceder mesmo assim (DESTRUTIVO, IRREVERSÍVEL — apenas em dev):\n\n  SET radiocheck.force_destructive_down = ''yes'';\n  -- então re-rode esta migração\n\nPra investigar antes:\n\n  SELECT id, email, role, client_id, name, phone, is_active, deleted_at, last_login_at\n  FROM users\n  WHERE client_id IS NOT NULL OR phone IS NOT NULL OR deleted_at IS NOT NULL\n     OR last_login_at IS NOT NULL OR is_active = false OR name <> '''';\n\n========================================',
        dirty_count;
    END IF;
  END IF;
END $$;

-- ═══ Restaurar UNIQUE original em email — SAFE SWAP ───────────────────
-- Mesmo princípio do up: cria o constraint antigo ANTES de dropar o novo
-- índice. Se houver duplicatas case-insensitive (que o partial unique
-- permitia, ex: rows com deleted_at), o ADD CONSTRAINT falha — o DROP
-- não roda → não há janela sem proteção.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_email_key'
  ) THEN
    -- ATENÇÃO: pode falhar se LOWER(email) tem colisão entre rows
    -- (deletadas ou não). Resolver com:
    --   UPDATE users SET email = email || '.dup' WHERE id IN (...);
    ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
  END IF;
END $$;

-- ═══ Dropar artefatos do up ───────────────────────────────────────────

DROP INDEX  IF EXISTS idx_users_email_active;
DROP INDEX  IF EXISTS idx_users_active;
DROP INDEX  IF EXISTS idx_users_client_id;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_client_role_consistency;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_client_id_fkey;

ALTER TABLE users
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS last_login_at,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS is_active,
  DROP COLUMN IF EXISTS phone,
  DROP COLUMN IF EXISTS name,
  DROP COLUMN IF EXISTS client_id;

COMMIT;
