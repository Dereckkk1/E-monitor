-- Adiciona campos de gerenciamento de usuários:
--   client_id     vínculo N:1 a clientes (NOT NULL pra viewer; NULL pra admin/operator)
--   name, phone   dados pessoais
--   is_active     desativar reversível (login bloqueado)
--   deleted_at    soft delete definitivo
--   last_login_at telemetria de uso (preenchido em login bem-sucedido)
--   updated_at    audit
ALTER TABLE users
  ADD COLUMN client_id     UUID REFERENCES clients(id) ON DELETE RESTRICT,
  ADD COLUMN name          TEXT NOT NULL DEFAULT '',
  ADD COLUMN phone         TEXT,
  ADD COLUMN is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN deleted_at    TIMESTAMPTZ,
  ADD COLUMN last_login_at TIMESTAMPTZ,
  ADD COLUMN updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Cliente (viewer) DEVE ter client_id; admin/operator NÃO podem ter.
ALTER TABLE users
  ADD CONSTRAINT users_client_role_consistency CHECK (
    (role = 'viewer'  AND client_id IS NOT NULL) OR
    (role IN ('admin','operator') AND client_id IS NULL)
  );

CREATE INDEX idx_users_client_id ON users(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX idx_users_active    ON users(is_active) WHERE deleted_at IS NULL;

-- UNIQUE parcial substitui o UNIQUE original em email.
-- Permite reusar email após exclusão (a linha antiga sai do índice porque
-- deleted_at deixou de ser NULL).
ALTER TABLE users DROP CONSTRAINT users_email_key;
CREATE UNIQUE INDEX idx_users_email_active
  ON users(LOWER(email)) WHERE deleted_at IS NULL;
