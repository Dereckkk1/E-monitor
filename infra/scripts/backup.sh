#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Radiocheck — Postgres physical backup (§14.4)
#
# Performs a daily pg_basebackup (tar + gzip), uploads it to Cloudflare R2,
# rotates local files (keeps last 7 days), and emits a JSON status line on
# stdout. Optionally writes a Prometheus textfile metrics file so node_exporter
# can scrape it (see infra/prometheus/textfile-collector/README.md).
#
# Required env:
#   PGUSER, PGPASSWORD, PGHOST, PGPORT, PGDATABASE
#   BACKUP_DIR                  -- local directory (default: /var/lib/radiocheck/backup)
#   R2_ENDPOINT                 -- e.g. https://<accountid>.r2.cloudflarestorage.com
#   R2_BUCKET                   -- bucket name (e.g. radiocheck-backups)
#   R2_ACCESS_KEY, R2_SECRET_KEY
#
# Optional env:
#   PROM_TEXTFILE_DIR           -- if set, writes prom metrics here
#   AWS_CLI                     -- override aws binary path (default: aws)
#   LOCAL_RETENTION_DAYS        -- default 7
#   LOCK_FILE                   -- flock target, default /var/lock/radiocheck-pg-backup.lock
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ─── Single-instance guard ──────────────────────────────────────────────────
# Two cron jobs landing on top of each other (or a manual retry while the cron
# is still running) would launch pg_basebackup twice in parallel and either
# corrupt local state or balloon load on the primary. flock(1) makes the second
# invocation exit cleanly with status 0 ("nothing to do — another run is in
# progress") instead of failing the cron.
LOCK_FILE="${LOCK_FILE:-/var/lock/radiocheck-pg-backup.lock}"
exec 200>"$LOCK_FILE" || { echo "[backup] cannot open lock file $LOCK_FILE" >&2; exit 1; }
if ! flock -n 200; then
    echo "[backup] another backup is already running (lock=$LOCK_FILE), exiting" >&2
    exit 0
fi

log() { printf '%s [backup] %s\n' "$(date -u +%FT%TZ)" "$*" >&2; }
fail() { log "FATAL: $*"; emit_metrics fail 0 0; emit_json fail 0 0; exit 1; }

START_EPOCH=$(date +%s)
TIMESTAMP=$(TZ=America/Sao_Paulo date +%Y%m%d-%H%M%S)

: "${PGHOST:?PGHOST not set}"
: "${PGPORT:?PGPORT not set}"
: "${PGUSER:?PGUSER not set}"
: "${PGPASSWORD:?PGPASSWORD not set}"
: "${PGDATABASE:?PGDATABASE not set}"
: "${R2_ENDPOINT:?R2_ENDPOINT not set}"
: "${R2_BUCKET:?R2_BUCKET not set}"
: "${R2_ACCESS_KEY:?R2_ACCESS_KEY not set}"
: "${R2_SECRET_KEY:?R2_SECRET_KEY not set}"

BACKUP_DIR="${BACKUP_DIR:-/var/lib/radiocheck/backup}"
LOCAL_RETENTION_DAYS="${LOCAL_RETENTION_DAYS:-7}"
AWS_CLI="${AWS_CLI:-aws}"
WORK_DIR="$BACKUP_DIR/$TIMESTAMP"
TARBALL="$BACKUP_DIR/radiocheck-$TIMESTAMP.tar.gz"

mkdir -p "$BACKUP_DIR"

# Cleanup partial work on exit (regardless of success — tarball survives, dir
# is intermediate).
cleanup() {
    if [[ -d "$WORK_DIR" ]]; then
        rm -rf "$WORK_DIR" || true
    fi
}
trap cleanup EXIT

emit_json() {
    local status="$1" size="$2" duration="$3"
    printf '{"timestamp":"%s","size_bytes":%s,"duration_seconds":%s,"status":"%s","tarball":"%s"}\n' \
        "$(date -u +%FT%TZ)" "$size" "$duration" "$status" "$TARBALL"
}

