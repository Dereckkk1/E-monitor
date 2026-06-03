package evidence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveAttribution maps a confirmed detection's short_id to the
// (commercialID, campaignID) it should be recorded under.
//
// campaign_materials is the source of truth: first look for a material linked to
// an active/programada campaign that targets this station and whose date range
// contains detectedAt (most recently added link wins). Only when the short_id
// has no such material link do we fall back to the legacy commercials.campaign_id.
//
// This ordering ensures a backfilled material reused in a new campaign is
// attributed to the campaign it actually runs in now — not the (possibly
// concluded) campaign its legacy commercial row still points at. Pre-fix the
// order was inverted, so reused backfills were attributed to the stale campaign.
//
// Returns pgx.ErrNoRows when the short_id resolves to neither a live material
// link nor a legacy commercial.
func resolveAttribution(ctx context.Context, db *pgxpool.Pool, shortID int32,
	stationID uuid.UUID, detectedAt time.Time) (commercialID, campaignID uuid.UUID, err error) {
	err = db.QueryRow(ctx, `
		SELECT m.id, cm.campaign_id
		FROM materials m
		JOIN campaign_materials cm ON cm.material_id = m.id
		JOIN campaigns ca           ON ca.id = cm.campaign_id
		WHERE m.short_id = $1
		  AND $2 = ANY(cm.target_stations)
		  AND ca.status IN ('programada','ativa')
		  AND $3::date BETWEEN ca.start_date AND ca.end_date
		ORDER BY cm.added_at DESC
		LIMIT 1
	`, shortID, stationID, detectedAt).Scan(&commercialID, &campaignID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = db.QueryRow(ctx,
			`SELECT c.id, c.campaign_id FROM commercials c WHERE c.short_id = $1 AND c.fingerprint_status = 'ready' LIMIT 1`,
			shortID,
		).Scan(&commercialID, &campaignID)
	}
	return commercialID, campaignID, err
}
