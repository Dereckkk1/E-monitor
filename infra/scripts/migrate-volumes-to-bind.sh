#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Radiocheck — migração de volumes nomeados para bind mounts (§14.4)
#
# Migra os 3 volumes críticos (pgdata, miniodata, mastersdata) de volumes
# nomeados do docker para paths bind no host. Bind mounts sobrevivem a
# `docker compose down -v` e `up --force-recreate` — defesa contra a classe
# de incidente que aconteceu em 2026-05-12.
#
# Procedimento:
#   1. Para todos os serviços que usam os volumes
#   2. Cria as pastas no host com ownership correto
#   3. Copia o conteúdo dos volumes nomeados para os paths host
#   4. Imprime as linhas a adicionar no .env
#   5. Pede confirmação antes de prosseguir (idempotente — pode rodar de novo)
#
# Pré-requisitos:
#   - docker compose stack já rodando
#   - sudo disponível (para criar pastas em /srv e chown)
#   - Pasta /srv (ou alternativa via TARGET_BASE) com espaço livre suficiente
#
# Uso:
#   sudo bash infra/scripts/migrate-volumes-to-bind.sh
#   sudo bash infra/scripts/migrate-volumes-to-bind.sh --dry-run
#
# Variáveis de ambiente:
#   TARGET_BASE  base path no host (default: /srv/radiocheck)
#   COMPOSE_FILE arquivo docker compose (default: infra/docker/docker-compose.yml)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

TARGET_BASE="${TARGET_BASE:-/srv/radiocheck}"
COMPOSE_FILE="${COMPOSE_FILE:-infra/docker/docker-compose.yml}"
DRY_RUN=0

for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=1 ;;
        --help|-h)
            sed -n '2,30p' "$0"
            exit 0
            ;;
    esac
done

log() { printf '\n\033[1;36m[migrate]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[migrate WARN]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[migrate ERROR]\033[0m %s\n' "$*" >&2; }

run() {
    if [[ $DRY_RUN -eq 1 ]]; then
        printf '  $ %s\n' "$*"
    else
        printf '  $ %s\n' "$*"
        eval "$@"
    fi
}

# Cada entrada: VOLUME_NAME:HOST_SUBDIR:UID:GID
VOLUMES=(
    "docker_pgdata:postgres-data:70:70"       # postgres:16-alpine usa UID 70
    "docker_miniodata:minio-data:1000:1000"   # minio usa UID 1000 por default
    "docker_mastersdata:masters-data:0:0"     # api roda como root
)

log "TARGET_BASE = $TARGET_BASE"
log "COMPOSE_FILE = $COMPOSE_FILE"
if [[ $DRY_RUN -eq 1 ]]; then
    log "DRY-RUN: comandos serão impressos, nada será executado"
fi

# 1. Confirma se os volumes existem
log "verificando volumes do docker..."
for entry in "${VOLUMES[@]}"; do
    vol="${entry%%:*}"
    if ! docker volume inspect "$vol" >/dev/null 2>&1; then
        warn "volume $vol não existe — pulando (ok se já migrado)"
    fi
done

# 2. Para serviços que tocam os volumes
log "parando serviços (api, postgres, minio, fingerprint, backup, radio-sim)..."
run "docker compose -f $COMPOSE_FILE stop api postgres minio fingerprint backup radio-sim || true"

# 3. Cria pastas + copia conteúdo
for entry in "${VOLUMES[@]}"; do
    IFS=':' read -r vol subdir uid gid <<< "$entry"
    target="$TARGET_BASE/$subdir"

    log "migrando $vol -> $target"

    run "mkdir -p '$target'"
    run "chown $uid:$gid '$target'"

    if docker volume inspect "$vol" >/dev/null 2>&1; then
        # Copia conteúdo via container temporário (preserva permissões/ownership)
        log "  copiando dados via container temporário..."
        run "docker run --rm -v $vol:/old -v $target:/new alpine sh -c 'cp -a /old/. /new/ 2>/dev/null || true'"
    else
        log "  volume $vol não existe, pulando cópia (target $target ficará vazio)"
    fi

    log "  conteúdo de $target:"
    if [[ $DRY_RUN -eq 0 ]]; then
        ls -la "$target" 2>&1 | head -5 || true
        printf '  total: '
        du -sh "$target" 2>&1 | awk '{print $1}'
    fi
done

# 4. Emite as linhas pro .env
log "Adicione/descomente as seguintes linhas em infra/docker/.env (PROD):"
echo
echo "  PGDATA_HOST_PATH=$TARGET_BASE/postgres-data"
echo "  MINIODATA_HOST_PATH=$TARGET_BASE/minio-data"
echo "  MASTERSDATA_HOST_PATH=$TARGET_BASE/masters-data"
echo

if [[ $DRY_RUN -eq 1 ]]; then
    log "dry-run terminado — nada foi alterado."
    exit 0
fi

cat <<'EOS'
================================================================================
Migração de dados concluída. Próximos passos manuais:

  1. Edite infra/docker/.env e adicione as 3 linhas acima.
  2. Suba a stack novamente:
       docker compose -f infra/docker/docker-compose.yml up -d
  3. Confira que postgres/minio subiram healthy:
       docker compose -f infra/docker/docker-compose.yml ps
  4. Faça uma query smoke-test no postgres pra confirmar que os dados
     vieram (lista de detections, commercials, etc.).
  5. Quando confirmar que tudo está ok, os volumes antigos podem ser
     removidos (libera espaço):
       docker volume rm docker_pgdata docker_miniodata docker_mastersdata
     ATENÇÃO: só remova DEPOIS de testar que o sistema roda da nova
     localização.
================================================================================
EOS
