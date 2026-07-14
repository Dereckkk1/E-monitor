package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProjectionDrift é uma divergência agregada entre a categoria gravada numa
// projeção (detection_campaigns.category) e o veredito atual do categorizador
// — invariante I da spec 2026-07-14.
type ProjectionDrift struct {
	CampaignID uuid.UUID
	From       string // categoria gravada
	To         string // categoria correta pelas regras vivas
	N          int64
}

// reconScopeSQL escopa TODAS as projeções com detected_at >= $1 (janela móvel
// do reconciler). Partições de detection_campaigns/detections podam por
// detected_at; materials via PK.
const reconScopeSQL = `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.detected_at >= $1
)`

// CountProjectionDrift recomputa a categoria de toda projeção na janela e
// devolve as divergências agrupadas por (campanha, from, to). SELECT-only —
// não muta nada. Usa a MESMA CTE de classificação do recat (fonte única).
func (dr *DistributionRules) CountProjectionDrift(ctx context.Context, since time.Time) ([]ProjectionDrift, error) {
	rows, err := dr.pool.Query(ctx, reconScopeSQL+recatClassifiedCTE+`
SELECT cl.campaign_id, dc.category, cl.new_category, count(*)
FROM classified cl
JOIN detection_campaigns dc
  ON dc.detection_id = cl.id AND dc.detected_at = cl.detected_at
 AND dc.campaign_id = cl.campaign_id
WHERE dc.category IS DISTINCT FROM cl.new_category
GROUP BY cl.campaign_id, dc.category, cl.new_category
ORDER BY count(*) DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectionDrift
	for rows.Next() {
		var p ProjectionDrift
		if err := rows.Scan(&p.CampaignID, &p.From, &p.To, &p.N); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HealProjectionDrift aplica o recat à janela inteira (todas as campanhas) e
// devolve o nº de projeções corrigidas. Idempotente — segunda chamada é no-op.
func (dr *DistributionRules) HealProjectionDrift(ctx context.Context, since time.Time) (int64, error) {
	tag, err := dr.pool.Exec(ctx, reconScopeSQL+recatClassifyTailSQL, since)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
