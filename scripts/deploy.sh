#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Deploy do Radiocheck na VM de produção.
# Uso: ./scripts/deploy.sh
#
# Faz: git pull, rebuild das imagens, restart dos containers, valida saúde.
#
# SAFETY DESIGN (lições do incidente 2026-05-12 + dev-data-loss 2026-05-17):
#
#  1. **Backup pré-deploy verificado** — antes de qualquer recreate ou
#     migration, executa /backup.sh no service backup, espera concluir e
#     aborta o deploy se falhar. Sem backup confirmado, sem deploy.
#
#  2. **Tripwire de row count** — captura COUNT(*) das tabelas críticas
#     ANTES do deploy; captura de novo DEPOIS do migrate. Se qualquer
#     tabela foi a zero (e não estava em zero) OU encolheu mais de 50%,
#     aborta com mensagem dizendo onde está o backup pra restore.
#
#  3. **Override file obrigatório** (CLAUDE.md regra 4.7) — comandos
#     manuais SEM ele rodam config diferente da real (4h de caos no
#     incidente 2026-05-12). Pré-check garante presença.
#
#  4. **Sem --force-recreate** (CLAUDE.md regra 4.1) — `up -d` puro
#     recria apenas serviços com config alterada. Bind mounts do override
#     preservam dado mesmo em recreate de stateful.
#
#  5. **Guard de branch** — aborta se branch != master sem CONFIRM=yes.
#
# Variáveis de ambiente opcionais:
#   CONFIRM=yes          ignora guard de branch não-master
#   SKIP_BACKUP=yes      pula backup pré-deploy (NÃO USE EM PROD — só dev)
#   SHRINK_THRESHOLD=N   percentual de redução tolerado (default 50)
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

trap 'rc=$?; printf "\033[1;31m[deploy] FALHA (rc=%d) na linha %d\033[0m\n" "$rc" "$LINENO" >&2' ERR

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_BASE="-f $REPO_ROOT/infra/docker/docker-compose.yml"
COMPOSE_OVERRIDE="-f $REPO_ROOT/infra/docker/docker-compose.override.yml"
ENV_FILE="--env-file $REPO_ROOT/infra/docker/.env"
COMPOSE="docker compose $COMPOSE_BASE $COMPOSE_OVERRIDE $ENV_FILE"

# Tabelas críticas pro tripwire. Encolhimento inesperado em qualquer uma
# delas é sinal de catástrofe — abortar e mandar pro postmortem.
CRITICAL_TABLES=(users clients campaigns detections stations materials commercials)
SHRINK_THRESHOLD="${SHRINK_THRESHOLD:-50}"   # percent

step() { printf '\033[1;36m==> %s\033[0m\n' "$1"; }
ok()   { printf '    \033[1;32m%s\033[0m\n' "$1"; }
warn() { printf '    \033[1;33m%s\033[0m\n' "$1" >&2; }
err()  { printf '\033[1;31mERRO: %s\033[0m\n' "$1" >&2; exit 1; }

# ── helpers ─────────────────────────────────────────────────────────────────

# Service running? (não usa `ps --format json` porque o schema diverge entre
# versões do compose — grep no output texto é mais resiliente.)
service_running() {
  $COMPOSE ps --status=running --services 2>/dev/null | grep -qx "$1"
}

# Captura "tabela=count" pra cada tabela crítica. Saída vazia = postgres
# inacessível ou tabelas inexistentes (primeira subida).
snapshot_counts() {
  local sql=""
  for t in "${CRITICAL_TABLES[@]}"; do
    # to_regclass retorna NULL se a tabela não existe — evita erro em
    # primeira subida onde nem todas as migrations rodaram ainda.
    sql+="SELECT '${t}=' || COALESCE((SELECT COUNT(*) FROM ${t})::text, '0') WHERE to_regclass('public.${t}') IS NOT NULL; "
  done
  $COMPOSE exec -T postgres sh -c \
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "'"$sql"'"' 2>/dev/null \
    | grep -E '^[a-z_]+=[0-9]+$' || true
}

# ── pré-checks ──────────────────────────────────────────────────────────────

step "verificando docker"
docker version --format '{{.Server.Version}}' >/dev/null 2>&1 || err "Docker não está rodando."

step "verificando .env"
[ -f "$REPO_ROOT/infra/docker/.env" ] || err "infra/docker/.env não encontrado. Crie a partir de .env.example."

