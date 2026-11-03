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
    -- Avisos da execução (feito.Ressalvas): o registro fiel do que a aprovação disse.
    ressalvas    TEXT[]      NOT NULL DEFAULT '{}',
    erro         TEXT        CHECK (erro IS NULL OR char_length(erro) <= 2000)
);
CREATE INDEX mcp_acoes_pendentes_do_dono ON mcp_acoes_pendentes (user_id, criada_em DESC, id DESC);
CREATE INDEX mcp_acoes_pendentes_vencendo ON mcp_acoes_pendentes (expira_em) WHERE estado = 'pendente';
CREATE INDEX mcp_acoes_pendentes_presas ON mcp_acoes_pendentes (aprovada_em) WHERE estado = 'aprovada';
-- F4-3: o mesmo pedido aberto não duplica.
CREATE UNIQUE INDEX mcp_acoes_pendentes_aberta_unica ON mcp_acoes_pendentes (user_id, args_hash)
    WHERE estado IN ('pendente', 'aprovada');

-- Revogar a credencial revoga os pedidos dela (revisão final da Fatia 4). O trigger users_revoga_mcp
-- (0070) mata os tokens e os códigos OAuth do usuário quando ele troca/redefine a senha, é desativado,
-- rebaixado de admin ou apagado; os pedidos `pendente` dele, porém, seguiriam aprováveis por até 24 h
-- com a conexão de IA que os fez já morta. Agora a mesma função fecha esses pedidos como `expirada`,
-- com o motivo em `erro` (o texto é o msgCredencialRevogada do pacote mcp; um teste confere que os dois
-- batem). Só `pendente`: a `aprovada` já está sendo executada por uma pessoa que clicou. Substitui a
-- função da 0070 por completo (CREATE OR REPLACE não toca o trigger nem a tabela `users`: sem lock);
-- a down desta migration devolve a versão da 0070.
CREATE OR REPLACE FUNCTION mcp_revogar_credenciais_do_usuario() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public
AS $$
BEGIN
    UPDATE mcp_tokens SET revoked_at = NOW() WHERE user_id = NEW.id AND revoked_at IS NULL;
    DELETE FROM mcp_oauth_codes WHERE user_id = NEW.id;
    UPDATE mcp_acoes_pendentes
       SET estado = 'expirada',
           erro = 'A conexão de IA que fez este pedido foi revogada; peça de novo por uma conexão válida.'
     WHERE user_id = NEW.id AND estado = 'pendente';
    RETURN NULL;
END;
$$;
