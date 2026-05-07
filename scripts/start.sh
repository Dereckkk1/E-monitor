#!/usr/bin/env bash
# Sobe o stack do Radiocheck (Docker + frontend) numa unica invocacao.
# Uso: ./scripts/start.sh
#
# - Containers ficam rodando em background quando o script termina.
# - Ctrl+C para apenas o frontend; pare os containers com `docker compose ... down`.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/infra/docker/docker-compose.yml"
ENV_EXAMPLE="$REPO_ROOT/infra/docker/.env.example"
ENV_FILE="$REPO_ROOT/infra/docker/.env"
FRONTEND_DIR="$REPO_ROOT/frontend"

step() { printf '\033[1;36m==> %s\033[0m\n' "$1"; }
warn() { printf '\033[1;33m%s\033[0m\n' "$1"; }
err()  { printf '\033[1;31m%s\033[0m\n' "$1" >&2; }

step "checando docker"
if ! docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then
    err "ERRO: docker nao esta rodando. Abra o Docker Desktop."
    exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
    step "criando infra/docker/.env a partir de .env.example"
    cp "$ENV_EXAMPLE" "$ENV_FILE"
fi

step "docker compose up -d --build"
docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d --build

API_PORT="$(grep -E '^API_PORT=' "$ENV_FILE" | cut -d= -f2 || echo 8080)"
API_PORT="${API_PORT:-8080}"
HEALTH_URL="http://localhost:${API_PORT}/v1/internal/health"

step "aguardando API em $HEALTH_URL"
ready=0
for _ in $(seq 1 30); do
    if curl -sf -m 2 "$HEALTH_URL" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 2
done
if [ "$ready" = "1" ]; then
    printf '    \033[1;32mAPI ok\033[0m\n'
else
    warn "    AVISO: API nao respondeu em 60s. Continuando. Veja: docker compose logs api"
fi

if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
    step "instalando dependencias do frontend (npm install)"
    (cd "$FRONTEND_DIR" && npm install)
fi

step "iniciando frontend em http://localhost:3000 (Ctrl+C para parar)"
echo
echo "  containers continuam rodando depois do Ctrl+C."
echo "  para parar tudo: docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env down"
echo
cd "$FRONTEND_DIR" && npm run dev
