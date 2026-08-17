package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Test fixtures ──────────────────────────────────────────────────────────
//
// Os helpers abaixo são privados a este arquivo. Outros _test.go usam padrões
// equivalentes (ver seedAirtimeFixture em detections_test.go). A escolha de
// duplicar localmente em vez de extrair em testhelpers_test.go é deliberada:
// os fixtures aqui são opinionados sobre PMM e perfil demográfico, que não
// fazem sentido em todos os outros suites.

func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(fmt.Sprintf("parseDate %q: %v", s, err))
	}
	return t
}

// insSeedClient cria um cliente e registra cleanup.
func insSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: name})
	if err != nil {
		t.Fatalf("seed client: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID) })
	return cli.ID
}

// insSeedCampaign cria uma campanha vinculada a um cliente.
func insSeedCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID, start, end string) uuid.UUID {
	t.Helper()
	camp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name:           "C-" + start,
		ClientID:       clientID,
		StartDate:      parseDate(start),
		EndDate:        parseDate(end),
		TargetStations: []uuid.UUID{}, // Create insere a coluna explicitamente; nil → NULL viola NOT NULL
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", camp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", camp.ID)
		pool.Exec(ctx, "DELETE FROM campaigns_pricing WHERE campaign_id = $1", camp.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", camp.ID)
	})
	return camp.ID
}

// insSeedStation cria uma estação com PMM + audience_profile completos.
// Percentuais em escala 0-100 (não 0-1) — espelha o formato real em
// stations.metadata.audience_profile. Cada dimensão (gender, social_class,
// age_ranges) deve somar ~100.
func insSeedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, pmm float64,
	malePct, femalePct, abPct, cPct, dePct, r18Pct, r25Pct, r50Pct float64) uuid.UUID {
	t.Helper()
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: name, Band: "FM",
		StreamURL: "http://test/" + strings.ReplaceAll(name, " ", "-") + "/" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("seed station: %v", err)
	}
	meta := fmt.Sprintf(`{
		"audience_profile": {
			"gender":      {"male": %f, "female": %f},
			"socialClass": {"classeAB": %f, "classeC": %f, "classeDE": %f},
			"ageRanges":   {"range18to24": %f, "range25to49": %f, "range50plus": %f}
		}
	}`, malePct, femalePct, abPct, cPct, dePct, r18Pct, r25Pct, r50Pct)
	if _, err := pool.Exec(ctx,
		"UPDATE stations SET pmm = $1, metadata = $2::jsonb WHERE id = $3",
		pmm, meta, stat.ID,
	); err != nil {
		t.Fatalf("seed station metadata: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM station_thresholds WHERE station_id = $1", stat.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})
	return stat.ID
}

// insSeedStationNoProfile cria estação sem PMM nem perfil — para testar exclusão.
func insSeedStationNoProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: name, Band: "FM",
		StreamURL: "http://test/" + strings.ReplaceAll(name, " ", "-") + "/" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("seed station: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM station_thresholds WHERE station_id = $1", stat.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})
	return stat.ID
}

// insSeedTypeAndMaterial cria um material_type + material para um cliente.
// Necessário pra criar detections (FK obrigatória) e distribution_rules
// (que referenciam o type).
func insSeedTypeAndMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID, label string) (typeID, materialID uuid.UUID) {
	t.Helper()
	typeID = uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`,
		typeID, label+"-"+typeID.String()[:8],
	); err != nil {
		t.Fatalf("seed material_type: %v", err)
	}
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID:          clientID,
		Title:             label,
		TypeID:            &typeID,
		DurationSeconds:   30,
		MasterStoragePath: "/tmp/" + label,
		MasterSHA256:      "ins-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("seed material: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM material_types WHERE id = $1", typeID)
	})
	return typeID, mat.ID
}

// insSeedDetection insere uma detection com category explícita, bypassando
// o categorizer. Detected_at é tratado como YYYY-MM-DD (12:00 UTC).
func insSeedDetection(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID, materialID, stationID uuid.UUID, category, dateISO string) {
	t.Helper()
	ts := parseDate(dateISO).Add(12 * time.Hour)
	var detID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
		RETURNING id
	`, stationID, materialID, campaignID, ts, category).Scan(&detID)
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	// F-119: a grade lê detection_campaigns. Raw insert bypassa o Create, então
	// semeia a projeção canônica aqui (mesma categoria da detecção).
	if _, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)
	`, detID, ts, campaignID, materialID, category); err != nil {
		t.Fatalf("seed projection: %v", err)
	}
}

// insSeedStationPricing insere uma linha em campaign_station_pricing.
// Pra mode='consolidated', consolidated >= 0; pra 'per_insertion' deve ser 0 (NULL).
// Cleanup é tratado pelo CASCADE da campanha (ver insSeedCampaign).
func insSeedStationPricing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID, stationID uuid.UUID, mode string, consolidated float64) {
	t.Helper()
	var consPtr *float64
	if mode == "consolidated" {
		consPtr = &consolidated
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_station_pricing(campaign_id, station_id, mode, consolidated_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (campaign_id, station_id) DO UPDATE
		    SET mode = EXCLUDED.mode,
		        consolidated_value = EXCLUDED.consolidated_value
	`, campaignID, stationID, mode, consPtr)
	if err != nil {
		t.Fatalf("seed campaign_station_pricing: %v", err)
	}
}

