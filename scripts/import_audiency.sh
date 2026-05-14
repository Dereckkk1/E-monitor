#!/usr/bin/env bash
# Orquestrador pra subir os dados da Audiency num ambiente (local ou prod).
#
# Sequência:
#   1. Importa STATIONS (preserva PMM + dados do E-radios; troca stream_url
#      por match de CNPJ ou band+freq+city+UF; cria as novas)
#   2. Importa CLIENTS (idempotente por CNPJ)
#   3. Importa CAMPAIGNS, filtrando pelo período passado em env
#
# Pré-condições:
#   - Os JSONs já existem em scripts/data/ (rodar fetch_audiency_*.mjs antes
#     OU receber via git — em prod o IP do datacenter é bloqueado pela
#     Audiency, então os JSONs viajam pelo repo).
#   - Container `postgres` rodando (start.sh / deploy.sh já fizeram isso).
#   - infra/docker/.env existe.
#
# Env vars relevantes:
#   FILTER_START_FROM   data ISO mínima pra start_date de campanha
#   FILTER_START_TO     data ISO máxima pra start_date de campanha
#   DRY_RUN=1           gera os SQLs em /tmp e mostra resumo, sem aplicar
#
# Uso típico:
#   # Tudo + campanhas de junho/2026
#   FILTER_START_FROM=2026-06-01 FILTER_START_TO=2026-06-30 \
#     ./scripts/import_audiency.sh
#
#   # Só simular
#   DRY_RUN=1 ./scripts/import_audiency.sh
#
# Em prod (na VM, com docker-compose.override.yml com bind mounts):
#   cd /srv/radiocheck   # ou onde quer que o repo esteja
#   git pull
#   FILTER_START_FROM=2026-06-01 FILTER_START_TO=2026-06-30 \
#     ./scripts/import_audiency.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="$REPO_ROOT/scripts/data"
TMP_DIR="${TMPDIR:-/tmp}"
COMPOSE_BASE="-f $REPO_ROOT/infra/docker/docker-compose.yml"
ENV_FILE="--env-file $REPO_ROOT/infra/docker/.env"
COMPOSE="docker compose $COMPOSE_BASE $ENV_FILE"

# Em prod, o override.yml existe (bind mount pra pgdata) e o deploy.sh
# sempre usa. Detectamos e adicionamos quando presente — em dev local
# não tem o override e é ok.
OVERRIDE="$REPO_ROOT/infra/docker/docker-compose.override.yml"
if [ -f "$OVERRIDE" ]; then
  COMPOSE="docker compose -f $REPO_ROOT/infra/docker/docker-compose.yml -f $OVERRIDE $ENV_FILE"
fi

DRY_RUN="${DRY_RUN:-0}"
FILTER_START_FROM="${FILTER_START_FROM:-}"
FILTER_START_TO="${FILTER_START_TO:-}"

step() { printf '\033[1;36m==> %s\033[0m\n' "$1"; }
ok()   { printf '    \033[1;32m%s\033[0m\n' "$1"; }
warn() { printf '\033[1;33m%s\033[0m\n' "$1"; }
err()  { printf '\033[1;31mERRO: %s\033[0m\n' "$1" >&2; exit 1; }

# ── pré-checks ─────────────────────────────────────────────────────────────

step "verificando pré-condições"

command -v node >/dev/null 2>&1 || err "node não encontrado no PATH."
command -v docker >/dev/null 2>&1 || err "docker não encontrado no PATH."

for f in audiency-stations.json audiency-clients.json audiency-campaigns.json; do
  [ -f "$DATA_DIR/$f" ] || err "$DATA_DIR/$f não existe. Rode fetch_audiency_*.mjs local ou pull o repo."
done
ok "JSONs presentes"

[ -f "$REPO_ROOT/infra/docker/.env" ] || err "infra/docker/.env não encontrado."

# Postgres precisa estar de pé
if ! $COMPOSE ps postgres --format json 2>/dev/null | grep -q '"State":"running"'; then
  err "postgres não está rodando. Suba o stack primeiro (start.sh ou deploy.sh)."
fi
ok "postgres ok"

PSQL_RUN() {
  if [ "$DRY_RUN" = "1" ]; then
    cat
    return 0
  fi
  $COMPOSE exec -T postgres psql -U radiocheck -d radiocheck
}

SQL_STATIONS="$TMP_DIR/import_audiency_stations.sql"
SQL_CLIENTS="$TMP_DIR/import_audiency_clients.sql"
SQL_CAMPAIGNS="$TMP_DIR/import_audiency_campaigns.sql"

