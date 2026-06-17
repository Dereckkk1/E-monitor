package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DistributionRule é uma regra de "X tocadas por dia do tipo Y nas emissoras Z"
// — distribuição por TIPO (migration 0019). Quem efetivamente toca é qualquer
// material com type_id = Y que esteja vinculado ao campaign via campaign_materials.
type DistributionRule struct {
	ID          uuid.UUID   `json:"id"`
	CampaignID  uuid.UUID   `json:"campaign_id"`
	TypeID      uuid.UUID   `json:"type_id"`
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
	TypeID      uuid.UUID
	StationIDs  []uuid.UUID
	StartDate   time.Time
	EndDate     time.Time
	WeekdayMask int16
	TimeStart   string // "HH:MM"
	TimeEnd     string // "HH:MM"
	PlaysPerDay int16
}

// ruleColumns uses to_char to normalize TIME to HH:MM string in SELECTs.
const ruleColumns = `id, campaign_id, type_id, station_ids,
       start_date, end_date, weekday_mask,
       to_char(time_start, 'HH24:MI') AS time_start,
       to_char(time_end,   'HH24:MI') AS time_end,
       plays_per_day, created_at, updated_at`

func (dr *DistributionRules) Create(ctx context.Context, in CreateDistributionRuleInput) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, type_id, station_ids, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, $3, $4, $5, $6, $7::time, $8::time, $9)
		RETURNING `+ruleColumns,
		in.CampaignID, in.TypeID, in.StationIDs,
		in.StartDate, in.EndDate, in.WeekdayMask,
		in.TimeStart, in.TimeEnd, in.PlaysPerDay,
	).Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (dr *DistributionRules) Get(ctx context.Context, id uuid.UUID) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules WHERE id = $1`, id,
	).Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs,
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
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs,
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
//   - the material's type_id matches the rule's type_id
//   - station_id is in station_ids array
//   - date is in [start_date, end_date]
//   - weekday of date matches weekday_mask
//
// Used by the categorizer (workers/internal/categorizer). Resolves the
// material's tipo via JOIN materials — when the material has no type_id
// (legacy), zero rules are returned and the categorizer falls back to orphan.
//
// IMPORTANT TZ contract: `date` MUST already be normalized to local date
// at midnight in America/Sao_Paulo. The SQL casts `$4::date` which uses
// the session timezone — passing a UTC time can resolve to the wrong
// weekday for events near midnight in SP TZ.
//
// Time-of-day matching (slot inclusion) is intentionally NOT done here —
// the categorizer evaluates that in Go after this method returns the
// applicable rules. Filter is date+weekday only.
func (dr *DistributionRules) ListApplicable(ctx context.Context,
	campaignID, materialID, stationID uuid.UUID, date time.Time) ([]DistributionRule, error) {

	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules r
		 WHERE r.campaign_id = $1
		   AND r.type_id = (SELECT type_id FROM materials WHERE id = $2)
		   AND $3 = ANY(r.station_ids)
		   AND $4::date BETWEEN r.start_date AND r.end_date
		   AND ((1 << EXTRACT(DOW FROM $4::date)::int) & r.weekday_mask) != 0`,
		campaignID, materialID, stationID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs,
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
		SET type_id = $2, station_ids = $3, start_date = $4, end_date = $5,
		    weekday_mask = $6, time_start = $7::time, time_end = $8::time,
		    plays_per_day = $9, updated_at = now()
		WHERE id = $1`,
		id, in.TypeID, in.StationIDs, in.StartDate, in.EndDate,
		in.WeekdayMask, in.TimeStart, in.TimeEnd, in.PlaysPerDay)
	return err
}

func (dr *DistributionRules) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `DELETE FROM distribution_rules WHERE id = $1`, id)
	return err
}

