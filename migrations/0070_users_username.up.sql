-- Migration 0070 — nome de usuário opcional, como segunda forma de login.
--
-- Contexto: o login sempre foi por e-mail. Agora o admin pode dar a uma conta
-- um `username` (opcional na criação, editável só pelo admin) e a pessoa entra
-- com ele OU com o e-mail. O e-mail continua obrigatório e funcionando igual.
--
-- O formato exclui `@` de propósito: é o que deixa o login sem ambiguidade —
-- identificador com `@` é e-mail, sem `@` é username. Minúsculas só, porque o
-- índice compara o valor cru; quem normaliza (trim + lower) é a aplicação
-- (users.NormalizeUsername), e o CHECK garante que nada fora dela entra.
--
-- SAFETY, e por que esta migration NÃO cai na regra 4.8 do CLAUDE.md (mesmo
-- raciocínio da 0068): a coluna nasce NESTA migration, toda linha existente fica
-- com `username` NULL. O CHECK aceita NULL e o índice é PARCIAL
-- (`WHERE username IS NOT NULL`), então ele é literalmente vazio no instante em
-- que é criado, com qualquer volume de dados. Não há dado de prod que faça isto
-- falhar.
--
-- ADD COLUMN nullable sem DEFAULT é rewrite-free no PG 11+. O ADD CONSTRAINT
-- CHECK varre a tabela pra validar, mas `users` tem centenas de linhas, não
-- milhões. lock_timeout pelo mesmo motivo da 0068: a DDL não pode prender a
-- leitura de `users` atrás de si enquanto a API antiga ainda serve tráfego.
--
-- Doc: docs/features/user-management.md §Nome de usuário

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_format;
ALTER TABLE users ADD CONSTRAINT users_username_format
    CHECK (username IS NULL OR username ~ '^[a-z0-9._-]{3,32}$');

-- Unicidade só entre contas não-deletadas, igual ao e-mail (0027): excluir uma
-- conta libera o username para outra pessoa.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_active
    ON users (username) WHERE username IS NOT NULL AND deleted_at IS NULL;

COMMIT;
