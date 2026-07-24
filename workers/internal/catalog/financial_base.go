package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogFinancials agrupa a base financeira COMPARTILHADA entre /campaigns
// (FinancialsByCampaign) e /insights (aggregateCore). Antes desta base as duas
// telas tinham cálculos independentes que derivaram (o Modelo B foi aplicado só
// no /insights) — ver docs/features/client-target-pmm.md e a spec
// 2026-07-24-unify-campaigns-insights-financials-design.md.
type CatalogFinancials struct {
	pool *pgxpool.Pool
}

// NewCatalogFinancials constrói o repositório da base financeira compartilhada.
func NewCatalogFinancials(pool *pgxpool.Pool) *CatalogFinancials {
	return &CatalogFinancials{pool: pool}
}

// FinancialBaseRow é uma linha por (campanha, emissora) da base A.
type FinancialBaseRow struct {
	CampaignID uuid.UUID
	StationID  uuid.UUID
	ClientID   uuid.UUID
	Plays      int64   // Σ(in_slot + bonus) na janela
	Invested   float64 // base A: per_insertion Σ unit×(in_slot+bonus); consolidado value×meses
}

// financialBaseCTE devolve uma CADEIA de CTEs terminando em `fin_base`, com
// colunas (campaign_id, station_id, client_id, plays, invested). Os dois
// consumidores EMBUTEM este mesmo texto — é o que garante paridade por
// construção. Params são placeholders (ex.: "$1"), o consumidor controla a
// numeração (mesmo contrato de monthsElapsedSQL).
//
// Base é PRICING-DRIVEN (parte de campaign_station_pricing): emissora sem
// pricing não entra — igual ao /campaigns histórico. Consolidada com zero plays
// na janela ainda soma invested (value×meses).
//
// ATENÇÃO: o consumidor DEVE ligar `todayP` via orMaxDate(today) — um today zero
// zera o consolidado (monthsElapsedSQL não conta nenhum mês antes de `today`).
func financialBaseCTE(campaignsP, clientP, stationsP, fromP, toP, todayP string) string {
	return `
	fb_dps AS (
	    -- Sem clamp de for_date em [GREATEST(start,from), LEAST(end,to)] (a versão
	    -- do /insights fazia): o categorizador já marca toda tocada fora de
	    -- [start_date,end_date] como out_date, então in_slot+bonus já é 0 fora do range.
	    SELECT campaign_id, station_id, type_id, in_slot, bonus
	    FROM daily_play_summary_for(` + fromP + `::date, ` + toP + `::date, ` + campaignsP + `::uuid[])
	    WHERE (` + stationsP + `::uuid[] = '{}' OR station_id = ANY(` + stationsP + `::uuid[]))
	),
	fb_plays AS (
	    -- plays soma in_slot+bonus de TODOS os tipos (entrega crua); fb_perins.invested
	    -- só soma tipos COM pricing (INNER JOIN). Assimetria intencional, não "consertar".
	    SELECT campaign_id, station_id, SUM(in_slot + bonus)::bigint AS plays
	    FROM fb_dps
	    GROUP BY campaign_id, station_id
	),
	fb_perins AS (
	    SELECT d.campaign_id, d.station_id,
	           COALESCE(SUM(tp.unit_value * (d.in_slot + d.bonus)), 0)::numeric AS invested
	    FROM fb_dps d
	    JOIN campaign_station_type_pricing tp
	      ON tp.campaign_id = d.campaign_id
	     AND tp.station_id  = d.station_id
	     AND tp.type_id     = d.type_id
	    GROUP BY d.campaign_id, d.station_id
	),
	fin_base AS (
	    SELECT
	        p.campaign_id,
	        p.station_id,
	        c.client_id,
	        COALESCE(fp.plays, 0)::bigint AS plays,
	        (CASE
	            WHEN p.mode = 'consolidated'
	                THEN COALESCE(p.consolidated_value, 0)::numeric
	                     * ` + monthsElapsedSQL("c.start_date", "c.end_date", todayP, fromP, toP) + `
	            WHEN p.mode = 'per_insertion'
	                THEN COALESCE(fpi.invested, 0)
	            ELSE 0
	        END)::float8 AS invested
	    FROM campaign_station_pricing p
	    JOIN campaigns c ON c.id = p.campaign_id
	    LEFT JOIN fb_plays  fp  ON fp.campaign_id  = p.campaign_id AND fp.station_id  = p.station_id
	    LEFT JOIN fb_perins fpi ON fpi.campaign_id = p.campaign_id AND fpi.station_id = p.station_id
	    WHERE (` + campaignsP + `::uuid[] IS NULL OR p.campaign_id = ANY(` + campaignsP + `::uuid[]))
	      AND (` + clientP + `::uuid IS NULL OR c.client_id = ` + clientP + `::uuid)
	      AND (` + stationsP + `::uuid[] = '{}' OR p.station_id = ANY(` + stationsP + `::uuid[]))
	)`
}

// FinancialBase roda o CTE compartilhado isoladamente (para teste e usos
// diretos). Os consumidores de produção EMBUTEM financialBaseCTE nas próprias
// queries. Ordem dos params: $1 campaigns, $2 client, $3 stations, $4 from,
// $5 to, $6 today.
//
// Semântica nil-vs-vazio dos filtros:
//   - campaignIDs: nil = todas as campanhas; slice vazio = nenhuma (contrato do
//     daily_play_summary_for, `= ANY('{}')` casa zero linhas).
//   - stationIDs: nil/vazio = todas as emissoras.
//
// `today` passa por orMaxDate: um today zero vira 9999-12-31 (campanha inteira),
// nunca colapsa o consolidado pra 0.
func (r *CatalogFinancials) FinancialBase(ctx context.Context, campaignIDs []uuid.UUID, clientID *uuid.UUID, stationIDs []uuid.UUID, from, to, today time.Time) ([]FinancialBaseRow, error) {
	if stationIDs == nil {
		stationIDs = []uuid.UUID{}
	}
	var camps interface{} = campaignIDs
	if campaignIDs == nil {
		camps = nil // NULL = todas
	}
	q := `WITH ` + financialBaseCTE("$1", "$2", "$3", "$4", "$5", "$6") + `
		SELECT campaign_id, station_id, client_id, plays, invested FROM fin_base`
	rows, err := r.pool.Query(ctx, q, camps, clientID, stationIDs, from, to, orMaxDate(today))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinancialBaseRow, 0)
	for rows.Next() {
		var b FinancialBaseRow
		if err := rows.Scan(&b.CampaignID, &b.StationID, &b.ClientID, &b.Plays, &b.Invested); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