// insSeedTypePricing insere unit_value para (campaign, station, type).
// Necessário pra modo per_insertion.
func insSeedTypePricing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID, stationID, typeID uuid.UUID, unitValue float64) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_station_type_pricing(campaign_id, station_id, type_id, unit_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (campaign_id, station_id, type_id) DO UPDATE
		    SET unit_value = EXCLUDED.unit_value
	`, campaignID, stationID, typeID, unitValue)
	if err != nil {
		t.Fatalf("seed campaign_station_type_pricing: %v", err)
	}
}

// insSeedDistributionRule cria uma regra distributiva via repo (que valida).
// weekdayMask: bit 0=domingo … bit 6=sábado. 0b1111111 = todos os dias.
func insSeedDistributionRule(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID, typeID, stationID uuid.UUID, start, end string, weekdayMask int, timeStart, timeEnd string, playsPerDay int) {
	t.Helper()
	_, err := NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID:  campaignID,
		TypeID:      typeID,
		StationIDs:  []uuid.UUID{stationID},
		MaterialIDs: []uuid.UUID{}, // vazio = todos os materiais (material_ids NOT NULL, migration 0043)
		StartDate:   parseDate(start),
		EndDate:     parseDate(end),
		WeekdayMask: int16(weekdayMask),
		TimeStart:   timeStart,
		TimeEnd:     timeEnd,
		PlaysPerDay: int16(playsPerDay),
	})
	if err != nil {
		t.Fatalf("seed distribution_rule: %v", err)
	}
}

// ─── Sanity: ensure fixtures shape what we think ────────────────────────────

func TestInsights_Fixture_StationMetaShape(t *testing.T) {
	ctx, pool := newTestDB(t)
	st := insSeedStation(t, ctx, pool, "FixtureCheck", 1234, 60, 40, 20, 50, 30, 30, 50, 20)

	var pmm float64
	var metaRaw string
	if err := pool.QueryRow(ctx, "SELECT pmm, metadata::text FROM stations WHERE id = $1", st).Scan(&pmm, &metaRaw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if pmm != 1234 {
		t.Fatalf("pmm = %v want 1234", pmm)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metaRaw), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ap, _ := parsed["audience_profile"].(map[string]any)
	if ap == nil {
		t.Fatalf("audience_profile missing in %s", metaRaw)
	}
	g, _ := ap["gender"].(map[string]any)
	if g["male"].(float64) != 60 {
		t.Fatalf("male pct = %v", g["male"])
	}
}

// ─── fetchCampaigns ─────────────────────────────────────────────────────────

func TestInsights_FetchCampaigns_ReturnsBriefs(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)
	client := insSeedClient(t, ctx, pool, "X")
	c1 := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	c2 := insSeedCampaign(t, ctx, pool, client, "2026-07-01", "2026-07-31")

	briefs, err := repo.fetchCampaigns(ctx, client, []uuid.UUID{c1, c2})
	if err != nil {
		t.Fatalf("fetchCampaigns: %v", err)
	}
	if len(briefs) != 2 {
		t.Fatalf("len = %d, want 2", len(briefs))
	}
	if briefs[0].StartDate != "2026-06-01" {
		t.Fatalf("first start = %q, want 2026-06-01", briefs[0].StartDate)
	}
}

func TestInsights_FetchCampaigns_RejectsCrossClient(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)
	cliA := insSeedClient(t, ctx, pool, "A")
	cliB := insSeedClient(t, ctx, pool, "B")
	cA := insSeedCampaign(t, ctx, pool, cliA, "2026-06-01", "2026-06-30")
	cB := insSeedCampaign(t, ctx, pool, cliB, "2026-06-01", "2026-06-30")

	_, err := repo.fetchCampaigns(ctx, cliA, []uuid.UUID{cA, cB})
	if err == nil {
		t.Fatal("expected error on cross-client request, got nil")
	}
	if !strings.Contains(err.Error(), "cross-client") {
		t.Fatalf("error should mention cross-client: %v", err)
	}
}

// ─── aggregateCore ──────────────────────────────────────────────────────────

// BASE DE IMPACTOS = pmm × (in_slot + bonus). Este teste é a rede de segurança
// da padronização: o fixture tem 5 in_slot + 2 out_slot + 1 bonus, e as 2
// out_slot NÃO podem entrar em impactos nem nos rateios demográficos (D3 —
// tocada fora da faixa contratada não vale nada comercialmente). Se alguém
// voltar a base pra det_count (todas as categorias), Impactos vai de 6000 pra
// 8000 e este teste quebra.
//
// veiculacoes_total continua sendo 8: é o KPI de CONTAGEM, exibido junto do
// breakdown por categoria, e portanto tem que somar as quatro.
func TestInsights_AggregateCore_ImpactosAndDemographics(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")

	// PMM=1000, gender M=60% F=40%, AB=20% C=50% DE=30%, age 30/50/20%
	st := insSeedStation(t, ctx, pool, "RadioX", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	for i := 0; i < 5; i++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	}
	for i := 0; i < 2; i++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "out_slot", "2026-06-11")
	}
	// 'bonus' é a categoria do excedente/sem-plano desde 0064 ('orphan' era o
	// nome antigo). O breakdown do /insights conta essa categoria — semear
	// 'orphan' aqui deixava o teste verde só porque a query também procurava
	// 'orphan'; os dois lados errados de forma consistente.
	insSeedDetection(t, ctx, pool, camp, mat, st, "bonus", "2026-06-12")

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")
	core, err := repo.aggregateCore(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: from, To: to, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	// (5 in_slot + 1 bonus) × 1000 = 6000 impactos. As 2 out_slot ficam fora.
	if core.Impactos != 6000 {
		t.Errorf("impactos = %d, want 6000 (6 × 1000; as 2 out_slot não são impacto)", core.Impactos)
	}
	// Contagem de veiculações continua somando as 4 categorias (8).
	if core.VeiculacoesTotal != 8 {
		t.Errorf("veic = %d, want 8", core.VeiculacoesTotal)
	}
	// Gender M = 6000 × 60% = 3600 — rateio do MESMO total de impactos.
	if core.Gender.M != 3600 {
		t.Errorf("gender_m = %d, want 3600", core.Gender.M)
	}
	if core.Gender.F != 2400 {
		t.Errorf("gender_f = %d, want 2400", core.Gender.F)
	}
	// Os splits demográficos têm que fechar de volta no total de impactos —
	// senão o gráfico e o KPI do topo da mesma tela contam coisas diferentes.
	if core.Gender.M+core.Gender.F != core.Impactos {
		t.Errorf("gender M+F = %d, want == impactos %d", core.Gender.M+core.Gender.F, core.Impactos)
	}
	if core.Class.AB+core.Class.C+core.Class.DE != core.Impactos {
		t.Errorf("class AB+C+DE = %d, want == impactos %d",
			core.Class.AB+core.Class.C+core.Class.DE, core.Impactos)
	}
	// AB = 6000 × 20% = 1200
	if core.Class.AB != 1200 {
		t.Errorf("class_ab = %d, want 1200", core.Class.AB)
	}
	// Breakdown
	if core.Breakdown.InSlot != 5 || core.Breakdown.OutSlot != 2 || core.Breakdown.ExtrasOrphan != 1 {
		t.Errorf("breakdown = %+v", core.Breakdown)
	}
}

// ─── aggregateBuckets ───────────────────────────────────────────────────────

func TestInsights_AggregateBuckets_DailyGranularity(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	// 1 play/dia programado
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	// dia 10: 2 in_slot
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	// dia 11: 1 out_slot
	insSeedDetection(t, ctx, pool, camp, mat, st, "out_slot", "2026-06-11")
	// dia 12: 1 bonus (ex-'orphan', renomeada na 0064) → vira "extras" no gráfico
	insSeedDetection(t, ctx, pool, camp, mat, st, "bonus", "2026-06-12")

	buckets, gran, err := repo.aggregateBuckets(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-10"), To: parseDate("2026-06-12"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateBuckets: %v", err)
	}
	if gran != "day" {
		t.Errorf("gran = %q, want day", gran)
	}
	if len(buckets) != 3 {
		t.Fatalf("buckets = %d, want 3 (10/11/12). got=%+v", len(buckets), buckets)
	}
	// 10: programado=1, in_slot=2, deficit=0 (max(0, 1-2-0))
	if buckets[0].Bucket != "2026-06-10" || buckets[0].InSlot != 2 || buckets[0].Programado != 1 {
		t.Errorf("day 10: %+v", buckets[0])
	}
	// 11: programado=1, out_slot=1, deficit=1. D3 (0065 + Task 7): out_slot NÃO
	// abate o contrato — antes era max(0, 1-0-1) = 0 e o dia aparecia cumprido
	// mesmo tendo tocado só fora da faixa contratada.
	if buckets[1].Bucket != "2026-06-11" || buckets[1].OutSlot != 1 || buckets[1].Deficit != 1 {
		t.Errorf("day 11: %+v (out_slot não pode abater o déficit)", buckets[1])
	}
	// 12: programado=1, extras=1 (bonus), deficit=1 (1-0)
	if buckets[2].Bucket != "2026-06-12" || buckets[2].Extras != 1 || buckets[2].Deficit != 1 {
		t.Errorf("day 12: %+v", buckets[2])
	}
}

// ─── Compute end-to-end ─────────────────────────────────────────────────────

func TestInsights_Compute_EndToEnd(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 2000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 100.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	for i := 0; i < 10; i++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-15")
	}

	out, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if out.KPIs.Impactos != 20000 {
		t.Errorf("impactos = %d, want 20000", out.KPIs.Impactos)
	}
	if out.KPIs.VeiculacoesTotal != 10 {
		t.Errorf("veic = %d, want 10", out.KPIs.VeiculacoesTotal)
	}
	if out.KPIs.Investido.Executado < 999 || out.KPIs.Investido.Executado > 1001 {
		t.Errorf("executado = %v, want ~1000", out.KPIs.Investido.Executado)
	}
	// CPM = (1000 / 20000) × 1000 = 50.0
	if out.KPIs.CPM < 49 || out.KPIs.CPM > 51 {
		t.Errorf("cpm = %v, want ~50", out.KPIs.CPM)
	}
	if out.Period.Granularity != "day" {
		t.Errorf("granularity = %q, want day", out.Period.Granularity)
	}
	if len(out.Campaigns) != 1 {
		t.Errorf("campaigns = %d, want 1", len(out.Campaigns))
	}
}

func TestInsights_AggregateBuckets_MonthlyGranularity(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-01-01", "2026-12-31")

	_, gran, err := repo.aggregateBuckets(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-01-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateBuckets: %v", err)
	}
	if gran != "month" {
		t.Errorf("gran = %q, want month (period > 31 days)", gran)
	}
}

// ─── aggregateInvestment ────────────────────────────────────────────────────

// TestInsights_AggregateInvestment_PerInsertion verifica que o modo
// per_insertion soma unit_value × expected (contratado) e unit_value ×
// (in_slot+out_slot) (executado), via daily_play_summary.
//
// Nota: este teste cria distribution_rules (programado) para que o view
// daily_play_summary tenha "expected" populado. Sem isso, expected=0 e o
// resultado fica vazio.
func TestInsights_AggregateInvestment_PerInsertion(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	// Pricing: per_insertion @ R$ 50 (apenas no type/station)
	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 50.0)

	// Programado: 1 play/dia × 30 dias = 30 expected
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	// 6 executadas em 6 dias distintos (1/dia). bonus é POR DIA:
	// max(0, in_slot_dia - expected_dia). Com 1 in_slot/dia e 1 expected/dia,
	// bonus=0. (Colocar as 6 no mesmo dia daria bonus=5 — a view é per-day.)
	for d := 1; d <= 6; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")
	inv, bon, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: from, To: to, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}

	// Contratado per_insertion = 50 × 30 = 1500
	if inv.Contratado < 1499 || inv.Contratado > 1501 {
		t.Errorf("contratado = %v, want ~1500", inv.Contratado)
	}
	// Executado per_insertion = 50 × 6 = 300
	if inv.Executado < 299 || inv.Executado > 301 {
		t.Errorf("executado = %v, want ~300", inv.Executado)
	}
	// bonus da view = COUNT(category = 'bonus') = 0 desde a 0065 (todas as
	// 6 tocadas são in_slot; 1 in_slot/dia == 1 expected/dia nos 6 dias).
	if bon.Count != 0 || bon.Valor != 0 {
		t.Errorf("bonificacao = %+v, want zero", bon)
	}
}

func TestInsights_AggregateCore_StationWithoutPMM(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")

	stOK := insSeedStation(t, ctx, pool, "OK", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	stNoPMM := insSeedStationNoProfile(t, ctx, pool, "SemPerfil")

	insSeedDetection(t, ctx, pool, camp, mat, stOK, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stNoPMM, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stNoPMM, "in_slot", "2026-06-11")

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")
	core, err := repo.aggregateCore(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: from, To: to, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	if core.Impactos != 1000 { // só stOK contribui
		t.Errorf("impactos = %d, want 1000", core.Impactos)
	}
	if core.VeiculacoesTotal != 3 { // todas contam como veiculações
		t.Errorf("veic = %d, want 3", core.VeiculacoesTotal)
	}
	if core.StationsCount != 2 {
		t.Errorf("stations = %d, want 2", core.StationsCount)
	}
	if core.StationsWithPMM != 1 {
		t.Errorf("stations_with_pmm = %d, want 1", core.StationsWithPMM)
	}
}

// ─── consolidated: proporcional ao período (Modelo B, fix 2026-07-08) ───────
//
// Ver docs/superpowers/specs/2026-07-08-insights-consolidated-period-proportional-design.md.
// O Investido/Bonificação consolidado é proporcional ao período selecionado:
//
//	executado = contrato × min(1, entregue_na_janela ÷ plano_da_campanha_INTEIRA)
//	bonus     = contrato × (bonus_na_janela ÷ plano_da_campanha_INTEIRA)
//
// O denominador é o plano da campanha inteira (fixo), NÃO da janela. Assim o
// número escala com o período (junho = fração; período todo = contrato) e é
// monotônico: alargar a janela pra dias ainda-não-veiculados não muda nada
// (entrega 0), e nunca decresce. Over-delivery capa no contrato e vai só pra
// bonificação. Não há clamp de "hoje" — dia futuro entrega 0 no numerador.

// approxEq compara floats com tolerância absoluta.
func approxEq(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// Proporcional ao período: contrato 3000, plano 1/dia em junho (30 do plano
// cheio). Entregue 1/dia nos 30 dias. Meia janela (até 15/06) = 15/30 do
// contrato = 1500; janela cheia = 30/30 = 3000. Escala com o período.
func TestInsights_Investment_Consolidated_PeriodProportional(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 3000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 30; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	half, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment (half): %v", err)
	}
	full, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment (full): %v", err)
	}
	if !approxEq(half.Executado, 1500, 1) {
		t.Errorf("meia janela: executado = %v, want ~1500 (15/30 do contrato)", half.Executado)
	}
	if !approxEq(full.Executado, 3000, 1) {
		t.Errorf("janela cheia: executado = %v, want ~3000 (30/30 do contrato)", full.Executado)
	}
	if full.Executado <= half.Executado {
		t.Errorf("deveria crescer com o período: half=%v full=%v", half.Executado, full.Executado)
	}
}

// Monotônico: alargar a janela pra incluir dias ainda-não-veiculados não muda
// o Investido (entrega 0 nesses dias). Contrato 3000, plano 1/dia 30 dias,
// entregue só nos dias 01–15. Janela até 15/06 e janela até 30/06 dão o MESMO
// valor (15/30 = 1500) — não infla nem deflaciona.
func TestInsights_Investment_Consolidated_UndeliveredDaysDoNotDeflate(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 3000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 15; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	toHalf, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment (half): %v", err)
	}
	toFull, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment (full): %v", err)
	}
	if !approxEq(toHalf.Executado, 1500, 1) || !approxEq(toFull.Executado, 1500, 1) {
		t.Errorf("dias não-veiculados deflacionaram: half=%v full=%v, want ambos ~1500", toHalf.Executado, toFull.Executado)
	}
}

// Over-delivery capa no contrato e vai pra bonificação, não pro investido.
// Contrato 1000, plano 1/dia dias 01–10 (plano cheio = 10). Entregue 2/dia
// (20 executados, 10 de excedente). executado = 1000 × min(1, 20/10) = 1000
// (capa). Bonificação = 1000 × 10/10 = 1000 (o excedente, à taxa do plano cheio).
func TestInsights_Investment_Consolidated_CapsAtContractOverDeliveryToBonus(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-10", 0b1111111, "00:00:00", "23:59:00", 1)

	// 2 tocadas/dia nos dias 01–10 contra plano de 1/dia → excedente de 1/dia.
	// No modelo de cota (0065) o excedente é gravado como 'bonus' pelo próprio
	// categorizador — in_slot nunca passa de expected. Semear as duas como
	// 'in_slot' produziria um estado que produção não gera mais.
	for d := 1; d <= 10; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
		insSeedDetection(t, ctx, pool, camp, mat, st, "bonus", fmt.Sprintf("2026-06-%02d", d))
	}

	inv, bon, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	if !approxEq(inv.Executado, 1000, 1) {
		t.Errorf("executado = %v, want ~1000 (capado no contrato, não 2000)", inv.Executado)
	}
	if !approxEq(bon.Valor, 1000, 1) {
		t.Errorf("bonificacao.valor = %v, want ~1000 (o excedente)", bon.Valor)
	}
	if bon.Count != 10 {
		t.Errorf("bonificacao.count = %d, want 10", bon.Count)
	}
}

// Déficit reduz o executado (o cap não vira piso): min(1, x) não floora x<1.
// Contrato 1000, plano cheio 10, entregue só 4 → 1000 × min(1, 4/10) = 400.
func TestInsights_Investment_Consolidated_DeficitReduces(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-10", 0b1111111, "00:00:00", "23:59:00", 1)

	for d := 1; d <= 4; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	inv, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	if !approxEq(inv.Executado, 400, 1) {
		t.Errorf("executado = %v, want ~400 (4/10 do contrato)", inv.Executado)
	}
}

// per_insertion (aditivo) não é afetado pela mudança de denominador: contratado
// = unit × plano da janela, executado = unit × entregue. Guard de regressão.
func TestInsights_Investment_PerInsertion_Additive(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 50.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	for d := 1; d <= 6; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	inv, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	if !approxEq(inv.Contratado, 1500, 1) { // 50 × 30 esperados
		t.Errorf("contratado = %v, want ~1500", inv.Contratado)
	}
	if !approxEq(inv.Executado, 300, 1) { // 50 × 6
		t.Errorf("executado = %v, want ~300", inv.Executado)
	}
}

// Regra do fornecedor (Compute): campanha com QUALQUER emissora consolidada
// mostra no Investido o valor TOTAL contratado (fixo — NÃO cresce com o
// período), zera a Bonificação e marca Consolidated=true. Contrato 2000.
func TestInsights_Compute_Consolidated_ShowsFixedTotal(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 2000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 15; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	half, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-15"), StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute half: %v", err)
	}
	full, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"), StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute full: %v", err)
	}

	if !half.Consolidated || !full.Consolidated {
		t.Errorf("Consolidated deveria ser true (half=%v full=%v)", half.Consolidated, full.Consolidated)
	}
	// Total fixo (2000) independente do período.
	if !approxEq(half.KPIs.Investido.Executado, 2000, 1) || !approxEq(full.KPIs.Investido.Executado, 2000, 1) {
		t.Errorf("Investido deveria ser o total fixo ~2000 em qualquer janela: half=%v full=%v",
			half.KPIs.Investido.Executado, full.KPIs.Investido.Executado)
	}
	// Bonificação zerada.
	if half.KPIs.Bonificacao.Valor != 0 || half.KPIs.Bonificacao.Count != 0 {
		t.Errorf("Bonificação deveria estar zerada em consolidado: %+v", half.KPIs.Bonificacao)
	}
}

// Campanha 100% por-inserção: NÃO é consolidada — segue por veiculação, com
// Bonificação normal e Consolidated=false.
func TestInsights_Compute_PerInsertion_NotConsolidated(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 2000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 100.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	// 10 tocadas num dia de plano 1 → 1 preenche a cota e 9 são bônus. É assim
	// que o categorizador de cota grava desde 0065; antes as 10 nasciam
	// 'in_slot' e a view sintetizava o bônus pelo excedente.
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-15")
	for i := 0; i < 9; i++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "bonus", "2026-06-15")
	}

	out, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"), StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if out.Consolidated {
		t.Errorf("per_insertion não deveria marcar Consolidated")
	}
	// executado por-inserção = 100 × 1 in_slot = 100. Era 1000 antes de 0065,
	// quando as 10 tocadas nasciam 'in_slot' e o investido faturava o excedente
	// junto. No modelo de cota só a tocada que preenche a meta fatura; as outras
	// 9 são bonificação (abaixo) — o valor entregue não muda de lugar, muda de
	// KPI. (Este número não se move na Task 7: o fixture não tem out_slot.)
	if !approxEq(out.KPIs.Investido.Executado, 100, 1) {
		t.Errorf("executado = %v, want ~100 (per_insertion, só o in_slot da cota)", out.KPIs.Investido.Executado)
	}
	// Bonificação continua computada (10 tocadas no dia, plano 1 → bonus 9 × 100)
	if out.KPIs.Bonificacao.Valor <= 0 {
		t.Errorf("Bonificação per_insertion deveria ser > 0, veio %v", out.KPIs.Bonificacao.Valor)
	}
}

// Consolidado ACUMULA POR MÊS: consolidated_value é MENSAL. Campanha de 3 meses
// (01/06–31/08) × R$1000/mês. O Investido cresce conforme os ciclos mensais
// começam (mês conta inteiro ao iniciar), limitado a 3, e 0 antes de começar.
func TestInsights_Compute_Consolidated_AccruesByMonth(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000) // R$1000/mês

	cases := []struct {
		today string
		want  float64
	}{
		{"2026-05-20", 0},    // antes de começar
		{"2026-06-10", 1000}, // mês 1 iniciado
		{"2026-07-15", 2000}, // mês 2 iniciado
		{"2026-08-20", 3000}, // mês 3 iniciado
		{"2026-09-05", 3000}, // depois do fim → cap em 3
	}
	for _, tc := range cases {
		out, err := repo.Compute(ctx, InsightsParams{
			ClientID: client, CampaignIDs: []uuid.UUID{camp},
			From: parseDate("2026-06-01"), To: parseDate("2026-08-31"),
			Today: parseDate(tc.today), StationIDs: []uuid.UUID{},
		})
		if err != nil {
			t.Fatalf("Compute %s: %v", tc.today, err)
		}
		if !approxEq(out.KPIs.Investido.Executado, tc.want, 1) {
			t.Errorf("hoje %s: investido = %v, want %v", tc.today, out.KPIs.Investido.Executado, tc.want)
		}
	}
}

// Filtrar um SUB-PERÍODO escopa o consolidado: campanha 3 meses × R$1000,
// filtrando só junho → 1000 (não 3000). O filtro define os meses contados.
// (today após o fim, então o escopo vem só do filtro, não do cap de hoje.)
func TestInsights_Compute_Consolidated_PeriodFilterScopes(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	cases := []struct {
		from, to string
		want     float64
	}{
		{"2026-06-01", "2026-06-30", 1000}, // só junho
		{"2026-06-01", "2026-07-31", 2000}, // jun + jul
		{"2026-06-01", "2026-08-31", 3000}, // campanha toda
		{"2026-07-01", "2026-07-31", 1000}, // só julho (mês do meio)
	}
	for _, tc := range cases {
		out, err := repo.Compute(ctx, InsightsParams{
			ClientID: client, CampaignIDs: []uuid.UUID{camp},
			From: parseDate(tc.from), To: parseDate(tc.to),
			Today: parseDate("2026-09-15"), StationIDs: []uuid.UUID{}, // após o fim
		})
		if err != nil {
			t.Fatalf("Compute %s..%s: %v", tc.from, tc.to, err)
		}
		if !approxEq(out.KPIs.Investido.Executado, tc.want, 1) {
			t.Errorf("filtro %s..%s: investido = %v, want %v", tc.from, tc.to, out.KPIs.Investido.Executado, tc.want)
		}
	}
}

// O incremento acontece na VIRADA do mês (1º de cada mês de calendário), não no
// aniversário de 30 dias. Campanha 09/06–08/07 × R$1000/mês: 1000 em junho, e
// dobra pra 2000 a partir de 01/07 (entrou em julho), mesmo durando ~1 mês.
func TestInsights_Compute_Consolidated_IncrementsAtCalendarMonthStart(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-09", "2026-07-08")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	cases := []struct {
		today string
		want  float64
	}{
		{"2026-06-20", 1000}, // junho
		{"2026-06-30", 1000}, // último dia de junho, ainda 1
		{"2026-07-01", 2000}, // dobra na virada pra julho
		{"2026-07-08", 2000}, // fim, ainda 2
	}
	for _, tc := range cases {
		out, err := repo.Compute(ctx, InsightsParams{
			ClientID: client, CampaignIDs: []uuid.UUID{camp},
			From: parseDate("2026-06-09"), To: parseDate("2026-07-08"),
			Today: parseDate(tc.today), StationIDs: []uuid.UUID{},
		})
		if err != nil {
			t.Fatalf("Compute %s: %v", tc.today, err)
		}
		if !approxEq(out.KPIs.Investido.Executado, tc.want, 1) {
			t.Errorf("hoje %s: investido = %v, want %v (virada de mês)", tc.today, out.KPIs.Investido.Executado, tc.want)
		}
	}
}

// /campaigns (FinancialsByCampaign) usa a MESMA regra: consolidado acumula por
// mês. 3 meses × R$1000, hoje mês 2 → total_invested = 2000.
func TestCampaigns_Financials_ConsolidatedAccruesByMonth(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaignsRepo := NewCampaigns(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	fins, err := campaignsRepo.FinancialsByCampaign(ctx, []uuid.UUID{client}, nil, parseDate("2026-07-15"))
	if err != nil {
		t.Fatalf("FinancialsByCampaign: %v", err)
	}
	var found bool
	for _, f := range fins {
		if f.CampaignID == camp {
			found = true
			if !approxEq(f.TotalInvested, 2000, 1) {
				t.Errorf("total_invested = %v, want ~2000 (1000/mês × 2 meses)", f.TotalInvested)
			}
		}
	}
	if !found {
		t.Fatalf("campanha %s não veio no FinancialsByCampaign", camp)
	}
}

// PARIDADE do recorte por página: pedir SÓ a campanha A tem que devolver
// exatamente a mesma linha que pedir todas. É o gate da troca de
// daily_play_summary (view, sem pushdown) por daily_play_summary_for(lo,hi,ids),
// cujo bound é [MIN(start_date), MAX(end_date)] do recorte.
//
// O cenário é montado pra o bound do recorte ser ESTRITAMENTE mais estreito
// que o global (B vai até agosto) e pra exercitar as três formas de linha que
// o bound poderia comer indevidamente:
//   - in_slot dentro do período (dias 1–3);
//   - bonus por excedente (2ª tocada do dia 1, expected=1 → bonus 1);
//   - out_date FORA do período (05/07) — tem que continuar valendo 0, que é o
//     que autoriza cortar a janela no período da campanha.
func TestCampaigns_Financials_PageSliceMatchesFullSet(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaignsRepo := NewCampaigns(pool)

	client := insSeedClient(t, ctx, pool, "X")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")

	campA := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	stA := insSeedStation(t, ctx, pool, "RA", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, campA, stA, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, campA, stA, typeID, 10.0)
	insSeedDistributionRule(t, ctx, pool, campA, typeID, stA,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 3; d++ {
		insSeedDetection(t, ctx, pool, campA, mat, stA, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}
	insSeedDetection(t, ctx, pool, campA, mat, stA, "bonus", "2026-06-01") // excedente do dia 1
	insSeedDetection(t, ctx, pool, campA, mat, stA, "out_date", "2026-07-05")

	// B só existe pra alargar o bound global nas DUAS pontas: sem recorte a
	// janela vira [01/05, 31/08], com recorte fica [01/06, 30/06]. A tocada de
	// B fica em junho porque detections só tem partição a partir de 2026-06.
	campB := insSeedCampaign(t, ctx, pool, client, "2026-05-01", "2026-08-31")
	stB := insSeedStation(t, ctx, pool, "RB", 2000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, campB, stB, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, campB, stB, typeID, 7.0)
	insSeedDistributionRule(t, ctx, pool, campB, typeID, stB,
		"2026-05-01", "2026-08-31", 0b1111111, "00:00:00", "23:59:00", 1)
	insSeedDetection(t, ctx, pool, campB, mat, stB, "in_slot", "2026-06-10")

	today := parseDate("2026-07-15")
	all, err := campaignsRepo.FinancialsByCampaign(ctx, nil, nil, today)
	if err != nil {
		t.Fatalf("FinancialsByCampaign(todas): %v", err)
	}
	var want *CampaignFinancials
	for i := range all {
		if all[i].CampaignID == campA {
			want = &all[i]
		}
	}
	if want == nil {
		t.Fatalf("campanha A não veio na chamada sem recorte")
	}
	// Sanity: o cenário tem que produzir número, senão a paridade compara zeros.
	// 3 in_slot (dias 1,2,3) × unit 10 = 30 investidos, mais 1 bonus (o
	// excedente do dia 1) × 10 = 10 de bonificação — que desde 2026-08-17 fica
	// FORA do investido (entrega gratuita). A tocada out_date de 05/07 NÃO
	// entra — é justamente o que o bound recortado também descarta.
	//
	// Era 50 antes de 0065: a view antiga contava o excedente DUAS vezes (a
	// tocada nascia 'in_slot' E o `GREATEST(in_slot - expected)` a somava de
	// novo como bônus). No modelo de cota cada tocada tem uma categoria só.
	if !approxEq(want.TotalInvested, 30, 0.01) {
		t.Fatalf("cenário inválido: invested = %v, want 30 (unit 10 × 3 in_slot; bônus não fatura)", want.TotalInvested)
	}
	if !approxEq(want.TotalBonusValue, 10, 0.01) {
		t.Fatalf("cenário inválido: bonus_value = %v, want 10 (unit 10 × 1 bonus)", want.TotalBonusValue)
	}

	page, err := campaignsRepo.FinancialsByCampaign(ctx, nil, []uuid.UUID{campA}, today)
	if err != nil {
		t.Fatalf("FinancialsByCampaign(recorte): %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("recorte devolveu %d linhas, want 1 (só a campanha pedida)", len(page))
	}
	got := page[0]
	if got.CampaignID != campA {
		t.Fatalf("recorte devolveu a campanha errada: %s", got.CampaignID)
	}
	if !approxEq(got.TotalInvested, want.TotalInvested, 0.01) {
		t.Errorf("total_invested: recorte=%v, todas=%v", got.TotalInvested, want.TotalInvested)
	}
	if !approxEq(got.TotalBonusValue, want.TotalBonusValue, 0.01) {
		t.Errorf("total_bonus_value: recorte=%v, todas=%v", got.TotalBonusValue, want.TotalBonusValue)
	}
	if got.TotalInsertions != want.TotalInsertions {
		t.Errorf("total_insertions: recorte=%d, todas=%d", got.TotalInsertions, want.TotalInsertions)
	}
	if !approxEq(got.TotalAudience, want.TotalAudience, 0.01) {
		t.Errorf("total_audience: recorte=%v, todas=%v", got.TotalAudience, want.TotalAudience)
	}
	if !approxEq(got.TotalAudienceTarget, want.TotalAudienceTarget, 0.01) {
		t.Errorf("total_audience_target: recorte=%v, todas=%v", got.TotalAudienceTarget, want.TotalAudienceTarget)
	}
	if got.StationsWithTarget != want.StationsWithTarget {
		t.Errorf("stations_with_target: recorte=%d, todas=%d", got.StationsWithTarget, want.StationsWithTarget)
	}
}

// Campanha MISTA (consolidada + por-inserção): o Investido bate com o
// /campaigns — consolidado fixo + por-inserção pelo ENTREGUE (unit×(in_slot+
// bonus)), NÃO pelo plano cheio (unit×expected). Contrato consolidado 400,
// por-inserção unit 10, plano 1/dia 30 dias mas só 3 entregues → total = 400 +
// 10×3 = 430 (não 400 + 10×30 = 700).
func TestInsights_Compute_Mixed_MatchesCampaignsFormula(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	stCons := insSeedStation(t, ctx, pool, "CONS", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	stIns := insSeedStation(t, ctx, pool, "INS", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, stCons, "consolidated", 400)
	insSeedStationPricing(t, ctx, pool, camp, stIns, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, stIns, typeID, 10.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, stCons,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, stIns,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	// por-inserção entrega 3 in_slot (1/dia) + 2 bonus (excedente do dia 1 e 2)
	for d := 1; d <= 3; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, stIns, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}
	insSeedDetection(t, ctx, pool, camp, mat, stIns, "bonus", "2026-06-01")
	insSeedDetection(t, ctx, pool, camp, mat, stIns, "bonus", "2026-06-02")

	out, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"), StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !out.Consolidated {
		t.Errorf("mista tem consolidada → Consolidated deveria ser true")
	}
	// Modo fornecedor: Investido = pacote + ENTREGUE das por-inserção (in_slot +
	// bonus, que é como o consolidatedSummary sempre precificou) = 400 + 50.
	if !approxEq(out.KPIs.Investido.Executado, 450, 1) {
		t.Errorf("investido = %v, want ~450 (400 pacote + 50 entregue; NÃO 700 = plano cheio)", out.KPIs.Investido.Executado)
	}

	// PARIDADE DE CPM EM PRICING MISTO. É aqui que a divergência histórica entre
	// as duas telas fecha: em modo fornecedor o /insights zera a Bonificação e
	// embute o bônus no Investido, enquanto o /campaigns exibe as duas parcelas
	// separadas — mas o NUMERADOR DO CPM é a mesma expressão dos dois lados:
	//
	//	/insights:  consolidatedSummary = pacote×meses + unit×(in_slot+bonus)
	//	/campaigns: total_invested + total_bonus_value
	//	          = (per_ins unit×in_slot + pacote×meses) + per_ins unit×bonus
	//
	// Sem somar `total_bonus_value` no /campaigns, o CPM daria 86,00 aqui contra
	// 90,00 no /insights — dois CPMs pra mesma campanha no mesmo período.
	campaignsRepo := NewCampaigns(pool)
	fins, err := campaignsRepo.FinancialsByCampaign(ctx, []uuid.UUID{client}, []uuid.UUID{camp}, time.Time{})
	if err != nil {
		t.Fatalf("FinancialsByCampaign: %v", err)
	}
	if len(fins) != 1 {
		t.Fatalf("FinancialsByCampaign devolveu %d linhas, want 1", len(fins))
	}
	if int64(fins[0].TotalAudience) != out.KPIs.Impactos {
		t.Errorf("impactos divergiu em pricing misto: insights %d × campaigns %v",
			out.KPIs.Impactos, fins[0].TotalAudience)
	}
	campCPM := (fins[0].TotalInvested + fins[0].TotalBonusValue) / fins[0].TotalAudience * 1000
	if !approxEq(campCPM, out.KPIs.CPM, 0.01) {
		t.Errorf("cpm divergiu em pricing misto: campaigns %v × insights %v "+
			"(invested %v + bonus_value %v ÷ audience %v)",
			campCPM, out.KPIs.CPM, fins[0].TotalInvested, fins[0].TotalBonusValue, fins[0].TotalAudience)
	}
	if !approxEq(out.KPIs.CPM, 90.0, 0.01) {
		t.Errorf("cpm = %v, want 90.00 ((400 pacote + 50 entregue) ÷ 5000 impactos × 1000)", out.KPIs.CPM)
	}
}

// PARIDADE DA BASE FINANCEIRA /insights × /campaigns (Task 7).
//
// Depois da Task 7 as duas telas leem a MESMA base — `in_slot + bonus` —, e
// desde 2026-08-17 partem essa base do MESMO jeito: o dinheiro pago
// (`in_slot`) separado da entrega gratuita (`bonus`). As identidades que travam
// isso são:
//
//	insights.Investido.Executado == campaigns.TotalInvested     (unit × in_slot)
//	insights.Bonificacao.Valor   == campaigns.TotalBonusValue   (unit × bonus)
//	insights.Impactos            == campaigns.TotalAudience
//	insights.KPIs.CPM            == (TotalInvested + TotalBonusValue) ÷ TotalAudience × 1000
//
// A quarta identidade é a definição de CPM do produto (2026-08-17): o numerador
// soma as DUAS parcelas — o CPM mede a eficiência da MÍDIA ENTREGUE A PREÇO DE
// TABELA, não a eficiência da negociação. A tocada de bônus já está no
// denominador (impactos conta in_slot + bonus), então tem que estar no numerador
// ao preço de tabela dela; senão campanha com muito bônus exibiria um CPM
// artificialmente baixo, incomparável com o de qualquer outra. Há uma asserção
// explícita abaixo que FALHA se alguém "simplificar" o numerador de volta pro
// valor pago.
//
// Antes de 2026-08-17 o /campaigns empacotava os dois num `total_invested` só,
// e a identidade era a SOMA (`Executado + Bonificação == TotalInvested`). Isso
// inflava o "Investimento" com veiculação que o cliente não pagou. As duas
// igualdades acima são mais fortes que aquela soma: pinam cada parcela.
//
// A segunda identidade é a padronização de "Impactos" (2026-08-17): as duas
// telas passaram a valorizar o MESMO conjunto (in_slot + bonus), então o número
// que o cliente lê é o mesmo em qualquer lugar do produto. Antes o /insights
// multiplicava PMM por TODAS as categorias aprovadas e vinha maior.
//
// O fixture tem UMA tocada out_slot de propósito: ela não pode aparecer em
// nenhum dos dois lados (D3 — tocada fora da faixa contratada não vale nada).
// Se o /insights voltasse a faturar out_slot como entrega, o Executado subiria
// de 30 pra 40 e a soma estouraria o total do /campaigns.
//
// As duas telas só batem porque a janela do /insights aqui é a campanha
// INTEIRA: o /insights é período-aware e o /campaigns não (whole-campaign).
// Filtrar um sub-período no /insights legitimamente diverge do /campaigns —
// não é bug, é escopo diferente.
func TestInsights_FinancialBase_MatchesCampaigns(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)
	campaignsRepo := NewCampaigns(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 10.0)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	// 3 in_slot (dias 1–3) + 1 bonus (excedente do dia 1) + 1 out_slot (dia 4).
	for d := 1; d <= 3; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}
	insSeedDetection(t, ctx, pool, camp, mat, st, "bonus", "2026-06-01")
	insSeedDetection(t, ctx, pool, camp, mat, st, "out_slot", "2026-06-04")

	today := parseDate("2026-07-15")
	out, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		Today: today, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	fins, err := campaignsRepo.FinancialsByCampaign(ctx, []uuid.UUID{client}, []uuid.UUID{camp}, today)
	if err != nil {
		t.Fatalf("FinancialsByCampaign: %v", err)
	}
	if len(fins) != 1 {
		t.Fatalf("FinancialsByCampaign devolveu %d linhas, want 1", len(fins))
	}

	// Executado = unit × in_slot = 10 × 3 = 30 (NÃO 40: o out_slot do dia 4 não
	// fatura). Bonificação = unit × bonus = 10 × 1 = 10.
	if !approxEq(out.KPIs.Investido.Executado, 30, 0.01) {
		t.Errorf("insights executado = %v, want 30 (10 × 3 in_slot; out_slot não fatura)", out.KPIs.Investido.Executado)
	}
	if !approxEq(out.KPIs.Bonificacao.Valor, 10, 0.01) {
		t.Errorf("insights bonificação = %v, want 10 (10 × 1 bonus)", out.KPIs.Bonificacao.Valor)
	}
	// /campaigns: invested = unit × in_slot = 10 × 3 = 30 (o bônus NÃO fatura),
	// bonus_value = unit × bonus = 10 × 1 = 10.
	if !approxEq(fins[0].TotalInvested, 30, 0.01) {
		t.Errorf("campaigns total_invested = %v, want 30 (10 × 3 in_slot; bônus é entrega gratuita)", fins[0].TotalInvested)
	}
	if !approxEq(fins[0].TotalBonusValue, 10, 0.01) {
		t.Errorf("campaigns total_bonus_value = %v, want 10 (10 × 1 bonus)", fins[0].TotalBonusValue)
	}
	// PARIDADE PARCELA A PARCELA — as duas metades, não a soma.
	if !approxEq(out.KPIs.Investido.Executado, fins[0].TotalInvested, 0.01) {
		t.Errorf("investido divergiu: insights executado %v × campaigns total_invested %v",
			out.KPIs.Investido.Executado, fins[0].TotalInvested)
	}
	if !approxEq(out.KPIs.Bonificacao.Valor, fins[0].TotalBonusValue, 0.01) {
		t.Errorf("bonificação divergiu: insights %v × campaigns total_bonus_value %v",
			out.KPIs.Bonificacao.Valor, fins[0].TotalBonusValue)
	}
	// E o breakdown do /insights tem que ver a mesma coisa: 3/1/0/1.
	if out.VeiculacoesBreakdown.InSlot != 3 || out.VeiculacoesBreakdown.OutSlot != 1 ||
		out.VeiculacoesBreakdown.ExtrasOrphan != 1 {
		t.Errorf("breakdown = %+v, want in_slot 3 / out_slot 1 / extras 1", out.VeiculacoesBreakdown)
	}

	// PARIDADE DE IMPACTOS. pmm 1000 × (3 in_slot + 1 bonus) = 4000 nas DUAS
	// telas. Se o /insights voltasse a multiplicar por todas as categorias
	// aprovadas, a tocada out_slot do dia 4 levaria o número a 5000 e o cliente
	// veria dois "Impactos" diferentes pra mesma campanha no mesmo período.
	if out.KPIs.Impactos != 4000 {
		t.Errorf("insights impactos = %d, want 4000 (1000 × (3 in_slot + 1 bonus); out_slot não é impacto)",
			out.KPIs.Impactos)
	}
	if int64(fins[0].TotalAudience) != out.KPIs.Impactos {
		t.Errorf("impactos divergiu: insights %d × campaigns %v",
			out.KPIs.Impactos, fins[0].TotalAudience)
	}
	// CPM = (investido + bonificado) ÷ impactos × 1000 = (30 + 10) ÷ 4000 × 1000
	// = 10,00. O numerador NÃO é só o pago: o CPM mede a eficiência da mídia
	// ENTREGUE a preço de tabela, e a tocada de bônus já está no denominador.
	if !approxEq(out.KPIs.CPM, 10.0, 0.01) {
		t.Errorf("insights cpm = %v, want 10.00 ((executado 30 + bonificação 10) ÷ 4000 impactos × 1000)", out.KPIs.CPM)
	}
	// GUARDA ANTI-"SIMPLIFICAÇÃO": se alguém voltar o numerador pro valor pago
	// (executado ÷ impactos = 7,50), este teste tem que gritar. A checagem é
	// explícita — não dá pra passar nos dois ao mesmo tempo.
	paidOnlyCPM := out.KPIs.Investido.Executado / float64(out.KPIs.Impactos) * 1000
	if approxEq(out.KPIs.CPM, paidOnlyCPM, 0.01) {
		t.Errorf("cpm = %v == numerador só do pago (%v): a bonificação (%v) SUMIU do numerador. "+
			"CPM = (investido + bonificado) ÷ impactos × 1000 — é o valor de tabela da mídia "+
			"entregue, não a eficiência da negociação. Ver docs/features/insights-dashboard.md.",
			out.KPIs.CPM, paidOnlyCPM, out.KPIs.Bonificacao.Valor)
	}
	// E o numerador é exatamente a soma das duas parcelas.
	wantNum := (out.KPIs.Investido.Executado + out.KPIs.Bonificacao.Valor) / float64(out.KPIs.Impactos) * 1000
	if !approxEq(out.KPIs.CPM, wantNum, 0.01) {
		t.Errorf("cpm = %v, want %v ((executado + bonificação) ÷ impactos × 1000)", out.KPIs.CPM, wantNum)
	}
	// O CPM do /campaigns é derivado no frontend com as DUAS parcelas
	// ((invested + bonus_value) ÷ audience × 1000). É a MESMA expressão do
	// /insights — é isso que impede as duas telas de exibirem CPMs diferentes
	// pra mesma campanha. Se o frontend voltar a dividir só `total_invested`,
	// esta igualdade quebra.
	campCPM := (fins[0].TotalInvested + fins[0].TotalBonusValue) / fins[0].TotalAudience * 1000
	if !approxEq(campCPM, out.KPIs.CPM, 0.01) {
		t.Errorf("cpm divergiu: campaigns %v × insights %v", campCPM, out.KPIs.CPM)
	}
}

// computeCPM slow path: o executado por-campanha usa o mesmo denominador de
// plano cheio. Campanha A tem fixed_cpm (força o slow path) mas 0 impactos
// (peso 0). Campanha B (sem fixed_cpm) domina; executado_B = 2000×15/30 = 1000,
// impactos_B = 15000 → CPM = 66,67.
func TestInsights_ComputeCPM_Consolidated_SlowPathUsesWholePlanDenominator(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")

	// A: força slow path (fixed_cpm setado), sem detecções → peso 0.
	campA := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET fixed_cpm = 50 WHERE id = $1`, campA); err != nil {
		t.Fatalf("set fixed_cpm: %v", err)
	}

	// B: consolidada, sem fixed_cpm; entregue metade do plano cheio.
	campB := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RXB", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, campB, st, "consolidated", 2000)
	insSeedDistributionRule(t, ctx, pool, campB, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 15; d++ {
		insSeedDetection(t, ctx, pool, campB, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	// Sub-janela até 15/06: discrimina denominador da janela (daria 133,67) do
	// denominador de plano cheio (66,67).
	cpm, err := repo.computeCPM(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{campA, campB},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	}, 0, 0)
	if err != nil {
		t.Fatalf("computeCPM: %v", err)
	}
	if !approxEq(cpm, 66.67, 0.5) {
		t.Errorf("cpm = %v, want ~66.67 (executado 15/30 no slow path)", cpm)
	}
}

// ─── PMM no target por cliente ──────────────────────────────────────────────

// TestInsights_AggregateCore_TargetPMM cobre as três combinações possíveis:
// emissora só com PMM global, só com target, e com os dois.
func TestInsights_AggregateCore_TargetPMM(t *testing.T) {
	ctx, pool := newTestDB(t)

	clientID := insSeedClient(t, ctx, pool, "Cliente Target Insights")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-06-30")
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, clientID, "Spot Target")

	// stAmbos: PMM 1000 e target 400. stSoPMM: PMM 1000, sem target.
	// stSoTarget: sem PMM, target 700.
	stAmbos := insSeedStation(t, ctx, pool, "Ambos", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stSoPMM := insSeedStation(t, ctx, pool, "SoPMM", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stSoTarget := insSeedStationNoProfile(t, ctx, pool, "SoTarget")

	repo := NewClientStationPMM(pool)
	v400, v700 := 400, 700
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stAmbos, PMMTarget: &v400},
		{StationID: stSoTarget, PMMTarget: &v700},
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	// 2 detecções em stAmbos, 1 em stSoPMM, 1 em stSoTarget.
	insSeedDetection(t, ctx, pool, camp, mat, stAmbos, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stAmbos, "in_slot", "2026-06-11")
	insSeedDetection(t, ctx, pool, camp, mat, stSoPMM, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stSoTarget, "in_slot", "2026-06-10")

	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{camp},
		From:        parseDate("2026-06-01"),
		To:          parseDate("2026-06-30"),
		StationIDs:  []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	// impactos = 2×1000 (ambos) + 1×1000 (soPMM) + 0 (soTarget, sem pmm) = 3000
	if core.Impactos != 3000 {
		t.Errorf("impactos = %d, want 3000", core.Impactos)
	}
	// impactos_target = 2×400 (ambos) + 0 (soPMM, sem target) + 1×700 = 1500
	if core.ImpactosTarget != 1500 {
		t.Errorf("impactos_target = %d, want 1500", core.ImpactosTarget)
	}
	if core.StationsCount != 3 {
		t.Errorf("stations_count = %d, want 3", core.StationsCount)
	}
	if core.StationsWithPMM != 2 {
		t.Errorf("stations_with_pmm = %d, want 2", core.StationsWithPMM)
	}
	if core.StationsWithTarget != 2 {
		t.Errorf("stations_with_target = %d, want 2", core.StationsWithTarget)
	}
}

// TestInsights_AggregateCore_TargetPMM_ZeroIsNotAbsent trava a distinção entre
// "não cadastrado" (sem linha → fora do contador e da soma) e pmm_target = 0
// (linha existe → conta no contador, soma zero).
func TestInsights_AggregateCore_TargetPMM_ZeroIsNotAbsent(t *testing.T) {
	ctx, pool := newTestDB(t)

	clientID := insSeedClient(t, ctx, pool, "Cliente Target Zero")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-06-30")
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, clientID, "Spot Zero")

	stZero := insSeedStation(t, ctx, pool, "TargetZero", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stAusente := insSeedStation(t, ctx, pool, "TargetAusente", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	zero := 0
	if _, _, err := NewClientStationPMM(pool).BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stZero, PMMTarget: &zero},
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	insSeedDetection(t, ctx, pool, camp, mat, stZero, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stAusente, "in_slot", "2026-06-10")

	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{camp},
		From:        parseDate("2026-06-01"),
		To:          parseDate("2026-06-30"),
		StationIDs:  []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}
	if core.ImpactosTarget != 0 {
		t.Errorf("impactos_target = %d, want 0", core.ImpactosTarget)
	}
	// só stZero tem cadastro; stAusente (sem linha) fica de fora.
	if core.StationsWithTarget != 1 {
		t.Errorf("stations_with_target = %d, want 1", core.StationsWithTarget)
	}
	if core.StationsCount != 2 {
		t.Errorf("stations_count = %d, want 2", core.StationsCount)
	}
}

