package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Domain types ────────────────────────────────────────────────────────────

// Pricing mode constants — espelham o CHECK constraint da migration 0022.
const (
	PricingModeConsolidated = "consolidated"
	PricingModePerInsertion = "per_insertion"
)

// StationPricing representa o pricing de uma emissora dentro de uma campanha.
// Em modo `consolidated`, ConsolidatedValue é set e PerType vazio. Em modo
// `per_insertion`, ConsolidatedValue é nil e PerType tem uma entrada por tipo.
type StationPricing struct {
	CampaignID        uuid.UUID     `json:"campaign_id"`
	StationID         uuid.UUID     `json:"station_id"`
	Mode              string        `json:"mode"`
	ConsolidatedValue *float64      `json:"consolidated_value,omitempty"`
	PerType           []TypePricing `json:"per_type"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

// TypePricing é uma entrada de valor por tipo de material — só preenchida
// quando o StationPricing.Mode = per_insertion.
type TypePricing struct {
	TypeID    uuid.UUID `json:"type_id"`
	UnitValue float64   `json:"unit_value"`
}

// ─── Errors ──────────────────────────────────────────────────────────────────

var (
	ErrPricingInvalidMode               = errors.New("pricing: invalid mode")
	ErrPricingConsolidatedRequiresValue = errors.New("pricing: consolidated mode requires consolidated_value")
	ErrPricingPerInsertionRequiresTypes = errors.New("pricing: per_insertion mode requires at least one per_type entry")
	ErrPricingMixedFields               = errors.New("pricing: consolidated_value and per_type are mutually exclusive")
	ErrPricingNegativeValue             = errors.New("pricing: values must be >= 0")
)

// ─── Repository ──────────────────────────────────────────────────────────────

type Pricing struct {
	pool *pgxpool.Pool
}

func NewPricing(pool *pgxpool.Pool) *Pricing {
	return &Pricing{pool: pool}
}

// ListByCampaign retorna todas as entradas de pricing de uma campanha,
// agregando por (station_id) e juntando o per-type quando aplicável. Vazio se
// nenhum pricing foi cadastrado ainda. Sempre retorna slice não-nil.
func (p *Pricing) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]StationPricing, error) {
	// 1. Lê o cabeçalho (uma linha por emissora).
	rows, err := p.pool.Query(ctx, `
		SELECT campaign_id, station_id, mode, consolidated_value, created_at, updated_at
		FROM campaign_station_pricing
		WHERE campaign_id = $1
		ORDER BY station_id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("pricing.ListByCampaign: query head: %w", err)
	}
	defer rows.Close()

	byStation := make(map[uuid.UUID]*StationPricing)
	order := make([]uuid.UUID, 0)
	for rows.Next() {
		var sp StationPricing
		if err := rows.Scan(&sp.CampaignID, &sp.StationID, &sp.Mode,
			&sp.ConsolidatedValue, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, fmt.Errorf("pricing.ListByCampaign: scan head: %w", err)
		}
		sp.PerType = []TypePricing{}
		byStation[sp.StationID] = &sp
		order = append(order, sp.StationID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing.ListByCampaign: rows.Err head: %w", err)
	}

	if len(byStation) == 0 {
		return []StationPricing{}, nil
	}

	// 2. Lê o detalhe por tipo numa única query e distribui no mapa.
	detRows, err := p.pool.Query(ctx, `
		SELECT station_id, type_id, unit_value
		FROM campaign_station_type_pricing
		WHERE campaign_id = $1
		ORDER BY station_id, type_id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("pricing.ListByCampaign: query detail: %w", err)
	}
	defer detRows.Close()

	for detRows.Next() {
		var stationID uuid.UUID
		var tp TypePricing
		if err := detRows.Scan(&stationID, &tp.TypeID, &tp.UnitValue); err != nil {
			return nil, fmt.Errorf("pricing.ListByCampaign: scan detail: %w", err)
		}
		if sp, ok := byStation[stationID]; ok {
			sp.PerType = append(sp.PerType, tp)
		}
	}
	if err := detRows.Err(); err != nil {
		return nil, fmt.Errorf("pricing.ListByCampaign: rows.Err detail: %w", err)
	}

	out := make([]StationPricing, 0, len(order))
	for _, sid := range order {
		out = append(out, *byStation[sid])
	}
	return out, nil
}

// UpsertInput é o payload de Upsert. ValidatePricingInput é chamado primeiro.
type UpsertInput struct {
	CampaignID        uuid.UUID
	StationID         uuid.UUID
	Mode              string
	ConsolidatedValue *float64
	PerType           []TypePricing
}

// ValidatePricingInput aplica as regras de negócio antes do hit no banco. Os
// CHECKs do schema cobrem boa parte, mas validar na app dá mensagens de erro
// úteis em vez de "constraint violation".
func ValidatePricingInput(in UpsertInput) error {
	switch in.Mode {
	case PricingModeConsolidated:
		if in.ConsolidatedValue == nil {
			return ErrPricingConsolidatedRequiresValue
		}
		if *in.ConsolidatedValue < 0 {
			return ErrPricingNegativeValue
		}
		if len(in.PerType) > 0 {
			return ErrPricingMixedFields
		}
	case PricingModePerInsertion:
		if in.ConsolidatedValue != nil {
			return ErrPricingMixedFields
		}
		if len(in.PerType) == 0 {
			return ErrPricingPerInsertionRequiresTypes
		}
		for _, t := range in.PerType {
			if t.UnitValue < 0 {
				return ErrPricingNegativeValue
			}
		}
	default:
		return ErrPricingInvalidMode
	}
	return nil
}

// Upsert grava o pricing de (campaign, station) atomicamente. Substitui o
// detalhe por tipo inteiro a cada chamada — não tenta merge incremental, pois
// o Step 5 do wizard sempre envia o estado completo.
func (p *Pricing) Upsert(ctx context.Context, in UpsertInput) (*StationPricing, error) {
	if err := ValidatePricingInput(in); err != nil {
		return nil, err
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing.Upsert: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Upsert do cabeçalho.
	_, err = tx.Exec(ctx, `
		INSERT INTO campaign_station_pricing
		    (campaign_id, station_id, mode, consolidated_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (campaign_id, station_id)
		DO UPDATE SET
		    mode = EXCLUDED.mode,
		    consolidated_value = EXCLUDED.consolidated_value`,
		in.CampaignID, in.StationID, in.Mode, in.ConsolidatedValue)
	if err != nil {
		return nil, fmt.Errorf("pricing.Upsert: upsert head: %w", err)
	}

	// 2. Limpa o detalhe atual e (se per_insertion) reinsere.
	if _, err := tx.Exec(ctx, `
		DELETE FROM campaign_station_type_pricing
		WHERE campaign_id = $1 AND station_id = $2`,
		in.CampaignID, in.StationID); err != nil {
		return nil, fmt.Errorf("pricing.Upsert: clear detail: %w", err)
	}

	for _, t := range in.PerType {
		if _, err := tx.Exec(ctx, `
			INSERT INTO campaign_station_type_pricing
			    (campaign_id, station_id, type_id, unit_value)
			VALUES ($1, $2, $3, $4)`,
			in.CampaignID, in.StationID, t.TypeID, t.UnitValue); err != nil {
			return nil, fmt.Errorf("pricing.Upsert: insert type %s: %w", t.TypeID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing.Upsert: commit: %w", err)
	}

	return p.GetByCampaignStation(ctx, in.CampaignID, in.StationID)
}

// GetByCampaignStation retorna o pricing de uma combinação específica. Retorna
// pgx.ErrNoRows se não houver entrada.
func (p *Pricing) GetByCampaignStation(ctx context.Context, campaignID, stationID uuid.UUID) (*StationPricing, error) {
	var sp StationPricing
	err := p.pool.QueryRow(ctx, `
		SELECT campaign_id, station_id, mode, consolidated_value, created_at, updated_at
		FROM campaign_station_pricing
		WHERE campaign_id = $1 AND station_id = $2`,
		campaignID, stationID,
	).Scan(&sp.CampaignID, &sp.StationID, &sp.Mode, &sp.ConsolidatedValue, &sp.CreatedAt, &sp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sp.PerType = []TypePricing{}
	if sp.Mode == PricingModePerInsertion {
		rows, err := p.pool.Query(ctx, `
			SELECT type_id, unit_value
			FROM campaign_station_type_pricing
			WHERE campaign_id = $1 AND station_id = $2
			ORDER BY type_id`, campaignID, stationID)
		if err != nil {
			return nil, fmt.Errorf("pricing.GetByCampaignStation: query detail: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tp TypePricing
			if err := rows.Scan(&tp.TypeID, &tp.UnitValue); err != nil {
				return nil, fmt.Errorf("pricing.GetByCampaignStation: scan detail: %w", err)
			}
			sp.PerType = append(sp.PerType, tp)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("pricing.GetByCampaignStation: rows.Err detail: %w", err)
		}
	}
	return &sp, nil
}

// Delete remove o pricing de uma (campaign, station). Cascade no schema
// elimina o detalhe automaticamente.
func (p *Pricing) Delete(ctx context.Context, campaignID, stationID uuid.UUID) error {
	_, err := p.pool.Exec(ctx,
		`DELETE FROM campaign_station_pricing WHERE campaign_id = $1 AND station_id = $2`,
		campaignID, stationID)
	return err
}
