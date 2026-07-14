package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Count acha a divergência; Heal cura; Count volta a zero. Cenário: projeção
// fan-out orphan + regra criada via repo (repo.Create NÃO dispara recat — quem
// dispara é o handler), ou seja, drift real como o do caso COPA.
func TestProjectionDrift_CountAndHeal(t *testing.T) {
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

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "drift-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "drift-spot")
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/da", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/db", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Drift FM", Band: "FM", StreamURL: "http://example.com/drift",
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{matB.ID},
		StartDate: rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 7,
	})
	require.NoError(t, err) // repo.Create não recategoriza → drift instalado

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	since := day.Add(-2 * time.Hour)

	// Count: 1 divergência orphan→in_slot na campanha B.
	drifts, err := rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	found := false
	for _, dr := range drifts {
		if dr.CampaignID == campB.ID {
			require.Equal(t, "orphan", dr.From)
			require.Equal(t, "in_slot", dr.To)
			require.GreaterOrEqual(t, dr.N, int64(1))
			found = true
		}
	}
	require.True(t, found, "drift da campanha B tem que aparecer no Count")

	// Heal: cura >= 1 linha; Count da B volta a zero.
	healed, err := rules.HealProjectionDrift(ctx, since)
	require.NoError(t, err)
	require.GreaterOrEqual(t, healed, int64(1))

	drifts, err = rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	for _, dr := range drifts {
		require.NotEqual(t, campB.ID, dr.CampaignID, "depois do Heal a B não pode ter drift")
	}

	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB)
}

// TestHealProjectionDriftForCampaign_ConvergesOutOfRange prova o fechamento do
// gap I1 (review 2026-07-14): uma projeção cujo detected_at cai FORA do período
// da campanha — categoria correta out_date — mas gravada errada (orphan) NUNCA é
// alcançada por RecategorizeForCampaign, que escopa por
// date_trunc(...) BETWEEN start_date AND end_date (recategorizeScope). Logo o
// backfill --all (que convergia campanha a campanha via RecategorizeForCampaign)
// jamais atribuía out_date a essas linhas fora-de-período e não fechava o
// histórico. HealProjectionDriftForCampaign é date-UNBOUNDED: escopa TODAS as
// projeções da campanha (qualquer data) e converge a fora-de-período pra out_date,
// cobrindo o mesmo conjunto que o reconciler HealProjectionDrift.
func TestHealProjectionDriftForCampaign_ConvergesOutOfRange(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// Campanha de 1 dia: sexta-feira mais recente <= agora (partição do mês existe).
	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	// Tocada 3 dias DEPOIS do fim da campanha → fora do período → out_date.
	outDay := day.AddDate(0, 0, 3)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "healcamp-cli"})
	require.NoError(t, err)
	camp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "healcamp-1day", ClientID: cli.ID,
		StartDate: day, EndDate: day, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "healcamp-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "healcamp-mat", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/hc", MasterSHA256: "healcamp-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Healcamp FM", Band: "FM", StreamURL: "http://example.com/healcamp",
	})
	require.NoError(t, err)

	// Regra geral cobrindo o dia/estação/tipo — uma tocada DENTRO do período seria
	// in_slot; a nossa cai fora do período, então vira out_date apesar da regra
	// (out_date é o primeiro ramo do categorizador, precede qualquer regra).
	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: camp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: day, EndDate: day,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 5,
	})
	require.NoError(t, err)

	// Tocada + projeção canônica fora do período. Create categoriza inline como
	// out_date; forçamos a gravação errada 'orphan' pra instalar o drift I1.
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: camp.ID,
		DetectedAt: outDay, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE detection_campaigns SET category = 'orphan' WHERE detection_id = $1 AND campaign_id = $2`,
		det.ID, camp.ID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, camp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, camp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	rules := NewDistributionRules(pool)

	// (1) O caminho date-bounded (RecategorizeForCampaign) NÃO alcança a projeção
	// fora do período [start,end] → continua orphan (o bug I1).
	require.NoError(t, rules.RecategorizeForCampaign(ctx, camp.ID))
	var afterBounded string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, camp.ID).Scan(&afterBounded))
	require.Equal(t, "orphan", afterBounded,
		"date-bounded RecategorizeForCampaign não pode alcançar projeção fora do período (I1)")

	// (2) O heal date-UNBOUNDED alcança a projeção e converge pra out_date.
	healed, err := rules.HealProjectionDriftForCampaign(ctx, camp.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, healed, int64(1))
	var afterHeal string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, camp.ID).Scan(&afterHeal))
	require.Equal(t, "out_date", afterHeal,
		"heal date-unbounded converge a projeção fora do período pra out_date")
}
