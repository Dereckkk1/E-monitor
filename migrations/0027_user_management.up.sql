-- Migration 0027 — extende `users` com client_id, soft delete e telemetria.
--
-- SAFETY DESIGN (lições do incidente 2026-05-12 + dev-data-loss 2026-05-17):
--
-- 1. Wrapped em BEGIN/COMMIT — qualquer falha rola tudo de volta.
-- 2. PRE-FLIGHT ASSERTIONS abortam ANTES de mudar schema se a data atual
--    violaria as novas constraints (viewer sem client_id; email duplicado
--    case-insensitive). Erros são descritivos com instruções de fix.
-- 3. Todo DDL é IDEMPOTENTE (IF NOT EXISTS + checks em pg_constraint) — pode
--    ser re-rodada sem efeito colateral em DB já parcialmente migrado.
-- 4. SAFE SWAP no email uniqueness: cria índice novo ANTES de dropar o
--    constraint antigo. Se o CREATE falhar, o DROP não roda — nunca há janela
--    sem proteção de unicidade.
-- 5. lock_timeout=5s + statement_timeout=60s: aborta cedo se outra sessão
--    segura lock conflitante, em vez de bloquear writes de prod por horas.
-- 6. updated_at backfill = created_at (não NOW()) — semântica de audit
--    honesta: rows existentes não foram "atualizadas" no momento da migração.
-- 7. Multi-step ADD COLUMN pra updated_at (nullable → backfill → NOT NULL +
--    DEFAULT) evita rewrite de tabela se PG < 11 (NOW() não é constante).

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ═══ Pre-flight assertions ════════════════════════════════════════════
-- Se qualquer assertion falhar, ROLLBACK automático e schema fica intacto.

-- Assertion 1: existing 'viewer' rows precisam ter client_id setado.
-- Antes da migração: client_id não existe → todos os viewers seriam NULL
-- após o ADD COLUMN → violariam o CHECK abaixo.
DO $$
DECLARE
  has_client_id_col boolean;
  bad_viewers       int;
BEGIN
  has_client_id_col := EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'users'
      AND column_name = 'client_id'
  );

  IF has_client_id_col THEN
    -- Re-run path: column already exists; check NULLs.
    SELECT count(*) INTO bad_viewers
    FROM users WHERE role = 'viewer' AND client_id IS NULL;
  ELSE
    -- First-run path: column will default to NULL → all viewers fail.
    SELECT count(*) INTO bad_viewers FROM users WHERE role = 'viewer';
  END IF;

  IF bad_viewers > 0 THEN
    RAISE EXCEPTION
      E'\n\n========================================\nMIGRATION 0027 ABORTED — VIEWERS WITHOUT CLIENT_ID\n========================================\n\n% user(s) com role=viewer % violariam o novo CHECK users_client_role_consistency.\n\nFix antes de re-tentar:\n  -- Opção A (descartar viewers órfãos):\n  DELETE FROM users WHERE role = ''viewer'' AND %L;\n\n  -- Opção B (promover a admin):\n  UPDATE users SET role = ''admin'' WHERE role = ''viewer'' AND %L;\n\n  -- Opção C (vincular a um cliente existente):\n  UPDATE users SET client_id = ''<UUID-do-cliente>'' WHERE role = ''viewer'' AND %L;\n\n========================================',
      bad_viewers,
      CASE WHEN has_client_id_col THEN 'com client_id NULL' ELSE 'existentes' END,
      CASE WHEN has_client_id_col THEN 'client_id IS NULL' ELSE 'TRUE' END,
      CASE WHEN has_client_id_col THEN 'client_id IS NULL' ELSE 'TRUE' END,
      CASE WHEN has_client_id_col THEN 'client_id IS NULL' ELSE 'TRUE' END;
  END IF;
END $$;

-- Assertion 2: nenhum par de emails case-insensitive entre rows não-deletadas.
-- O novo UNIQUE INDEX em LOWER(email) WHERE deleted_at IS NULL falharia
-- silenciosamente — pior, o DROP do users_email_key teria sido o último ato
-- "bem-sucedido" se não fosse o BEGIN/COMMIT envolvendo tudo.
DO $$
DECLARE
  has_deleted_at boolean;
  dup_count      int;
  dup_sample     text;
