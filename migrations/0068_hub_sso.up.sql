-- Migration 0068 — coluna de ligação com o E-Hub (Central de Clientes).
--
-- Contexto: o E-Hub (RFC-001) é o portal único de clientes da E-mídias. Quem
-- entra por lá abre o E-monitor com um clique, já logado. `hub_id` é a ÚNICA
-- coluna que liga os dois sistemas: presente = registro "vinculado", cuja
-- identidade (nome, e-mail, ativo/inativo) passa a ser do hub. O que continua
-- sendo daqui é a autorização interna — `role` e escopo de cliente —, que o hub
-- nunca toca (decisão D9 do RFC).
--
-- Ausente = registro local de sempre. O login local continua funcionando
-- exatamente como hoje, para sempre (decisão D5 — coexistência permanente).
--
-- SAFETY, e por que esta migration NÃO cai na regra 4.8 do CLAUDE.md:
--
-- A 4.8 existe porque `CREATE UNIQUE INDEX` e `ADD CONSTRAINT` podem passar num
-- DB local vazio e colidir em prod, onde há dado. Aqui não há esse risco, e o
-- motivo é estrutural, não otimismo: as colunas são criadas NESTA migration, e
-- os índices são PARCIAIS (`WHERE hub_id IS NOT NULL`). Toda linha existente
-- nasce com `hub_id` NULL e portanto fica FORA do índice — ele é literalmente
-- vazio no instante em que é criado, com 0 ou 10 milhões de linhas na tabela.
-- Não existe conjunto de dados de produção capaz de fazer isto falhar.
--
-- ADD COLUMN nullable sem DEFAULT é rewrite-free no PG 11+; o lock
-- AccessExclusive dura instantes. O lock_timeout evita que a DDL entre na fila
-- FIFO e prenda leitura de `users`/`clients` atrás de si durante o deploy,
-- quando a API antiga ainda serve tráfego.
--
-- Doc: docs/features/hub-sso.md

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE users
    -- ObjectId do usuário no hub (24 chars hex). TEXT e não UUID: o hub é
    -- MongoDB, e o id dele não é um UUID.
    ADD COLUMN IF NOT EXISTS hub_id TEXT;

ALTER TABLE clients
    -- ObjectId do cliente no hub. É o que permite resolver, no primeiro SSO de
    -- um usuário `level: client`, QUAL tenant local é o dele. Sem esta coluna
    -- preenchida, o SSO daquele cliente recusa com `client_not_provisioned`
    -- em vez de adivinhar.
    ADD COLUMN IF NOT EXISTS hub_id TEXT;

-- Parciais de propósito (ver SAFETY acima): sem o WHERE, o índice trataria
-- todos os NULLs como valores e o segundo registro local sem hub_id colidiria
-- com o primeiro.
CREATE UNIQUE INDEX IF NOT EXISTS users_hub_id_key
    ON users (hub_id) WHERE hub_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS clients_hub_id_key
    ON clients (hub_id) WHERE hub_id IS NOT NULL;

COMMIT;
