#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR=${BACKUP_DIR:-/backup}
LATEST=$(ls -t "$BACKUP_DIR"/radiocheck_*.sql.gz 2>/dev/null | head -1)
if [ -z "$LATEST" ]; then echo "ERRO: nenhum backup encontrado em $BACKUP_DIR"; exit 1; fi

echo "Testando restore de: $LATEST"
TEST_CONTAINER="radiocheck_restore_test_$$"
trap 'docker rm -f "$TEST_CONTAINER" 2>/dev/null || true' EXIT

docker run --name "$TEST_CONTAINER" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=radiocheck_test \
  -d postgres:16-alpine

sleep 5

zcat "$LATEST" | docker exec -i "$TEST_CONTAINER" \
  psql -U postgres -d radiocheck_test -q

# Verificar integridade mínima
COUNT=$(docker exec "$TEST_CONTAINER" psql -U postgres -d radiocheck_test -t -c \
  "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'")

docker rm -f "$TEST_CONTAINER"

if [ "$COUNT" -lt 5 ]; then
  echo "FALHA: apenas $COUNT tabelas encontradas após restore"
  exit 1
fi

echo "SUCESSO: restore testado — $COUNT tabelas públicas restauradas de $LATEST"
