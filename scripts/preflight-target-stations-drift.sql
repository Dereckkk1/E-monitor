-- preflight-target-stations-drift.sql
-- ─────────────────────────────────────────────────────────────────────────────
-- Pre-flight OBRIGATÓRIO antes do deploy do fix da zona morta do material
-- reaproveitado (docs/superpowers/specs/2026-06-03-reused-material-dead-zone-design.md).
--
-- O fix passa a usar campaign_materials.target_stations (em vez de
-- commercials.target_stations) para decidir quais emissoras um material backfill
-- roda na sua campanha ORIGINAL. No backfill (migration 0016) os dois arrays
-- foram copiados iguais; só divergem se alguém editou as emissoras pela UI
-- legada de /commercials sem passar pelo wizard.
--
-- Esta query lista os casos onde divergiram, em campanhas ativas/programadas.
-- ESPERADO: 0 linhas. Se vier algo, revisar caso a caso ANTES de deployar —
-- aquelas emissoras podem mudar de conjunto após o deploy.
--
-- Uso:
--   docker compose -f infra/docker/docker-compose.yml \
--                  -f infra/docker/docker-compose.override.yml \
--                  --env-file infra/docker/.env \
--     exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -x' \
--     < scripts/preflight-target-stations-drift.sql
-- ─────────────────────────────────────────────────────────────────────────────

SELECT c.short_id,
       c.title,
       c.campaign_id,
       ca.name                AS campanha,
       ca.status              AS status_campanha,
       c.target_stations      AS commercials_stations,
       cm.target_stations     AS campaign_materials_stations
FROM commercials c
JOIN materials m            ON m.id = c.id                    -- é backfill (UUID compartilhado)
JOIN campaign_materials cm  ON cm.material_id = m.id
JOIN campaigns ca           ON ca.id = cm.campaign_id
WHERE ca.status IN ('programada','ativa')
  AND ca.id = c.campaign_id                                  -- a campanha ORIGINAL do commercial
  AND NOT (c.target_stations <@ cm.target_stations
           AND c.target_stations @> cm.target_stations)      -- conjuntos diferentes
ORDER BY c.short_id;