// TestInsights_AggregateCore_TargetPMM_MultiClientNoDoubleCount é a rede de
// segurança da mudança COUNT(*) → COUNT(DISTINCT station_id): per_station passou
// a particionar por cliente, então a MESMA emissora usada por campanhas de dois
// clientes rende duas linhas. Os contadores não podem contá-la duas vezes; as
// SOMAS (impactos) continuam somando tudo.
func TestInsights_AggregateCore_TargetPMM_MultiClientNoDoubleCount(t *testing.T) {
	ctx, pool := newTestDB(t)

	cliA := insSeedClient(t, ctx, pool, "Cliente A Multi")
	cliB := insSeedClient(t, ctx, pool, "Cliente B Multi")
	campA := insSeedCampaign(t, ctx, pool, cliA, "2026-06-01", "2026-06-30")
	campB := insSeedCampaign(t, ctx, pool, cliB, "2026-06-01", "2026-06-30")
	_, matA := insSeedTypeAndMaterial(t, ctx, pool, cliA, "Spot A Multi")
	_, matB := insSeedTypeAndMaterial(t, ctx, pool, cliB, "Spot B Multi")

	// UMA emissora, compartilhada pelas duas campanhas/clientes.
	st := insSeedStation(t, ctx, pool, "Compartilhada", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	vA, vB := 400, 100
	repo := NewClientStationPMM(pool)
	if _, _, err := repo.BulkUpsert(ctx, cliA, []TargetPMMEntry{{StationID: st, PMMTarget: &vA}}); err != nil {
		t.Fatalf("seed target A: %v", err)
	}
	if _, _, err := repo.BulkUpsert(ctx, cliB, []TargetPMMEntry{{StationID: st, PMMTarget: &vB}}); err != nil {
		t.Fatalf("seed target B: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = ANY($1)", []uuid.UUID{cliA, cliB})
	})

	insSeedDetection(t, ctx, pool, campA, matA, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, campB, matB, st, "in_slot", "2026-06-10")

	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{campA, campB},
		From:        parseDate("2026-06-01"),
		To:          parseDate("2026-06-30"),
		StationIDs:  []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	if core.VeiculacoesTotal != 2 {
		t.Errorf("veiculacoes_total = %d, want 2", core.VeiculacoesTotal)
	}
	// somas não são afetadas pelo particionamento: 1×1000 + 1×1000
	if core.Impactos != 2000 {
		t.Errorf("impactos = %d, want 2000", core.Impactos)
	}
	// cada tocada resolve o target do SEU cliente: 1×400 + 1×100
	if core.ImpactosTarget != 500 {
		t.Errorf("impactos_target = %d, want 500", core.ImpactosTarget)
	}
	// UMA emissora — sem DISTINCT, estes três leriam 2.
	if core.StationsCount != 1 {
		t.Errorf("stations_count = %d, want 1 (emissora contada em dobro)", core.StationsCount)
	}
	if core.StationsWithPMM != 1 {
		t.Errorf("stations_with_pmm = %d, want 1 (emissora contada em dobro)", core.StationsWithPMM)
	}
	if core.StationsWithTarget != 1 {
		t.Errorf("stations_with_target = %d, want 1 (emissora contada em dobro)", core.StationsWithTarget)
	}
}
