#!/usr/bin/env bash
# Deploy do Radiocheck na VM de producao.
# Uso: ./scripts/deploy.sh
#
# Faz: git pull, rebuild das imagens, restart dos containers, valida saude.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_BASE="-f $REPO_ROOT/infra/docker/docker-compose.yml"
COMPOSE_OVERRIDE="-f $REPO_ROOT/infra/docker/docker-compose.override.yml"
ENV_FILE="--env-file $REPO_ROOT/infra/docker/.env"
COMPOSE="docker compose $COMPOSE_BASE $COMPOSE_OVERRIDE $ENV_FILE"

step() { printf '\033[1;36m==> %s\033[0m\n' "$1"; }
ok()   { printf '    \033[1;32m%s\033[0m\n' "$1"; }
err()  { printf '\033[1;31mERRO: %s\033[0m\n' "$1" >&2; exit 1; }

# ── pré-checks ──────────────────────────────────────────────────────────────

step "verificando docker"
docker version --format '{{.Server.Version}}' >/dev/null 2>&1 || err "Docker não está rodando."

step "verificando .env"
[ -f "$REPO_ROOT/infra/docker/.env" ] || err "infra/docker/.env não encontrado. Crie a partir de .env.example."

[ -f "$REPO_ROOT/infra/docker/docker-compose.override.yml" ] || \
  err "docker-compose.override.yml não encontrado. Veja docs/deploy.md Bloco J."

# ── atualizar código ─────────────────────────────────────────────────────────

step "git pull"
git -C "$REPO_ROOT" pull --ff-only

# ── rebuild e restart ────────────────────────────────────────────────────────

step "build das imagens"
$COMPOSE build --pull

step "subindo containers"
$COMPOSE up -d

# ── aguardar migrations ───────────────────────────────────────────────────────

step "aguardando migrations"
for i in $(seq 1 30); do
  status=$($COMPOSE ps migrate --format json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[0].get('State',''))" 2>/dev/null || echo "")
  if [ "$status" = "exited" ]; then
    code=$($COMPOSE ps migrate --format json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[0].get('ExitCode','1'))" 2>/dev/null || echo "1")
    if [ "$code" = "0" ]; then
      ok "migrations aplicadas"
      break
    else
      err "migrations falharam. Veja: docker compose logs migrate"
    fi
  fi
  sleep 2
done

# ── health check ─────────────────────────────────────────────────────────────

step "aguardando API"
ready=0
for i in $(seq 1 30); do
  if curl -sf -m 3 "http://localhost:8080/v1/internal/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done

if [ "$ready" = "1" ]; then
  ok "API respondendo em http://localhost:8080"
else
  err "API não respondeu em 60s. Veja: $COMPOSE logs api"
fi

# ── status final ──────────────────────────────────────────────────────────────

step "status dos containers"
$COMPOSE ps

echo
ok "deploy concluído."
