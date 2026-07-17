package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DailySummaryRow is one row from the daily_play_summary view.
// Migration 0019: one row per (campaign, TYPE, station, day) — not per material.
// The 6 metric counts:
//   - expected: programmed plays (gray) — sum of rules' plays_per_day or override
//   - in_slot:  actual plays inside time slot (green)
//   - deficit:  max(0, expected - in_slot - out_slot) (red, "still owed")
//   - bonus:    max(0, in_slot - expected) + orphan_count (blue)
//   - out_slot: plays in-date but out-of-slot (yellow)
//   - out_date: plays out of campaign date range (purple)
type DailySummaryRow struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	TypeID     uuid.UUID `json:"type_id"`
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
// The underlying view is defined in migration 0018. As of migration 0052,
// reads go through daily_play_summary_for(from, to, campaigns) — a
// parametrized function semantically byte-identical to the view but with
// filter pushdown (partition pruning) instead of scanning the full live
// distribution_rules + distribution_overrides + detections history on every
// call. See follow-up F-84.
func (ds *DailySummaryRepo) ListByCampaign(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DailySummaryRow, error) {

	// Congelamento na data de cancelamento (política "manter e marcar"): para
	// campanhas canceladas, dias após cancelled_at não geram mais programado/
	// déficit — a obrigação acabou no cancelamento. Campanhas não-canceladas
	// (cancelled_at NULL) e canceladas legadas sem timestamp passam intactas.
	rows, err := ds.pool.Query(ctx, `
		SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
		       dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
		FROM daily_play_summary_for($2::date, $3::date, ARRAY[$1]::uuid[]) dps
		JOIN campaigns c ON c.id = dps.campaign_id
		WHERE (c.status <> 'cancelada' OR c.cancelled_at IS NULL
		       OR dps.for_date <= (c.cancelled_at AT TIME ZONE 'America/Sao_Paulo')::date)
		ORDER BY dps.station_id, dps.type_id, dps.for_date`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailySummaryRow
	for rows.Next() {
		var r DailySummaryRow
		if err := rows.Scan(&r.CampaignID, &r.TypeID, &r.StationID, &r.ForDate,
			&r.Expected, &r.InSlot, &r.Deficit, &r.Bonus,
			&r.OutSlot, &r.OutDate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
