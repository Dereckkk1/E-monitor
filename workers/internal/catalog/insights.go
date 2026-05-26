package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Insights agrupa as queries de agregação do dashboard /insights.
//
// O design é "one-shot": Compute() roda 4 SELECTs em sequência e devolve um
// payload já formatado para serialização JSON, sem paginação. A query de
// buckets temporais reaproveita a view daily_play_summary (criada na
// migration 0019) ao invés de reimplementar a expansão de regras +
// weekday_mask + overrides. Detecções de materiais sem type_id não
// aparecem nessa view (limitação conhecida do projeto), porém continuam
// contribuindo para impactos/breakdown porque essas métricas são lidas
// direto da tabela detections.
type Insights struct {
	pool *pgxpool.Pool
}

func NewInsights(pool *pgxpool.Pool) *Insights {
	return &Insights{pool: pool}
}

// InsightsParams são os filtros validados aplicados ao cálculo.
type InsightsParams struct {
	ClientID    uuid.UUID
	CampaignIDs []uuid.UUID
	From        time.Time   // inclusive (date-only, UTC)
	To          time.Time   // inclusive (date-only, UTC end-of-day)
	StationIDs  []uuid.UUID // vazio = todas as estações das campanhas
}

// InsightsPayload é o response completo do endpoint.
type InsightsPayload struct {
	Period               PeriodSpec               `json:"period"`
	Campaigns            []CampaignBrief          `json:"campaigns"`
	KPIs                 InsightsKPIs             `json:"kpis"`
	ClassPyramid         ClassPyramidData         `json:"class_pyramid"`
	AgeRanges            AgeRangesData            `json:"age_ranges"`
	VeiculacoesBreakdown VeiculacoesBreakdownData `json:"veiculacoes_breakdown"`
	Buckets              []BucketRow              `json:"buckets"`
}

type PeriodSpec struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Granularity string `json:"granularity"` // "day" | "month"
}

type CampaignBrief struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	StartDate string    `json:"start_date"`
	EndDate   string    `json:"end_date"`
}

type InsightsKPIs struct {
	Impactos         int64        `json:"impactos"`
	VeiculacoesTotal int64        `json:"veiculacoes_total"`
	StationsCount    int          `json:"stations_count"`
	StationsWithPMM  int          `json:"stations_with_pmm"`
	CPM              float64      `json:"cpm"`
	Bonificacao      BonificacaoK `json:"bonificacao"`
	Investido        InvestidoK   `json:"investido"`
	Gender           GenderK      `json:"gender"`
}

type BonificacaoK struct {
	Valor float64 `json:"valor"`
	Count int64   `json:"count"`
}

type InvestidoK struct {
	Contratado float64 `json:"contratado"`
	Executado  float64 `json:"executado"`
}

type GenderK struct {
	M int64 `json:"m"`
	F int64 `json:"f"`
}

type ClassPyramidData struct {
	AB int64 `json:"ab"`
	C  int64 `json:"c"`
	DE int64 `json:"de"`
}

type AgeRangesData struct {
	R18_24  int64 `json:"r18_24"`
	R25_49  int64 `json:"r25_49"`
	R50Plus int64 `json:"r50_plus"`
}

// Note: extras_orphan aqui usa a definição da view daily_play_summary —
// é o "bonus" (max(0, in_slot - expected) + orphan), não só orphan puro.
// Reflete melhor a noção comercial de "mídia ganha".
type VeiculacoesBreakdownData struct {
	InSlot       int64 `json:"in_slot"`
	OutSlot      int64 `json:"out_slot"`
	OutDate      int64 `json:"out_date"`
	ExtrasOrphan int64 `json:"extras_orphan"`
}

type BucketRow struct {
	Bucket     string `json:"bucket"` // "YYYY-MM-DD" diário ou "YYYY-MM" mensal
	Programado int    `json:"programado"`
	InSlot     int    `json:"in_slot"`
	OutSlot    int    `json:"out_slot"`
	OutDate    int    `json:"out_date"`
	Deficit    int    `json:"deficit"`
	Extras     int    `json:"extras"`
}

// Compute é o entry-point do repo. Implementado em incrementos pelos
// helpers privados fetchCampaigns / aggregateCore / aggregateInvestment /
// aggregateBuckets. Mantemos sql.NullString como import disponível
// para os scans dos modos de pricing nos próximos passos.
func (r *Insights) Compute(ctx context.Context, p InsightsParams) (*InsightsPayload, error) {
	_ = sql.ErrNoRows // placeholder — usado nos próximos commits
	return nil, fmt.Errorf("not implemented")
}

// fetchCampaigns devolve briefs (id, nome, datas) das campanhas pedidas,
// validando que TODAS pertencem ao clientID. Se uma única campanha não
// pertence ao cliente (ou não existe), devolve erro com a palavra
// "cross-client" — o handler converte isso em 403.
//
// Isso é o anti-oracle: um cliente B nunca consegue extrair metadata de
// campanhas de A nem pelo simples ato de ter o uuid.
func (r *Insights) fetchCampaigns(ctx context.Context, clientID uuid.UUID, ids []uuid.UUID) ([]CampaignBrief, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, start_date::text, end_date::text
		FROM campaigns
		WHERE id = ANY($1::uuid[]) AND client_id = $2
		ORDER BY start_date ASC
	`, ids, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CampaignBrief
	for rows.Next() {
		var b CampaignBrief
		if err := rows.Scan(&b.ID, &b.Name, &b.StartDate, &b.EndDate); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(ids) {
		return nil, fmt.Errorf("insights: %d campaigns requested, %d found for client (cross-client or invalid id)", len(ids), len(out))
	}
	return out, nil
}
