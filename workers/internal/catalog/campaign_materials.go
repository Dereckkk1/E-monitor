package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CampaignMaterial struct {
	CampaignID     uuid.UUID   `json:"campaign_id"`
	MaterialID     uuid.UUID   `json:"material_id"`
	TargetStations []uuid.UUID `json:"target_stations"`
	AddedAt        time.Time   `json:"added_at"`
}

type CampaignMaterials struct {
	pool *pgxpool.Pool
}

func NewCampaignMaterials(pool *pgxpool.Pool) *CampaignMaterials {
	return &CampaignMaterials{pool: pool}
}

// Link inserts a campaign↔material association. If it already exists, target_stations
// is replaced (upsert semantics — operator may re-add to reset stations).
func (cm *CampaignMaterials) Link(ctx context.Context, campaignID, materialID uuid.UUID, stations []uuid.UUID) error {
	if stations == nil {
		stations = []uuid.UUID{}
	}
	_, err := cm.pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, $3)
		ON CONFLICT (campaign_id, material_id) DO UPDATE
		   SET target_stations = EXCLUDED.target_stations`,
		campaignID, materialID, stations)
	return err
}

func (cm *CampaignMaterials) Unlink(ctx context.Context, campaignID, materialID uuid.UUID) error {
	_, err := cm.pool.Exec(ctx,
		`DELETE FROM campaign_materials WHERE campaign_id = $1 AND material_id = $2`,
		campaignID, materialID)
	return err
}

func (cm *CampaignMaterials) UpdateStations(ctx context.Context, campaignID, materialID uuid.UUID, stations []uuid.UUID) error {
	if stations == nil {
		stations = []uuid.UUID{}
	}
	_, err := cm.pool.Exec(ctx,
		`UPDATE campaign_materials SET target_stations = $3
		 WHERE campaign_id = $1 AND material_id = $2`,
		campaignID, materialID, stations)
	return err
}

func (cm *CampaignMaterials) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]CampaignMaterial, error) {
	rows, err := cm.pool.Query(ctx,
		`SELECT campaign_id, material_id, target_stations, added_at
		 FROM campaign_materials
		 WHERE campaign_id = $1 ORDER BY added_at ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignMaterial
	for rows.Next() {
		var l CampaignMaterial
		if err := rows.Scan(&l.CampaignID, &l.MaterialID, &l.TargetStations, &l.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (cm *CampaignMaterials) ListByMaterial(ctx context.Context, materialID uuid.UUID) ([]CampaignMaterial, error) {
	rows, err := cm.pool.Query(ctx,
		`SELECT campaign_id, material_id, target_stations, added_at
		 FROM campaign_materials
		 WHERE material_id = $1 ORDER BY added_at DESC`, materialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignMaterial
	for rows.Next() {
		var l CampaignMaterial
		if err := rows.Scan(&l.CampaignID, &l.MaterialID, &l.TargetStations, &l.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
