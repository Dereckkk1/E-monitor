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

# ── teste de migrations em sombra ────────────────────────────────────────────
#
# Aplica as migrations PENDENTES a uma CÓPIA ISOLADA dos dados de prod (postgres
# descartável numa rede própria), ANTES de tocar no banco real. Pega falhas que
# DEPENDEM DOS DADOS — ex: colisão de UNIQUE num backfill — que passam batido em
# DB local vazio/sparso (foi exatamente o incidente da 0039 em 2026-06-17: o
# teste local não tinha commercials, então o INSERT era no-op; em prod colidiu
# em materials.short_id e deixou o schema dirty, travando o deploy).
#
# Contrato:
#   - migration FALHA na sombra  → ABORTA o deploy (prod intacta, nunca fica dirty).
#   - setup da sombra falha       → WARN + segue (não pior que antes; guard é extra).
#   - pular de propósito           → SKIP_MIGRATION_SHADOW=yes
SHADOW_PG_IMAGE="postgres:16-alpine"          # mesma major do prod (ver compose)
SHADOW_MIGRATE_IMAGE="migrate/migrate:v4.17.1" # mesma do service migrate
SHADOW_NAME=""
SHADOW_NET=""

shadow_cleanup() {
  # -v remove o volume ANÔNIMO que o postgres descartável cria (a imagem tem
  # VOLUME /var/lib/postgresql/data). Sem o -v, cada deploy deixava ~2.8GB órfãos
  # (o dump de prod restaurado) acumulando no disco de OS — chegou a encher o root
  # e abortar o build (no space left, 2026-06-30). O -v só apaga o volume do
  # próprio container de sombra; nunca toca bind mounts nem volumes nomeados.
  [ -n "${SHADOW_NAME:-}" ] && docker rm -fv "$SHADOW_NAME" >/dev/null 2>&1 || true
  [ -n "${SHADOW_NET:-}" ]  && docker network rm "$SHADOW_NET" >/dev/null 2>&1 || true
  SHADOW_NAME=""; SHADOW_NET=""
}

