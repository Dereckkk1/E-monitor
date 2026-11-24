#!/usr/bin/env bash
# Roda `go test` de cada pacote de workers/ num banco PRÓPRIO, clonado de um
# banco-modelo já migrado (CREATE DATABASE ... TEMPLATE).
#
# Por quê (1º run do CI, 2026-11-24, 894 falhas, nenhuma de produto):
#   - com um banco só, os pacotes deixam linhas pro próximo: depois de alguns
#     pacotes há >10 clients e o dbtest.GuardOrSkip (que existe pra não rodar
#     teste em banco REAL) recusa os pacotes seguintes — 662 falhas;
#   - o mesmo compartilhamento é o que obrigava `-p 1` (deadlock/contagem errada).
#   Banco por pacote = cada pacote vê o banco recém-migrado que os testes supõem,
#   e dá pra rodar pacotes em paralelo.
#
# Uso (de qualquer lugar):
#   PG_URL_BASE=postgres://radiocheck:radiocheck@localhost:5432 \
#   TEMPLATE_DB=radiocheck_tmpl PARALELO=3 COVER_OUT=workers/coverage.out \
#     bash scripts/ci/go-test-isolado.sh
#
# Pré-requisito: o TEMPLATE_DB já migrado + scripts/ci/test-db-partitions.sql,
# e SEM conexão aberta nele (o Postgres não clona banco em uso).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
: "${PG_URL_BASE:?ex.: postgres://radiocheck:radiocheck@localhost:5432}"
TEMPLATE_DB="${TEMPLATE_DB:-radiocheck_tmpl}"
PARALELO="${PARALELO:-2}"
COVER_OUT="${COVER_OUT:-$ROOT/workers/coverage.out}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

psql_admin() {   # psql nativo se houver; senão o da imagem postgres (rede do host)
  if command -v psql >/dev/null 2>&1; then
    psql "$PG_URL_BASE/postgres" -v ON_ERROR_STOP=1 -qAt "$@"
  else
    docker run --rm -i --network host postgres:16-alpine \
      psql "$PG_URL_BASE/postgres" -v ON_ERROR_STOP=1 -qAt "$@"
  fi
}

cd "$ROOT/workers"
mapfile -t PKGS < <(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | sed '/^$/d')
echo "pacotes com teste: ${#PKGS[@]} · paralelo: $PARALELO · modelo: $TEMPLATE_DB"

# 1) um banco por pacote (sequencial: CREATE DATABASE ... TEMPLATE é rápido e
#    criar em série evita disputa pelo modelo)
for i in "${!PKGS[@]}"; do
  db="$(printf 't_%03d' "$i")"
  psql_admin -c "DROP DATABASE IF EXISTS $db" -c "CREATE DATABASE $db TEMPLATE $TEMPLATE_DB"
  printf '%s\t%s\n' "$db" "${PKGS[$i]}" >> "$WORK/plano.tsv"
done

# 2) testes em paralelo, cada um no seu banco
roda_um() {
  local db="$1" pkg="$2" ini fim rc=0
  ini=$(date +%s)
  TEST_DATABASE_URL="$PG_URL_BASE/$db?sslmode=disable" \
    go test -count=1 -covermode=atomic -coverprofile="$WORK/$db.cov" "$pkg" > "$WORK/$db.log" 2>&1 || rc=$?
  fim=$(date +%s)
  if [ "$rc" -eq 0 ]; then
    printf 'ok    %-50s %4ss\n' "$pkg" "$((fim - ini))"
  else
    printf 'FALHA %-50s %4ss\n' "$pkg" "$((fim - ini))"
    echo "$db" >> "$WORK/falhas"
  fi
}
export -f roda_um
export PG_URL_BASE WORK
tr '\t' ' ' < "$WORK/plano.tsv" | xargs -P "$PARALELO" -n 2 bash -c 'roda_um "$0" "$1"'

# 3) cobertura única (mesmo formato de um `go test -coverprofile` só)
echo "mode: atomic" > "$COVER_OUT"
for f in "$WORK"/*.cov; do [ -f "$f" ] && tail -n +2 "$f" >> "$COVER_OUT"; done

# 4) log completo só de quem falhou
if [ -s "$WORK/falhas" ]; then
  while read -r db; do
    pkg="$(awk -F'\t' -v d="$db" '$1==d {print $2}' "$WORK/plano.tsv")"
    echo; echo "════════ $pkg ($db) ════════"; cat "$WORK/$db.log"
  done < "$WORK/falhas"
  echo; echo "✗ $(wc -l < "$WORK/falhas") pacote(s) com falha"
  exit 1
fi
echo "✓ todos os ${#PKGS[@]} pacotes passaram"