BEGIN
  has_deleted_at := EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'users'
      AND column_name = 'deleted_at'
  );

  IF has_deleted_at THEN
    SELECT count(*), string_agg(email_l, ', ' ORDER BY email_l)
      INTO dup_count, dup_sample
    FROM (
      SELECT LOWER(email) AS email_l FROM users
      WHERE deleted_at IS NULL
      GROUP BY LOWER(email)
      HAVING count(*) > 1
      LIMIT 10
    ) t;
  ELSE
    SELECT count(*), string_agg(email_l, ', ' ORDER BY email_l)
      INTO dup_count, dup_sample
    FROM (
      SELECT LOWER(email) AS email_l FROM users
      GROUP BY LOWER(email)
      HAVING count(*) > 1
      LIMIT 10
    ) t;
  END IF;

  IF dup_count > 0 THEN
    RAISE EXCEPTION
      E'\n\n========================================\nMIGRATION 0027 ABORTED — DUPLICATE EMAILS (CASE-INSENSITIVE)\n========================================\n\n% par(es) de emails diferindo apenas em maiúsculas/minúsculas (primeiras 10): %\n\nO novo UNIQUE INDEX em LOWER(email) não pode ser criado com duplicatas.\n\nFix antes de re-tentar:\n  -- Listar duplicatas:\n  SELECT LOWER(email), array_agg(email), array_agg(id)\n  FROM users\n  GROUP BY LOWER(email)\n  HAVING count(*) > 1;\n\n  -- Resolver caso-a-caso (mergeando, renomeando, ou deletando o duplicado).\n\n========================================',
      dup_count, dup_sample;
  END IF;
END $$;

-- ═══ Adicionar colunas (idempotente) ══════════════════════════════════

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS client_id     UUID,
  ADD COLUMN IF NOT EXISTS name          TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS phone         TEXT,
  ADD COLUMN IF NOT EXISTS is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS deleted_at    TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS updated_at    TIMESTAMPTZ;

-- Backfill updated_at = created_at pra rows existentes (audit honesto: elas
-- não foram "atualizadas" no momento da migração). Idempotente: só toca
-- rows onde updated_at é NULL (= recém-criada pela coluna).
UPDATE users SET updated_at = created_at WHERE updated_at IS NULL;

-- Promove updated_at a NOT NULL com default pra inserts futuros. Em PG 11+
-- isso é metadata-only; em PG < 11 evita rewrite porque NOW() não é
-- constante (DEFAULT é setado APÓS o NOT NULL).
ALTER TABLE users ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE users ALTER COLUMN updated_at SET DEFAULT NOW();

-- ═══ Foreign key (idempotente) ════════════════════════════════════════

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_client_id_fkey'
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_client_id_fkey
      FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE RESTRICT;
  END IF;
END $$;

-- ═══ CHECK constraint (idempotente) ═══════════════════════════════════

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_client_role_consistency'
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_client_role_consistency CHECK (
        (role = 'viewer'  AND client_id IS NOT NULL) OR
        (role IN ('admin','operator') AND client_id IS NULL)
      );
  END IF;
END $$;

-- ═══ Índices regulares (idempotente) ══════════════════════════════════

CREATE INDEX IF NOT EXISTS idx_users_client_id
  ON users(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_active
  ON users(is_active) WHERE deleted_at IS NULL;

-- ═══ Email uniqueness — SAFE SWAP ═════════════════════════════════════
-- ORDEM CRÍTICA: cria o novo índice partial UNIQUE FIRST. Só DEPOIS dropa
-- o constraint antigo. Se o CREATE falhar (duplicata não pega pelo pre-
-- flight check), o DROP não roda → tabela permanece com email_key original.
-- Nunca há janela sem proteção de unicidade.

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_active
  ON users(LOWER(email)) WHERE deleted_at IS NULL;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;

COMMIT;
