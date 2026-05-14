#!/usr/bin/env bash
# ----------------------------------------------------------------------------
# bootstrap-migrations.sh
#
# Popula a tabela `schema_migrations` (usada pelo golang-migrate) num DB que
# JÁ tem todas as migrations 0001-0014 aplicadas manualmente. Idempotente:
# pode rodar várias vezes sem efeito colateral.
#
# Rodar UMA ÚNICA VEZ, antes do primeiro `docker compose up -d` que ative
# o service `migrate`. Em DBs novos (volume `pgdata` vazio) NÃO é necessário —
# o service `migrate` vai aplicar 0001..0014 do zero.
#
# Pré-requisito: docker compose up -d postgres (postgres healthy).
# ----------------------------------------------------------------------------
set -euo pipefail

# Move pra raiz do repo (script vive em scripts/)
cd "$(dirname "$0")/.."

ENV_FILE="infra/docker/.env"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERRO: $ENV_FILE não encontrado. Crie-o a partir de infra/docker/.env.example." >&2
  exit 1
fi

PGUSER=$(grep -E '^POSTGRES_USER=' "$ENV_FILE" | cut -d= -f2-)
PGDB=$(grep -E '^POSTGRES_DB=' "$ENV_FILE" | cut -d= -f2-)

if [[ -z "$PGUSER" || -z "$PGDB" ]]; then
  echo "ERRO: POSTGRES_USER ou POSTGRES_DB não definidos em $ENV_FILE." >&2
  exit 1
fi

# Versão atual aplicada manualmente. Atualizar este valor APENAS quando o
# bootstrap for re-rodado contra um DB que recebeu novas migrations fora do
# runner (caso raro).
CURRENT_VERSION="${BOOTSTRAP_VERSION:-14}"

echo "→ Bootstrapping schema_migrations: versão=$CURRENT_VERSION, dirty=false"
echo "  (DB=$PGDB user=$PGUSER)"

docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  psql -U "$PGUSER" -d "$PGDB" -v ON_ERROR_STOP=1 -v "ver=$CURRENT_VERSION" <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint NOT NULL PRIMARY KEY,
  dirty   boolean NOT NULL
);

INSERT INTO schema_migrations (version, dirty)
VALUES (:ver, false)
ON CONFLICT (version) DO NOTHING;

SELECT version, dirty FROM schema_migrations ORDER BY version;
SQL

echo "✓ Bootstrap completo. O service 'migrate' agora pode rodar sem reaplicar 0001..$CURRENT_VERSION."
