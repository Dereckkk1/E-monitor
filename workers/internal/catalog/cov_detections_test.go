package catalog

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covCDay é o meio-dia UTC de 2026-06-10 mais um deslocamento em minutos —
// data fixa dentro das partições, imune a virada de dia.
func covCDay(minutes int) time.Time {
	return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
}

func TestCovC_Detections_CreateManual(t *testing.T) {
	w := covCSeedWorld(t, "manual")
	dets := NewDetections(w.pool)
	user := covCUser(t, w)

	// Sem vínculo material × emissora na campanha → recusa e não grava nada.
	_, err := dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: covCDay(0), ManualBy: user, ManualNote: "x",
	})
	require.ErrorIs(t, err, ErrMaterialNotLinkedToStation)
	var n int
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT count(*) FROM detections WHERE station_id=$1`, w.station).Scan(&n))
	assert.Zero(t, n)

	// Vínculo existe mas para OUTRA emissora → continua recusando.
	outra := covCSeedStation(t, w, "Rádio Teste FM Outra")
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{outra}))
	_, err = dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: covCDay(0), ManualBy: user,
	})
	require.ErrorIs(t, err, ErrMaterialNotLinkedToStation)

	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	got, err := dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: covCDay(0), ManualBy: user, ManualNote: "  comprovante enviado  ",
	})
	require.NoError(t, err)
	assert.Equal(t, w.station, got.StationID)
	assert.Equal(t, w.mat, got.CommercialID)
	assert.Equal(t, w.camp, got.CampaignID)
	assert.InDelta(t, 1.0, got.Confidence, 0.0001, "manual é ground-truth declarado")
	assert.Zero(t, got.HashCount)
	assert.Equal(t, "missing", got.EvidenceStatus, "veiculação manual não tem áudio")
	require.NotNil(t, got.ManualAt)
	require.NotNil(t, got.ManualBy)
	assert.Equal(t, user, *got.ManualBy)
	require.NotNil(t, got.ManualNote)
	assert.Equal(t, "comprovante enviado", *got.ManualNote, "nota é aparada")
	// Sem regra de distribuição a meta do dia é 0 → excedente = bonus.
	assert.Equal(t, "bonus", got.Category)

	var proj string
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		got.ID, w.camp).Scan(&proj))
	assert.Equal(t, "bonus", proj, "projeção canônica nasce junto, com a mesma categoria")

	// Nota só com espaços vira NULL.
	semNota, err := dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: covCDay(5), ManualBy: user, ManualNote: "   ",
	})
	require.NoError(t, err)
	assert.Nil(t, semNota.ManualNote)
}

func TestCovC_Detections_CreateManual_RespeitaCotaDoDia(t *testing.T) {
	w := covCSeedWorld(t, "manual-quota")
	dets := NewDetections(w.pool)
	user := covCUser(t, w)
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	_, err := NewDistributionRules(w.pool).Create(w.ctx, CreateDistributionRuleInput{
		CampaignID: w.camp, TypeID: w.typeID, StationIDs: []uuid.UUID{w.station},
		MaterialIDs: []uuid.UUID{},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 127, TimeStart: "10:00", TimeEnd: "12:00", PlaysPerDay: 1,
	})
	require.NoError(t, err)

	sp, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)
	first, err := dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: time.Date(2026, 6, 10, 10, 30, 0, 0, sp), ManualBy: user,
	})
	require.NoError(t, err)
	second, err := dets.CreateManual(w.ctx, CreateManualInput{
		StationID: w.station, CommercialID: w.mat, CampaignID: w.camp,
		DetectedAt: time.Date(2026, 6, 10, 11, 0, 0, 0, sp), ManualBy: user,
	})
	require.NoError(t, err)
	// Cota 1/dia: a primeira dentro da faixa preenche, a segunda é excedente.
	assert.Equal(t, "in_slot", first.Category)
	assert.Equal(t, "bonus", second.Category)
}

func TestCovC_Detections_AggregatesEImpactoCanonico(t *testing.T) {
	w := covCSeedWorld(t, "aggr")
	dets := NewDetections(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Dois Agg")
	mat2 := covCSeedMaterial(t, w, "Material B", 15)

	// Estação 1 / material A: 2 in_slot, 1 out_slot, 1 out_date, 2 bonus.
	for i, cat := range []string{"in_slot", "in_slot", "out_slot", "out_date", "bonus", "bonus"} {
		covCInsertDet(t, w, w.mat, w.station, covCDay(i), cat)
	}
	// Estação 2 / material B: 1 in_slot + 1 legado 'orphan' (sinônimo de bonus).
	covCInsertDet(t, w, mat2, st2, covCDay(10), "in_slot")
	covCInsertDet(t, w, mat2, st2, covCDay(11), "orphan")
	// Fora do conjunto aprovado: retratada, ignorada e audit_rejected NÃO contam.
	retr := covCInsertDet(t, w, w.mat, w.station, covCDay(20), "in_slot")
	ign := covCInsertDet(t, w, w.mat, w.station, covCDay(21), "in_slot")
	rej := covCInsertDet(t, w, w.mat, w.station, covCDay(22), "in_slot")
	user := covCUser(t, w)
	_, err := w.pool.Exec(w.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, retr)
	require.NoError(t, err)
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET ignored_at = now(), ignored_by = $2 WHERE id = $1`, ign, user)
	require.NoError(t, err)
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET evidence_status = 'audit_rejected' WHERE id = $1`, rej)
	require.NoError(t, err)

	filter := AggregateFilter{CampaignIDs: []uuid.UUID{w.camp}}

	// ── por (material × emissora) ──
	rows, err := dets.AggregateByMaterialStation(w.ctx, filter)
	require.NoError(t, err)
	byKey := map[[2]uuid.UUID]MaterialStationRow{}
	for _, r := range rows {
		byKey[[2]uuid.UUID{r.MaterialID, r.StationID}] = r
	}
	a := byKey[[2]uuid.UUID{w.mat, w.station}]
	assert.Equal(t, 6, a.Count)
	assert.Equal(t, 2, a.InSlotCount)
	assert.Equal(t, 1, a.OutSlotCount)
	assert.Equal(t, 1, a.OutDateCount)
	assert.Equal(t, 2, a.BonusCount)
	// Base canônica de impactos = in_slot + bonus (out_slot/out_date não valem).
	assert.Equal(t, 4, a.ImpactCount)
	assert.Equal(t, a.Count, a.InSlotCount+a.OutSlotCount+a.OutDateCount+a.BonusCount,
		"breakdown por categoria soma sempre == Count")
	assert.True(t, a.FirstDetectedAt.Equal(covCDay(0)))
	assert.True(t, a.LastDetectedAt.Equal(covCDay(5)))
	assert.Equal(t, "Material aggr", a.MaterialTitle)
	b := byKey[[2]uuid.UUID{mat2, st2}]
	assert.Equal(t, 2, b.Count)
	assert.Equal(t, 1, b.InSlotCount)
	assert.Equal(t, 1, b.BonusCount, "'orphan' legado entra como bonificação")
	assert.Equal(t, 2, b.ImpactCount)

	// ── por emissora ──
	stRows, err := dets.AggregateByStation(w.ctx, filter)
	require.NoError(t, err)
	require.Len(t, stRows, 2)
	assert.Equal(t, w.station, stRows[0].StationID, "ordena por total desc")
	assert.Equal(t, 6, stRows[0].Count)
	assert.Equal(t, 4, stRows[0].ImpactCount)
	assert.Equal(t, st2, stRows[1].StationID)
	assert.Equal(t, 2, stRows[1].ImpactCount)
	assert.Nil(t, stRows[0].StationPMMTarget, "sem linha em client_station_pmm → nil, não zero")

	// ── por material ──
	byMat, err := dets.AggregateByMaterial(w.ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 8, byMat.TotalDetections)
	assert.Equal(t, 2, byMat.DistinctMaterials)
	require.Len(t, byMat.Data, 2)
	assert.Equal(t, w.mat, byMat.Data[0].MaterialID)
	assert.Equal(t, 6, byMat.Data[0].Count)

	// Filtro de data recorta o intervalo.
	from := covCDay(10)
	to := covCDay(11)
	ranged, err := dets.AggregateByStation(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, StartDate: &from, EndDate: &to})
	require.NoError(t, err)
	require.Len(t, ranged, 1)
	assert.Equal(t, st2, ranged[0].StationID)

	// Sem campanha que case → listas vazias e não-nil.
	none, err := dets.AggregateByStation(w.ctx, AggregateFilter{CampaignIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	noneMS, err := dets.AggregateByMaterialStation(w.ctx, AggregateFilter{CampaignIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.NotNil(t, noneMS)
	assert.Empty(t, noneMS)
	noneMat, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{CampaignIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.NotNil(t, noneMat.Data)
	assert.Zero(t, noneMat.TotalDetections)
}

func TestCovC_Detections_AggregateByMaterial_FiltrosQeCarteira(t *testing.T) {
	w := covCSeedWorld(t, "aggr-q")
	dets := NewDetections(w.pool)
	matB := covCSeedMaterial(t, w, "Jingle Primavera", 15)
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	covCInsertDet(t, w, matB, w.station, covCDay(1), "in_slot")
	covCInsertDet(t, w, matB, w.station, covCDay(2), "bonus")

	// Busca por texto (acento/caixa-insensitiva) filtra por título do material.
	q, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, Q: "  PRIMAVERA  "})
	require.NoError(t, err)
	require.Len(t, q.Data, 1)
	assert.Equal(t, matB, q.Data[0].MaterialID)
	assert.Equal(t, 2, q.Data[0].Count)
	require.NotNil(t, q.Data[0].MaterialDurationSec)
	assert.InDelta(t, 15, *q.Data[0].MaterialDurationSec, 0.001)
	require.NotNil(t, q.Data[0].MaterialTypeID)
	assert.Equal(t, w.typeID, *q.Data[0].MaterialTypeID)

	// No máximo 4 tokens contam: o 5o ("inexistente") é ignorado, então a busca
	// ainda acha o material pelos 4 primeiros.
	q5, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, Q: "jingle primavera rádio teste inexistente"})
	require.NoError(t, err)
	require.Len(t, q5.Data, 1)
	assert.Equal(t, matB, q5.Data[0].MaterialID)

	// Token que não casa com nada exclui tudo (tokens são AND).
	semMatch, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, Q: "zzzinexistente"})
	require.NoError(t, err)
	assert.Empty(t, semMatch.Data)

	// Escopo de carteira: cliente dono vê; cliente alheio e carteira vazia não.
	mine, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, ClientIDs: []uuid.UUID{w.client}})
	require.NoError(t, err)
	assert.Equal(t, 3, mine.TotalDetections)
	other, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, ClientIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.Zero(t, other.TotalDetections)
	empty, err := dets.AggregateByMaterial(w.ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{w.camp}, ClientIDs: []uuid.UUID{}})
	require.NoError(t, err)
	assert.Zero(t, empty.TotalDetections, "carteira vazia falha fechada")
}

func TestCovC_Detections_ListPaged_FiltrosEOrdenacao(t *testing.T) {
	w := covCSeedWorld(t, "listpaged")
	dets := NewDetections(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Segunda")
	matB := covCSeedMaterial(t, w, "Vinheta Especial", 15)
	id0 := covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	id1 := covCInsertDet(t, w, matB, w.station, covCDay(10), "bonus")
	id2 := covCInsertDet(t, w, w.mat, st2, covCDay(20), "out_slot")
	camps := []uuid.UUID{w.camp}

	ids := func(r *ListPagedResult) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(r.Data))
		for _, d := range r.Data {
			out = append(out, d.ID)
		}
		return out
	}

	desc, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id2, id1, id0}, ids(desc), "default = mais recente primeiro")
	assert.Equal(t, 3, desc.Total)
	assert.Equal(t, 1, desc.Page)
	assert.Equal(t, 10, desc.PageSize, "pageSize 0 cai no default")
	assert.Equal(t, 1, desc.TotalPages)
	require.NotNil(t, desc.Data[0].ClientName)
	assert.Equal(t, "Cliente Teste listpaged", *desc.Data[0].ClientName)
	require.NotNil(t, desc.Data[0].CampaignName)
	assert.Equal(t, "Campanha listpaged", *desc.Data[0].CampaignName)

	asc, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Sort: "detected_at_asc"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id0, id1, id2}, ids(asc))

	// Página inválida e pageSize fora de 1..200 são normalizados.
	norm, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Page: -3, PageSize: 999})
	require.NoError(t, err)
	assert.Equal(t, 1, norm.Page)
	assert.Equal(t, 10, norm.PageSize)

	// Paginação: pageSize 2 → 2 páginas, a segunda com 1 linha.
	p2, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Page: 2, PageSize: 2, Sort: "detected_at_asc"})
	require.NoError(t, err)
	assert.Equal(t, 2, p2.TotalPages)
	assert.Equal(t, 3, p2.Total)
	assert.Equal(t, []uuid.UUID{id2}, ids(p2))

	// Filtros do MCP: emissora, material e categorias.
	byStation, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, StationID: &st2})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id2}, ids(byStation))
	byMat, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, MaterialID: &matB})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id1}, ids(byMat))
	byCat, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Categories: []string{"bonus", "out_slot"}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{id1, id2}, ids(byCat))

	// Texto livre cruza título do material, nome da emissora e cliente.
	byText, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Q: "vinheta"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id1}, ids(byText))
	byTwoTokens, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, Q: "segunda material"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id2}, ids(byTwoTokens), "tokens são AND")

	// Janela de datas.
	from, to := covCDay(5), covCDay(15)
	win, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, StartDate: &from, EndDate: &to})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{id1}, ids(win))

	// Carteira: cliente alheio não enxerga; seleção de campanhas vazia casa zero.
	alheio, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: camps, ClientIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.Zero(t, alheio.Total)
	assert.Equal(t, 1, alheio.TotalPages)
	assert.NotNil(t, alheio.Data)
	vazia, err := dets.ListPaged(w.ctx, ListPagedFilter{CampaignIDs: []uuid.UUID{}})
	require.NoError(t, err)
	assert.Zero(t, vazia.Total)
}

func TestCovC_Detections_IterateForExport(t *testing.T) {
	w := covCSeedWorld(t, "export")
	dets := NewDetections(w.pool)
	matB := covCSeedMaterial(t, w, "Spot Natal", 15)
	typeB := covCSeedType(t, w, "covc-export-tipo-b")
	_, err := w.pool.Exec(w.ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, matB, typeB)
	require.NoError(t, err)
	id0 := covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	id1 := covCInsertDet(t, w, matB, w.station, covCDay(10), "in_slot")

	// Preço unitário só aparece em per_insertion e é por tipo do material.
	_, err = NewPricing(w.pool).Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: w.station, Mode: PricingModePerInsertion,
		PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 42.5}},
	})
	require.NoError(t, err)

	var seen []DetectionEnriched
	err = dets.IterateForExport(w.ctx, ListPagedFilter{CampaignIDs: []uuid.UUID{w.camp}, Sort: "detected_at_asc"},
		func(d DetectionEnriched) error { seen = append(seen, d); return nil })
	require.NoError(t, err)
	require.Len(t, seen, 2)
	assert.Equal(t, id0, seen[0].ID)
	assert.Equal(t, id1, seen[1].ID)
	require.NotNil(t, seen[0].UnitPrice)
	assert.InDelta(t, 42.5, *seen[0].UnitPrice, 0.001)
	assert.Nil(t, seen[1].UnitPrice, "tipo B não tem preço cadastrado")
	assert.NotNil(t, seen[0].StationShortID)

	// Ordem default = mais recente primeiro; texto livre filtra.
	var desc []uuid.UUID
	require.NoError(t, dets.IterateForExport(w.ctx, ListPagedFilter{CampaignIDs: []uuid.UUID{w.camp}},
		func(d DetectionEnriched) error { desc = append(desc, d.ID); return nil }))
	assert.Equal(t, []uuid.UUID{id1, id0}, desc)
	var filtered []uuid.UUID
	require.NoError(t, dets.IterateForExport(w.ctx, ListPagedFilter{CampaignIDs: []uuid.UUID{w.camp}, Q: "natal"},
		func(d DetectionEnriched) error { filtered = append(filtered, d.ID); return nil }))
	assert.Equal(t, []uuid.UUID{id1}, filtered)

	// Erro do callback interrompe a iteração e propaga.
	sentinel := errors.New("parou")
	calls := 0
	err = dets.IterateForExport(w.ctx, ListPagedFilter{CampaignIDs: []uuid.UUID{w.camp}},
		func(DetectionEnriched) error { calls++; return sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, calls)

	// Pool/contexto quebrado devolve erro.
	require.Error(t, dets.IterateForExport(covCCancelled(), ListPagedFilter{},
		func(DetectionEnriched) error { return nil }))
}

func TestCovC_Detections_UpdateEvidence(t *testing.T) {
	w := covCSeedWorld(t, "evid")
	dets := NewDetections(w.pool)
	at := covCDay(0)
	id := covCInsertDet(t, w, w.mat, w.station, at, "in_slot")

	require.NoError(t, dets.UpdateEvidence(w.ctx, id, at, "available", "evidence/x.m4a", 4096))
	got, err := dets.Get(w.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "available", got.EvidenceStatus)
	require.NotNil(t, got.EvidenceKey)
	assert.Equal(t, "evidence/x.m4a", *got.EvidenceKey)
	require.NotNil(t, got.EvidenceSizeBytes)
	assert.EqualValues(t, 4096, *got.EvidenceSizeBytes)

	// audit_rejected tira a tocada do conjunto aprovado (transacional).
	require.NoError(t, dets.UpdateEvidence(w.ctx, id, at, "audit_rejected", "", 0))
	got, err = dets.Get(w.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "audit_rejected", got.EvidenceStatus)
	var approved int
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT count(*) FROM detections d WHERE d.id=$1 AND d.detected_at=$2 AND `+ApprovedDetectionsFilter,
		id, at).Scan(&approved))
	assert.Zero(t, approved)

	// Detecção inexistente é no-op silencioso nos dois caminhos (UPDATE cru de 0
	// linhas; mutateApprovedSet sem células afetadas).
	require.NoError(t, dets.UpdateEvidence(w.ctx, uuid.New(), at, "missing", "", 0))
	require.NoError(t, dets.UpdateEvidence(w.ctx, uuid.New(), at, "audit_rejected", "", 0))
}

