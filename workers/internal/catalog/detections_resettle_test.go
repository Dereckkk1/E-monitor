package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// cellFixture monta uma célula-dia completa (cliente, campanha, tipo, material,
// emissora, regra) pros testes de re-fechamento. A regra é sempre 10:00–12:00,
// todo dia, no período 01–30/06/2026; o dia dos testes é 10/06/2026 (quarta).
type cellFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	dets    *Detections
	client  uuid.UUID
	camp    uuid.UUID
	typeID  uuid.UUID
	mat     uuid.UUID
	station uuid.UUID
}

func newCellFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, playsPerDay int16) *cellFixture {
	t.Helper()

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: name + "-cli"})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: name + "-cmp", ClientID: cli.ID,
		StartDate: start, EndDate: end, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, name+"-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: name + "-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/" + name, MasterSHA256: name + "-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: name + " FM", Band: "FM", StreamURL: "http://example.com/" + name + "/" + uuid.NewString(),
	})
	require.NoError(t, err)
	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: start, EndDate: end,
		WeekdayMask: 127, TimeStart: "10:00", TimeEnd: "12:00", PlaysPerDay: playsPerDay,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns dc USING detections d
		                WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                  AND d.station_id = $1`, stat.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE station_id = $1`, stat.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	return &cellFixture{
		ctx: ctx, pool: pool, dets: NewDetections(pool),
		client: cli.ID, camp: cmp.ID, typeID: typeID, mat: mat.ID, station: stat.ID,
	}
}

// play insere uma veiculação às h:m (hora local SP) do dia da célula.
func (f *cellFixture) play(t *testing.T, h, m int) *Detection {
	t.Helper()
	sp, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)
	det, err := f.dets.Create(f.ctx, CreateDetectionInput{
		StationID: f.station, CommercialID: f.mat, CampaignID: f.camp,
		DetectedAt:         time.Date(2026, 6, 10, h, m, 0, 0, sp),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	return det
}

// cat lê a categoria da tocada-base.
func (f *cellFixture) cat(t *testing.T, d *Detection) string {
	t.Helper()
	var c string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		d.ID, d.DetectedAt).Scan(&c))
	return c
}

// projCat lê a categoria da projeção canônica (detection_campaigns) — é dela que
// a grade e os relatórios leem (F-119), então ela tem que andar junto da base.
func (f *cellFixture) projCat(t *testing.T, d *Detection) string {
	t.Helper()
	var c string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND detected_at=$2 AND campaign_id=$3`,
		d.ID, d.DetectedAt, f.camp).Scan(&c))
	return c
}

// seedUser cria um usuário real — Ignore grava ignored_by, que tem FK pra users.
func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, 'x', 'admin') RETURNING id`,
		"resettle-"+uuid.NewString()+"@test.local").Scan(&id))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
	return id
}

// Retratar uma tocada tira ela do conjunto aprovado e LIBERA a vaga que ela
// ocupava na cota do dia. Sem re-fechamento a vaga fica vazia pra sempre: a
// excedente segue bonus e o déficit fica superestimado.
func TestResettle_RetractRefillsTheFreedSlot(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "retract", 2) // meta 2/dia, faixa 10:00–12:00

	outside := f.play(t, 3, 0) // fora da faixa
	p1 := f.play(t, 10, 10)
	p2 := f.play(t, 10, 20)
	p3 := f.play(t, 10, 30) // excedente: a meta fechou em p1+p2

	require.Equal(t, "in_slot", f.cat(t, p1))
	require.Equal(t, "in_slot", f.cat(t, p2))
	require.Equal(t, "bonus", f.cat(t, p3), "meta 2 já fechada dentro da faixa")
	require.Equal(t, "bonus", f.cat(t, outside), "meta fechada → a de fora é excedente")

	// Retrai a 1ª in_slot: a vaga liberada é do p3 (próximo cronológico DENTRO
	// da faixa), não da tocada de fora — out_slot nunca vira in_slot (spec §2.4).
	require.NoError(t, f.dets.RetractByID(ctx, p1.ID, p1.DetectedAt, time.Now().UTC()))
	require.Equal(t, "in_slot", f.cat(t, p3), "a vaga liberada promove o excedente")
	require.Equal(t, "in_slot", f.projCat(t, p3), "projeção canônica anda junto")
	require.Equal(t, "in_slot", f.cat(t, p2), "quem já tinha vaga não a perde")
	require.Equal(t, "bonus", f.cat(t, outside), "meta continua fechada dentro da faixa")

	// Retrai a 2ª: agora só p3 fica dentro da faixa, a meta 2 REABRE e a tocada
	// de fora volta a segurar o déficit.
	require.NoError(t, f.dets.RetractByID(ctx, p2.ID, p2.DetectedAt, time.Now().UTC()))
	require.Equal(t, "in_slot", f.cat(t, p3))
	require.Equal(t, "out_slot", f.cat(t, outside), "meta reaberta → a de fora volta a out_slot")
	require.Equal(t, "out_slot", f.projCat(t, outside))
}

