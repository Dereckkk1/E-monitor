#!/usr/bin/env bash
# check-fingerprint-freshness.sh — gate de verificação pós-migração de fingerprint.
#
# Depois de QUALQUER mudança na matemática de peaks/hash (generator.py +
# pkg/audio/*.go), todo o catálogo precisa ser re-fingerprintado. Este script
# verifica que nenhum material 'ready' ficou com fingerprint anterior à data
# de corte — o gap que cegou 32 materiais de 08 a 12/06 (incidente
# 2026-06-12-detection-recall-gaps).
#
# Uso (na VM, na raiz do repo):
#   ./scripts/check-fingerprint-freshness.sh <data-de-corte>
#   ./scripts/check-fingerprint-freshness.sh 2026-06-08
#
# Exit 0 = catálogo são. Exit 1 = materiais stale (lista no stdout) — NÃO
# considere a migração concluída.
set -euo pipefail

CUTOFF="${1:?uso: $0 <data-de-corte YYYY-MM-DD> (data do deploy da mudança de hash)}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose -f $REPO_ROOT/infra/docker/docker-compose.yml"
# Em prod o override (bind mounts) sempre existe; em dev pode não existir.
if [ -f "$REPO_ROOT/infra/docker/docker-compose.override.yml" ]; then
  COMPOSE="$COMPOSE -f $REPO_ROOT/infra/docker/docker-compose.override.yml"
fi
COMPOSE="$COMPOSE --env-file $REPO_ROOT/infra/docker/.env"

# shellcheck disable=SC2086
STALE=$($COMPOSE exec -T postgres psql -U "${POSTGRES_USER:-radiocheck}" -d "${POSTGRES_DB:-radiocheck}" -tA -c "
SELECT count(*) FROM materials
WHERE fingerprint_status = 'ready' AND fingerprint_generated_at < '${CUTOFF}'::date;")

# shellcheck disable=SC2086
NOT_READY=$($COMPOSE exec -T postgres psql -U "${POSTGRES_USER:-radiocheck}" -d "${POSTGRES_DB:-radiocheck}" -tA -c "
SELECT count(*) FROM materials WHERE fingerprint_status <> 'ready';")

echo "corte: ${CUTOFF} | stale (ready com fingerprint antigo): ${STALE} | não-prontos: ${NOT_READY}"

if [ "${STALE}" != "0" ]; then
  echo ""
  echo "✗ MIGRAÇÃO INCOMPLETA — materiais 'ready' com fingerprint anterior ao corte:"
  # shellcheck disable=SC2086
  $COMPOSE exec -T postgres psql -U "${POSTGRES_USER:-radiocheck}" -d "${POSTGRES_DB:-radiocheck}" -c "
SELECT short_id, title, fingerprint_generated_at
FROM materials
WHERE fingerprint_status = 'ready' AND fingerprint_generated_at < '${CUTOFF}'::date
ORDER BY fingerprint_generated_at;"
  echo "Re-dispare o re-fingerprint (passo 2 de docs/operations/refingerprint-density-migration.md)"
  echo "e rode este check de novo até zerar."
  exit 1
fi

if [ "${NOT_READY}" != "0" ]; then
  echo "⚠ Há ${NOT_READY} materiais não-prontos (pending/generating/failed) — o reconciler"
  echo "  da fila re-tenta sozinho; se persistir >30min, ver runbook FingerprintStuck."
fi

echo "✓ Catálogo consistente com o corte ${CUTOFF}."
