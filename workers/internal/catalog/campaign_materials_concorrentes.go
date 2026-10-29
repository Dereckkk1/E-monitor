package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// VinculoConcorrente é o vínculo de OUTRA campanha que disputa a atribuição das
// tocadas de um material: campanha programada ou ativa, de vigência que cruza a
// da campanha dada, com ao menos uma emissora em comum e o MESMO áudio (o próprio
// material ou outro com o mesmo master_sha256).
type VinculoConcorrente struct {
	CampaignID    uuid.UUID
	CampaignName  string
	Status        string
	StartDate     time.Time
	EndDate       time.Time
	MaterialID    uuid.UUID
	MaterialTitle string
	// MesmoMaterial: o vínculo é do próprio material (e não de um duplicado com o
	// mesmo master_sha256).
	MesmoMaterial bool
	AddedAt       time.Time
	// EmissorasComuns: as emissoras do vínculo concorrente que também estão em
	// `estacoes` (o que o chamador perguntou).
	EmissorasComuns []uuid.UUID
}

// ListarConcorrentes acha os vínculos de outras campanhas que disputam, com o
// vínculo (campaignID, materialID), a atribuição das tocadas nas emissoras dadas.
//
// É o recorte do evidence.resolveAttribution / resolveAllAttributions: só conta
// campanha programada ou ativa, e a tocada tem de cair na vigência dela
// (aqui: vigência que CRUZA [inicio, fim]); o material é o mesmo, ou outro com o
// mesmo master_sha256 (o fan-out multi-atribuição busca por master_sha256).
// Cancelada e concluída não atribuem tocada nova e ficam de fora.
func (cm *CampaignMaterials) ListarConcorrentes(ctx context.Context, campaignID, materialID uuid.UUID,
	inicio, fim time.Time, estacoes []uuid.UUID) ([]VinculoConcorrente, error) {
	if len(estacoes) == 0 {
		return nil, nil
	}
	rows, err := cm.pool.Query(ctx, `
		SELECT ca.id, ca.name, ca.status, ca.start_date, ca.end_date,
		       m.id, m.title, (cm.material_id = $2) AS mesmo, cm.added_at,
		       ARRAY(SELECT s FROM unnest(cm.target_stations) s WHERE s = ANY($5::uuid[])) AS comuns
		  FROM campaign_materials cm
		  JOIN campaigns ca ON ca.id = cm.campaign_id
		  JOIN materials m  ON m.id = cm.material_id
		 WHERE cm.campaign_id <> $1
		   AND ca.status IN ('programada','ativa')
		   AND ca.start_date <= $4::date AND ca.end_date >= $3::date
		   AND cm.target_stations && $5::uuid[]
		   AND (cm.material_id = $2
		        OR (m.master_sha256 <> ''
		            AND m.master_sha256 = (SELECT master_sha256 FROM materials WHERE id = $2)))
		 ORDER BY ca.start_date, ca.name, cm.added_at`,
		campaignID, materialID, inicio, fim, estacoes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VinculoConcorrente
	for rows.Next() {
		var v VinculoConcorrente
		if err := rows.Scan(&v.CampaignID, &v.CampaignName, &v.Status, &v.StartDate, &v.EndDate,
			&v.MaterialID, &v.MaterialTitle, &v.MesmoMaterial, &v.AddedAt, &v.EmissorasComuns); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
