package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A 3ª tocada (fora da faixa) chega DEPOIS da meta ter fechado dentro da faixa
// → ela nasce bonus. E a 1ª, gravada out_slot quando chegou sozinha às 03:00,
// tem que ter sido reclassificada pra bonus pelo fechamento do dia.
func TestInsertPath_SettlesWholeCellDay(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "settle-cli"})
	require.NoError(t, err)
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "settle-cmp", ClientID: cli.ID,
		StartDate: day.AddDate(0, 0, -5), EndDate: day.AddDate(0, 0, 5),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "settle-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "settle-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/s", MasterSHA256: "settle-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Settle FM", Band: "FM", StreamURL: "http://example.com/settle/" + uuid.NewString(),
	})
	require.NoError(t, err)

	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: day.AddDate(0, 0, -5), EndDate: day.AddDate(0, 0, 5),
		WeekdayMask: 127, TimeStart: "10:00", TimeEnd: "12:00", PlaysPerDay: 2,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns dc USING detections d
		                WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                  AND d.campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	sp, _ := time.LoadLocation("America/Sao_Paulo")
	mk := func(h, m int) uuid.UUID {
		d, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
			StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
			DetectedAt:         time.Date(2026, 6, 10, h, m, 0, 0, sp),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
			Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
		})
		require.NoError(t, err)
		return d.ID
	}
	catOf := func(id uuid.UUID) string {
		var c string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id=$1`, id).Scan(&c))
		return c
	}
	projCatOf := func(id uuid.UUID) string {
		var c string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
			id, cmp.ID).Scan(&c))
		return c
	}

	early := mk(3, 0) // fora da faixa, meta ainda aberta
	require.Equal(t, "out_slot", catOf(early), "sozinha às 03:00, a meta está aberta")

	first := mk(10, 30)
	second := mk(11, 0) // fecha a meta dentro da faixa

	require.Equal(t, "in_slot", catOf(first))
	require.Equal(t, "in_slot", catOf(second))
	require.Equal(t, "bonus", catOf(early),
		"meta fechou dentro da faixa → a das 03:00 vira excedente")

	// A projeção canônica (detection_campaigns) tem que andar junto da base —
	// a grade e os relatórios lêem dela (F-119).
	require.Equal(t, "bonus", projCatOf(early), "projeção canônica reescrita junto")
	require.Equal(t, "in_slot", projCatOf(first))

	// 4ª tocada dentro da faixa, com a meta já fechada → nasce bonus.
	fourth := mk(11, 30)
	require.Equal(t, "bonus", catOf(fourth), "excedente dentro da faixa")
	require.Equal(t, "in_slot", catOf(first), "quem já preencheu a cota não perde a vaga")
}

// A reatribuição (§18.2.2) recomputa o lugar de uma tocada que JÁ está no banco.
// O fechamento tem que EXCLUIR essa linha do conjunto carregado — senão ela
// disputa a cota do dia contra si mesma e a meta 1 a joga pra bonus.
func TestInsertPath_ReattributionDoesNotDoubleCountItself(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "reat-cli"})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "reat-cmp", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "reat-spot")
	mats := NewMaterials(pool)
	matA, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "reat-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/ra", MasterSHA256: "reatA-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "reat-B", TypeID: &typeID, DurationSeconds: 15,
		MasterStoragePath: "/tmp/rb", MasterSHA256: "reatB-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Reat FM", Band: "FM", StreamURL: "http://example.com/reat/" + uuid.NewString(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns dc USING detections d
		                WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                  AND d.campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 127, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	require.NoError(t, err)

	dets := NewDetections(pool)
	detTime := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) // 09:00 BRT
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: cmp.ID,
		DetectedAt: detTime, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	require.NoError(t, err)
	require.Equal(t, "in_slot", det.Category)

	// Re-aponta pro corte B no mesmo dia/campanha: continua sendo UMA tocada.
	require.NoError(t, dets.ReattributeDetection(ctx, det.ID, det.DetectedAt, matB.ID, cmp.ID, stat.ID))

	var base, proj string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&base))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, cmp.ID).Scan(&proj))
	require.Equal(t, "in_slot", base, "a própria row não pode disputar a cota contra si mesma")
	require.Equal(t, "in_slot", proj)
}

// Multi-atribuição (F-119): o fechamento da campanha SECUNDÁRIA reescreve a
// projeção dela, mas nunca a tocada-base (que pertence à campanha canônica) —
// mesma guarda do recatApplySQL.
func TestSettleCellDay_SecondaryCampaignDoesNotTouchBaseRow(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "fanout-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "fanout-base", ClientID: cli.ID,
		StartDate: start, EndDate: end, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "fanout-secundaria", ClientID: cli.ID,
		StartDate: start, EndDate: end, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "fanout-spot")
	mats := NewMaterials(pool)
	matA, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "fanout-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/fa", MasterSHA256: "fanoutA-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "fanout-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/fb", MasterSHA256: "fanoutB-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Fanout FM", Band: "FM", StreamURL: "http://example.com/fanout/" + uuid.NewString(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns dc USING detections d
		                WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                  AND d.campaign_id = $1`, campA.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, campA.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Só a campanha B tem regra (meta 1, faixa 08:00–10:00).
	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: start, EndDate: end,
		WeekdayMask: 127, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	require.NoError(t, err)

	dets := NewDetections(pool)
	first := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) // 09:00 BRT
	d1, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: first, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	require.NoError(t, err)
	require.Equal(t, "bonus", d1.Category, "campanha A não tem regra → meta 0")

	// Projeção fan-out da d1 na B, nascida desatualizada (a regra da B só entrou
	// depois) — é ela que o fechamento da B tem que corrigir.
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, d1.ID, d1.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	// Fechamento da célula-dia da B para uma segunda tocada às 09:30.
	cat, err := dets.CategorizeFor(ctx, campB.ID, matB.ID, stat.ID, first.Add(30*time.Minute))
	require.NoError(t, err)
	require.Equal(t, "bonus", cat, "a d1 (mais cedo) fica com a única vaga da meta")

	var projB, base, projA string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		d1.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB, "projeção da B reescrita pelo fechamento da B")

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		d1.ID, d1.DetectedAt).Scan(&base))
	require.Equal(t, "bonus", base, "tocada-base (campanha A) intocada pelo fechamento da B")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		d1.ID, campA.ID).Scan(&projA))
	require.Equal(t, "bonus", projA, "projeção canônica da A intocada")
}

