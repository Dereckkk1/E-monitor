package catalog

import (
	"context"
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
	// Today é "hoje" no fuso America/Sao_Paulo (date-only). Usado para acumular
	// o valor consolidado por mês (ciclos mensais já iniciados até hoje — ver
	// consolidatedSummary). Zero value = sem "hoje" → cai no total cheio.
	Today time.Time
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
	// Consolidated é true quando QUALQUER emissora da seleção tem pricing
	// consolidado. Nesse caso o Investido mostra o valor total contratado
	// (fixo, estilo fornecedor) e o frontend esconde o card de Bonificação
	// (que fica zerada). Ver docs/features/insights-dashboard.md.
	Consolidated bool `json:"consolidated"`
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
	// Status permite o frontend marcar visualmente uma campanha cancelada num
	// relatório histórico (política "manter e marcar"): os números pré-cancel
	// continuam contando, mas a campanha aparece rotulada como cancelada.
	Status string `json:"status"`
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
	// Bloco "no target": só faz sentido quando o cliente tem cadastro de
	// PMM no target. StationsWithTarget == 0 → o frontend esconde os cards.
	ImpactosTarget     int64   `json:"impactos_target"`
	StationsWithTarget int     `json:"stations_with_target"`
	CPMTarget          float64 `json:"cpm_target"`
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

// Compute é o entry-point do repo. Roda os 4 helpers em sequência
// (validate-campaigns → core → investment → buckets) e devolve o
// payload completo formatado para serialização JSON.
//
// CPM padrão = (investido_executado / impactos) × 1000. Quando impactos = 0
// (sem detecções na seleção), CPM = 0 (em vez de NaN/Inf).
//
// Override por fixed_cpm: cada campanha pode ter um CPM fixo pré-acordado.
// Quando setado, o CPM exibido é a média ponderada por impactos:
//
//	cpm = Σ(per_campaign_cpm × impactos_campaign) / Σ(impactos_campaign)
//
// onde per_campaign_cpm = fixed_cpm (se setado) OU dinâmico da campanha.
// Investido (contratado/executado) NÃO é alterado — continua sendo o número
// real derivado do pricing por emissora.
func (r *Insights) Compute(ctx context.Context, p InsightsParams) (*InsightsPayload, error) {
	briefs, err := r.fetchCampaigns(ctx, p.ClientID, p.CampaignIDs)
	if err != nil {
		return nil, err
	}
	core, err := r.aggregateCore(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("aggregateCore: %w", err)
	}
	inv, bon, err := r.aggregateInvestment(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("aggregateInvestment: %w", err)
	}
	buckets, gran, err := r.aggregateBuckets(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("aggregateBuckets: %w", err)
	}

	// Regra do fornecedor: se QUALQUER emissora da seleção é consolidada, o
	// Investido mostra o valor TOTAL contratado (fixo — não cresce com o
	// período) e a Bonificação some (zerada; o frontend esconde o card). O
	// cálculo por-veiculação/Modelo B do aggregateInvestment é preservado (útil
	// se a regra mudar) mas sobrescrito aqui pra consolidado. Campanha 100%
	// por-inserção segue por veiculação (inv/bon inalterados).
	total, hasConsolidated, err := r.consolidatedSummary(ctx, p.CampaignIDs, p.StationIDs, p.From, p.To, p.Today)
	if err != nil {
		return nil, fmt.Errorf("consolidatedSummary: %w", err)
	}
	if hasConsolidated {
		inv.Executado = total
		bon = BonificacaoK{}
	}

	cpm, err := r.computeCPM(ctx, p, inv.Executado, core.Impactos)
	if err != nil {
		return nil, fmt.Errorf("computeCPM: %w", err)
	}

	// CPM no target é SEMPRE dinâmico (executado ÷ impactos_target × 1000),
	// mesmo em campanha com fixed_cpm: o CPM fixo é contratado sobre a base
	// total de audiência, não sobre o recorte de público-alvo.
	var cpmTarget float64
	if core.ImpactosTarget > 0 {
		cpmTarget = (inv.Executado / float64(core.ImpactosTarget)) * 1000.0
	}

	return &InsightsPayload{
		Period: PeriodSpec{
			From:        p.From.Format("2006-01-02"),
			To:          p.To.Format("2006-01-02"),
			Granularity: gran,
		},
		Campaigns: briefs,
		KPIs: InsightsKPIs{
			Impactos:         core.Impactos,
			VeiculacoesTotal: core.VeiculacoesTotal,
			StationsCount:    core.StationsCount,
			StationsWithPMM:  core.StationsWithPMM,
			CPM:              cpm,
			Bonificacao:      bon,
			Investido:        inv,
			Gender:           core.Gender,

			ImpactosTarget:     core.ImpactosTarget,
			StationsWithTarget: core.StationsWithTarget,
			CPMTarget:          cpmTarget,
		},
		ClassPyramid:         core.Class,
		AgeRanges:            core.Ages,
		VeiculacoesBreakdown: core.Breakdown,
		Buckets:              buckets,
		Consolidated:         hasConsolidated,
	}, nil
}

// orMaxDate devolve t, ou uma data no futuro distante quando t é zero (o "hoje"
// não foi injetado — testes/uso legado). Com data distante, o acumulado
// consolidado cai no total cheio do contrato (todos os ciclos).
func orMaxDate(t time.Time) time.Time {
	if t.IsZero() {
		return time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return t
}

// monthsElapsedSQL conta os MESES DE CALENDÁRIO da campanha que (a) já
// começaram até `todayParam`, (b) estão dentro da janela [fromExpr, toExpr]
// selecionada. O valor consolidado é mensal e incrementa na VIRADA de cada mês
// (todo dia 1º): o 1º mês conta a partir da data de início (0 antes dela), e ao
// entrar num novo mês de calendário soma mais 1. Filtrar um sub-período escopa
// a contagem (ex.: filtrar só junho de uma campanha de 3 meses → 1). Para o
// /campaigns (sem filtro) passe start_date/end_date como from/to → sem escopo.
//
// generate_series pelos 1ºs de cada mês; conta os que satisfazem começou-até-
// hoje E dentro-da-janela. startCol/endCol = colunas de data da campanha.
func monthsElapsedSQL(startCol, endCol, todayParam, fromExpr, toExpr string) string {
	return `(SELECT count(*)::int
	  FROM generate_series(date_trunc('month', ` + startCol + `), date_trunc('month', ` + endCol + `), interval '1 month') gm(ms)
	  WHERE GREATEST(gm.ms::date, ` + startCol + `) <= (` + todayParam + `)::date
	    AND GREATEST(gm.ms::date, ` + startCol + `) <= (` + toExpr + `)::date
	    AND LEAST((gm.ms + interval '1 month' - interval '1 day')::date, ` + endCol + `) >= (` + fromExpr + `)::date)`
}

// consolidatedSummary devolve o valor TOTAL da campanha e se há QUALQUER
// emissora consolidada na seleção (respeitando o filtro de estações). Quando há
// consolidada, o /insights entra em modo fornecedor: Investido = esse total e
// Bonificação some.
//
// per_ins_delivered lê daily_play_summary_for(from, to, campaigns) (migration
// 0052, Task 13) em vez da view — pushdown, byte-idêntico ao original.
//
// A fórmula é IDÊNTICA à do /campaigns (catalog.Campaigns.FinancialsByCampaign)
// pra as duas telas nunca divergirem:
//
//	total = Σ_estação (
//	    consolidated: consolidated_value × meses_decorridos  (valor é MENSAL;
//	                  acumula por ciclo mensal iniciado até `today`)
//	  + per_insertion: unit_value × (in_slot + bonus)        (o ENTREGUE)
//	)
//
// Não depende de from/to (whole-campaign); depende de `today` só pro acúmulo
// mensal do consolidado.
func (r *Insights) consolidatedSummary(ctx context.Context, campaignIDs, stationIDs []uuid.UUID, from, to, today time.Time) (float64, bool, error) {
	var total float64
	var hasConsolidated bool
	err := r.pool.QueryRow(ctx, `
		WITH camp_meta AS (
		    SELECT id, start_date, end_date,
		           `+monthsElapsedSQL("start_date", "end_date", "$5", "$3", "$4")+` AS months_elapsed
		    FROM campaigns WHERE id = ANY($1::uuid[])
		),
		per_ins_delivered AS (
		    -- valor ENTREGUE das emissoras por-inserção NA JANELA [from,to]:
		    -- unit × (in_slot + bonus), igual ao /campaigns (não o plano cheio).
		    SELECT s.campaign_id, s.station_id,
		           COALESCE(SUM(tp.unit_value * (s.in_slot + s.bonus)), 0)::numeric AS pi_delivered
		    FROM daily_play_summary_for($3::date, $4::date, $1::uuid[]) s
		    JOIN camp_meta cm ON cm.id = s.campaign_id
		    JOIN campaign_station_type_pricing tp
		      ON tp.campaign_id = s.campaign_id
		     AND tp.station_id  = s.station_id
		     AND tp.type_id     = s.type_id
		    WHERE s.for_date BETWEEN GREATEST(cm.start_date, $3::date) AND LEAST(cm.end_date, $4::date)
		      AND ($2::uuid[] = '{}' OR s.station_id = ANY($2::uuid[]))
		    GROUP BY s.campaign_id, s.station_id
		)
		SELECT
		    COALESCE(SUM(
		        CASE WHEN csp.mode='consolidated'
		             THEN COALESCE(csp.consolidated_value, 0)::numeric * cm.months_elapsed
		             ELSE COALESCE(pd.pi_delivered, 0) END
		    ), 0)::float8 AS total,
		    COALESCE(BOOL_OR(csp.mode='consolidated'), false) AS has_consolidated
		FROM campaign_station_pricing csp
		JOIN camp_meta cm ON cm.id = csp.campaign_id
		LEFT JOIN per_ins_delivered pd
		  ON pd.campaign_id = csp.campaign_id AND pd.station_id = csp.station_id
		WHERE csp.campaign_id = ANY($1::uuid[])
		  AND ($2::uuid[] = '{}' OR csp.station_id = ANY($2::uuid[]))
	`, campaignIDs, stationIDs, from, to, orMaxDate(today)).Scan(&total, &hasConsolidated)
	if err != nil {
		return 0, false, err
	}
	return total, hasConsolidated, nil
}

// coreAggregates é o resultado interno usado pelo Compute().
type coreAggregates struct {
	Impactos           int64
	ImpactosTarget     int64
	VeiculacoesTotal   int64
	StationsCount      int
	StationsWithPMM    int
	StationsWithTarget int
	Gender             GenderK
	Class              ClassPyramidData
	Ages               AgeRangesData
	Breakdown          VeiculacoesBreakdownData
}

// aggregateCore lê a tabela detections direta + perfil demográfico em
// stations.meta. Calcula impactos totais, gender split, class pyramid e
// age ranges. Estações sem PMM são contadas em veiculações totais (e em
// stations_count) mas NÃO somam impactos demográficos — o numerador
// requer PMM, e a UI mostra "X de Y emissoras com perfil" como contexto.
//
// O breakdown de veiculações (in_slot/out_slot/out_date/extras_orphan)
// é calculado direto da coluna category — não usa a view daily_play_summary
// porque queremos contar detecções mesmo para materiais sem type_id.
func (r *Insights) aggregateCore(ctx context.Context, p InsightsParams) (*coreAggregates, error) {
	row := r.pool.QueryRow(ctx, `
		WITH filt AS (
		    -- client_id vem da campanha: o PMM no target é resolvido por
		    -- (cliente da campanha, emissora) — ver client_station_pmm.
		    SELECT d.id, d.station_id, d.category, c.client_id
		    FROM detection_attributions d
		    JOIN campaigns c ON c.id = d.campaign_id
		    WHERE d.campaign_id = ANY($1::uuid[])
		      -- Conjunto "aprovado" (catalog.ApprovedDetectionsFilter): exclui
		      -- retratadas (§18.2.2), ignoradas (admin "Desconsiderar") e
		      -- rejeitadas pelo audit §9.9 — mesma régua da view
		      -- daily_play_summary e do resto do sistema. Sem isso o /insights
		      -- divergia do /detections (impactos/veiculações inflados).
		      AND `+ApprovedDetectionsFilter+`
		      AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		),
		per_station AS (
		    SELECT f.station_id, f.client_id,
		           COUNT(*)::bigint AS det_count,
		           COUNT(*) FILTER (WHERE f.category='in_slot')::bigint  AS in_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_slot')::bigint AS out_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_date')::bigint AS out_date_n,
		           COUNT(*) FILTER (WHERE f.category='orphan')::bigint   AS orphan_n
		    FROM filt f
		    GROUP BY f.station_id, f.client_id
		),
		joined AS (
		    SELECT ps.*,
		           s.pmm,
		           cst.pmm_target,
		           (s.metadata->'audience_profile'->'gender'      ->>'male')::float    AS male_p,
		           (s.metadata->'audience_profile'->'gender'      ->>'female')::float  AS female_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeAB')::float AS ab_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeC')::float  AS c_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeDE')::float AS de_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range18to24')::float AS r18_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range25to49')::float AS r25_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range50plus')::float AS r50_p
		    FROM per_station ps
		    JOIN stations s ON s.id = ps.station_id
		    LEFT JOIN client_station_pmm cst
		           ON cst.client_id = ps.client_id AND cst.station_id = ps.station_id
		)
		-- Atenção: percentuais em metadata.audience_profile estão em escala
		-- 0-100 (não 0-1). Por isso multiplicamos por (pct / 100.0).
		SELECT
		    COALESCE(SUM(det_count), 0)::bigint                                  AS veic_total,
		    -- DISTINCT obrigatório: per_station agora particiona por cliente, então
		    -- uma emissora usada por 2 clientes vira 2 linhas. Sem DISTINCT os
		    -- contadores dobrariam (as SOMAS não são afetadas).
		    COUNT(DISTINCT station_id)::int                                      AS stations_count,
		    COUNT(DISTINCT station_id) FILTER (WHERE pmm IS NOT NULL)::int       AS stations_with_pmm,
		    COUNT(DISTINCT station_id) FILTER (WHERE pmm_target IS NOT NULL)::int AS stations_with_target,
		    COALESCE(SUM(det_count * pmm) FILTER (WHERE pmm IS NOT NULL), 0)::bigint                                  AS impactos,
		    COALESCE(SUM(det_count * pmm_target) FILTER (WHERE pmm_target IS NOT NULL), 0)::bigint                    AS impactos_target,
		    COALESCE(SUM(det_count * pmm * male_p   / 100.0) FILTER (WHERE pmm IS NOT NULL AND male_p   IS NOT NULL), 0)::bigint AS gender_m,
		    COALESCE(SUM(det_count * pmm * female_p / 100.0) FILTER (WHERE pmm IS NOT NULL AND female_p IS NOT NULL), 0)::bigint AS gender_f,
		    COALESCE(SUM(det_count * pmm * ab_p     / 100.0) FILTER (WHERE pmm IS NOT NULL AND ab_p     IS NOT NULL), 0)::bigint AS cls_ab,
		    COALESCE(SUM(det_count * pmm * c_p      / 100.0) FILTER (WHERE pmm IS NOT NULL AND c_p      IS NOT NULL), 0)::bigint AS cls_c,
		    COALESCE(SUM(det_count * pmm * de_p     / 100.0) FILTER (WHERE pmm IS NOT NULL AND de_p     IS NOT NULL), 0)::bigint AS cls_de,
		    COALESCE(SUM(det_count * pmm * r18_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r18_p    IS NOT NULL), 0)::bigint AS age_18,
		    COALESCE(SUM(det_count * pmm * r25_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r25_p    IS NOT NULL), 0)::bigint AS age_25,
		    COALESCE(SUM(det_count * pmm * r50_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r50_p    IS NOT NULL), 0)::bigint AS age_50,
		    COALESCE(SUM(in_slot_n),  0)::bigint AS sum_in,
		    COALESCE(SUM(out_slot_n), 0)::bigint AS sum_out,
		    COALESCE(SUM(out_date_n), 0)::bigint AS sum_outdate,
		    COALESCE(SUM(orphan_n),   0)::bigint AS sum_orphan
		FROM joined
	`, p.CampaignIDs, p.From, p.To, p.StationIDs)

	out := &coreAggregates{}
	if err := row.Scan(
		&out.VeiculacoesTotal, &out.StationsCount, &out.StationsWithPMM, &out.StationsWithTarget,
		&out.Impactos, &out.ImpactosTarget,
		&out.Gender.M, &out.Gender.F,
		&out.Class.AB, &out.Class.C, &out.Class.DE,
		&out.Ages.R18_24, &out.Ages.R25_49, &out.Ages.R50Plus,
		&out.Breakdown.InSlot, &out.Breakdown.OutSlot, &out.Breakdown.OutDate, &out.Breakdown.ExtrasOrphan,
	); err != nil {
		return nil, err
	}
	return out, nil
}

// aggregateBuckets devolve a série temporal para o gráfico 4 da página.
// Lê da view daily_play_summary (já agregada por campaign × type × station
// × day) e soma por bucket diário ou mensal.
//
// Granularidade: ≤ 31 dias filtrados → diário (YYYY-MM-DD); > 31 → mensal
// (YYYY-MM). A decisão é local pra evitar dependência circular com o
// período computado no Compute() — o teste pode controlar via params.
//
// "extras" usa `orphan` puro (não `bonus`), pra evitar double-count com
// `in_slot` no mesmo gráfico — bonus inclui in_slot-acima-de-expected
// que já é mostrado em in_slot. Bonificação KPI (no aggregateInvestment)
// usa bonus separadamente.
//
// A CTE `agg` lê daily_play_summary_for(from, to, campaigns) (migration 0052,
// Task 13) em vez da view — pushdown, byte-idêntico ao original. As leituras
// de daily_play_summary em aggregateInvestment/computeCPM (denominador do
// Modelo B) e em campaign_failures.ListHistorical FICARAM na view de
// propósito — decisão do dono (tudo-ou-nada nesses statements; ver Task 13).
func (r *Insights) aggregateBuckets(ctx context.Context, p InsightsParams) ([]BucketRow, string, error) {
	days := int(p.To.Sub(p.From).Hours()/24) + 1
	gran := "day"
	// daily_play_summary key + detection grouping expression. Strings paralelas
	// pq summary tem for_date::date e detections tem detected_at::timestamptz.
	summaryBucket := "for_date::text"
	detectionBucket := "(date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date)::text"
	if days > 31 {
		gran = "month"
		summaryBucket = "to_char(for_date, 'YYYY-MM')"
		detectionBucket = "to_char(date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo'), 'YYYY-MM')"
	}

	query := fmt.Sprintf(`
		WITH agg AS (
		    SELECT %s AS bucket,
		           SUM(expected)::int  AS programado,
		           SUM(in_slot)::int   AS in_slot,
		           SUM(out_slot)::int  AS out_slot,
		           SUM(out_date)::int  AS out_date,
		           GREATEST(0, SUM(expected) - SUM(in_slot) - SUM(out_slot))::int AS deficit
		    FROM daily_play_summary_for($2::date, $3::date, $1::uuid[])
		    WHERE ($4::uuid[] = '{}' OR station_id = ANY($4::uuid[]))
		    GROUP BY 1
		),
		orphan AS (
		    SELECT %s AS bucket,
		           COUNT(*)::int AS extras
		    FROM detection_attributions d
		    WHERE d.campaign_id = ANY($1::uuid[])
		      -- conjunto "aprovado" (catalog.ApprovedDetectionsFilter): antes só
		      -- filtrava retracted_at, deixando ignoradas/audit_rejected inflarem
		      -- os "extras" do gráfico vs o resto do sistema.
		      AND `+ApprovedDetectionsFilter+`
		      AND d.category = 'orphan'
		      AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		    GROUP BY 1
		)
		SELECT COALESCE(a.bucket, o.bucket) AS bucket,
		       COALESCE(a.programado, 0),
		       COALESCE(a.in_slot,    0),
		       COALESCE(a.out_slot,   0),
		       COALESCE(a.out_date,   0),
		       COALESCE(a.deficit,    0),
		       COALESCE(o.extras,     0)
		FROM agg a
		FULL OUTER JOIN orphan o ON a.bucket = o.bucket
		ORDER BY bucket
	`, summaryBucket, detectionBucket)

	rows, err := r.pool.Query(ctx, query, p.CampaignIDs, p.From, p.To, p.StationIDs)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []BucketRow
	for rows.Next() {
		var b BucketRow
		if err := rows.Scan(&b.Bucket, &b.Programado, &b.InSlot, &b.OutSlot, &b.OutDate, &b.Deficit, &b.Extras); err != nil {
			return nil, "", err
		}
		out = append(out, b)
	}
	return out, gran, rows.Err()
}

// aggregateInvestment calcula investido (contratado / executado) e
// bonificação somando contribuições por (campaign, station) seguindo
// o modo de pricing definido em campaign_station_pricing:
//
//   - mode = 'consolidated' (proporcional ao período — Modelo B):
//
//   - contratado  = consolidated_value × overlap_days / total_days
//
//   - executado   = consolidated_value × LEAST(1, entregue_na_janela / plano_da_campanha_INTEIRA)
//     O denominador é o plano da campanha inteira (não da janela) → o número
//     escala com o período e é monotônico. Cap em 100%: over-delivery vai pra bônus.
//
//   - bonificação = consolidated_value × bonus_na_janela / plano_da_campanha_INTEIRA
//     (excedente à taxa estável contrato ÷ plano_total)
//
//   - mode = 'per_insertion':
//
//   - contratado  = Σ_type (unit_value × expected)
//
//   - executado   = Σ_type (unit_value × (in_slot+out_slot))
//
//   - bonificação = Σ_type (unit_value × bonus)
//
// "bonus" é o campo da view daily_play_summary que inclui orphan +
// (in_slot acima do expected). Esse é o sentido comercial de "mídia
// ganha" — alinha com a decisão da spec de incluir extras na bonificação.
// Bonificação count usa bonus diretamente (não orphan_count puro).
func (r *Insights) aggregateInvestment(ctx context.Context, p InsightsParams) (InvestidoK, BonificacaoK, error) {
	row := r.pool.QueryRow(ctx, `
		WITH camp_meta AS (
		    SELECT id, start_date, end_date,
		           GREATEST(0, (LEAST(end_date, $3::date) - GREATEST(start_date, $2::date) + 1))::int AS overlap_days,
		           (end_date - start_date + 1)::int AS total_days
		    FROM campaigns
		    WHERE id = ANY($1::uuid[])
		),
		-- NUMERADOR consolidado: entregue + bônus DENTRO da janela [from,to].
		-- Dia futuro/não-veiculado entrega 0, então alargar a janela nunca
		-- deflaciona (não precisa de clamp de "hoje").
		cs_window AS (
		    SELECT s.campaign_id, s.station_id,
		           SUM(s.in_slot + s.out_slot)::bigint  AS executed,
		           SUM(s.bonus)::bigint                 AS bonus
		    FROM daily_play_summary s
		    JOIN camp_meta cm ON cm.id = s.campaign_id
		    WHERE s.for_date BETWEEN GREATEST(cm.start_date, $2::date) AND LEAST(cm.end_date, $3::date)
		      AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		    GROUP BY s.campaign_id, s.station_id
		),
		-- DENOMINADOR consolidado: plano da campanha INTEIRA (independe da
		-- janela). É o que torna o investido proporcional ao período e dá a
		-- taxa estável por inserção (contrato ÷ plano_total).
		cs_plan AS (
		    SELECT s.campaign_id, s.station_id,
		           SUM(s.expected)::bigint AS plan_expected
		    FROM daily_play_summary s
		    JOIN camp_meta cm ON cm.id = s.campaign_id
		    WHERE s.for_date BETWEEN cm.start_date AND cm.end_date
		      AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		    GROUP BY s.campaign_id, s.station_id
		),
		cs_per_ins AS (
		    SELECT s.campaign_id, s.station_id,
		           COALESCE(SUM(tp.unit_value * s.expected), 0)::numeric                AS pi_contratado,
		           COALESCE(SUM(tp.unit_value * (s.in_slot + s.out_slot)), 0)::numeric AS pi_executado,
		           COALESCE(SUM(tp.unit_value * s.bonus), 0)::numeric                  AS pi_bonus
		    FROM daily_play_summary s
		    JOIN camp_meta cm ON cm.id = s.campaign_id
		    JOIN campaign_station_type_pricing tp
		      ON tp.campaign_id = s.campaign_id
		     AND tp.station_id  = s.station_id
		     AND tp.type_id     = s.type_id
		    WHERE s.for_date BETWEEN GREATEST(cm.start_date, $2::date) AND LEAST(cm.end_date, $3::date)
		      AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		    GROUP BY s.campaign_id, s.station_id
		),
		final AS (
		    SELECT
		        csp.campaign_id, csp.station_id, csp.mode,
		        cm.overlap_days, cm.total_days,
		        COALESCE(csp.consolidated_value, 0)::numeric AS consolidated_value,
		        COALESCE(w.executed, 0)::numeric       AS executed,
		        COALESCE(w.bonus,    0)::bigint         AS bonus,
		        COALESCE(pl.plan_expected, 0)::numeric  AS plan_expected,
		        COALESCE(pi.pi_contratado, 0)::numeric AS pi_contratado,
		        COALESCE(pi.pi_executado,  0)::numeric AS pi_executado,
		        COALESCE(pi.pi_bonus,      0)::numeric AS pi_bonus
		    FROM campaign_station_pricing csp
		    JOIN camp_meta cm ON cm.id = csp.campaign_id
		    LEFT JOIN cs_window  w  ON w.campaign_id  = csp.campaign_id AND w.station_id  = csp.station_id
		    LEFT JOIN cs_plan    pl ON pl.campaign_id = csp.campaign_id AND pl.station_id = csp.station_id
		    LEFT JOIN cs_per_ins pi ON pi.campaign_id = csp.campaign_id AND pi.station_id = csp.station_id
		    WHERE csp.campaign_id = ANY($1::uuid[])
		      AND ($4::uuid[] = '{}' OR csp.station_id = ANY($4::uuid[]))
		)
		SELECT
		    COALESCE(SUM(
		        CASE
		            WHEN mode='consolidated' AND total_days > 0 THEN consolidated_value * overlap_days::numeric / total_days
		            WHEN mode='per_insertion' THEN pi_contratado
		            ELSE 0
		        END
		    ), 0)::float8 AS contratado,
		    COALESCE(SUM(
		        CASE
		            -- Proporcional: entregue_na_janela ÷ plano_da_campanha_inteira.
		            -- Cap em 100%: over-delivery não infla o investido (vai pra bônus).
		            WHEN mode='consolidated' AND plan_expected > 0 THEN consolidated_value * LEAST(1, executed / plan_expected)
		            WHEN mode='per_insertion' THEN pi_executado
		            ELSE 0
		        END
		    ), 0)::float8 AS executado,
		    COALESCE(SUM(
		        CASE
		            -- Bônus à mesma taxa estável (contrato ÷ plano_total) × excedente_na_janela.
		            WHEN mode='consolidated' AND plan_expected > 0 THEN consolidated_value * bonus::numeric / plan_expected
		            WHEN mode='per_insertion' THEN pi_bonus
		            ELSE 0
		        END
		    ), 0)::float8 AS bonificacao_valor,
		    COALESCE(SUM(bonus), 0)::bigint AS bonificacao_count
		FROM final
	`, p.CampaignIDs, p.From, p.To, p.StationIDs)

	var inv InvestidoK
	var bon BonificacaoK
	if err := row.Scan(&inv.Contratado, &inv.Executado, &bon.Valor, &bon.Count); err != nil {
		return inv, bon, err
	}
	return inv, bon, nil
}

// computeCPM aplica a regra de fixed_cpm por campanha em cima dos números
// agregados. Quando NENHUMA campanha selecionada tem fixed_cpm, devolve o
// CPM dinâmico clássico (executado / impactos × 1000) — fast path. Quando
// pelo menos uma tem fixed_cpm, faz uma query por-campanha pra calcular a
// média ponderada por impactos:
//
//	per_campaign_cpm = COALESCE(fixed_cpm, dynamic_cpm)
//	cpm_final = Σ(per_campaign_cpm × impactos) / Σ(impactos)
//
// Campanhas sem impactos não contribuem (peso zero); se a soma total de
// impactos for zero, devolve 0.
func (r *Insights) computeCPM(ctx context.Context, p InsightsParams, totalExecutado float64, totalImpactos int64) (float64, error) {
	// Fast path: nenhum CPM fixo nas campanhas selecionadas → cálculo clássico.
	var anyFixed bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
		    SELECT 1 FROM campaigns
		    WHERE id = ANY($1::uuid[]) AND fixed_cpm IS NOT NULL
		)`, p.CampaignIDs).Scan(&anyFixed); err != nil {
		return 0, err
	}
	if !anyFixed {
		if totalImpactos == 0 {
			return 0, nil
		}
		return (totalExecutado / float64(totalImpactos)) * 1000.0, nil
	}

	// Path com fixed_cpm: precisa de impactos e executado por campanha.
	// Reusa a mesma estrutura de filtros do aggregateCore + aggregateInvestment
	// mas agrupado por campaign_id em vez de agregado.
	rows, err := r.pool.Query(ctx, `
		WITH per_campaign_impactos AS (
		    SELECT d.campaign_id,
		           COALESCE(SUM(s.pmm), 0)::float8 AS impactos
		    FROM detection_attributions d
		    JOIN stations s ON s.id = d.station_id
		    WHERE d.campaign_id = ANY($1::uuid[])
		      -- conjunto "aprovado" (catalog.ApprovedDetectionsFilter) — impactos
		      -- do CPM têm que bater com veiculações_total do aggregateCore.
		      AND `+ApprovedDetectionsFilter+`
		      AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date BETWEEN $2 AND $3
		      AND s.pmm IS NOT NULL
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		    GROUP BY d.campaign_id
		),
		camp_meta AS (
		    SELECT id, start_date, end_date, fixed_cpm,
		           GREATEST(0, (LEAST(end_date, $3::date) - GREATEST(start_date, $2::date) + 1))::int AS overlap_days,
		           (end_date - start_date + 1)::int AS total_days
		    FROM campaigns
		    WHERE id = ANY($1::uuid[])
		),
		per_campaign_exec AS (
		    SELECT csp.campaign_id,
		           COALESCE(SUM(
		               CASE
		                   -- Espelha aggregateInvestment (Modelo B): entregue_janela ÷
		                   -- plano_da_campanha_inteira, cap em 100%.
		                   WHEN csp.mode='consolidated' AND COALESCE(pl.plan_expected, 0) > 0
		                       THEN csp.consolidated_value * LEAST(1, COALESCE(w.executed, 0)::numeric / pl.plan_expected::numeric)
		                   WHEN csp.mode='per_insertion'
		                       THEN COALESCE(pi.pi_executado, 0)
		                   ELSE 0
		               END
		           ), 0)::float8 AS executado
		    FROM campaign_station_pricing csp
		    JOIN camp_meta cm ON cm.id = csp.campaign_id
		    LEFT JOIN (
		        -- numerador: entregue na janela [from,to]
		        SELECT s.campaign_id, s.station_id,
		               SUM(s.in_slot + s.out_slot)::bigint AS executed
		        FROM daily_play_summary s
		        JOIN camp_meta cm2 ON cm2.id = s.campaign_id
		        WHERE s.for_date BETWEEN GREATEST(cm2.start_date, $2::date) AND LEAST(cm2.end_date, $3::date)
		          AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		        GROUP BY s.campaign_id, s.station_id
		    ) w ON w.campaign_id = csp.campaign_id AND w.station_id = csp.station_id
		    LEFT JOIN (
		        -- denominador: plano da campanha inteira
		        SELECT s.campaign_id, s.station_id,
		               SUM(s.expected)::bigint AS plan_expected
		        FROM daily_play_summary s
		        JOIN camp_meta cm2 ON cm2.id = s.campaign_id
		        WHERE s.for_date BETWEEN cm2.start_date AND cm2.end_date
		          AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		        GROUP BY s.campaign_id, s.station_id
		    ) pl ON pl.campaign_id = csp.campaign_id AND pl.station_id = csp.station_id
		    LEFT JOIN (
		        SELECT s.campaign_id, s.station_id,
		               COALESCE(SUM(tp.unit_value * (s.in_slot + s.out_slot)), 0)::numeric AS pi_executado
		        FROM daily_play_summary s
		        JOIN camp_meta cm2 ON cm2.id = s.campaign_id
		        JOIN campaign_station_type_pricing tp
		          ON tp.campaign_id = s.campaign_id
		         AND tp.station_id  = s.station_id
		         AND tp.type_id     = s.type_id
		        WHERE s.for_date BETWEEN GREATEST(cm2.start_date, $2::date) AND LEAST(cm2.end_date, $3::date)
		          AND ($4::uuid[] = '{}' OR s.station_id = ANY($4::uuid[]))
		        GROUP BY s.campaign_id, s.station_id
		    ) pi ON pi.campaign_id = csp.campaign_id AND pi.station_id = csp.station_id
		    WHERE ($4::uuid[] = '{}' OR csp.station_id = ANY($4::uuid[]))
		    GROUP BY csp.campaign_id
		)
		SELECT cm.id,
		       cm.fixed_cpm,
		       COALESCE(pi.impactos, 0)::float8  AS impactos,
		       COALESCE(pe.executado, 0)::float8 AS executado
		FROM camp_meta cm
		LEFT JOIN per_campaign_impactos pi ON pi.campaign_id = cm.id
		LEFT JOIN per_campaign_exec     pe ON pe.campaign_id = cm.id
	`, p.CampaignIDs, p.From, p.To, p.StationIDs)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var weightedSum, totalWeight float64
	for rows.Next() {
		var id uuid.UUID
		var fixed *float64
		var impactos, executado float64
		if err := rows.Scan(&id, &fixed, &impactos, &executado); err != nil {
			return 0, err
		}
		if impactos <= 0 {
			continue
		}
		var perCPM float64
		if fixed != nil {
			perCPM = *fixed
		} else {
			perCPM = (executado / impactos) * 1000.0
		}
		weightedSum += perCPM * impactos
		totalWeight += impactos
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if totalWeight == 0 {
		return 0, nil
	}
	return weightedSum / totalWeight, nil
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
		SELECT id, name, start_date::text, end_date::text, status
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
		if err := rows.Scan(&b.ID, &b.Name, &b.StartDate, &b.EndDate, &b.Status); err != nil {
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