# ── 1. STATIONS ────────────────────────────────────────────────────────────
step "1/3 — stations"
node "$REPO_ROOT/scripts/import_stations_audiency_to_sql.mjs" > "$SQL_STATIONS"
LINES=$(wc -l < "$SQL_STATIONS")
ok "SQL gerado ($LINES linhas)"

if [ "$DRY_RUN" = "1" ]; then
  ok "[DRY_RUN] SQL em $SQL_STATIONS — não aplicado."
else
  step "aplicando SQL de stations"
  $COMPOSE exec -T postgres psql -U radiocheck -d radiocheck < "$SQL_STATIONS" | tail -20
fi

# ── 2. CLIENTS ─────────────────────────────────────────────────────────────
step "2/3 — clients"
node "$REPO_ROOT/scripts/import_clients_to_sql.mjs" > "$SQL_CLIENTS"
LINES=$(wc -l < "$SQL_CLIENTS")
ok "SQL gerado ($LINES linhas)"

if [ "$DRY_RUN" = "1" ]; then
  ok "[DRY_RUN] SQL em $SQL_CLIENTS — não aplicado."
else
  step "aplicando SQL de clients"
  $COMPOSE exec -T postgres psql -U radiocheck -d radiocheck < "$SQL_CLIENTS" | tail -10
fi

# ── 3. CAMPAIGNS (com filtro de data opcional) ─────────────────────────────
step "3/3 — campaigns"
if [ -n "$FILTER_START_FROM" ] || [ -n "$FILTER_START_TO" ]; then
  warn "Filtro de start_date ativo: ${FILTER_START_FROM:-(sem mín)} .. ${FILTER_START_TO:-(sem máx)}"
fi

FILTER_START_FROM="$FILTER_START_FROM" FILTER_START_TO="$FILTER_START_TO" \
  node "$REPO_ROOT/scripts/import_campaigns_audiency_to_sql.mjs" > "$SQL_CAMPAIGNS"
LINES=$(wc -l < "$SQL_CAMPAIGNS")
ok "SQL gerado ($LINES linhas)"

if [ "$DRY_RUN" = "1" ]; then
  ok "[DRY_RUN] SQL em $SQL_CAMPAIGNS — não aplicado."
else
  step "aplicando SQL de campaigns"
  $COMPOSE exec -T postgres psql -U radiocheck -d radiocheck < "$SQL_CAMPAIGNS" | tail -10
fi

# ── 4. Reescreve logos pra usar o proxy /v1/internal/audiency-image ────────
# Necessário porque o gerador antigo (antes do proxy) deixava URLs absolutas
# da Audiency, que o browser não consegue carregar por causa de cross-origin
# / CSP. Os geradores atuais já emitem URL relativa, mas a passagem aqui é
# defensiva pra qualquer linha residual gravada antes do fix.
step "4/4 — reescrita defensiva de logos absolutos"
if [ "$DRY_RUN" = "1" ]; then
  ok "[DRY_RUN] sem reescrita."
else
  $COMPOSE exec -T postgres psql -U radiocheck -d radiocheck <<'SQL' | tail -10
UPDATE stations SET logo_url = '/v1/internal/audiency-image?token=' || (metadata->>'audiency_file_token')
WHERE logo_url LIKE 'https://api.audiency.io/%' AND metadata->>'audiency_file_token' IS NOT NULL;
UPDATE clients SET logo_url = '/v1/internal/audiency-image?token=' || (metadata->>'audiency_file_token')
WHERE logo_url LIKE 'https://api.audiency.io/%' AND metadata->>'audiency_file_token' IS NOT NULL;
SQL
fi

echo
ok "Concluído."
echo
echo "  Pra conferir manualmente:"
echo "    $COMPOSE exec postgres psql -U radiocheck -d radiocheck -c \\"
echo "      \"SELECT COUNT(*) FROM stations WHERE metadata ? 'audiency_id'\""
echo "    $COMPOSE exec postgres psql -U radiocheck -d radiocheck -c \\"
echo "      \"SELECT COUNT(*) FROM clients WHERE metadata ? 'audiency_id'\""
echo "    $COMPOSE exec postgres psql -U radiocheck -d radiocheck -c \\"
echo "      \"SELECT COUNT(*) FROM campaigns WHERE metadata ? 'audiency_campaign_id'\""
