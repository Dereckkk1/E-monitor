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
	insSeedDetection(t, ctx, pool, camp, mat, st, "orphan", "2026-06-12")

	from := parseDate("2026-06-01")
	to := parseDate("2026-06-30")
	core, err := repo.aggregateCore(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: from, To: to, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	// 8 detecções × 1000 = 8000 impactos
	if core.Impactos != 8000 {
		t.Errorf("impactos = %d, want 8000", core.Impactos)
	}
	if core.VeiculacoesTotal != 8 {
		t.Errorf("veic = %d, want 8", core.VeiculacoesTotal)
	}
	// Gender M = 8000 × 60% = 4800
	if core.Gender.M != 4800 {
		t.Errorf("gender_m = %d, want 4800", core.Gender.M)
	}
	if core.Gender.F != 3200 {
		t.Errorf("gender_f = %d, want 3200", core.Gender.F)
	}
	// AB = 8000 × 20% = 1600
	if core.Class.AB != 1600 {
		t.Errorf("class_ab = %d, want 1600", core.Class.AB)
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
	// dia 12: 1 orphan
	insSeedDetection(t, ctx, pool, camp, mat, st, "orphan", "2026-06-12")

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
	// 12: programado=1, extras=1 (orphan), deficit=1 (1-0-0)
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
	// bonus da view = Σ_dia max(0, in_slot_dia - expected_dia) + orphan = 0
	// (1 in_slot/dia == 1 expected/dia em cada um dos 6 dias).
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

// ─── consolidated: clamp em "hoje" + cap no contrato (fix 2026-07-08) ────────
//
// Ver docs/superpowers/specs/2026-07-08-insights-consolidated-fill-cap-design.md.
// Dois defeitos na fórmula consolidada, corrigidos aqui:
//   A) dias futuros (esperado>0/executado=0) inflavam o denominador →
//      executado/bonificação/CPM caíam ao alargar a janela.
//   B) over-delivery (in_slot acima do plano) entrava no executado (razão >1,
//      executado > contrato) E na bonificação — double-count. Agora o
//      executado capa em 100% do contrato; o excedente fica só na bonificação.
//
// A injeção de "hoje" é via InsightsParams.Today (date-only). Zero value =
// sem clamp (comportamento legado dos testes/handlers que não setam).

// approxEq compara floats com tolerância absoluta.
func approxEq(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// A) Dias futuros não derrubam o executado consolidado.
//
// Contrato 3000, plano 1/dia em junho (30 esperados). Entregue 1/dia nos dias
// 01–15 (15 executados). Today=15/06 → dias 16–30 são futuro. Com o fix, a
// janela de fill vai só até hoje: 15 executados / 15 esperados = 100% → 3000.
// Sem o fix, 15/30 = 50% → 1500.
func TestInsights_Investment_Consolidated_FutureDaysDoNotDeflate(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RX", 1000, 50, 50, 30, 40, 30, 30, 40, 30)

	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 3000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)

	// 1 in_slot/dia nos dias 01–15 (passado relativo a Today=15/06).
	for d := 1; d <= 15; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	inv, _, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		Today:      parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	if !approxEq(inv.Executado, 3000, 1) {
		t.Errorf("executado = %v, want ~3000 (fill até hoje = 15/15 = 100%%, não 15/30)", inv.Executado)
	}
}

// B) Over-delivery capa no contrato e vai pra bonificação, não pro investido.
//
// Contrato 1000, plano 1/dia dias 01–10 (10 esperados). Entregue 2/dia (20
// executados, 10 de excedente). Today=01/07 (tudo passado). Com o fix:
// executado = 1000 × min(1, 20/10) = 1000 (capa). Bonificação = 1000 × 10/10 =
// 1000 (o excedente). Sem o fix, executado = 1000 × 20/10 = 2000.
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

	// 2 in_slot/dia nos dias 01–10 → excedente de 1/dia.
	for d := 1; d <= 10; d++ {
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
		insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	inv, bon, err := repo.aggregateInvestment(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		Today:      parseDate("2026-07-01"),
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

// Déficit passado reduz o executado (o cap não vira piso). Guard: passa antes
// e depois do fix — garante que min(1, x) não floora quando x<1.
//
// Contrato 1000, plano 1/dia dias 01–10 (10 esperados). Entregue só 4.
// executado = 1000 × min(1, 4/10) = 400.
func TestInsights_Investment_Consolidated_PastDeficitReduces(t *testing.T) {
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
		Today:      parseDate("2026-07-01"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	if !approxEq(inv.Executado, 400, 1) {
		t.Errorf("executado = %v, want ~400 (4/10 do contrato)", inv.Executado)
	}
}

// per_insertion não é afetado pelo clamp de "hoje": o contratado mantém o
// plano cheio (30 esperados) mesmo com Today no meio do período. Guard contra
// vazamento do clamp pro modo aditivo.
func TestInsights_Investment_PerInsertion_UnaffectedByTodayClamp(t *testing.T) {
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
		Today:      parseDate("2026-06-15"), // no meio: não deve cortar o per_insertion
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateInvestment: %v", err)
	}
	// contratado = 50 × 30 esperados = 1500 (plano cheio, sem clamp)
	if !approxEq(inv.Contratado, 1500, 1) {
		t.Errorf("contratado = %v, want ~1500 (per_insertion não é clampeado por Today)", inv.Contratado)
	}
	// executado = 50 × 6 = 300
	if !approxEq(inv.Executado, 300, 1) {
		t.Errorf("executado = %v, want ~300", inv.Executado)
	}
}

// Compute end-to-end (fast path do CPM): campanha consolidada com dias futuros
// não derruba Investido nem CPM. Contrato 2000, pmm 1000, 15 tocadas nos dias
// 01–15, Today=15/06. Investido = 2000 (100% até hoje). Impactos = 15×1000 =
// 15000. CPM = 2000/15000×1000 = 133,33. Sem o fix: Investido 1000, CPM 66,67.
func TestInsights_Compute_Consolidated_FutureDaysDoNotDeflateInvestidoOrCPM(t *testing.T) {
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

	out, err := repo.Compute(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		Today:      parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !approxEq(out.KPIs.Investido.Executado, 2000, 1) {
		t.Errorf("investido = %v, want ~2000", out.KPIs.Investido.Executado)
	}
	if !approxEq(out.KPIs.CPM, 133.33, 0.5) {
		t.Errorf("cpm = %v, want ~133.33 (2000/15000×1000)", out.KPIs.CPM)
	}
}

// computeCPM slow path (com fixed_cpm em jogo): o executado por-campanha também
// usa clamp+cap. Campanha A tem fixed_cpm (força o slow path) mas 0 impactos
// (peso 0). Campanha B (sem fixed_cpm) domina o CPM ponderado; seu executado
// deve vir clampeado até hoje. CPM esperado = 133,33 (não 66,67).
func TestInsights_ComputeCPM_Consolidated_SlowPathUsesClampedExecutado(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewInsights(pool)

	client := insSeedClient(t, ctx, pool, "X")

	// A: força slow path (fixed_cpm setado), sem detecções → peso 0.
	campA := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET fixed_cpm = 50 WHERE id = $1`, campA); err != nil {
		t.Fatalf("set fixed_cpm: %v", err)
	}

	// B: consolidada, sem fixed_cpm, com dias futuros.
	campB := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot30")
	st := insSeedStation(t, ctx, pool, "RXB", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, campB, st, "consolidated", 2000)
	insSeedDistributionRule(t, ctx, pool, campB, typeID, st,
		"2026-06-01", "2026-06-30", 0b1111111, "00:00:00", "23:59:00", 1)
	for d := 1; d <= 15; d++ {
		insSeedDetection(t, ctx, pool, campB, mat, st, "in_slot", fmt.Sprintf("2026-06-%02d", d))
	}

	cpm, err := repo.computeCPM(ctx, InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{campA, campB},
		From: parseDate("2026-06-01"), To: parseDate("2026-06-30"),
		Today:      parseDate("2026-06-15"),
		StationIDs: []uuid.UUID{},
	}, 0, 0)
	if err != nil {
		t.Fatalf("computeCPM: %v", err)
	}
	if !approxEq(cpm, 133.33, 0.5) {
		t.Errorf("cpm = %v, want ~133.33 (executado clampeado no slow path)", cpm)
	}
}
