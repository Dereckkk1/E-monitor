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
		Name:      "C-" + start,
		ClientID:  clientID,
		StartDate: parseDate(start),
		EndDate:   parseDate(end),
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
// gender, class e age devem somar 1.0 dentro de cada dimensão.
func insSeedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, pmm float64,
	maleP, femaleP, abP, cP, deP, r18P, r25P, r50P float64) uuid.UUID {
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
	}`, maleP, femaleP, abP, cP, deP, r18P, r25P, r50P)
	if _, err := pool.Exec(ctx,
		"UPDATE stations SET pmm = $1, meta = $2::jsonb WHERE id = $3",
		pmm, meta, stat.ID,
	); err != nil {
		t.Fatalf("seed station meta: %v", err)
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
	_, err := pool.Exec(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
	`, stationID, materialID, campaignID, ts, category)
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}
}

// insSeedPricing insere uma linha em campaigns_pricing.
// mode = "consolidated" usa consolidated_value; "per_insertion" usa price_per_insertion.
func insSeedPricing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID uuid.UUID, mode string, consolidated, perIns float64) {
	t.Helper()
	var consPtr, perPtr *float64
	if consolidated > 0 {
		consPtr = &consolidated
	}
	if perIns > 0 {
		perPtr = &perIns
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO campaigns_pricing(campaign_id, mode, consolidated_value, price_per_insertion)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (campaign_id) DO UPDATE SET mode = EXCLUDED.mode,
		    consolidated_value = EXCLUDED.consolidated_value,
		    price_per_insertion = EXCLUDED.price_per_insertion
	`, campaignID, mode, consPtr, perPtr)
	if err != nil {
		t.Fatalf("seed pricing: %v", err)
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
	st := insSeedStation(t, ctx, pool, "FixtureCheck", 1234, 0.6, 0.4, 0.2, 0.5, 0.3, 0.3, 0.5, 0.2)

	var pmm float64
	var metaRaw string
	if err := pool.QueryRow(ctx, "SELECT pmm, meta::text FROM stations WHERE id = $1", st).Scan(&pmm, &metaRaw); err != nil {
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
	if g["male"].(float64) != 0.6 {
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
