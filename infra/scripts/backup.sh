#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR=${BACKUP_DIR:-/backup}
DATABASE_URL=${DATABASE_URL:?DATABASE_URL not set}
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
FILE="$BACKUP_DIR/radiocheck_$TIMESTAMP.sql.gz"

mkdir -p "$BACKUP_DIR"
pg_dump "$DATABASE_URL" | gzip > "$FILE"
echo "Backup criado: $FILE ($(du -sh "$FILE" | cut -f1))"

# Rotação: remove backups com mais de 30 dias
find "$BACKUP_DIR" -name "radiocheck_*.sql.gz" -mtime +30 -delete
echo "Rotação concluída. Backups retidos: $(ls "$BACKUP_DIR"/*.sql.gz 2>/dev/null | wc -l)"