func TestCovC_Detections_ResettleCellDay(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "covc-resettle", 1)
	// Duas tocadas dentro da faixa com cota 1: a 1ª in_slot, a 2ª bonus.
	d1 := f.play(t, 10, 5)
	d2 := f.play(t, 10, 15)
	require.Equal(t, "in_slot", f.cat(t, d1))
	require.Equal(t, "bonus", f.cat(t, d2))

	// Corrompe a categoria direto no banco e prova que o re-fechamento a conserta.
	_, err := f.pool.Exec(f.ctx, `UPDATE detections SET category = 'out_slot' WHERE id = $1`, d1.ID)
	require.NoError(t, err)
	_, err = f.pool.Exec(f.ctx, `UPDATE detections SET category = 'in_slot' WHERE id = $1`, d2.ID)
	require.NoError(t, err)

	require.NoError(t, f.dets.ResettleCellDay(f.ctx, f.camp, f.mat, f.station, d1.DetectedAt))
	assert.Equal(t, "in_slot", f.cat(t, d1), "a mais antiga volta a ocupar a vaga")
	assert.Equal(t, "bonus", f.cat(t, d2))
	assert.Equal(t, "in_slot", f.projCat(t, d1), "projeção canônica acompanha a base")

	require.Error(t, f.dets.ResettleCellDay(covCCancelled(), f.camp, f.mat, f.station, d1.DetectedAt))
}

