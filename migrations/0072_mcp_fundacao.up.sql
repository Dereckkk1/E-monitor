-- MCP do E-monitor — fundação (spec docs/superpowers/specs/2026-10-27-mcp-server-design.md §4).
-- Cria 3 tabelas novas, uma função e um trigger em `users`. Nenhuma linha
-- existente é tocada, então o shadow_migration_test do deploy.sh não tem o que
-- colidir (regra 4.8).
--
-- SAFETY (convenção 0062/0068 — migrations que tocam `users`):
-- lock_timeout/statement_timeout: CREATE TRIGGER em `users` pede um lock que
-- conflita com DML na tabela. No deploy a API ANTIGA continua servindo login
-- (segurando ROW EXCLUSIVE em users via last_login_at) enquanto o `migrate`
-- roda; sem timeout o CREATE TRIGGER entra na fila FIFO de locks e prende atrás
-- de si toda escrita em `users`. Com o timeout, a migration falha rápido em vez
-- de virar outage ao vivo. RECUPERAÇÃO: ao estourar o lock_timeout a migration
-- falha e o golang-migrate deixa a versão 72 `dirty`; o deploy NÃO se repete
-- sozinho. Rode `UPDATE schema_migrations SET version=71, dirty=false` e faça o
-- redeploy (mesma convenção de 0062/0068).

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Cliente OAuth registrado por DCR (RFC 7591). client_secret_hash NULL = cliente
-- público (PKCE obrigatório); preenchido = confidencial (Gemini Enterprise).
CREATE TABLE mcp_oauth_clients (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id          TEXT NOT NULL UNIQUE,
    client_secret_hash TEXT,
    nome               TEXT NOT NULL,
    redirect_uris      TEXT[] NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- NULL até o primeiro /token: a faxina do registro apaga quem nunca chegou lá.
    first_token_at     TIMESTAMPTZ
);

-- Token pessoal e tokens OAuth na mesma tabela (contrato §3.1). Só o hash
-- SHA-256(JWT_SECRET:token) é guardado. expires_at é NOT NULL de propósito:
-- o hub aceitava documento sem validade (NaN <= now é false) — aqui não compila.
CREATE TABLE mcp_tokens (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tipo         TEXT NOT NULL CHECK (tipo IN ('pessoal', 'oauth_access', 'oauth_refresh')),
    token_hash   TEXT NOT NULL UNIQUE,
    nome         TEXT NOT NULL,
    client_id    TEXT REFERENCES mcp_oauth_clients(client_id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX mcp_tokens_user_vivos ON mcp_tokens (user_id) WHERE revoked_at IS NULL;

-- Código de autorização: uso único (DELETE ... RETURNING), 10 minutos.
CREATE TABLE mcp_oauth_codes (
    code_hash      TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL REFERENCES mcp_oauth_clients(client_id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Revogação automática (spec §4.1): trocar/redefinir senha, desativar, rebaixar de
-- admin ou apagar o usuário mata toda credencial do MCP e todo código OAuth pendente.
-- Trigger, e não código no repo, para cobrir TODO caminho que mexe em users (tela,
-- sync do hub, bloqueio do /admin/monitoring) e para que reativar alguém não
-- ressuscite tokens antigos.
CREATE FUNCTION mcp_revogar_credenciais_do_usuario() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public
AS $$
BEGIN
    UPDATE mcp_tokens SET revoked_at = NOW() WHERE user_id = NEW.id AND revoked_at IS NULL;
    DELETE FROM mcp_oauth_codes WHERE user_id = NEW.id;
    RETURN NULL;
END;
$$;

CREATE TRIGGER users_revoga_mcp
    AFTER UPDATE OF password_hash, is_active, role, deleted_at ON users
    FOR EACH ROW
    WHEN (NEW.password_hash IS DISTINCT FROM OLD.password_hash
       OR (OLD.is_active AND NOT NEW.is_active)
       OR (OLD.role = 'admin' AND NEW.role IS DISTINCT FROM 'admin')
       OR (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL))
    EXECUTE FUNCTION mcp_revogar_credenciais_do_usuario();

COMMIT;
