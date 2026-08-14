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
//
// O escopo só ESCOLHE as células-dia; o recatClassifiedCTE expande cada linha
// pra célula-dia completa e recarrega o conjunto aprovado dela. Uma célula-dia
// cortada ao meio pela janela (since caindo no meio do dia local) é reavaliada
// INTEIRA, inclusive as tocadas anteriores a $1 — que é o comportamento correto
// sob cota: a meta é do dia, não da janela do reconciler.
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
// não muta nada. Usa a MESMA CTE de classificação do recat (fonte única), então
// `found` aqui e `healed` do HealProjectionDrift contam a mesma coisa (projeções)
// e são comparáveis.
//
// CONJUNTO COMPARADO: desde o fechamento por célula-dia (spec 2026-08-14) o
// `classified` só emite linha pra projeção cuja tocada está no conjunto aprovado
// (ApprovedDetectionsFilter) — projeção de tocada retratada/ignorada/rejeitada/
// ambígua não é contada nem curada. Tinha que ser assim nas DUAS pontas: contar
// sem poder curar produziria drift PERMANENTE e o ProjectionDriftPersistent
// dispararia pra sempre sobre linhas que nenhuma tela lê. A projeção de toda
// tocada aprovada da janela continua comparada — inclusive as que o `scope` não
// mencionou, porque a expansão pra célula-dia completa as traz.
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

// HealProjectionDrift aplica o recat à janela inteira (todas as campanhas).
// Idempotente — segunda chamada é no-op.
//
// Devolve o nº de PROJEÇÕES corrigidas — a mesma unidade que o
// CountProjectionDrift conta, pra que `healed` seja comparável a `found`. É o
// applyRecat que garante isso: ele aplica os dois UPDATEs como statements
// separados (detections antes, pela ordem de lock) e devolve o tag do de
// detection_campaigns. No padrão de CTE data-modifying o tag viria do UPDATE
// principal — que a ordem de lock obriga a ser o de detections — e este número
// leria 0 justamente nos casos de alcance (fan-out secundário, fora do período).
func (dr *DistributionRules) HealProjectionDrift(ctx context.Context, since time.Time) (int64, error) {
	return dr.runRecat(ctx, reconScopeSQL, since)
}

// HealProjectionDriftForCampaign resincroniza TODAS as projeções de UMA campanha
// (qualquer data, sem o date-bound de RecategorizeForCampaign) à categoria correta
// pelo categorizador — projeções fora do período da campanha convergem pra out_date.
// Usado pelo backfill --all pra convergência completa do histórico (I1 do review
// 2026-07-14). Reusa o mesmo pipeline de classificação (fonte única) e devolve o
// nº de projeções corrigidas.
func (dr *DistributionRules) HealProjectionDriftForCampaign(ctx context.Context, campaignID uuid.UUID) (int64, error) {
	return dr.runRecat(ctx, `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.campaign_id = $1
)`, campaignID)
}
