#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Radiocheck — Postgres restore test (§14.4)
#
# Pulls the most recent daily backup tarball from R2, extracts it into a
# throwaway Postgres container, and runs smoke queries to validate that the
# data is readable. Emits a JSON status line and (optionally) Prometheus
# textfile metrics.
#
# This script is invoked monthly by cron. It does NOT touch production data.
#
# Required env: same R2_*/PG* as backup.sh, plus DOCKER_BIN if non-default.
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

log() { printf '%s [restore-test] %s\n' "$(date -u +%FT%TZ)" "$*" >&2; }

START_EPOCH=$(date +%s)

: "${R2_ENDPOINT:?R2_ENDPOINT not set}"
: "${R2_BUCKET:?R2_BUCKET not set}"
: "${R2_ACCESS_KEY:?R2_ACCESS_KEY not set}"
: "${R2_SECRET_KEY:?R2_SECRET_KEY not set}"

DOCKER_BIN="${DOCKER_BIN:-docker}"
AWS_CLI="${AWS_CLI:-aws}"
WORK_ROOT="${RESTORE_WORK_DIR:-/tmp/radiocheck-restore-test}"
WORK_DIR="$WORK_ROOT/$$-$(date +%s)"
CONTAINER_NAME="radiocheck-restore-test-$$"
PG_IMAGE="${PG_IMAGE:-postgres:16-alpine}"
TEST_PG_PASSWORD="$(head -c 16 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"

mkdir -p "$WORK_DIR"

cleanup() {
    "$DOCKER_BIN" rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR" || true
}
trap cleanup EXIT

emit_json() {
    local status="$1" duration="$2" detail="${3:-}"
    printf '{"timestamp":"%s","status":"%s","duration_seconds":%s,"detail":"%s"}\n' \
        "$(date -u +%FT%TZ)" "$status" "$duration" "$detail"
}

emit_metrics() {
    local status="$1"
    if [[ -z "${PROM_TEXTFILE_DIR:-}" ]]; then
        return 0
    fi
    mkdir -p "$PROM_TEXTFILE_DIR"
    local out="$PROM_TEXTFILE_DIR/radiocheck_postgres_restore_test.prom"
    local tmp="$out.tmp.$$"
    {
        printf '# HELP radiocheck_postgres_restore_test_last_status 1=ok 0=fail\n'
        printf '# TYPE radiocheck_postgres_restore_test_last_status gauge\n'
        printf 'radiocheck_postgres_restore_test_last_status %s\n' "$([[ "$status" == "ok" ]] && echo 1 || echo 0)"
        printf '# HELP radiocheck_postgres_restore_test_last_run_timestamp Unix epoch of last test run.\n'
        printf '# TYPE radiocheck_postgres_restore_test_last_run_timestamp gauge\n'
        printf 'radiocheck_postgres_restore_test_last_run_timestamp %s\n' "$(date +%s)"
    } > "$tmp"
    mv "$tmp" "$out"
}

fail() {
    local reason="$1"
    log "FAIL: $reason"
    local end=$(date +%s)
    emit_metrics fail
    emit_json fail "$((end - START_EPOCH))" "$reason"
    exit 1
}

export AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-auto}"

# 1) Find latest daily backup in R2.
log "listing s3://$R2_BUCKET/postgres/daily/"
LATEST_KEY=$("$AWS_CLI" s3 ls "s3://$R2_BUCKET/postgres/daily/" \
    --endpoint-url "$R2_ENDPOINT" \
    | awk '{print $4}' | grep -E 'radiocheck-.*\.tar\.gz$' | sort | tail -1 || true)

if [[ -z "$LATEST_KEY" ]]; then
    fail "no backup found in s3://$R2_BUCKET/postgres/daily/"
fi
log "latest backup: $LATEST_KEY"

# 2) Download it.
LOCAL_TARBALL="$WORK_DIR/$LATEST_KEY"
"$AWS_CLI" s3 cp "s3://$R2_BUCKET/postgres/daily/$LATEST_KEY" "$LOCAL_TARBALL" \
    --endpoint-url "$R2_ENDPOINT" \
    || fail "download failed"