// O fechamento tem que pegar o advisory lock da célula-dia ANTES de ler. É o
// único ponto de encontro entre dois fechamentos concorrentes da mesma célula:
// quando nada precisa ser reescrito eles não compartilham nenhuma linha, então
// sem o lock os dois lêem o mesmo conjunto, os dois acham que há vaga na cota e
// os dois gravam in_slot (in_slot > N, silencioso). Aqui uma sessão externa
// segura a chave e o Create tem que ESPERAR — não passar direto.
func TestSettleCellDay_TakesCellDayAdvisoryLockBeforeReading(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "lock-cli"})
	require.NoError(t, err)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "lock-cmp", ClientID: cli.ID,
		StartDate: start, EndDate: end, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "lock-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "lock-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/lk", MasterSHA256: "lock-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Lock FM", Band: "FM", StreamURL: "http://example.com/lock/" + uuid.NewString(),
	})
	require.NoError(t, err)
	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: start, EndDate: end,
		WeekdayMask: 127, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns dc USING detections d
		                WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                  AND d.campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	sp, _ := time.LoadLocation("America/Sao_Paulo")
	at := time.Date(2026, 6, 10, 9, 0, 0, 0, sp)
	// Mesma chave que settleCellDay monta: (campanha, emissora, dia local SP).
	key := cmp.ID.String() + "|" + stat.ID.String() + "|" + at.Format("2006-01-02")

	holder, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key)
	require.NoError(t, err)

	in := CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: at, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	began := time.Now()
	_, err = NewDetections(pool).Create(blockedCtx, in)
	waited := time.Since(began)
	require.Error(t, err, "com a célula-dia travada por outra sessão, o Create tem que esperar")
	require.GreaterOrEqual(t, waited, 1500*time.Millisecond,
		"tem que ter BLOQUEADO no lock, não falhado na hora")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM detections WHERE campaign_id = $1`, cmp.ID).Scan(&n))
	require.Zero(t, n, "nada persistido enquanto o lock estava tomado")

	require.NoError(t, holder.Rollback(ctx)) // solta o advisory lock
	det, err := NewDetections(pool).Create(ctx, in)
	require.NoError(t, err, "solto o lock, o mesmo Create passa")
	require.Equal(t, "in_slot", det.Category)
}
