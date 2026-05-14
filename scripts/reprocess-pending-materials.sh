#!/usr/bin/env bash
# reprocess-pending-materials.sh
# Republishes fingerprint.generate for every material currently in
# fingerprint_status='pending' or 'failed'. Used after deploying the
# materials fingerprint pipeline to drain the backlog created by the
# pre-fix wizard upload bug.
#
# Idempotent: the daemon's first action is mark_status('generating'),
# so running this twice causes only one set of work to complete.
#
# Usage:
#   ./scripts/reprocess-pending-materials.sh
#
# Required: docker compose stack running with services postgres + nats +
# fingerprint reachable. The script execs into the nats container to
# publish (no extra image pull needed).

set -euo pipefail

# Allow override for prod (different compose file path) but default to
# the standard local layout.
COMPOSE_FILE="${COMPOSE_FILE:-infra/docker/docker-compose.yml}"

# Auto-detect compose project name by inspecting any running container's
# labels. Falls back to 'docker' which matches the local dev default.
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-$(docker ps --filter "label=com.docker.compose.service=postgres" \
    --format '{{ index .Labels "com.docker.compose.project" }}' | head -1)}"
PROJECT_NAME="${PROJECT_NAME:-docker}"

echo "compose file: $COMPOSE_FILE"
echo "compose project: $PROJECT_NAME"

# Pull the list of material IDs to reprocess.
mapfile -t IDS < <(
    docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" exec -T postgres \
        psql -U "${POSTGRES_USER:-radiocheck}" -d "${POSTGRES_DB:-radiocheck}" -tAc "
            SELECT id FROM materials
            WHERE fingerprint_status IN ('pending','failed')
            ORDER BY created_at;
        "
)

if [ "${#IDS[@]}" -eq 0 ]; then
    echo "no pending/failed materials — nothing to do."
    exit 0
fi

echo "reprocessing ${#IDS[@]} material(s):"
printf '  - %s\n' "${IDS[@]}"

# Publish one NATS message per material. We exec into the nats container
# itself (which has the nats CLI baked in for nats:latest, or we fall
# back to bash echo into the management port — keep it simple with nats
# subject publishing via docker run nats-box which is small).
#
# Prefer execing into an already-running container that has the nats CLI.
# The fingerprint container is the most likely candidate; if it doesn't
# have the CLI, we fall back to publishing via a one-off nats-box image
# attached to the compose network.
NETWORK_NAME=$(docker network ls --filter "name=${PROJECT_NAME}_default" --format '{{.Name}}' | head -1)
NETWORK_NAME="${NETWORK_NAME:-${PROJECT_NAME}_default}"

for id in "${IDS[@]}"; do
    payload=$(printf '{"material_id":"%s"}' "$id")
    echo "publishing fingerprint.generate for $id"
    docker run --rm --network "$NETWORK_NAME" \
        natsio/nats-box:latest nats pub --server=nats://nats:4222 \
        fingerprint.generate "$payload"
done

echo "done. monitor with:"
echo "  docker compose -f $COMPOSE_FILE -p $PROJECT_NAME logs -f fingerprint"
