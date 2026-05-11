package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DailySummaryRow is one row from the daily_play_summary view.
// One row per (campaign, material, station, day) tuple with the 6 metric counts:
//   - expected: programmed plays (gray) — sum of rules' plays_per_day or override
//   - in_slot:  actual plays inside time slot (green)
//   - deficit:  max(0, expected - in_slot - out_slot) (red, "still owed")
//   - bonus:    max(0, in_slot - expected) + orphan_count (blue)
//   - out_slot: plays in-date but out-of-slot (yellow)
//   - out_date: plays out of campaign date range (purple)
type DailySummaryRow struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	MaterialID uuid.UUID `json:"material_id"`
	StationID  uuid.UUID `json:"station_id"`
	ForDate    time.Time `json:"for_date"`
	Expected   int32     `json:"expected"`
	InSlot     int32     `json:"in_slot"`
	Deficit    int32     `json:"deficit"`
	Bonus      int32     `json:"bonus"`
	OutSlot    int32     `json:"out_slot"`
	OutDate    int32     `json:"out_date"`
}

type DailySummaryRepo struct {
	pool *pgxpool.Pool
}

func NewDailySummary(pool *pgxpool.Pool) *DailySummaryRepo {
	return &DailySummaryRepo{pool: pool}
}

// ListByCampaign returns the daily summary rows for the given campaign
// where for_date is between `from` and `to` inclusive.
//
// The view is defined in migration 0018. It's not materialized — every call
// reads from live distribution_rules + distribution_overrides + detections.
// See follow-up F-84 for plans to materialize.
func (ds *DailySummaryRepo) ListByCampaign(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DailySummaryRow, error) {

	rows, err := ds.pool.Query(ctx, `
		SELECT campaign_id, material_id, station_id, for_date,
		       expected, in_slot, deficit, bonus, out_slot, out_date
		FROM daily_play_summary
		WHERE campaign_id = $1
		  AND for_date BETWEEN $2 AND $3
		ORDER BY station_id, material_id, for_date`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailySummaryRow
	for rows.Next() {
		var r DailySummaryRow
		if err := rows.Scan(&r.CampaignID, &r.MaterialID, &r.StationID, &r.ForDate,
			&r.Expected, &r.InSlot, &r.Deficit, &r.Bonus,
			&r.OutSlot, &r.OutDate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