# 3) Extract bundle: outer tarball contains base.tar.gz + pg_wal.tar.gz.
log "extracting bundle"
mkdir -p "$WORK_DIR/extract"
tar -xzf "$LOCAL_TARBALL" -C "$WORK_DIR/extract" || fail "tar extract failed"

# 4) Now extract base.tar.gz into a pg data dir, and pg_wal.tar.gz into pg_wal.
PGDATA_DIR="$WORK_DIR/pgdata"
mkdir -p "$PGDATA_DIR"
if [[ -f "$WORK_DIR/extract/base.tar.gz" ]]; then
    tar -xzf "$WORK_DIR/extract/base.tar.gz" -C "$PGDATA_DIR" || fail "base extract failed"
else
    fail "base.tar.gz missing from bundle"
fi

if [[ -f "$WORK_DIR/extract/pg_wal.tar.gz" ]]; then
    mkdir -p "$PGDATA_DIR/pg_wal"
    tar -xzf "$WORK_DIR/extract/pg_wal.tar.gz" -C "$PGDATA_DIR/pg_wal" || fail "pg_wal extract failed"
fi

chmod -R 700 "$PGDATA_DIR" || true

# 5) Spin up an isolated Postgres container with the restored data dir mounted.
log "starting restore container $CONTAINER_NAME"
"$DOCKER_BIN" run -d --rm \
    --name "$CONTAINER_NAME" \
    -e POSTGRES_PASSWORD="$TEST_PG_PASSWORD" \
    -v "$PGDATA_DIR:/var/lib/postgresql/data" \
    "$PG_IMAGE" \
    >/dev/null \
    || fail "docker run failed"

# Wait for postgres to accept connections (max 60s).
for i in $(seq 1 30); do
    if "$DOCKER_BIN" exec "$CONTAINER_NAME" pg_isready -U postgres >/dev/null 2>&1; then
        log "postgres ready after ${i}x2s"
        break
    fi
    sleep 2
    if [[ "$i" == "30" ]]; then
        "$DOCKER_BIN" logs "$CONTAINER_NAME" >&2 || true
        fail "postgres did not become ready in 60s"
    fi
done

# 6) Smoke queries. Counts >= 0 is the minimum (table must exist & be readable).
QUERY='SELECT
    (SELECT count(*) FROM clients) AS clients,
    (SELECT count(*) FROM commercials) AS commercials,
    (SELECT count(*) FROM detections WHERE detected_at > NOW() - INTERVAL '\''7 days'\'') AS recent_detections;'

log "running smoke queries"
RESULT=$("$DOCKER_BIN" exec "$CONTAINER_NAME" \
    psql -U postgres -d "${PGDATABASE:-radiocheck}" -tA -F'|' -c "$QUERY" 2>&1) \
    || { log "smoke query stderr: $RESULT"; fail "smoke query failed"; }

log "smoke result: $RESULT"

# Result format: "<clients>|<commercials>|<recent>"
IFS='|' read -r CLIENTS COMMERCIALS RECENT <<< "$RESULT"

# Tolerate an empty DB (test envs); just ensure the queries returned numbers.
if ! [[ "$CLIENTS" =~ ^[0-9]+$ && "$COMMERCIALS" =~ ^[0-9]+$ && "$RECENT" =~ ^[0-9]+$ ]]; then
    fail "smoke result not numeric: clients=$CLIENTS commercials=$COMMERCIALS recent=$RECENT"
fi

END_EPOCH=$(date +%s)
DURATION=$((END_EPOCH - START_EPOCH))
log "restore OK clients=$CLIENTS commercials=$COMMERCIALS recent_detections=$RECENT duration=${DURATION}s"
emit_metrics ok
emit_json ok "$DURATION" "clients=$CLIENTS;commercials=$COMMERCIALS;recent_detections=$RECENT"