// Direção inversa: a tocada que VOLTA pro conjunto aprovado retoma a vaga da
// cota (é cronologicamente anterior) e rebaixa quem tinha ocupado o lugar dela.
// Cobre os 3 caminhos de re-entrada — ClearRetraction, Restore (des-ignorar) —
// e os 2 de saída que faltavam — Ignore e MarkAmbiguous.
func TestResettle_ReenteringPlayRetakesTheQuota(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "reenter", 1) // meta 1/dia
	userID := seedUser(t, ctx, pool)

	p1 := f.play(t, 10, 10)
	p2 := f.play(t, 10, 20)
	require.Equal(t, "in_slot", f.cat(t, p1))
	require.Equal(t, "bonus", f.cat(t, p2))

	// --- retração / des-retração ---
	require.NoError(t, f.dets.RetractByID(ctx, p1.ID, p1.DetectedAt, time.Now().UTC()))
	require.Equal(t, "in_slot", f.cat(t, p2), "vaga liberada pela retração")

	require.NoError(t, f.dets.ClearRetraction(ctx, p1.ID, p1.DetectedAt))
	require.Equal(t, "in_slot", f.cat(t, p1), "de volta ao conjunto, retoma a vaga (é a mais antiga)")
	require.Equal(t, "in_slot", f.projCat(t, p1))
	require.Equal(t, "bonus", f.cat(t, p2), "rebaixada de volta a excedente")

	// --- ignorar / restaurar ---
	require.NoError(t, f.dets.Ignore(ctx, p1.ID, userID))
	require.Equal(t, "in_slot", f.cat(t, p2), "ignorada sai da cota")

	require.NoError(t, f.dets.Restore(ctx, p1.ID))
	require.Equal(t, "in_slot", f.cat(t, p1), "restaurada retoma a vaga")
	require.Equal(t, "bonus", f.cat(t, p2))

	// --- gêmeo ambíguo (sai do conjunto: ambiguous ⟺ retracted) ---
	require.NoError(t, f.dets.MarkAmbiguous(ctx, p1.ID, p1.DetectedAt))
	require.Equal(t, "in_slot", f.cat(t, p2), "ambígua sai da cota")
	require.Equal(t, "in_slot", f.projCat(t, p2))
}

// A reatribuição move a tocada de uma célula-dia pra outra. O destino já era
// refechado; a ORIGEM não — e é lá que a vaga liberada precisa ser reaproveitada.
func TestResettle_ReattributionSettlesSourceCellDay(t *testing.T) {
	ctx, pool := newTestDB(t)
	src := newCellFixture(t, ctx, pool, "reatsrc", 1) // meta 1 na campanha de origem
	dst := newCellFixture(t, ctx, pool, "reatdst", 1) // meta 1 na campanha de destino

	// As duas células precisam da MESMA emissora pra reatribuição fazer sentido:
	// o destino é (campanha nova, tipo do material novo, MESMA emissora, dia).
	_, err := pool.Exec(ctx, `UPDATE distribution_rules SET station_ids = $2
	                          WHERE campaign_id = $1`,
		dst.camp, []uuid.UUID{src.station})
	require.NoError(t, err)
	dst.station = src.station

	p1 := src.play(t, 10, 10)
	p2 := src.play(t, 10, 20)
	require.Equal(t, "in_slot", src.cat(t, p1))
	require.Equal(t, "bonus", src.cat(t, p2), "meta 1 já ocupada pela p1")

	// p1 sai da campanha de origem pro material/campanha de destino.
	require.NoError(t, src.dets.ReattributeDetection(ctx, p1.ID, p1.DetectedAt,
		dst.mat, dst.camp, src.station))

	// Destino: a tocada reatribuída ocupa a meta 1 de lá.
	var dstCat string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns
		 WHERE detection_id=$1 AND detected_at=$2 AND campaign_id=$3`,
		p1.ID, p1.DetectedAt, dst.camp).Scan(&dstCat))
	require.Equal(t, "in_slot", dstCat, "célula-dia de destino fechada com a tocada nova")

	// Origem: a vaga que a p1 deixou tem que ser ocupada pela p2.
	require.Equal(t, "in_slot", src.cat(t, p2), "célula-dia de ORIGEM refechada")
	require.Equal(t, "in_slot", src.projCat(t, p2))
}
