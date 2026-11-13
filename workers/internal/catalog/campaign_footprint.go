package catalog

import (
	"context"

	"github.com/google/uuid"
)

// CampaignFootprint é o que está pendurado numa campanha: o histórico que
// impediria apagá-la com segurança (veiculações, projeções, comprovantes manuais,
// pós-venda) e as linhas pequenas que um DELETE levaria junto (vínculos de
// material, regras, ajustes do dia e preços). Só leitura: serve ao preview de
// quem vai apagar, que precisa dizer o que vai embora ANTES de apagar.
type CampaignFootprint struct {
	// Histórico (booleanos: EXISTS para no primeiro achado, a tabela de
	// detecções é grande).
	HasDetections    bool // detections.campaign_id
	HasProjections   bool // detection_campaigns.campaign_id (a tocada pode ser de outra campanha)
	HasManualBatches bool // manual_proof_batches
	HasPostSale      bool // post_sale_report_campaigns

	// O que vai junto num DELETE (CASCADE).
	MaterialLinks int // campaign_materials
	Rules         int // distribution_rules
	Overrides     int // distribution_overrides
	Prices        int // campaign_station_pricing
}

// Footprint lê a CampaignFootprint de uma campanha numa consulta só. Uma
// campanha inexistente devolve tudo zerado/falso (quem chama já conferiu que ela
// existe).
func (c *Campaigns) Footprint(ctx context.Context, id uuid.UUID) (CampaignFootprint, error) {
	var f CampaignFootprint
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM detections WHERE campaign_id = $1),
		       EXISTS(SELECT 1 FROM detection_campaigns WHERE campaign_id = $1),
		       EXISTS(SELECT 1 FROM manual_proof_batches WHERE campaign_id = $1),
		       EXISTS(SELECT 1 FROM post_sale_report_campaigns WHERE campaign_id = $1),
		       (SELECT count(*) FROM campaign_materials WHERE campaign_id = $1),
		       (SELECT count(*) FROM distribution_rules WHERE campaign_id = $1),
		       (SELECT count(*) FROM distribution_overrides WHERE campaign_id = $1),
		       (SELECT count(*) FROM campaign_station_pricing WHERE campaign_id = $1)`, id,
	).Scan(&f.HasDetections, &f.HasProjections, &f.HasManualBatches, &f.HasPostSale,
		&f.MaterialLinks, &f.Rules, &f.Overrides, &f.Prices)
	return f, err
}
