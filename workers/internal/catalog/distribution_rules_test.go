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
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
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
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
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

	// 1. Cria uma detection ANTES de qualquer regra → category=bonus
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

// TestDistributionRules_RecategorizeForMaterial cobre o bug do print:
// material cadastrado com um tipo, veicula (detection vira bonus porque não
// há regra pro tipo antigo), e depois o operador troca o tipo pra um que JÁ
// TEM regra. Sem recategorizar por material, a detection continua 'bonus' e
// some pra "bonificação (sem meta)" no resumo diário. RecategorizeForMaterial,
// rodado após o UPDATE do type_id, precisa virar a detection pra in_slot.
func TestDistributionRules_RecategorizeForMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeOld := seedType(t, ctx, pool, "Old")
	typeNew := seedType(t, ctx, pool, "New")
	mats := NewMaterials(pool)
	// Material nasce com o tipo ANTIGO (sem regra).
	mat, _ := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeOld, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-mat-retype",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Retype", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	// Regra existe SÓ pro tipo NOVO.
	repo := NewDistributionRules(pool)
	if _, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeNew,
		StationIDs:  []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Detection no horário coberto pela regra do tipo novo, mas o material
	// ainda é do tipo antigo → o fechamento por cota marca bonus no insert.
	dets := NewDetections(pool)
	detTime := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) // qua 09:00 BRT
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detTime,
		Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("create detection: %v", err)
	}

	readCat := func() string {
		t.Helper()
		var c string
		if err := pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
			det.ID, det.DetectedAt).Scan(&c); err != nil {
			t.Fatalf("read category: %v", err)
		}
		return c
	}

	// Sanidade: começa bonus (tipo antigo não tem regra → meta do dia = 0 →
	// a tocada é excedente). Era `orphan` antes da spec 2026-08-14, quando
	// "tocou sem regra aplicável" tinha veredito próprio; agora cai no caso
	// geral da cota e vira bonus explícito (D4).
	if got := readCat(); got != "bonus" {
		t.Fatalf("pré-condição: category = %q, want bonus", got)
	}

	// Operador troca o tipo do material pro tipo NOVO (que tem regra)...
	if err := mats.UpdateType(ctx, mat.ID, &typeNew); err != nil {
		t.Fatalf("update type: %v", err)
	}
	// ...sem recategorizar, a detection segue bonus (é o bug). Recategoriza:
	if err := repo.RecategorizeForMaterial(ctx, mat.ID); err != nil {
		t.Fatalf("recategorize for material: %v", err)
	}

	if got := readCat(); got != "in_slot" {
		t.Errorf("após troca de tipo + recategorize: category = %q, want in_slot", got)
	}
}

func TestDistributionRules_MaterialIDs_Roundtrip(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeID := seedType(t, ctx, pool, "Spot")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})
	m1, m2 := uuid.New(), uuid.New()
	repo := NewDistributionRules(pool)
	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{uuid.New()},
		MaterialIDs: []uuid.UUID{m1, m2},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rule.MaterialIDs) != 2 {
		t.Fatalf("MaterialIDs len = %d, want 2", len(rule.MaterialIDs))
	}
	got, _ := repo.Get(ctx, rule.ID)
	if len(got.MaterialIDs) != 2 {
		t.Errorf("after Get: MaterialIDs len = %d, want 2", len(got.MaterialIDs))
	}
	// Default vazio quando não informado.
	rule2, _ := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{uuid.New()},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	if len(rule2.MaterialIDs) != 0 {
		t.Errorf("default MaterialIDs len = %d, want 0", len(rule2.MaterialIDs))
	}
}

