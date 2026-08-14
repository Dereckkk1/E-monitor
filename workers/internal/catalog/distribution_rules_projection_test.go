package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Regressão do caso COPA 10/07 (spec 2026-07-14): tocada física atribuída à
// campanha A (base) carrega projeção fan-out F-119 na campanha B. A regra da B
// é criada DEPOIS da tocada → projeção nasceu orphan. O recat disparado pela
// criação da regra TEM que alcançar a projeção fan-out (escopo por
// dc.campaign_id, não d.campaign_id) — e NÃO pode escrever na tocada-base nem
// na projeção canônica da A (guarda d.campaign_id = cl.campaign_id).
func TestRecategorizeForRule_ReachesFanoutProjections(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// Sexta-feira mais recente <= agora, 12:00 SP (partição do mês existe).
	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "projrecat-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "projrecat-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "projrecat-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "projrecat-spot")
	sha := "projrecat-" + uuid.NewString()
	materials := NewMaterials(pool)
	// Mesmo áudio subido 2× (o 43≡143 do caso real): matA na base, matB na secundária.
	matA, err := materials.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "projrecat-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/pa", MasterSHA256: sha,
	})
	require.NoError(t, err)
	matB, err := materials.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "projrecat-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/pb", MasterSHA256: sha,
	})
	require.NoError(t, err)

	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Projrecat FM", Band: "FM", StreamURL: "http://example.com/projrecat",
	})
	require.NoError(t, err)

	// Tocada física na base (campA/matA). Sem regra em A → orphan (base e projeção canônica).
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	// Projeção fan-out F-119 na campanha B (material da B), nascida orphan —
	// não havia regra na B no instante do insert. Espelha service.go/InsertProjections.
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Regra carve-out criada DEPOIS na B, nomeando matB, cobrindo o slot da tocada.
	rules := NewDistributionRules(pool)
	rule, err := rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{matB.ID},
		StartDate:   rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 7,
	})
	require.NoError(t, err)
	require.NoError(t, rules.RecategorizeForRule(ctx, rule.ID))

	// A projeção fan-out na B tem que virar in_slot.
	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB, "recat da regra deve alcançar a projeção fan-out")

	// Guarda: base (campA) e projeção canônica da A ficam como nasceram — o recat
	// da B não pode escrever categoria da B na tocada-base da A. O valor esperado
	// é 'bonus' porque a A não tem regra (meta 0) e o insert-path fecha a
	// célula-dia com cota (spec 2026-08-14 D4: o antigo 'orphan' virou 'bonus');
	// o que o teste prova continua sendo a INTOCABILIDADE, não o rótulo.
	var base, projA string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&base))
	require.Equal(t, "bonus", base, "tocada-base da campanha A intocada")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campA.ID).Scan(&projA))
	require.Equal(t, "bonus", projA, "projeção canônica da A intocada")
}

// Mudança de tipo do material deve reclassificar as projeções que o carregam
// em QUALQUER campanha — não só onde ele é a atribuição-base.
func TestRecategorizeForMaterial_ReachesFanoutProjections(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "matrecat-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "matrecat-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "matrecat-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeRight := seedType(t, ctx, pool, "matrecat-certo")
	typeWrong := seedType(t, ctx, pool, "matrecat-errado")
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "matrecat-B", TypeID: &typeWrong, DurationSeconds: 30,
		MasterStoragePath: "/tmp/mb", MasterSHA256: "matrecat-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "matrecat-A", TypeID: &typeWrong, DurationSeconds: 30,
		MasterStoragePath: "/tmp/ma", MasterSHA256: "matrecat-" + uuid.NewString(),
	})
	require.NoError(t, err)

	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Matrecat FM", Band: "FM", StreamURL: "http://example.com/matrecat",
	})
	require.NoError(t, err)

	// Regra GERAL na B para o tipo certo (sem carve-out).
	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeRight,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 5,
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	// Projeção fan-out na B com matB (tipo errado → orphan no insert).
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Corrige o tipo do matB e recategoriza por material (o que o handler
	// PATCH /materials/:id faz).
	_, err = pool.Exec(ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, matB.ID, typeRight)
	require.NoError(t, err)
	require.NoError(t, rules.RecategorizeForMaterial(ctx, matB.ID))

	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB, "projeção fan-out do material deve ser reclassificada")
}