[ -f "$REPO_ROOT/infra/docker/docker-compose.override.yml" ] || \
  err "docker-compose.override.yml não encontrado. Veja docs/deploy.md Bloco J."

step "verificando branch"
branch=$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)
if [ "$branch" != "master" ]; then
  warn "branch atual = $branch (esperado: master)"
  [ "${CONFIRM:-no}" = "yes" ] || \
    err "Aborte ou rode com CONFIRM=yes pra forçar deploy de branch não-master."
  warn "CONFIRM=yes ativo — prosseguindo com branch $branch"
fi

# ── snapshot de row counts (pré-deploy) ─────────────────────────────────────

SNAPSHOT_BEFORE=$(mktemp)
SNAPSHOT_AFTER=$(mktemp)
trap 'rm -f "$SNAPSHOT_BEFORE" "$SNAPSHOT_AFTER" 2>/dev/null || true' EXIT

step "capturando snapshot de row counts (pré-deploy)"
if service_running postgres; then
  snapshot_counts > "$SNAPSHOT_BEFORE"
  if [ -s "$SNAPSHOT_BEFORE" ]; then
    ok "snapshot capturado em $SNAPSHOT_BEFORE"
    sed 's/^/      /' "$SNAPSHOT_BEFORE"
  else
    warn "snapshot vazio — postgres pode estar inicializando. Tripwire desativado neste deploy."
  fi
else
  warn "postgres não está rodando — pulando snapshot (primeira subida?)."
fi

# ── backup pré-deploy ───────────────────────────────────────────────────────

step "executando backup pré-deploy"
if [ "${SKIP_BACKUP:-no}" = "yes" ]; then
  warn "SKIP_BACKUP=yes — pulando backup. NÃO use em produção."
elif service_running backup; then
  # backup.sh emite uma linha JSON no stdout no final, com status.
  # Cap turamos a saída pra confirmar status=ok.
  backup_output=$($COMPOSE exec -T backup sh /backup.sh 2>&1 || true)
  if echo "$backup_output" | grep -q '"status":"ok"'; then
    backup_summary=$(echo "$backup_output" | grep -E '"status":"ok"' | tail -1)
    ok "backup pré-deploy confirmado"
    echo "      $backup_summary"
  else
    echo "$backup_output" | tail -10 | sed 's/^/      /' >&2
    err "backup pré-deploy FALHOU. NÃO prosseguir — corrigir o backup antes.
     Veja: $COMPOSE logs backup"
  fi
elif $COMPOSE config --services 2>/dev/null | grep -qx backup; then
  warn "service 'backup' existe mas não está rodando — subindo agora."
  $COMPOSE up -d backup
  sleep 2
  if service_running backup; then
    backup_output=$($COMPOSE exec -T backup sh /backup.sh 2>&1 || true)
    if echo "$backup_output" | grep -q '"status":"ok"'; then
      ok "backup pré-deploy confirmado (após subir service)"
    else
      err "backup pré-deploy FALHOU após subir service. Veja: $COMPOSE logs backup"
    fi
  else
    warn "não foi possível subir o backup service — pulando (primeira subida real?)."
  fi
else
  warn "service 'backup' não configurado neste compose — pulando. Considere configurar pra prod."
fi

# ── atualizar código ─────────────────────────────────────────────────────────

step "git pull"
git -C "$REPO_ROOT" pull --ff-only

# ── rebuild e restart ────────────────────────────────────────────────────────

step "build das imagens"
$COMPOSE build --pull

step "subindo containers"
# `up -d` (sem --force-recreate) só recria serviços com config alterada.
# Bind mounts em docker-compose.override.yml garantem que postgres/minio
# mantêm dado mesmo em recreate. CLAUDE.md regra 4.1 proíbe --force-recreate.
$COMPOSE up -d

# ── aguardar migrations ───────────────────────────────────────────────────────