// Garante que o recategorize SQL respeita a tolerância de 15 min nos extremos
// da faixa (igual ao categorizer.SlotToleranceSeconds). Antes do fix, o SQL
// usava BETWEEN time_start AND time_end direto e reclassificava como out_slot
// uma detection a 10 min do início do slot.
func TestDistributionRules_Recategorize_CarveOut(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mats := NewMaterials(pool)
	special, _ := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Special", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-special",
	})
	normal, _ := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Normal", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-normal",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Recat", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = ANY($1)", []uuid.UUID{special.ID, normal.ID})
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	dets := NewDetections(pool)
	mk := func(mat uuid.UUID, utc time.Time) *Detection {
		t.Helper()
		d, err := dets.Create(ctx, CreateDetectionInput{
			StationID: stat.ID, CommercialID: mat, CampaignID: cmp.ID,
			DetectedAt: utc, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		if err != nil {
			t.Fatalf("create detection: %v", err)
		}
		return d
	}
	// special toca 16/06 (semana 3) 18:30 BRT — vai virar out_date após a regra dele.
	dSpecialLate := mk(special.ID, time.Date(2026, 6, 16, 21, 30, 0, 0, time.UTC))
	// normal toca 16/06 10:00 BRT — coberto pela regra geral → in_slot.
	dNormal := mk(normal.ID, time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC))

	repo := NewDistributionRules(pool)
	// Regra GERAL (todos do tipo, mês todo, 07-19h).
	if _, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "07:00", TimeEnd: "19:00", PlaysPerDay: 3,
	}); err != nil {
		t.Fatalf("create general rule: %v", err)
	}
	// Regra ESPECÍFICA de special: 1ª semana, 18-19h. Dispara o carve-out.
	specRule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{special.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "18:00", TimeEnd: "19:00", PlaysPerDay: 1,
	})
	if err != nil {
		t.Fatalf("create specific rule: %v", err)
	}
	if err := repo.RecategorizeForRule(ctx, specRule.ID); err != nil {
		t.Fatalf("recategorize: %v", err)
	}

	readCat := func(d *Detection) string {
		t.Helper()
		var c string
		pool.QueryRow(ctx, `SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
			d.ID, d.DetectedAt).Scan(&c)
		return c
	}
	// special fora do período da regra dele (semana 3) → out_date, mesmo com a
	// regra geral cobrindo 07-19h (carve-out ignora a geral).
	if got := readCat(dSpecialLate); got != "out_date" {
		t.Errorf("special late: category = %q, want out_date", got)
	}
	// normal não é carved-out → regra geral → in_slot.
	if got := readCat(dNormal); got != "in_slot" {
		t.Errorf("normal: category = %q, want in_slot", got)
	}
}

func TestDistributionRules_RecategorizeRespectsSlotTolerance(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
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
	// (±15 min) de uma rule 09:30–10:00 com plays_per_day = 3.
	//
	// A tolerância decide só o booleano "dentro da faixa"; QUEM leva in_slot é a
	// cota do dia (spec 2026-08-14). Dentro da faixa, em ordem cronológica:
	// 09:15, 09:20, 10:14, 10:15 — as 3 primeiras preenchem N=3 e a 4ª sobra.
	// Fora da faixa: como a meta já fechou DENTRO da faixa, não seguram déficit
	// nenhum e também são excedente (passo 4 / decisão D2).
	//
	//   09:15 → -15 min do início, exato no limite → dentro, 1ª da cota → in_slot
	//   09:20 → -10 min do início, dentro          → dentro, 2ª da cota → in_slot
	//   10:14 → +14 min do fim,    dentro          → dentro, 3ª da cota → in_slot
	//   10:15 → +15 min do fim,    exato no limite → dentro, excede N   → bonus
	//   09:14 → -16 min do início, FORA            → meta cheia         → bonus
	//   10:16 → +16 min do fim,    FORA            → meta cheia         → bonus
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
	detEarlyIn := mk(9, 20, "early-in")     // -10 min
	detEarlyEdge := mk(9, 15, "early-edge") // -15 min limite
	detEarlyOut := mk(9, 14, "early-out")   // -16 min fora
	detLateIn := mk(10, 14, "late-in")      // +14 min
	detLateEdge := mk(10, 15, "late-edge")  // +15 min limite
	detLateOut := mk(10, 16, "late-out")    // +16 min fora

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
		{"09:14 (-16min, out)", detEarlyOut, "bonus"},
		{"10:14 (+14min, in)", detLateIn, "in_slot"},
		{"10:15 (+15min, edge)", detLateEdge, "bonus"},
		{"10:16 (+16min, out)", detLateOut, "bonus"},
	}
	for _, c := range cases {
		if got := readCat(c.det); got != c.want {
			t.Errorf("%s: category = %q, want %q", c.name, got, c.want)
		}
	}
}
