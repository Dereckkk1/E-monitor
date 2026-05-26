package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedType creates a material type for tests and registers cleanup. Tests
// can't reuse the seeded types from migration 0016 because the cleanup of
// other tests may have deleted them via cascading FK; making a fresh one
// per test keeps things isolated.
func seedType(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	tid := uuid.New()
	// Append a unique suffix to satisfy material_types.name UNIQUE constraint.
	_, err := pool.Exec(ctx, `INSERT INTO material_types (id, name, color)
		VALUES ($1, $2, '#3b82f6')`, tid, name+"-"+tid.String()[:8])
	if err != nil {
		t.Fatalf("seed type: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM material_types WHERE id = $1", tid)
	})
	return tid
}

func TestDistributionRules_CRUD(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})
	station := uuid.New()

	repo := NewDistributionRules(pool)

	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID:  cmp.ID,
		TypeID:      typeID,
		StationIDs:  []uuid.UUID{station},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62,
		TimeStart:   "08:15",
		TimeEnd:     "10:45",
		PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rule.PlaysPerDay != 3 {
		t.Errorf("PlaysPerDay = %d, want 3", rule.PlaysPerDay)
	}
	if rule.TypeID != typeID {
		t.Errorf("TypeID = %v, want %v", rule.TypeID, typeID)
	}

	list, _ := repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	// Update
	if err := repo.Update(ctx, rule.ID, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{station},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:15", TimeEnd: "10:45",
		PlaysPerDay: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	r, err := repo.Get(ctx, rule.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if r.PlaysPerDay != 5 {
		t.Errorf("after update: PlaysPerDay = %d, want 5", r.PlaysPerDay)
	}

	// Delete
	if err := repo.Delete(ctx, rule.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 0 {
		t.Errorf("after delete: len = %d, want 0", len(list))
	}
}

func TestDistributionRules_Constraints(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "y",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewDistributionRules(pool)

	// end_date < start_date deve falhar (rule_dates_valid)
	_, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{uuid.New()},
		StartDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})
	if err == nil {
		t.Error("expected error for end < start, got nil")
	}
}

func TestDistributionRules_RecategorizeAfterCreate(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-bulk",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Bulk", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	// 1. Cria uma detection ANTES de qualquer regra → category=orphan
	dets := NewDetections(pool)
	// 10/06/2026 (qua) às 09:00 BRT (12:00 UTC)
	detTime := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detTime,
		Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("create detection: %v", err)
	}

	// 2. Cria regra que cobre essa data + faixa (por TIPO, não material)
	repo := NewDistributionRules(pool)
	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// 3. Roda recategorização
	if err := repo.RecategorizeForRule(ctx, rule.ID); err != nil {
		t.Fatalf("recategorize: %v", err)
	}

	// 4. Verifica que a detection virou in_slot
	var cat string
	err = pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&cat)
	if err != nil {
		t.Fatalf("read category: %v", err)
	}
	if cat != "in_slot" {
		t.Errorf("after recategorize: category = %q, want in_slot", cat)
	}
}

// Garante que o recategorize SQL respeita a tolerância de 15 min nos extremos
// da faixa (igual ao categorizer.SlotToleranceSeconds). Antes do fix, o SQL
// usava BETWEEN time_start AND time_end direto e reclassificava como out_slot
// uma detection a 10 min do início do slot.
func TestDistributionRules_RecategorizeRespectsSlotTolerance(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-toler",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Toler", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	dets := NewDetections(pool)
	// Quarta-feira 10/06/2026. Detections que exercitam a janela tolerada
	// (±15 min) de uma rule 09:30–10:00:
	//   09:20 → -10 min do início, dentro da tolerância → in_slot
	//   09:15 → -15 min do início, exato no limite      → in_slot
	//   09:14 → -16 min do início, fora                 → out_slot
	//   10:14 → +14 min do fim,    dentro              → in_slot
	//   10:15 → +15 min do fim,    exato no limite    → in_slot
	//   10:16 → +16 min do fim,    fora               → out_slot
	mk := func(h, m int, suffix string) *Detection {
		t.Helper()
		// BRT = UTC-3, então hora local h:m corresponde a (h+3):m UTC.
		ts := time.Date(2026, 6, 10, h+3, m, 0, 0, time.UTC)
		d, err := dets.Create(ctx, CreateDetectionInput{
			StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
			DetectedAt: ts,
			Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		if err != nil {
			t.Fatalf("create detection %s: %v", suffix, err)
		}
		return d
	}
	detEarlyIn := mk(9, 20, "early-in")    // -10 min
	detEarlyEdge := mk(9, 15, "early-edge") // -15 min limite
	detEarlyOut := mk(9, 14, "early-out")  // -16 min fora
	detLateIn := mk(10, 14, "late-in")     // +14 min
	detLateEdge := mk(10, 15, "late-edge") // +15 min limite
	detLateOut := mk(10, 16, "late-out")   // +16 min fora

	// Cria rule que NÃO cobre nenhuma das detections sem tolerância,
	// mas cobre as 3 "*OK*"/edge quando a tolerância é aplicada.
	repo := NewDistributionRules(pool)
	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "09:30", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if err := repo.RecategorizeForRule(ctx, rule.ID); err != nil {
		t.Fatalf("recategorize: %v", err)
	}

	readCat := func(d *Detection) string {
		t.Helper()
		var c string
		if err := pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
			d.ID, d.DetectedAt).Scan(&c); err != nil {
			t.Fatalf("read category: %v", err)
		}
		return c
	}

	cases := []struct {
		name string
		det  *Detection
		want string
	}{
		{"09:20 (-10min, in tolerance)", detEarlyIn, "in_slot"},
		{"09:15 (-15min, edge)", detEarlyEdge, "in_slot"},
		{"09:14 (-16min, out)", detEarlyOut, "out_slot"},
		{"10:14 (+14min, in)", detLateIn, "in_slot"},
		{"10:15 (+15min, edge)", detLateEdge, "in_slot"},
		{"10:16 (+16min, out)", detLateOut, "out_slot"},
	}
	for _, c := range cases {
		if got := readCat(c.det); got != c.want {
			t.Errorf("%s: category = %q, want %q", c.name, got, c.want)
		}
	}
}