func TestCovC_Detections_ReattributeDetection(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "covc-reattr", 1)
	det := f.play(t, 10, 30)
	require.Equal(t, "in_slot", f.cat(t, det))

	// Segundo corte do mesmo cliente, mesmo tipo.
	mat2, err := NewMaterials(f.pool).Create(f.ctx, CreateMaterialInput{
		ClientID: f.client, Title: "Corte 15s", TypeID: &f.typeID, DurationSeconds: 15,
		MasterStoragePath: "/tmp/covc-reattr-2", MasterSHA256: "covc-" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { f.pool.Exec(f.ctx, `DELETE FROM materials WHERE id = $1`, mat2.ID) })

	require.NoError(t, f.dets.ReattributeDetection(f.ctx, det.ID, det.DetectedAt, mat2.ID, f.camp, f.station))
	got, err := f.dets.Get(f.ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, mat2.ID, got.CommercialID, "tocada passa a apontar o novo corte")
	assert.Equal(t, "in_slot", got.Category, "continua ocupando a vaga da cota")
	var projMat uuid.UUID
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT commercial_id FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, f.camp).Scan(&projMat))
	assert.Equal(t, mat2.ID, projMat, "projeção canônica anda junto com a base")

	// Detecção inexistente → erro (ErrNoRows) e nada é alterado.
	err = f.dets.ReattributeDetection(f.ctx, uuid.New(), det.DetectedAt, mat2.ID, f.camp, f.station)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Detections_ReattributeRejectedDetection(t *testing.T) {
	ctx, pool := newTestDB(t)
	f := newCellFixture(t, ctx, pool, "covc-reattr-rej", 1)
	det := f.play(t, 10, 30)

	mat2, err := NewMaterials(f.pool).Create(f.ctx, CreateMaterialInput{
		ClientID: f.client, Title: "Corte 15s rej", TypeID: &f.typeID, DurationSeconds: 15,
		MasterStoragePath: "/tmp/covc-reattr-rej-2", MasterSHA256: "covc-" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { f.pool.Exec(f.ctx, `DELETE FROM materials WHERE id = $1`, mat2.ID) })

	// Linha ainda aprovada: o guard devolve ErrReattributeNoRow e não altera nada.
	err = f.dets.ReattributeRejectedDetection(f.ctx, det.ID, det.DetectedAt, mat2.ID, f.camp, f.station, 0.8)
	require.ErrorIs(t, err, ErrReattributeNoRow)
	got, err := f.dets.Get(f.ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, f.mat, got.CommercialID)

	// Rejeitada pelo audit: reatribui, vira 'missing' (conta, sem áudio) e grava a cobertura.
	require.NoError(t, f.dets.UpdateEvidence(f.ctx, det.ID, det.DetectedAt, "audit_rejected", "", 0))
	require.NoError(t, f.dets.ReattributeRejectedDetection(f.ctx, det.ID, det.DetectedAt, mat2.ID, f.camp, f.station, 0.8))
	got, err = f.dets.Get(f.ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, mat2.ID, got.CommercialID)
	assert.Equal(t, "missing", got.EvidenceStatus)
	require.NotNil(t, got.AuditCoverage)
	assert.InDelta(t, 0.8, *got.AuditCoverage, 0.0001)
	assert.Equal(t, "in_slot", got.Category)
}

func TestCovC_Detections_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "det-dberr")
	dets := NewDetections(w.pool)
	ctx := covCCancelled()
	now := covCDay(0)
	id := uuid.New()

	_, err := dets.Create(ctx, CreateDetectionInput{StationID: w.station, CommercialID: w.mat, CampaignID: w.camp, DetectedAt: now})
	require.Error(t, err)
	_, err = dets.CreateManual(ctx, CreateManualInput{StationID: w.station, CommercialID: w.mat, CampaignID: w.camp, DetectedAt: now})
	require.Error(t, err)
	_, err = dets.ListPaged(ctx, ListPagedFilter{})
	require.Error(t, err)
	_, err = dets.AggregateByMaterial(ctx, AggregateFilter{})
	require.Error(t, err)
	_, err = dets.AggregateByMaterialStation(ctx, AggregateFilter{})
	require.Error(t, err)
	_, err = dets.AggregateByStation(ctx, AggregateFilter{})
	require.Error(t, err)
	_, err = dets.Get(ctx, id)
	require.Error(t, err)
	_, err = dets.GetClientID(ctx, id)
	require.Error(t, err)
	_, err = dets.List(ctx, ListFilter{})
	require.Error(t, err)
	require.Error(t, dets.UpdateEvidence(ctx, id, now, "available", "k", 1))
	require.Error(t, dets.UpdateEvidence(ctx, id, now, "audit_rejected", "", 0))
	require.Error(t, dets.ReattributeDetection(ctx, id, now, w.mat, w.camp, w.station))
	require.Error(t, dets.ReattributeRejectedDetection(ctx, id, now, w.mat, w.camp, w.station, 0.5))
	require.Error(t, dets.Ignore(ctx, id, id))
	require.Error(t, dets.Restore(ctx, id))
	require.Error(t, dets.ClearRetraction(ctx, id, now))
	require.Error(t, dets.RetractByID(ctx, id, now, now))
	require.Error(t, dets.MarkAmbiguous(ctx, id, now))
	require.Error(t, dets.MarkEvidenceExpired(ctx, id, now))
	require.Error(t, dets.SetAuditCoverage(ctx, id, now, 0.1))
	_, _, err = dets.FindCutWithSiblings(ctx, id)
	require.Error(t, err)
	_, err = dets.FindSiblingDetectionInWindow(ctx, 1, w.station, now, 5)
	require.Error(t, err)
}

