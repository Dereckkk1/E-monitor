-- Uso único do token de confirmação do MCP (spec §5.2, plano Fatia 3 F3-5).
-- O token é HMAC sem estado; esta tabela guarda o SHA-256 dos já usados até
-- a expiração. Consumir = INSERT ON CONFLICT DO NOTHING; falha de negócio
-- apaga a linha (o token volta a valer).
CREATE TABLE mcp_confirmacoes_usadas (
    hash      BYTEA       PRIMARY KEY CHECK (octet_length(hash) = 32),
    usado_em  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expira_em TIMESTAMPTZ NOT NULL
);
CREATE INDEX mcp_confirmacoes_usadas_expira ON mcp_confirmacoes_usadas (expira_em);
