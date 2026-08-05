-- Migration 0062 — carteira de clientes por usuário (agências).
--
-- Contexto: users.client_id é 1:1 e agências precisam de um login que enxergue
-- vários clientes. user_clients passa a ser a carteira; users.client_id vira o
-- "cliente principal" e continua NOT NULL pra viewer (CHECK users_client_role_
-- consistency da 0027 permanece intacta).
--
-- SAFETY (convenção 0027/0028/0030/0047/0050 — migrations que tocam `users`):
--
-- 1. lock_timeout/statement_timeout: CREATE TRIGGER em `users` pede um lock
--    que conflita com DML na tabela. No deploy (scripts/deploy.sh) a API
--    ANTIGA continua servindo tráfego real — login grava
--    users.last_login_at — enquanto o serviço `migrate` roda. Sem timeout,
--    o CREATE TRIGGER entra na fila FIFO de locks do Postgres e prende atrás
--    de si todo read/write subsequente em `users`: vira outage ao vivo em vez
--    de um erro de migration. O shadow migration test (DB isolado, sem
--    tráfego concorrente) não pega esse cenário.
--
-- Doc (criado junto com a feature): docs/features/multi-client-user.md

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE IF NOT EXISTS user_clients (
    -- CASCADE: o vínculo não tem vida própria fora do usuário.
    user_id    UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    -- RESTRICT espelha users_client_id_fkey: é o que sustenta o 409
    -- "client_has_dependents" ao tentar deletar cliente com usuário vinculado.
    client_id  UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, client_id)
);

CREATE INDEX IF NOT EXISTS idx_user_clients_client ON user_clients(client_id);

-- Backfill: cada usuário com client_id vira uma linha. Idempotente e sem risco
-- de colisão — a PK é (user_id, client_id) e cada usuário tem no máximo um
-- client_id hoje.
INSERT INTO user_clients (user_id, client_id)
SELECT id, client_id FROM users WHERE client_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- Invariante users.client_id ∈ user_clients.
--
-- O trigger só ADICIONA; remover vínculo é sempre explícito (users.Repo.
-- SetClients). Auto-corretivo de propósito: qualquer caminho que escreva
-- users.client_id (fluxo de boas-vindas, testes, reparo manual em prod) fica
-- consistente em vez de quebrar.
CREATE OR REPLACE FUNCTION sync_user_primary_client() RETURNS TRIGGER AS $$
BEGIN
    -- Guard redundante ao WHEN do trigger abaixo, de propósito: o WHEN é o
    -- fast path (evita a chamada PL/pgSQL quando não há o que sincronizar),
    -- este IF é a rede de segurança caso a função seja um dia anexada a um
    -- trigger sem o WHEN. Não "limpar" — é redundância deliberada.
    IF NEW.client_id IS NOT NULL THEN
        INSERT INTO user_clients (user_id, client_id)
        VALUES (NEW.id, NEW.client_id)
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL; -- AFTER trigger: valor de retorno é ignorado
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_user_primary_client ON users;
CREATE TRIGGER trg_sync_user_primary_client
    AFTER INSERT OR UPDATE OF client_id ON users
    FOR EACH ROW
    WHEN (NEW.client_id IS NOT NULL)
    EXECUTE FUNCTION sync_user_primary_client();

COMMIT;
