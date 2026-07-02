package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRecategorizeForCampaign_RespectsOverride: o insert-path (categorizer.Categorize)
// respeita distribution_overrides — quando há override pra (campanha, tipo,
// estação, dia), ele supersede as rules. Mas recatClassifyTailSQL (usado por
// RecategorizeForRule/ForCampaign/ForMaterial) IGNORAVA overrides: re-rodar uma
// regra reclassificava pela regra e flipava a categoria que o insert tinha
// gravado pelo override (audit 2026-07-02 G1). Este teste prova que o recat
// respeita o override (plays_expected=0 → out_slot), não a regra (in_slot).
func TestRecategorizeForCampaign_RespectsOverride(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "recat-ov-cli"})
	require.NoError(t, err)
	now := time.Now()
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "recat-ov", ClientID: cli.ID,
		StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 1),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "recat-ov-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "recat-ov-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/ro", MasterSHA256: "recat-ov-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Recat OV FM", Band: "FM", StreamURL: "http://example.com/recatov",
	})
	require.NoError(t, err)

	// Regra geral (material_ids vazio) cobrindo o dia inteiro → detecção de hoje é in_slot.
	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{},
		StartDate:   now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 1),
		WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "23:59", PlaysPerDay: 10,
	})
	require.NoError(t, err)

	detectedAt := now
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detectedAt, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM distribution_overrides WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Precondição: sem override, a regra classifica in_slot.
	var cat string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&cat))
	require.Equal(t, "in_slot", cat, "precondição: regra cobre o dia → in_slot")

	// Override plays_expected=0 (faixa inerte) pra célula+dia → tudo out_slot.
	// for_date usa a MESMA expressão que insert-path (detections.go:233) e o recat.
	_, err = pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date, plays_expected, time_start, time_end)
		VALUES ($1, $2, $3, ($4::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date, 0, '00:00', '00:01')`,
		cmp.ID, typeID, stat.ID, detectedAt)
	require.NoError(t, err)

	// Recategoriza a campanha → deve respeitar o override, não a regra.
	require.NoError(t, rules.RecategorizeForCampaign(ctx, cmp.ID))

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&cat))
	require.Equal(t, "out_slot", cat,
		"recat deve respeitar override plays_expected=0 (out_slot), não a regra (in_slot)")
}