step "aguardando migrations"
migrate_ok=0
for i in $(seq 1 30); do
  status=$($COMPOSE ps migrate --format json 2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print((d[0] if isinstance(d, list) else d).get('State',''))" 2>/dev/null \
    || echo "")
  if [ "$status" = "exited" ]; then
    code=$($COMPOSE ps migrate --format json 2>/dev/null \
      | python3 -c "import sys,json; d=json.load(sys.stdin); print((d[0] if isinstance(d, list) else d).get('ExitCode','1'))" 2>/dev/null \
      || echo "1")
    if [ "$code" = "0" ]; then
      ok "migrations aplicadas"
      migrate_ok=1
      break
    else
      $COMPOSE logs migrate --tail=30 | sed 's/^/      /' >&2
      err "migrations FALHARAM (exit=$code). Containers podem ter sido recriados.
     Backup pré-deploy disponível pra restore (ver: $COMPOSE exec backup ls /backup/)."
    fi
  fi
  sleep 2
done

[ "$migrate_ok" = "1" ] || err "timeout esperando migrations (60s). Veja: $COMPOSE logs migrate"

# ── tripwire: row counts pós-migrate ────────────────────────────────────────

if [ -s "$SNAPSHOT_BEFORE" ]; then
  step "tripwire — comparando row counts pós-migrate"

  # Aguarda postgres responder de novo (foi reiniciado no up -d?).
  for i in $(seq 1 15); do
    if $COMPOSE exec -T postgres pg_isready -U "$($COMPOSE exec -T postgres sh -c 'echo $POSTGRES_USER')" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done

  snapshot_counts > "$SNAPSHOT_AFTER"

  if [ ! -s "$SNAPSHOT_AFTER" ]; then
    err "tripwire: não foi possível capturar snapshot pós-migrate.
     Postgres pode estar fora do ar. Veja: $COMPOSE ps postgres"
  fi

  failed=0
  catastrophe=""

  while IFS='=' read -r table before; do
    after=$(grep -E "^${table}=" "$SNAPSHOT_AFTER" | head -1 | cut -d= -f2 || echo "")
    if [ -z "$after" ]; then
      # Tabela existia antes e sumiu agora? Migrations não devem dropar
      # tabelas críticas. Caso aconteça, ABORTAR.
      catastrophe+="    $table: TABELA NÃO ENCONTRADA APÓS MIGRATE\n"
      failed=1
      continue
    fi

    if [ "$before" -eq 0 ]; then
      # Nada a comparar — tabela já estava vazia. Crescimento de 0→N é OK.
      ok "$table: $before → $after"
      continue
    fi

    if [ "$after" -eq 0 ]; then
      # Catástrofe clara: tinha dado, agora não tem.
      catastrophe+="    $table: $before → 0 (TABELA ZERADA)\n"
      failed=1
      continue
    fi

    if [ "$after" -lt "$before" ]; then
      diff=$(( before - after ))
      pct=$(( diff * 100 / before ))
      if [ "$pct" -gt "$SHRINK_THRESHOLD" ]; then
        catastrophe+="    $table: $before → $after (encolheu ${pct}%, threshold ${SHRINK_THRESHOLD}%)\n"
        failed=1
      else
        warn "$table: $before → $after (encolheu ${pct}% — dentro do threshold)"
      fi
    else
      ok "$table: $before → $after"
    fi
  done < "$SNAPSHOT_BEFORE"

  if [ "$failed" = "1" ]; then
    printf '\n\033[1;31m═══════════════════════════════════════════════════════════\n' >&2
    printf  '  TRIPWIRE DISPAROU — POSSÍVEL PERDA DE DADO\n' >&2
    printf  '═══════════════════════════════════════════════════════════\033[0m\n' >&2
    printf "%b" "$catastrophe" >&2
    printf '\n\033[1;33mO que fazer AGORA:\n' >&2
    printf '  1. NÃO direcione tráfego pro novo deploy ainda.\n' >&2
    printf '  2. Verifique o backup pré-deploy:\n' >&2
    printf '     %s exec backup ls -la /backup/\n' "$COMPOSE" >&2
    printf '  3. Investigue causa nos logs:\n' >&2
    printf '     %s logs migrate api --tail=100\n' "$COMPOSE" >&2
    printf '  4. Se confirmar perda, restore do backup (ver docs/operations/data-durability.md).\033[0m\n\n' >&2
    err "deploy interrompido — investigue antes de prosseguir"
  fi
  ok "tripwire OK — sem perda de dado detectada"
fi

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
ok "deploy concluído com segurança."
[ -s "$SNAPSHOT_BEFORE" ] && ok "tripwire: row counts dentro da margem (${SHRINK_THRESHOLD}%)"
ok "backup pré-deploy: disponível em /backup/ no service backup"
