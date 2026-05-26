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

// coreAggregates é o resultado interno usado pelo Compute().
type coreAggregates struct {
	Impactos         int64
	VeiculacoesTotal int64
	StationsCount    int
	StationsWithPMM  int
	Gender           GenderK
	Class            ClassPyramidData
	Ages             AgeRangesData
	Breakdown        VeiculacoesBreakdownData
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
		    SELECT d.id, d.station_id, d.category
		    FROM detections d
		    WHERE d.campaign_id = ANY($1::uuid[])
		      AND d.retracted_at IS NULL
		      AND d.detected_at::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		),
		per_station AS (
		    SELECT f.station_id,
		           COUNT(*)::bigint AS det_count,
		           COUNT(*) FILTER (WHERE f.category='in_slot')::bigint  AS in_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_slot')::bigint AS out_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_date')::bigint AS out_date_n,
		           COUNT(*) FILTER (WHERE f.category='orphan')::bigint   AS orphan_n
		    FROM filt f
		    GROUP BY f.station_id
		),
		joined AS (
		    SELECT ps.*,
		           s.pmm,
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
		)
		-- Atenção: percentuais em metadata.audience_profile estão em escala
		-- 0-100 (não 0-1). Por isso multiplicamos por (pct / 100.0).
		SELECT
		    COALESCE(SUM(det_count), 0)::bigint                                  AS veic_total,
		    COUNT(*)::int                                                        AS stations_count,
		    COUNT(*) FILTER (WHERE pmm IS NOT NULL)::int                         AS stations_with_pmm,
		    COALESCE(SUM(det_count * pmm) FILTER (WHERE pmm IS NOT NULL), 0)::bigint                                  AS impactos,
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
		&out.VeiculacoesTotal, &out.StationsCount, &out.StationsWithPMM,
		&out.Impactos,
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
func (r *Insights) aggregateBuckets(ctx context.Context, p InsightsParams) ([]BucketRow, string, error) {
	days := int(p.To.Sub(p.From).Hours()/24) + 1
	gran := "day"
	// daily_play_summary key + detection grouping expression. Strings paralelas
	// pq summary tem for_date::date e detections tem detected_at::timestamptz.
	summaryBucket := "for_date::text"
	detectionBucket := "(date_trunc('day', detected_at AT TIME ZONE 'America/Sao_Paulo')::date)::text"
	if days > 31 {
		gran = "month"
		summaryBucket = "to_char(for_date, 'YYYY-MM')"
		detectionBucket = "to_char(date_trunc('day', detected_at AT TIME ZONE 'America/Sao_Paulo'), 'YYYY-MM')"
	}

	query := fmt.Sprintf(`
		WITH agg AS (
		    SELECT %s AS bucket,
		           SUM(expected)::int  AS programado,
		           SUM(in_slot)::int   AS in_slot,
		           SUM(out_slot)::int  AS out_slot,
		           SUM(out_date)::int  AS out_date,
		           GREATEST(0, SUM(expected) - SUM(in_slot) - SUM(out_slot))::int AS deficit
		    FROM daily_play_summary
		    WHERE campaign_id = ANY($1::uuid[])
		      AND for_date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR station_id = ANY($4::uuid[]))
		    GROUP BY 1
		),
		orphan AS (
		    SELECT %s AS bucket,
		           COUNT(*)::int AS extras
		    FROM detections
		    WHERE campaign_id = ANY($1::uuid[])
		      AND retracted_at IS NULL
		      AND category = 'orphan'
		      AND detected_at::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR station_id = ANY($4::uuid[]))
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
//   - mode = 'consolidated':
//     * contratado  = consolidated_value × overlap_days / total_days
//     * executado   = consolidated_value × (in_slot+out_slot) / expected
//                     (zero quando expected=0)
//     * bonificação = consolidated_value × bonus / expected  (avg-per-slot)
//
//   - mode = 'per_insertion':
//     * contratado  = Σ_type (unit_value × expected)
//     * executado   = Σ_type (unit_value × (in_slot+out_slot))
//     * bonificação = Σ_type (unit_value × bonus)
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
		cs_totals AS (
		    SELECT s.campaign_id, s.station_id,
		           SUM(s.expected)::bigint                   AS expected,
		           SUM(s.in_slot + s.out_slot)::bigint       AS executed,
		           SUM(s.bonus)::bigint                      AS bonus
		    FROM daily_play_summary s
		    JOIN camp_meta cm ON cm.id = s.campaign_id
		    WHERE s.for_date BETWEEN GREATEST(cm.start_date, $2::date) AND LEAST(cm.end_date, $3::date)
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
		        COALESCE(t.expected, 0)::numeric  AS expected,
		        COALESCE(t.executed, 0)::numeric  AS executed,
		        COALESCE(t.bonus,    0)::bigint   AS bonus,
		        COALESCE(pi.pi_contratado, 0)::numeric AS pi_contratado,
		        COALESCE(pi.pi_executado,  0)::numeric AS pi_executado,
		        COALESCE(pi.pi_bonus,      0)::numeric AS pi_bonus
		    FROM campaign_station_pricing csp
		    JOIN camp_meta cm ON cm.id = csp.campaign_id
		    LEFT JOIN cs_totals  t  ON t.campaign_id  = csp.campaign_id AND t.station_id  = csp.station_id
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
		            WHEN mode='consolidated' AND expected > 0 THEN consolidated_value * executed / expected
		            WHEN mode='per_insertion' THEN pi_executado
		            ELSE 0
		        END
		    ), 0)::float8 AS executado,
		    COALESCE(SUM(
		        CASE
		            WHEN mode='consolidated' AND expected > 0 THEN consolidated_value * bonus::numeric / expected
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
