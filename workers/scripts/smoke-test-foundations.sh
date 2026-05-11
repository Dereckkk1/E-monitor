#!/bin/bash
# smoke-test-foundations.sh — Plan 1 (Foundations) smoke test.
#
# Exercises the new endpoints added by Tasks 13-19. Requires:
#   - API running on http://localhost:8080 (or HOST env var)
#   - Valid JWT in TOKEN env var (login via /v1/internal/auth/login first)
#
# Run:
#   HOST=http://localhost:8080 TOKEN="$(get-jwt)" ./smoke-test-foundations.sh

set -euo pipefail

HOST="${HOST:-http://localhost:8080}"
if [ -z "${TOKEN:-}" ]; then
  echo "ERROR: set TOKEN env var first"
  echo "  TOKEN=\$(curl -s -X POST \$HOST/v1/internal/auth/login -H 'Content-Type: application/json' -d '{\"email\":\"...\",\"password\":\"...\"}' | jq -r '.token')"
  exit 1
fi

AUTH=(-H "Authorization: Bearer $TOKEN")
JSON=(-H "Content-Type: application/json")

echo "1. List material_types (expect >= 6 seeds)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/material-types" | jq '. | length')
echo "   material_types count: $COUNT"
if [ "$COUNT" -lt 6 ]; then
  echo "   FAIL: expected >= 6"
  exit 1
fi

echo "2. Create test client"
CLIENT_ID=$(curl -fsS "${AUTH[@]}" "${JSON[@]}" -X POST "$HOST/v1/internal/clients" \
  -d '{"name":"Smoke Test Client"}' | jq -r '.id')
echo "   client_id = $CLIENT_ID"

echo "3. Create test campaign"
CAMPAIGN_ID=$(curl -fsS "${AUTH[@]}" "${JSON[@]}" -X POST "$HOST/v1/internal/campaigns" \
  -d "{\"name\":\"Smoke Camp\",\"client_id\":\"$CLIENT_ID\",\"start_date\":\"2026-06-01T00:00:00Z\",\"end_date\":\"2026-06-30T00:00:00Z\",\"target_stations\":[]}" \
  | jq -r '.id')
echo "   campaign_id = $CAMPAIGN_ID"

echo "4. List materials for client (expect empty)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/clients/$CLIENT_ID/materials" | jq '. | length')
echo "   materials for new client: $COUNT (expected 0)"

echo "5. List distribution-rules for campaign (expect empty)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/campaigns/$CAMPAIGN_ID/distribution-rules" | jq '. | length')
echo "   rules for new campaign: $COUNT (expected 0)"

echo "6. Daily summary for campaign (expect empty)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/campaigns/$CAMPAIGN_ID/daily-summary?from=2026-06-01&to=2026-06-30" | jq '. | length')
echo "   daily-summary rows: $COUNT (expected 0)"

echo "7. Campaign-materials list for empty campaign (expect empty)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/campaigns/$CAMPAIGN_ID/materials" | jq '. | length')
echo "   campaign-materials: $COUNT (expected 0)"

echo "8. Distribution-overrides list (expect empty)"
COUNT=$(curl -fsS "${AUTH[@]}" "$HOST/v1/internal/campaigns/$CAMPAIGN_ID/distribution-overrides?from=2026-06-01&to=2026-06-30" | jq '. | length')
echo "   overrides: $COUNT (expected 0)"

echo "OK Foundations smoke test PASSED"