func TestCovC_Detections_GetEClientID_NaoEncontrado(t *testing.T) {
	w := covCSeedWorld(t, "det-notfound")
	dets := NewDetections(w.pool)
	_, err := dets.Get(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = dets.GetClientID(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	id := covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	cid, err := dets.GetClientID(w.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, w.client, *cid)
}

func TestCovC_Detections_FindCutWithSiblings(t *testing.T) {
	w := covCSeedWorld(t, "siblings")
	dets := NewDetections(w.pool)
	sib := covCSeedMaterial(t, w, "Corte Irmao", 14.6)
	// Só materiais 'ready' contam como irmãos.
	_, err := w.pool.Exec(w.ctx, `UPDATE materials SET fingerprint_status='ready' WHERE id = $1`, sib)
	require.NoError(t, err)
	notReady := covCSeedMaterial(t, w, "Corte Pendente", 10)
	_ = notReady

	self, siblings, err := dets.FindCutWithSiblings(w.ctx, w.mat)
	require.NoError(t, err)
	assert.Equal(t, w.mat, self.ID)
	assert.Equal(t, 30, self.DurationSeconds)
	require.Len(t, siblings, 1)
	assert.Equal(t, sib, siblings[0].ID)
	assert.Equal(t, 15, siblings[0].DurationSeconds, "14.6s arredonda para 15")

	// UUID que não é material (commercial legado / inexistente): self zero, sem irmãos.
	self, siblings, err = dets.FindCutWithSiblings(w.ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, SiblingCut{}, self)
	assert.Empty(t, siblings)
}
