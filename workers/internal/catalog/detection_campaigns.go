package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Projection é uma atribuição de uma tocada física (uma linha em detections) a
// uma campanha. F-119 multi-atribuição: uma tocada gera N projeções, uma por
// campanha que roda o mesmo áudio (master_sha256) naquela emissora.
// CommercialID é o material DESTA campanha; Category é calculada pelas regras
// DESTA campanha (pode diferir entre projeções da mesma tocada).
type Projection struct {
	CampaignID   uuid.UUID
	CommercialID uuid.UUID
	Category     string
}

// DetectionCampaigns é o repo da tabela detection_campaigns (as projeções).
// detections continua sendo a tocada física (dona de evidência/audit/dedup);
// esta tabela só carrega a atribuição por-campanha.
type DetectionCampaigns struct {
	pool *pgxpool.Pool
}

func NewDetectionCampaigns(pool *pgxpool.Pool) *DetectionCampaigns {
	return &DetectionCampaigns{pool: pool}
}

// InsertProjections grava as projeções de uma tocada física. Idempotente via a
// PK (detection_id, detected_at, campaign_id) + ON CONFLICT DO NOTHING — logo
// re-delivery do NATS ou re-audit não duplicam linhas.
func (d *DetectionCampaigns) InsertProjections(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, projs []Projection) error {
	for _, p := range projs {
		if _, err := d.pool.Exec(ctx, `
			INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
			detectionID, detectedAt, p.CampaignID, p.CommercialID, p.Category); err != nil {
			return err
		}
	}
	return nil
}
