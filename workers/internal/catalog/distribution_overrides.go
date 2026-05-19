package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DistributionOverride é uma exceção pontual ao "expected" calculado pelas
// rules — uma célula (campaign, type, station, date) com um valor próprio
// E uma faixa horária própria. Migration 0031 adicionou time_start/time_end.
//
// Quando um override existe pra uma célula+dia, o categorizador o trata como
// a única fonte de verdade pra (count, janela de tempo). Rules são ignoradas
// para essa célula+dia (decisões D1/D7 do spec override-time-window).
type DistributionOverride struct {
	CampaignID    uuid.UUID  `json:"campaign_id"`
	TypeID        uuid.UUID  `json:"type_id"`
	StationID     uuid.UUID  `json:"station_id"`
	ForDate       time.Time  `json:"for_date"`
	PlaysExpected int16      `json:"plays_expected"`
	TimeStart     string     `json:"time_start"` // "HH:MM"
	TimeEnd       string     `json:"time_end"`   // "HH:MM"
	Reason        *string    `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
}

type DistributionOverrides struct {
	pool *pgxpool.Pool
}

func NewDistributionOverrides(pool *pgxpool.Pool) *DistributionOverrides {
	return &DistributionOverrides{pool: pool}
}

type UpsertOverrideInput struct {
	CampaignID    uuid.UUID
	TypeID        uuid.UUID
	StationID     uuid.UUID
	ForDate       time.Time
	PlaysExpected int16
	TimeStart     string // "HH:MM" — obrigatório (NOT NULL no banco mesmo quando count=0)
	TimeEnd       string // "HH:MM" — obrigatório
	Reason        *string
	CreatedBy     *uuid.UUID
}

// Upsert inserts or updates an override keyed by (campaign, type, station, date).
// Override has precedence over distribution_rules in the daily_play_summary view
// and supersedes rules for in_slot/out_slot decisions in the categorizer.
func (do *DistributionOverrides) Upsert(ctx context.Context, in UpsertOverrideInput) error {
	_, err := do.pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date,
		   plays_expected, time_start, time_end, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::time, $7::time, $8, $9)
		ON CONFLICT (campaign_id, type_id, station_id, for_date)
		DO UPDATE SET
		  plays_expected = EXCLUDED.plays_expected,
		  time_start     = EXCLUDED.time_start,
		  time_end       = EXCLUDED.time_end,
		  reason         = EXCLUDED.reason,
		  created_by     = EXCLUDED.created_by`,
		in.CampaignID, in.TypeID, in.StationID, in.ForDate,
		in.PlaysExpected, in.TimeStart, in.TimeEnd, in.Reason, in.CreatedBy)
	return err
}

func (do *DistributionOverrides) Delete(ctx context.Context,
	campaignID, typeID, stationID uuid.UUID, forDate time.Time) error {
	_, err := do.pool.Exec(ctx,
		`DELETE FROM distribution_overrides
		 WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date=$4`,
		campaignID, typeID, stationID, forDate)
	return err
}

func (do *DistributionOverrides) ListByCampaignAndDateRange(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DistributionOverride, error) {
	rows, err := do.pool.Query(ctx, `
		SELECT campaign_id, type_id, station_id, for_date,
		       plays_expected,
		       to_char(time_start, 'HH24:MI') AS time_start,
		       to_char(time_end,   'HH24:MI') AS time_end,
		       reason, created_at, created_by
		FROM distribution_overrides
		WHERE campaign_id = $1 AND for_date BETWEEN $2 AND $3
		ORDER BY for_date ASC`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionOverride
	for rows.Next() {
		var o DistributionOverride
		if err := rows.Scan(&o.CampaignID, &o.TypeID, &o.StationID, &o.ForDate,
			&o.PlaysExpected, &o.TimeStart, &o.TimeEnd,
			&o.Reason, &o.CreatedAt, &o.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