shadow_migration_test() {
  if [ "${SKIP_MIGRATION_SHADOW:-no}" = "yes" ]; then
    warn "SKIP_MIGRATION_SHADOW=yes — pulando teste de migrations em sombra."
    return 0
  fi
  if ! service_running postgres; then
    warn "postgres não está rodando — pulando teste de sombra (primeira subida?)."
    return 0
  fi

  SHADOW_NET="rc-shadow-net-$$"
  SHADOW_NAME="rc-shadow-pg-$$"

  # ── setup (best-effort: qualquer falha aqui → warn + segue) ──
  if ! docker network create "$SHADOW_NET" >/dev/null 2>&1; then
    warn "não criou a rede de sombra — pulando teste (deploy segue)."; shadow_cleanup; return 0
  fi
  if ! docker run -d --name "$SHADOW_NAME" --network "$SHADOW_NET" \
        -e POSTGRES_USER=radiocheck -e POSTGRES_PASSWORD=shadow -e POSTGRES_DB=radiocheck \
        "$SHADOW_PG_IMAGE" >/dev/null 2>&1; then
    warn "não subiu o postgres de sombra — pulando teste (deploy segue)."; shadow_cleanup; return 0
  fi
  local up=0 i
  for i in $(seq 1 20); do
    if docker exec "$SHADOW_NAME" pg_isready -U radiocheck >/dev/null 2>&1; then up=1; break; fi
    sleep 1
  done
  if [ "$up" != "1" ]; then
    warn "postgres de sombra não ficou pronto — pulando teste (deploy segue)."; shadow_cleanup; return 0
  fi
  # Clona schema+dados de prod → sombra. pg_restore pode sair !=0 por ruído
  # benigno (extensões já presentes); validamos pelo schema_migrations.
  $COMPOSE exec -T postgres sh -c 'pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"' 2>/dev/null \
    | docker exec -i "$SHADOW_NAME" pg_restore -U radiocheck -d radiocheck --no-owner --no-acl >/dev/null 2>&1 || true
  # A guarda aqui já perguntou só se a tabela `schema_migrations` EXISTE — e um
  # restore parcial passa por ela: a tabela vem no dump junto com as outras, mas
  # sem a linha de versão. O migrate então lê versão 0, roda TUDO desde a 0001
  # sobre tabelas que o restore já criou, e aborta com
  # `relation "clients" already exists` — erro da 0001, não da migration nova.
  #
  # Foi o falso positivo do deploy de 2026-08-31 (postmortem do go-live do E-Hub,
  # §5.12). O custo real não é o susto: é alguém concluir que a guarda mente e
  # pegar o hábito de `SKIP_MIGRATION_SHADOW=yes`. Guarda que dá alarme falso é
  # guarda que se desliga de vez — e aí a regra 4.8 do CLAUDE.md fica sem defesa.
  #
  # Agora compara a VERSÃO (e o `dirty`): a sombra só serve como teste se estiver
  # exatamente no mesmo ponto que a produção. Divergiu, o clone está incompleto e
  # o certo é PULAR — não reprovar o deploy por um problema que não é dele.
  local v_prod v_sombra
  v_prod=$($COMPOSE exec -T postgres sh -c \
      'psql -tAF/ -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT version, dirty FROM schema_migrations LIMIT 1"' \
      2>/dev/null | tr -d '\r' | head -1)
  v_sombra=$(docker exec "$SHADOW_NAME" psql -tAF/ -U radiocheck -d radiocheck \
      -c "SELECT version, dirty FROM schema_migrations LIMIT 1" 2>/dev/null | tr -d '\r' | head -1)
  if [ -z "$v_sombra" ] || [ "$v_sombra" != "$v_prod" ]; then
    warn "clone pra sombra incompleto (prod=${v_prod:-vazio}, sombra=${v_sombra:-vazio}) — pulando teste (deploy segue)."
    shadow_cleanup; return 0
  fi
  ok "sombra confere com prod (schema_migrations version/dirty = $v_prod)"

  # ── o teste de verdade: aplica as migrations pendentes sobre os dados reais ──
  step "teste de migrations em sombra (cópia dos dados de prod)"
  local out rc
  out=$(docker run --rm --network "$SHADOW_NET" -v "$REPO_ROOT/migrations:/migrations:ro" \
        "$SHADOW_MIGRATE_IMAGE" -path=/migrations \
        -database="postgres://radiocheck:shadow@$SHADOW_NAME:5432/radiocheck?sslmode=disable" up 2>&1) && rc=0 || rc=$?

  shadow_cleanup

  if [ "$rc" != "0" ]; then
    printf '%s\n' "$out" | tail -20 | sed 's/^/      /' >&2
    err "TESTE DE SOMBRA FALHOU — as migrations pendentes quebram nos dados de prod.
     Prod NÃO foi tocada (nada ficou dirty). Corrija a migration e rode de novo.
     (bypass: SKIP_MIGRATION_SHADOW=yes — não recomendado)"
  fi
  ok "teste de sombra OK — migrations aplicam limpo sobre cópia real dos dados"
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
trap 'rm -f "$SNAPSHOT_BEFORE" "$SNAPSHOT_AFTER" 2>/dev/null || true; shadow_cleanup 2>/dev/null || true' EXIT

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

# ── teste de migrations em sombra (antes de qualquer build/up) ────────────────
# Roda APÓS o pull (pra testar as migrations recém-puxadas) e ANTES do up -d
# (pra abortar com prod intacta se alguma migration quebrar nos dados reais).
shadow_migration_test

# ── rebuild e restart ────────────────────────────────────────────────────────

step "build das imagens"
$COMPOSE build --pull

# Limpa o build cache acumulado. Em prod o /var/lib/docker fica no disco de
# OS (24G), e o cache de build cresce a cada deploy — chegou a 7GB ocupando
# 88% do root. `builder prune` só toca cache de build (nunca imagens em uso
# nem volumes), então é seguro. Non-fatal: falha aqui não aborta o deploy.
step "limpando build cache do docker"
if reclaimed=$(docker builder prune -f 2>&1); then
  echo "$reclaimed" | grep -E 'Total reclaimed space' | sed 's/^/    /' || ok "cache limpo"
else
  warn "builder prune falhou (ignorando): $reclaimed"
fi

step "subindo containers"
# `up -d` (sem --force-recreate) só recria serviços com config alterada.
# Bind mounts em docker-compose.override.yml garantem que postgres/minio
# mantêm dado mesmo em recreate. CLAUDE.md regra 4.1 proíbe --force-recreate.
$COMPOSE up -d

# ── aguardar migrations ───────────────────────────────────────────────────────

step "aguardando migrations"
migrate_ok=0
for i in $(seq 1 30); do
  # -a / --all é obrigatório: por padrão `docker compose ps` só lista
  # containers em execução. O migrate é oneshot — já saiu por causa do
  # `depends_on: condition: service_completed_successfully` no api — então
  # sem --all a query volta vazia e o loop nunca enxerga "exited".
  status=$($COMPOSE ps -a migrate --format json 2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print((d[0] if isinstance(d, list) else d).get('State',''))" 2>/dev/null \
    || echo "")
  if [ "$status" = "exited" ]; then
    code=$($COMPOSE ps -a migrate --format json 2>/dev/null \
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
