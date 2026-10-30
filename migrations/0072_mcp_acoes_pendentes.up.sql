-- Pedidos de escrita `critico` do MCP à espera de aprovação humana (spec §5.2,
-- plano Fatia 4 F4-2/F4-3). A tool não executa: grava os args canônicos e o
-- preview; o dono aprova em /pendentes, logado, e só aí o executor roda.
-- Estados: pendente → aprovada → executada; recusa de negócio volta a
-- pendente (com erro); exceção = falhou (final); recusada e expirada finais.
-- `tentativa` conta as aprovações: só a vigente fecha o pedido. Sem poda.
CREATE TABLE mcp_acoes_pendentes (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id     UUID        REFERENCES mcp_tokens(id) ON DELETE SET NULL,
    tool         TEXT        NOT NULL CHECK (char_length(tool) BETWEEN 1 AND 100),
    args         JSONB       NOT NULL,
    args_hash    BYTEA       NOT NULL CHECK (octet_length(args_hash) = 32),
    motivo       TEXT        NOT NULL DEFAULT '',
    preview      JSONB       NOT NULL,
    estado       TEXT        NOT NULL DEFAULT 'pendente'
                 CHECK (estado IN ('pendente','aprovada','executada','falhou','recusada','expirada')),
    tentativa    INT         NOT NULL DEFAULT 0,
    criada_em    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expira_em    TIMESTAMPTZ NOT NULL,
    aprovada_em  TIMESTAMPTZ,
    executada_em TIMESTAMPTZ,
    recusada_em  TIMESTAMPTZ,
    resultado    JSONB,
    erro         TEXT        CHECK (erro IS NULL OR char_length(erro) <= 2000)
);
CREATE INDEX mcp_acoes_pendentes_do_dono ON mcp_acoes_pendentes (user_id, criada_em DESC, id DESC);
CREATE INDEX mcp_acoes_pendentes_vencendo ON mcp_acoes_pendentes (expira_em) WHERE estado = 'pendente';
CREATE INDEX mcp_acoes_pendentes_presas ON mcp_acoes_pendentes (aprovada_em) WHERE estado = 'aprovada';
-- F4-3: o mesmo pedido aberto não duplica.
CREATE UNIQUE INDEX mcp_acoes_pendentes_aberta_unica ON mcp_acoes_pendentes (user_id, args_hash)
    WHERE estado IN ('pendente', 'aprovada');
