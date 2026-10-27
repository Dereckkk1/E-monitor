-- MCP do E-monitor — fundação (spec docs/superpowers/specs/2026-10-27-mcp-server-design.md §4).
-- Só CREATE TABLE: nenhuma linha existente é tocada, então o shadow_migration_test
-- do deploy.sh não tem o que colidir (regra 4.8).

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