// RecategorizeForRule re-classifica todas as detections potencialmente
// afetadas pela criação/edição/exclusão da regra dada. Como rules agora são
// por tipo, o "scope" são todos os materiais daquele tipo (resolvidos via
// JOIN materials), nas station_ids da rule, no date range dela.
func (dr *DistributionRules) RecategorizeForRule(ctx context.Context, ruleID uuid.UUID) error {
	r, err := dr.Get(ctx, ruleID)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, r.CampaignID, &r.TypeID, r.StationIDs, r.StartDate, r.EndDate)
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

// recatClassifyTailSQL é o trecho compartilhado que replica
// categorizer.Categorize em SQL. Espera uma CTE `scope(id, detected_at,
// campaign_id, material_id, type_id, station_id)` definida antes dele e
// é parameter-free (toda variação de escopo mora na CTE scope que o precede).
//
// Lógica, pra cada detection do scope:
//   - Se a data local (SP timezone) está fora do range da campanha → out_date
//   - Senão se existe ANY rule com type_id = material.type_id, station_id, weekday e
//     time_of_day dentro da faixa tolerada (±15min em cada extremo) → in_slot
//   - Senão se existe ANY rule com type_id+station+weekday casando (mas time não) → out_slot
//   - Senão → orphan
//
// IMPORTANTE: a tolerância de 900s (15 min) DEVE bater com
// categorizer.SlotToleranceSeconds. Sem ela, recategorizações disparadas
// por create/edit de rule reclassificavam como out_slot detections que o
// categorizer Go (no insert) tinha marcado in_slot — divergência silenciosa.
// Fonte única: tanto recategorizeScope (rule/campaign) quanto
// RecategorizeForMaterial (mudança de tipo do material) usam este trecho.
const recatClassifyTailSQL = `,
classified AS (
    SELECT
        s.id, s.detected_at,
        CASE
            WHEN date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                 NOT BETWEEN c.start_date AND c.end_date
                THEN 'out_date'
            WHEN s.type_id IS NULL THEN 'orphan'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.type_id = s.type_id
                  AND s.station_id = ANY(r.station_ids)
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                  AND EXTRACT(EPOCH FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo')::time)
                      BETWEEN EXTRACT(EPOCH FROM r.time_start) - 900
                          AND EXTRACT(EPOCH FROM r.time_end)   + 900
            )
                THEN 'in_slot'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.type_id = s.type_id
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
  AND d.category IS DISTINCT FROM cl.new_category`

// recategorizeScope é o motor SQL pra escopos rule/campaign. Pra cada detection
// no escopo (campaign + opcional type via JOIN materials + opcional stations +
// date range), computa a nova categoria via recatClassifyTailSQL e UPDATE em batch.
func (dr *DistributionRules) recategorizeScope(ctx context.Context,
	campaignID uuid.UUID, typeID *uuid.UUID, stationIDs []uuid.UUID,
	from, to time.Time) error {

	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT d.id, d.detected_at, d.campaign_id, d.commercial_id AS material_id,
           m.type_id, d.station_id
    FROM detections d
    JOIN materials m ON m.id = d.commercial_id
    WHERE d.campaign_id = $1
      AND ($2::uuid IS NULL OR m.type_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
)`+recatClassifyTailSQL,
		campaignID, typeID, stationIDs, from, to)
	return err
}

// RecategorizeForMaterial re-classifica TODAS as detections de um material,
// em todas as campanhas onde ele aparece. Usado quando o type_id do material
// muda: a categoria gravada (detections.category) foi computada no insert com
// o tipo antigo e fica obsoleta — uma detection que casava uma regra do tipo
// novo continua marcada 'orphan' (some pra "bônus" no resumo diário). Como o
// scope resolve m.type_id ao vivo (JOIN materials), rodar isto APÓS o UPDATE
// do type_id reclassifica corretamente contra as regras do tipo atual.
func (dr *DistributionRules) RecategorizeForMaterial(ctx context.Context, materialID uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT d.id, d.detected_at, d.campaign_id, d.commercial_id AS material_id,
           m.type_id, d.station_id
    FROM detections d
    JOIN materials m ON m.id = d.commercial_id
    WHERE d.commercial_id = $1
)`+recatClassifyTailSQL,
		materialID)
	return err
}