emit_metrics() {
    local status="$1" size="$2" duration="$3"
    if [[ -z "${PROM_TEXTFILE_DIR:-}" ]]; then
        return 0
    fi
    mkdir -p "$PROM_TEXTFILE_DIR"
    local now
    now=$(date +%s)
    local out="$PROM_TEXTFILE_DIR/radiocheck_postgres_backup.prom"
    local tmp="$out.tmp.$$"
    {
        printf '# HELP radiocheck_postgres_backup_last_success_timestamp Unix epoch of last successful backup.\n'
        printf '# TYPE radiocheck_postgres_backup_last_success_timestamp gauge\n'
        if [[ "$status" == "ok" ]]; then
            printf 'radiocheck_postgres_backup_last_success_timestamp %s\n' "$now"
        else
            # Preserve previous success timestamp if present (don't lie about success).
            local prev
            prev=$(grep -E '^radiocheck_postgres_backup_last_success_timestamp [0-9]+' "$out" 2>/dev/null | tail -1 | awk '{print $2}' || true)
            printf 'radiocheck_postgres_backup_last_success_timestamp %s\n' "${prev:-0}"
        fi
        printf '# HELP radiocheck_postgres_backup_size_bytes Size of last backup tarball.\n'
        printf '# TYPE radiocheck_postgres_backup_size_bytes gauge\n'
        printf 'radiocheck_postgres_backup_size_bytes %s\n' "$size"
        printf '# HELP radiocheck_postgres_backup_duration_seconds Duration of last backup attempt.\n'
        printf '# TYPE radiocheck_postgres_backup_duration_seconds gauge\n'
        printf 'radiocheck_postgres_backup_duration_seconds %s\n' "$duration"
        printf '# HELP radiocheck_postgres_backup_last_status 1 ok 0 fail\n'
        printf '# TYPE radiocheck_postgres_backup_last_status gauge\n'
        printf 'radiocheck_postgres_backup_last_status %s\n' "$([[ "$status" == "ok" ]] && echo 1 || echo 0)"
    } > "$tmp"
    mv "$tmp" "$out"
}

log "starting backup PGHOST=$PGHOST PGDATABASE=$PGDATABASE -> $TARBALL"

mkdir -p "$WORK_DIR"

# pg_basebackup: physical backup, tar format, gzipped, with WAL streamed
# alongside (so the backup is restoreable on its own without external WAL).
PGPASSWORD="$PGPASSWORD" pg_basebackup \
    -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" \
    -D "$WORK_DIR" \
    -F tar \
    -z \
    -X stream \
    -P \
    -c fast \
    -l "radiocheck-$TIMESTAMP" \
    || fail "pg_basebackup failed"

log "pg_basebackup completed; bundling artifacts"

# pg_basebackup -F tar -z creates base.tar.gz + pg_wal.tar.gz inside $WORK_DIR.
# Bundle them into a single tarball for atomic upload.
( cd "$BACKUP_DIR" && tar -czf "$TARBALL" -C "$WORK_DIR" . ) \
    || fail "bundling tarball failed"

SIZE=$(stat -c '%s' "$TARBALL" 2>/dev/null || stat -f '%z' "$TARBALL")
MD5=$(md5sum "$TARBALL" 2>/dev/null | awk '{print $1}' || md5 -q "$TARBALL")
log "tarball size=${SIZE}B md5=$MD5"

# Upload to R2 via aws-cli with custom endpoint.
export AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-auto}"

REMOTE_KEY="postgres/daily/radiocheck-$TIMESTAMP.tar.gz"
log "uploading to s3://$R2_BUCKET/$REMOTE_KEY"
"$AWS_CLI" s3 cp "$TARBALL" "s3://$R2_BUCKET/$REMOTE_KEY" \
    --endpoint-url "$R2_ENDPOINT" \
    --metadata "md5=$MD5,timestamp=$TIMESTAMP" \
    || fail "s3 upload failed"

# Rotate: delete local tarballs older than $LOCAL_RETENTION_DAYS.
log "rotating local backups (keeping last ${LOCAL_RETENTION_DAYS} days)"
find "$BACKUP_DIR" -maxdepth 1 -type f -name 'radiocheck-*.tar.gz' \
    -mtime "+$LOCAL_RETENTION_DAYS" -print -delete >&2 || true

END_EPOCH=$(date +%s)
DURATION=$((END_EPOCH - START_EPOCH))

log "backup OK size=${SIZE}B duration=${DURATION}s remote=s3://$R2_BUCKET/$REMOTE_KEY"
emit_metrics ok "$SIZE" "$DURATION"
emit_json ok "$SIZE" "$DURATION"
