package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DistributionRule struct {
	ID          uuid.UUID   `json:"id"`
	CampaignID  uuid.UUID   `json:"campaign_id"`
	MaterialID  uuid.UUID   `json:"material_id"`
	StationIDs  []uuid.UUID `json:"station_ids"`
	StartDate   time.Time   `json:"start_date"`
	EndDate     time.Time   `json:"end_date"`
	WeekdayMask int16       `json:"weekday_mask"`
	TimeStart   string      `json:"time_start"` // HH:MM
	TimeEnd     string      `json:"time_end"`   // HH:MM
	PlaysPerDay int16       `json:"plays_per_day"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type DistributionRules struct {
	pool *pgxpool.Pool
}

func NewDistributionRules(pool *pgxpool.Pool) *DistributionRules {
	return &DistributionRules{pool: pool}
}

type CreateDistributionRuleInput struct {
	CampaignID  uuid.UUID
	MaterialID  uuid.UUID
	StationIDs  []uuid.UUID
	StartDate   time.Time
	EndDate     time.Time
	WeekdayMask int16
	TimeStart   string // "HH:MM"
	TimeEnd     string // "HH:MM"
	PlaysPerDay int16
}

// ruleColumns uses to_char to normalize TIME to HH:MM string in SELECTs.
const ruleColumns = `id, campaign_id, material_id, station_ids,
       start_date, end_date, weekday_mask,
       to_char(time_start, 'HH24:MI') AS time_start,
       to_char(time_end,   'HH24:MI') AS time_end,
       plays_per_day, created_at, updated_at`

func (dr *DistributionRules) Create(ctx context.Context, in CreateDistributionRuleInput) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, material_id, station_ids, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, $3, $4, $5, $6, $7::time, $8::time, $9)
		RETURNING `+ruleColumns,
		in.CampaignID, in.MaterialID, in.StationIDs,
		in.StartDate, in.EndDate, in.WeekdayMask,
		in.TimeStart, in.TimeEnd, in.PlaysPerDay,
	).Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (dr *DistributionRules) Get(ctx context.Context, id uuid.UUID) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules WHERE id = $1`, id,
	).Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (dr *DistributionRules) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]DistributionRule, error) {
	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules
		 WHERE campaign_id = $1
		 ORDER BY start_date ASC, time_start ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListApplicable returns rules where:
//   - material_id matches
//   - station_id is in station_ids array
//   - date is in [start_date, end_date]
//   - weekday of date matches weekday_mask
//
// Used by the categorizer (workers/internal/categorizer).
//
// IMPORTANT TZ contract: `date` MUST already be normalized to local date
// at midnight in America/Sao_Paulo. The SQL casts `$4::date` which uses
// the session timezone to extract the date portion — passing a UTC time
// can resolve to the wrong weekday for events near midnight in SP TZ.
// Caller's responsibility: do the TZ conversion in Go before invoking this.
//
// Time-of-day matching (slot inclusion) is intentionally NOT done here —
// the categorizer evaluates that in Go after this method returns the
// applicable rules. Filter is date+weekday only.
func (dr *DistributionRules) ListApplicable(ctx context.Context,
	campaignID, materialID, stationID uuid.UUID, date time.Time) ([]DistributionRule, error) {

	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules
		 WHERE campaign_id = $1
		   AND material_id = $2
		   AND $3 = ANY(station_ids)
		   AND $4::date BETWEEN start_date AND end_date
		   AND ((1 << EXTRACT(DOW FROM $4::date)::int) & weekday_mask) != 0`,
		campaignID, materialID, stationID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (dr *DistributionRules) Update(ctx context.Context, id uuid.UUID, in CreateDistributionRuleInput) error {
	_, err := dr.pool.Exec(ctx, `
		UPDATE distribution_rules
		SET material_id = $2, station_ids = $3, start_date = $4, end_date = $5,
		    weekday_mask = $6, time_start = $7::time, time_end = $8::time,
		    plays_per_day = $9, updated_at = now()
		WHERE id = $1`,
		id, in.MaterialID, in.StationIDs, in.StartDate, in.EndDate,
		in.WeekdayMask, in.TimeStart, in.TimeEnd, in.PlaysPerDay)
	return err
}

func (dr *DistributionRules) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `DELETE FROM distribution_rules WHERE id = $1`, id)
	return err
}

// RecategorizeForRule re-classifica todas as detections potencialmente
// afetadas pela criação/edição/exclusão da regra dada. Usa lógica SQL
// equivalente ao categorizer Go: pra cada detection que cabe no escopo
// (campaign, material, station_ids, date_range), decide in_slot/out_slot
// /orphan/out_date e UPDATE category.
//
// Performance: roda inteiro em SQL (sem N+1). Milissegundos pra cobrir
// uma campanha inteira mesmo com centenas de milhares de detections.
func (dr *DistributionRules) RecategorizeForRule(ctx context.Context, ruleID uuid.UUID) error {
	r, err := dr.Get(ctx, ruleID)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, r.CampaignID, &r.MaterialID, r.StationIDs, r.StartDate, r.EndDate)
}

// RecategorizeForCampaign re-classifica todas as detections de uma campanha.
// Útil ao deletar uma regra (não sabemos mais o scope dela) ou pra backfill manual.
func (dr *DistributionRules) RecategorizeForCampaign(ctx context.Context, campaignID uuid.UUID) error {
	var start, end time.Time
	err := dr.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, campaignID,
	).Scan(&start, &end)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, campaignID, nil, nil, start, end)
}

// recategorizeScope é o motor SQL. Pra cada detection no escopo, computa
// a nova categoria e UPDATE em batch.
//
// SQL lógica (replica do categorizer.Categorize em SQL):
//   - Pra cada detection que casa scope (campaign + opcional material + opcional stations + date range):
//   - Se a data local (SP timezone) está fora do range da campanha → out_date
//   - Senão se existe ANY rule onde detection.time_local está em [time_start, time_end] → in_slot
//   - Senão se existe ANY rule mesmo material+station+weekday+date → out_slot
//   - Senão → orphan
func (dr *DistributionRules) recategorizeScope(ctx context.Context,
	campaignID uuid.UUID, materialID *uuid.UUID, stationIDs []uuid.UUID,
	from, to time.Time) error {

	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT d.id, d.detected_at, d.campaign_id, d.commercial_id AS material_id, d.station_id
    FROM detections d
    WHERE d.campaign_id = $1
      AND ($2::uuid IS NULL OR d.commercial_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
),
classified AS (
    SELECT
        s.id, s.detected_at,
        CASE
            WHEN date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                 NOT BETWEEN c.start_date AND c.end_date
                THEN 'out_date'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.material_id = s.material_id
                  AND s.station_id = ANY(r.station_ids)
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                  AND (s.detected_at AT TIME ZONE 'America/Sao_Paulo')::time
                      BETWEEN r.time_start AND r.time_end
            )
                THEN 'in_slot'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.material_id = s.material_id
                  AND s.station_id = ANY(r.station_ids)
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
            )
                THEN 'out_slot'
            ELSE 'orphan'
        END AS new_category
    FROM scope s
    JOIN campaigns c ON c.id = s.campaign_id
)
UPDATE detections d
SET category = cl.new_category
FROM classified cl
WHERE d.id = cl.id AND d.detected_at = cl.detected_at
  AND d.category IS DISTINCT FROM cl.new_category`,
		campaignID, materialID, stationIDs, from, to)
	return err
}
