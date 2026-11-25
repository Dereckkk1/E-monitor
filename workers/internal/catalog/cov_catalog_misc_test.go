package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/audit"
)

// ─── material_types ─────────────────────────────────────────────────────────

func TestCovC_MaterialTypes_CRUD(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewMaterialTypes(pool)
	name := "covc-tipo-" + uuid.NewString()[:8]

	desc := "Descrição de teste"
	created, err := repo.Create(ctx, CreateMaterialTypeInput{Name: name, Description: &desc})
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM material_types WHERE id = $1`, created.ID) })
	assert.Equal(t, "#94a3b8", created.Color, "cor vazia cai no slate padrão")
	require.NotNil(t, created.Description)
	assert.Equal(t, desc, *created.Description)

	got, err := repo.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, name, got.Name)
	_, err = repo.Get(ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Nome duplicado viola a UNIQUE.
	_, err = repo.Create(ctx, CreateMaterialTypeInput{Name: name})
	require.Error(t, err)

	newName := name + "-v2"
	upd, err := repo.Update(ctx, created.ID, CreateMaterialTypeInput{Name: newName, Color: "#ff0000"})
	require.NoError(t, err)
	assert.Equal(t, newName, upd.Name)
	assert.Equal(t, "#ff0000", upd.Color)
	assert.Nil(t, upd.Description, "Update substitui a descrição (nil apaga)")
	semCor, err := repo.Update(ctx, created.ID, CreateMaterialTypeInput{Name: newName})
	require.NoError(t, err)
	assert.Equal(t, "#94a3b8", semCor.Color)

	_, err = repo.Update(ctx, uuid.New(), CreateMaterialTypeInput{Name: "x"})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	list, err := repo.List(ctx)
	require.NoError(t, err)
	var found bool
	for i, tp := range list {
		if tp.ID == created.ID {
			found = true
		}
		if i > 0 {
			assert.LessOrEqual(t, list[i-1].Name, tp.Name, "ordenado por nome")
		}
	}
	assert.True(t, found)

	require.NoError(t, repo.Delete(ctx, created.ID))
	_, err = repo.Get(ctx, created.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_MaterialTypes_ErroDeBancoPropaga(t *testing.T) {
	_, pool := newTestDB(t)
	repo := NewMaterialTypes(pool)
	ctx := covCCancelled()
	_, err := repo.List(ctx)
	require.Error(t, err)
	_, err = repo.Get(ctx, uuid.New())
	require.Error(t, err)
	_, err = repo.Create(ctx, CreateMaterialTypeInput{Name: "x"})
	require.Error(t, err)
	_, err = repo.Update(ctx, uuid.New(), CreateMaterialTypeInput{Name: "x"})
	require.Error(t, err)
	require.Error(t, repo.Delete(ctx, uuid.New()))
}

// ─── materials ──────────────────────────────────────────────────────────────

func TestCovC_Materials_AtualizacoesELeituras(t *testing.T) {
	w := covCSeedWorld(t, "mat-upd")
	repo := NewMaterials(w.pool)
	ghost := uuid.New()

	// Título.
	require.NoError(t, repo.UpdateTitle(w.ctx, w.mat, "Novo Título"))
	got, err := repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	assert.Equal(t, "Novo Título", got.Title)
	require.ErrorIs(t, repo.UpdateTitle(w.ctx, ghost, "x"), pgx.ErrNoRows)

	// Script: seta, limpa (nil) e inexistente.
	script := "Texto falado do comercial."
	require.NoError(t, repo.UpdateScript(w.ctx, w.mat, &script))
	got, err = repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	require.NotNil(t, got.Script)
	assert.Equal(t, script, *got.Script)
	require.NoError(t, repo.UpdateScript(w.ctx, w.mat, nil))
	got, err = repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	assert.Nil(t, got.Script)
	require.ErrorIs(t, repo.UpdateScript(w.ctx, ghost, &script), pgx.ErrNoRows)

	// Tipo: troca, limpa e inexistente (0 linhas).
	type2 := covCSeedType(t, w, "covc-mat-type2")
	n, err := repo.UpdateType(w.ctx, w.mat, &type2)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	got, err = repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	require.NotNil(t, got.TypeID)
	assert.Equal(t, type2, *got.TypeID)
	n, err = repo.UpdateType(w.ctx, w.mat, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	n, err = repo.UpdateType(w.ctx, ghost, nil)
	require.NoError(t, err)
	assert.Zero(t, n)

	// Acknowledge carimba o aviso de similaridade e é idempotente.
	require.NoError(t, repo.Acknowledge(w.ctx, w.mat))
	require.NoError(t, repo.Acknowledge(w.ctx, w.mat))
	got, err = repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	assert.NotNil(t, got.SimilarityAckdAt)
}

func TestCovC_Materials_ListagensEBusca(t *testing.T) {
	w := covCSeedWorld(t, "mat-list")
	repo := NewMaterials(w.pool)
	other := covCSeedMaterial(t, w, "Vinheta Natal", 10)

	all, err := repo.ListByClient(w.ctx, w.client, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	filt, err := repo.ListByClient(w.ctx, w.client, "natal")
	require.NoError(t, err)
	require.Len(t, filt, 1)
	assert.Equal(t, other, filt[0].ID)

	none, err := repo.ListByClient(w.ctx, uuid.New(), "")
	require.NoError(t, err)
	assert.Empty(t, none)

	// Dedup por (cliente, sha): o mais antigo (menor short_id) ganha.
	m, err := repo.Get(w.ctx, w.mat)
	require.NoError(t, err)
	dup, err := repo.GetByClientAndSHA(w.ctx, w.client, m.MasterSHA256)
	require.NoError(t, err)
	assert.Equal(t, w.mat, dup.ID)
	_, err = repo.GetByClientAndSHA(w.ctx, w.client, "sha-que-nao-existe")
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.GetByClientAndSHA(w.ctx, uuid.New(), m.MasterSHA256)
	require.ErrorIs(t, err, pgx.ErrNoRows, "o mesmo áudio em OUTRO cliente é legítimo")
}

func TestCovC_Materials_ProntosPorCampanhaEEmissora(t *testing.T) {
	w := covCSeedWorld(t, "mat-ready")
	repo := NewMaterials(w.pool)
	cm := NewCampaignMaterials(w.pool)
	pending := covCSeedMaterial(t, w, "Pendente", 20)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Ready Dois")
	_, err := w.pool.Exec(w.ctx, `UPDATE materials SET fingerprint_status='ready' WHERE id = $1`, w.mat)
	require.NoError(t, err)
	require.NoError(t, cm.Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	require.NoError(t, cm.Link(w.ctx, w.camp, pending, []uuid.UUID{w.station}))

	// ListReadyIDsByCampaign: só ready.
	ids, err := repo.ListReadyIDsByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{w.mat}, ids)

	// Por emissora: precisa estar em target_stations do vínculo.
	got, err := repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, w.station)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, w.mat, got[0].ID)
	assert.InDelta(t, 30, got[0].DurationSeconds, 0.001)
	other, err := repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, st2)
	require.NoError(t, err)
	assert.Empty(t, other)
	nilCamps, err := repo.ListReadyByCampaignsForStation(w.ctx, nil, w.station)
	require.NoError(t, err)
	assert.Nil(t, nilCamps)
}

func TestCovC_Materials_Delete(t *testing.T) {
	w := covCSeedWorld(t, "mat-del")
	repo := NewMaterials(w.pool)
	loose := covCSeedMaterial(t, w, "Solto", 10)

	n, err := repo.Delete(w.ctx, uuid.New())
	require.NoError(t, err)
	assert.Zero(t, n)

	n, err = repo.Delete(w.ctx, loose)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	_, err = repo.Get(w.ctx, loose)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Material vinculado a campanha não pode ser apagado (FK 23503): o erro sobe.
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, nil))
	_, err = repo.Delete(w.ctx, w.mat)
	require.Error(t, err)
	_, err = repo.Get(w.ctx, w.mat)
	require.NoError(t, err, "a recusa não apaga")
}

func TestCovC_Materials_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "mat-dberr")
	repo := NewMaterials(w.pool)
	ctx := covCCancelled()
	id := w.mat
	_, err := repo.Create(ctx, CreateMaterialInput{ClientID: w.client, Title: "x"})
	require.Error(t, err)
	_, err = repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.GetByClientAndSHA(ctx, w.client, "x")
	require.Error(t, err)
	_, err = repo.ListByClient(ctx, w.client, "")
	require.Error(t, err)
	_, err = repo.ListReadyIDsByCampaign(ctx, w.camp)
	require.Error(t, err)
	_, err = repo.ListReadyByCampaignsForStation(ctx, []uuid.UUID{w.camp}, w.station)
	require.Error(t, err)
	_, err = repo.UpdateType(ctx, id, nil)
	require.Error(t, err)
	require.Error(t, repo.UpdateTitle(ctx, id, "x"))
	require.Error(t, repo.UpdateScript(ctx, id, nil))
	_, err = repo.Delete(ctx, id)
	require.Error(t, err)
	require.Error(t, repo.Acknowledge(ctx, id))
}

// ─── campaign_materials ─────────────────────────────────────────────────────

func TestCovC_CampaignMaterials_VinculoCRUD(t *testing.T) {
	w := covCSeedWorld(t, "cm-crud")
	cm := NewCampaignMaterials(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Vinculo Dois")

	// Link com nil vira lista vazia e é upsert (reenviar troca as emissoras).
	require.NoError(t, cm.Link(w.ctx, w.camp, w.mat, nil))
	byCamp, err := cm.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.Len(t, byCamp, 1)
	assert.NotNil(t, byCamp[0].TargetStations)
	assert.Empty(t, byCamp[0].TargetStations)

	require.NoError(t, cm.Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	byCamp, err = cm.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.Len(t, byCamp, 1, "upsert não duplica o vínculo")
	assert.Equal(t, []uuid.UUID{w.station}, byCamp[0].TargetStations)

	// UpdateStations devolve quantas linhas tocou (0 = vínculo inexistente).
	n, err := cm.UpdateStations(w.ctx, w.camp, w.mat, []uuid.UUID{w.station, st2})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	n, err = cm.UpdateStations(w.ctx, w.camp, uuid.New(), nil)
	require.NoError(t, err)
	assert.Zero(t, n)
	n, err = cm.UpdateStations(w.ctx, w.camp, w.mat, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	byCamp, err = cm.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Empty(t, byCamp[0].TargetStations)

	// Por material: mais recente primeiro, inclui outras campanhas.
	camp2 := covCNewCampaign(t, w, "Campanha Dois CM", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, cm.Link(w.ctx, camp2.ID, w.mat, []uuid.UUID{st2}))
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, camp2.ID) })
	byMat, err := cm.ListByMaterial(w.ctx, w.mat)
	require.NoError(t, err)
	require.Len(t, byMat, 2)
	assert.Equal(t, camp2.ID, byMat[0].CampaignID, "added_at DESC")

	// Unlink devolve 0 quando não existia.
	removed, err := cm.Unlink(w.ctx, camp2.ID, w.mat)
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)
	removed, err = cm.Unlink(w.ctx, camp2.ID, w.mat)
	require.NoError(t, err)
	assert.Zero(t, removed)
}

func TestCovC_CampaignMaterials_ListarConcorrentes(t *testing.T) {
	w := covCSeedWorld(t, "cm-conc")
	cm := NewCampaignMaterials(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Conc Dois")
	stOther := covCSeedStation(t, w, "Rádio Teste FM Conc Outra")
	jun1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	jun30 := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	// Vínculo "dono" da pergunta.
	require.NoError(t, cm.Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station, st2}))

	// Duplicado do material (mesmo sha, outro id).
	var sha string
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT master_sha256 FROM materials WHERE id=$1`, w.mat).Scan(&sha))
	dup, err := NewMaterials(w.pool).Create(w.ctx, CreateMaterialInput{
		ClientID: w.client, Title: "Duplicado", TypeID: &w.typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/covc-dup", MasterSHA256: sha,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, dup.ID)
		w.pool.Exec(w.ctx, `DELETE FROM materials WHERE id = $1`, dup.ID)
	})

	same := covCNewCampaign(t, w, "Concorrente Mesmo Material", jun1, jun30)
	viaDup := covCNewCampaign(t, w, "Concorrente Duplicado", jun1, jun30)
	noOverlap := covCNewCampaign(t, w, "Concorrente Sem Cruzamento", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	otherStation := covCNewCampaign(t, w, "Concorrente Outra Emissora", jun1, jun30)
	cancelled := covCNewCampaign(t, w, "Concorrente Cancelada", jun1, jun30)
	for _, c := range []*Campaign{same, viaDup, noOverlap, otherStation, cancelled} {
		c := c
		t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, c.ID) })
	}
	require.NoError(t, cm.Link(w.ctx, same.ID, w.mat, []uuid.UUID{w.station, stOther}))
	require.NoError(t, cm.Link(w.ctx, viaDup.ID, dup.ID, []uuid.UUID{st2}))
	require.NoError(t, cm.Link(w.ctx, noOverlap.ID, w.mat, []uuid.UUID{w.station}))
	require.NoError(t, cm.Link(w.ctx, otherStation.ID, w.mat, []uuid.UUID{stOther}))
	require.NoError(t, cm.Link(w.ctx, cancelled.ID, w.mat, []uuid.UUID{w.station}))
	require.NoError(t, NewCampaigns(w.pool).UpdateStatus(w.ctx, cancelled.ID, "cancelada"))

	got, err := cm.ListarConcorrentes(w.ctx, w.camp, w.mat, jun1, jun30, []uuid.UUID{w.station, st2})
	require.NoError(t, err)
	byCamp := map[uuid.UUID]VinculoConcorrente{}
	for _, v := range got {
		byCamp[v.CampaignID] = v
	}
	require.Len(t, got, 2, "só vínculos de campanha viva, com vigência e emissora em comum")
	assert.True(t, byCamp[same.ID].MesmoMaterial)
	assert.Equal(t, []uuid.UUID{w.station}, byCamp[same.ID].EmissorasComuns, "stOther não foi perguntada")
	assert.False(t, byCamp[viaDup.ID].MesmoMaterial, "mesmo áudio por outro material")
	assert.Equal(t, dup.ID, byCamp[viaDup.ID].MaterialID)
	assert.Equal(t, []uuid.UUID{st2}, byCamp[viaDup.ID].EmissorasComuns)
	_, selfPresent := byCamp[w.camp]
	assert.False(t, selfPresent, "a própria campanha fica de fora")

	// Sem emissoras perguntadas: nada a disputar.
	none, err := cm.ListarConcorrentes(w.ctx, w.camp, w.mat, jun1, jun30, nil)
	require.NoError(t, err)
	assert.Nil(t, none)

	_, err = cm.ListarConcorrentes(covCCancelled(), w.camp, w.mat, jun1, jun30, []uuid.UUID{w.station})
	require.Error(t, err)
}

func TestCovC_CampaignMaterials_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "cm-dberr")
	cm := NewCampaignMaterials(w.pool)
	ctx := covCCancelled()
	require.Error(t, cm.Link(ctx, w.camp, w.mat, nil))
	_, err := cm.Unlink(ctx, w.camp, w.mat)
	require.Error(t, err)
	_, err = cm.UpdateStations(ctx, w.camp, w.mat, nil)
	require.Error(t, err)
	_, err = cm.ListByCampaign(ctx, w.camp)
	require.Error(t, err)
	_, err = cm.ListByMaterial(ctx, w.mat)
	require.Error(t, err)
}

// ─── distribution_rules.ListApplicable ──────────────────────────────────────

func TestCovC_DistributionRules_ListApplicable(t *testing.T) {
	w := covCSeedWorld(t, "dr-appl")
	repo := NewDistributionRules(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Regra Dois")

	// Segunda a sexta (bits 1..5 = 0b0111110 = 62) em junho/2026, só na emissora 1.
	rule, err := repo.Create(w.ctx, CreateDistributionRuleInput{
		CampaignID: w.camp, TypeID: w.typeID, StationIDs: []uuid.UUID{w.station},
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "18:00", PlaysPerDay: 3, Name: "Conjunto Teste",
	})
	require.NoError(t, err)
	assert.Equal(t, "08:00", rule.TimeStart)
	assert.Equal(t, "Conjunto Teste", rule.Name)
	assert.NotNil(t, rule.MaterialIDs, "regra geral guarda '{}' e não NULL")

	quarta := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	sabado := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)
	julho := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

	got, err := repo.ListApplicable(w.ctx, w.camp, w.mat, w.station, quarta)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, rule.ID, got[0].ID)

	for name, c := range map[string]struct {
		station uuid.UUID
		day     time.Time
	}{
		"dia da semana fora da máscara": {w.station, sabado},
		"fora do período":               {w.station, julho},
		"outra emissora":                {st2, quarta},
	} {
		none, err := repo.ListApplicable(w.ctx, w.camp, w.mat, c.station, c.day)
		require.NoError(t, err, name)
		assert.Empty(t, none, name)
	}

	// Material de outro tipo (ou sem tipo) não casa com a regra.
	type2 := covCSeedType(t, w, "covc-dr-type2")
	_, err = w.pool.Exec(w.ctx, `UPDATE materials SET type_id=$2 WHERE id=$1`, w.mat, type2)
	require.NoError(t, err)
	none, err := repo.ListApplicable(w.ctx, w.camp, w.mat, w.station, quarta)
	require.NoError(t, err)
	assert.Empty(t, none)

	// Update + Delete.
	require.NoError(t, repo.Update(w.ctx, rule.ID, CreateDistributionRuleInput{
		TypeID: w.typeID, StationIDs: []uuid.UUID{w.station, st2},
		StartDate: rule.StartDate, EndDate: rule.EndDate, WeekdayMask: 127,
		TimeStart: "06:00", TimeEnd: "22:00", PlaysPerDay: 5, Name: "Renomeado",
	}))
	upd, err := repo.Get(w.ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, int16(5), upd.PlaysPerDay)
	assert.Equal(t, "Renomeado", upd.Name)
	assert.Equal(t, "06:00", upd.TimeStart)
	assert.Len(t, upd.StationIDs, 2)
	require.NoError(t, repo.Delete(w.ctx, rule.ID))
	_, err = repo.Get(w.ctx, rule.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Erros de banco.
	_, err = repo.ListApplicable(covCCancelled(), w.camp, w.mat, w.station, quarta)
	require.Error(t, err)
}

// ─── client_station_pmm ─────────────────────────────────────────────────────

func TestCovC_ClientStationPMM_ListaUpsertEForStations(t *testing.T) {
	w := covCSeedWorld(t, "pmm")
	repo := NewClientStationPMM(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM PMM Dois")
	stFora := covCSeedStation(t, w, "Rádio Teste FM PMM Fora")
	covCTarget(t, w, w.station, st2)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM client_station_pmm WHERE client_id = $1`, w.client) })

	// Antes de cadastrar: as emissoras-alvo aparecem com PMMTarget nil ("não cadastrado").
	rows, err := repo.ListForClient(w.ctx, w.client)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Nil(t, r.PMMTarget)
	}

	v1000, v0 := 1000, 0
	up, del, err := repo.BulkUpsert(w.ctx, w.client, []TargetPMMEntry{
		{StationID: w.station, PMMTarget: &v1000},
		{StationID: st2, PMMTarget: &v0},      // zero é linha, não ausência
		{StationID: stFora, PMMTarget: &v1000}, // fora das campanhas
	})
	require.NoError(t, err)
	assert.Equal(t, 3, up)
	assert.Zero(t, del)

	rows, err = repo.ListForClient(w.ctx, w.client)
	require.NoError(t, err)
	require.Len(t, rows, 2, "ListForClient só enxerga emissoras das campanhas do cliente")
	byID := map[uuid.UUID]TargetPMMRow{}
	for _, r := range rows {
		byID[r.StationID] = r
	}
	require.NotNil(t, byID[w.station].PMMTarget)
	assert.Equal(t, 1000, *byID[w.station].PMMTarget)
	require.NotNil(t, byID[st2].PMMTarget)
	assert.Equal(t, 0, *byID[st2].PMMTarget, "zero ≠ ausente")

	// ForStations enxerga também a emissora que nenhuma campanha usa; ausência fica fora do mapa.
	m, err := repo.ForStations(w.ctx, w.client, []uuid.UUID{w.station, st2, stFora, uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]int{w.station: 1000, st2: 0, stFora: 1000}, m)
	empty, err := repo.ForStations(w.ctx, w.client, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// nil apaga; reenviar o mesmo lote é idempotente.
	up, del, err = repo.BulkUpsert(w.ctx, w.client, []TargetPMMEntry{{StationID: w.station, PMMTarget: nil}, {StationID: uuid.New(), PMMTarget: nil}})
	require.NoError(t, err)
	assert.Zero(t, up)
	assert.Equal(t, 1, del, "só a linha existente conta como apagada")
	m, err = repo.ForStations(w.ctx, w.client, []uuid.UUID{w.station})
	require.NoError(t, err)
	assert.Empty(t, m)

	v7 := 7
	up, _, err = repo.BulkUpsert(w.ctx, w.client, []TargetPMMEntry{{StationID: st2, PMMTarget: &v7}})
	require.NoError(t, err)
	assert.Equal(t, 1, up)
	m, err = repo.ForStations(w.ctx, w.client, []uuid.UUID{st2})
	require.NoError(t, err)
	assert.Equal(t, 7, m[st2], "upsert atualiza o valor existente")

	// Erros de banco.
	_, err = repo.ListForClient(covCCancelled(), w.client)
	require.Error(t, err)
	_, err = repo.ForStations(covCCancelled(), w.client, []uuid.UUID{st2})
	require.Error(t, err)
	_, _, err = repo.BulkUpsert(covCCancelled(), w.client, nil)
	require.Error(t, err)
	// FK: emissora inexistente com valor falha e desfaz o lote inteiro.
	_, _, err = repo.BulkUpsert(w.ctx, w.client, []TargetPMMEntry{
		{StationID: w.station, PMMTarget: &v1000}, {StationID: uuid.New(), PMMTarget: &v1000}})
	require.Error(t, err)
	m, err = repo.ForStations(w.ctx, w.client, []uuid.UUID{w.station})
	require.NoError(t, err)
	assert.Empty(t, m, "lote com erro não grava nada")
}

// ─── station_dial ───────────────────────────────────────────────────────────

func TestCovC_NormalizeDial(t *testing.T) {
	tests := []struct{ in, want string }{
		{"99,90", "99.9"},
		{"  100.0 ", "100"},
		{"710", "710"},
		{"88.5", "88.5"},
		{"abc", ""},
		{"", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, NormalizeDial(tc.in), tc.in)
	}
}

func TestCovC_Stations_CandidatosPorDial(t *testing.T) {
	w := covCSeedWorld(t, "dial")
	repo := NewStations(w.pool)
	mk := func(name string, freq *float64, state string) uuid.UUID {
		st, err := repo.Create(w.ctx, CreateStationInput{
			Name: name, Band: "FM", FrequencyMHz: freq, State: &state,
			StreamURL: "http://example.com/covc/dial/" + uuid.NewString(),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			w.pool.Exec(w.ctx, `DELETE FROM station_thresholds WHERE station_id = $1`, st.ID)
			w.pool.Exec(w.ctx, `DELETE FROM stations WHERE id = $1`, st.ID)
		})
		return st.ID
	}
	// UF exclusiva ("ZZ" não existe de verdade) isola o teste do restante do banco.
	f991 := 99.1
	porDial := mk("Rádio Dial Cadastrado", &f991, "ZZ")
	semDialComNumero := mk("89 - FM (89.1)", nil, "ZZ")
	semDialSemNumero := mk("Rádio Sem Numero", nil, "ZZ")
	outraFreq := 101.1
	outroDial := mk("Rádio Outro Dial", &outraFreq, "ZZ")
	outraUF := mk("Rádio Outra UF", &f991, "YY")

	got, err := repo.CandidatosPorDial(w.ctx, []string{"ZZ"}, []string{NormalizeDial("99,10")})
	require.NoError(t, err)
	ids := map[uuid.UUID]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	assert.True(t, ids[porDial], "dial bate")
	assert.True(t, ids[semDialComNumero], "sem dial mas com número no nome: quem chama confere")
	assert.False(t, ids[semDialSemNumero])
	assert.False(t, ids[outroDial])
	assert.False(t, ids[outraUF])

	// Entradas vazias não consultam.
	none, err := repo.CandidatosPorDial(w.ctx, nil, []string{"99.1"})
	require.NoError(t, err)
	assert.Nil(t, none)
	none, err = repo.CandidatosPorDial(w.ctx, []string{"ZZ"}, nil)
	require.NoError(t, err)
	assert.Nil(t, none)
	_, err = repo.CandidatosPorDial(covCCancelled(), []string{"ZZ"}, []string{"99.1"})
	require.Error(t, err)
}

// ─── notifications ──────────────────────────────────────────────────────────

func TestCovC_Notifications_FalhaDoDiaViraNotificacao(t *testing.T) {
	w := covCSeedWorld(t, "notif")
	repo := NewNotifications(w.pool)
	user := covCUser(t, w)

	// Campanha com janela larga em torno de hoje e meta de 2/dia, sem nenhuma tocada:
	// todo dia fechado dentro da janela [hoje-7, ontem] tem déficit.
	hoje := time.Now().UTC().Truncate(24 * time.Hour)
	_, err := w.pool.Exec(w.ctx, `UPDATE campaigns SET start_date=$2, end_date=$3, status='ativa' WHERE id=$1`,
		w.camp, hoje.AddDate(0, 0, -20), hoje.AddDate(0, 0, 20))
	require.NoError(t, err)
	covCTarget(t, w, w.station)
	_, err = NewDistributionRules(w.pool).Create(w.ctx, CreateDistributionRuleInput{
		CampaignID: w.camp, TypeID: w.typeID, StationIDs: []uuid.UUID{w.station},
		StartDate: hoje.AddDate(0, 0, -20), EndDate: hoje.AddDate(0, 0, 20),
		WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "23:59", PlaysPerDay: 2,
	})
	require.NoError(t, err)
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM notification_reads WHERE user_id = $1`, user) })

	res, err := repo.List(w.ctx, user)
	require.NoError(t, err)
	var mine []Notification
	for _, n := range res.Items {
		if n.CampaignID == w.camp {
			mine = append(mine, n)
		}
	}
	require.NotEmpty(t, mine, "campanha com déficit nos últimos 7 dias fechados notifica")
	first := mine[0]
	assert.Equal(t, "campaign_failure", first.Kind)
	assert.Equal(t, w.client, first.ClientID)
	assert.Equal(t, "Cliente Teste notif", first.ClientName)
	assert.Equal(t, "campaign_failure:"+w.camp.String()+":"+first.OccurredOn, first.Key)
	assert.Nil(t, first.ReadAt)
	assert.GreaterOrEqual(t, res.UnreadCount, len(mine))
	// Hoje (dia em curso) nunca aparece: o dia ainda não fechou.
	for _, n := range mine {
		assert.NotEqual(t, time.Now().UTC().Format("2006-01-02"), n.OccurredOn)
	}

	// Marca uma como lida: idempotente e reflete no List.
	nIns, err := repo.MarkRead(w.ctx, user, []string{first.Key})
	require.NoError(t, err)
	assert.Equal(t, 1, nIns)
	nIns, err = repo.MarkRead(w.ctx, user, []string{first.Key})
	require.NoError(t, err)
	assert.Zero(t, nIns)
	read, err := repo.HasRead(w.ctx, user, first.Key)
	require.NoError(t, err)
	assert.True(t, read)
	notRead, err := repo.HasRead(w.ctx, user, "daily_failures_digest:1999-01-01")
	require.NoError(t, err)
	assert.False(t, notRead)

	res2, err := repo.List(w.ctx, user)
	require.NoError(t, err)
	assert.Equal(t, res.UnreadCount-1, res2.UnreadCount)

	// MarkAllReadInWindow marca o resto (não confia em lista do cliente) e zera os não lidos.
	marked, err := repo.MarkAllReadInWindow(w.ctx, user)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, marked, len(mine)-1)
	res3, err := repo.List(w.ctx, user)
	require.NoError(t, err)
	for _, n := range res3.Items {
		if n.CampaignID == w.camp {
			assert.NotNil(t, n.ReadAt)
		}
	}

	// Cancelada some das notificações.
	_, _, err = NewCampaigns(w.pool).CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	res4, err := repo.List(w.ctx, user)
	require.NoError(t, err)
	for _, n := range res4.Items {
		assert.NotEqual(t, w.camp, n.CampaignID)
	}
}

func TestCovC_Notifications_ErroDeBancoPropaga(t *testing.T) {
	_, pool := newTestDB(t)
	repo := NewNotifications(pool)
	ctx := covCCancelled()
	_, err := repo.List(ctx, uuid.New())
	require.Error(t, err)
	_, err = repo.MarkRead(ctx, uuid.New(), []string{"k"})
	require.Error(t, err)
	_, err = repo.MarkAllReadInWindow(ctx, uuid.New())
	require.Error(t, err)
	_, err = repo.HasRead(ctx, uuid.New(), "k")
	require.Error(t, err)
}

// ─── twin_discriminative ────────────────────────────────────────────────────

func TestCovC_TwinDiscriminative_ListagensEPopulate(t *testing.T) {
	w := covCSeedWorld(t, "twin")
	repo := NewTwinDiscriminative(w.pool)
	twin := covCSeedMaterial(t, w, "Gêmeo", 30)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM material_twin_discriminative WHERE material_id = ANY($1) OR twin_id = ANY($1)`,
			[]uuid.UUID{w.mat, twin})
	})

	// Sem pares: ListForMaterial vazio; Get devolve (nil, 0, nil).
	none, err := repo.ListForMaterial(w.ctx, w.mat)
	require.NoError(t, err)
	assert.Empty(t, none)
	rs, frames, err := repo.Get(w.ctx, w.mat, twin)
	require.NoError(t, err)
	assert.Nil(t, rs)
	assert.Zero(t, frames)

	disc := []audit.FrameRange{{Lo: 100, Hi: 150}, {Lo: 200, Hi: 234}}
	require.NoError(t, repo.Upsert(w.ctx, w.mat, twin, disc, 84))
	list, err := repo.ListForMaterial(w.ctx, w.mat)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, twin, list[0].TwinID)
	assert.Equal(t, disc, list[0].Disc)
	assert.Equal(t, 84, list[0].DiscFrames)
	var twinShort int32
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT short_id FROM materials WHERE id=$1`, twin).Scan(&twinShort))
	assert.Equal(t, twinShort, list[0].TwinShortID)

	// Upsert sobrescreve; par "não separável" (0 frames) guarda ranges vazios.
	require.NoError(t, repo.Upsert(w.ctx, w.mat, twin, nil, 0))
	rs, frames, err = repo.Get(w.ctx, w.mat, twin)
	require.NoError(t, err)
	assert.Nil(t, rs)
	assert.Zero(t, frames)

	// Detecções elegíveis: aprovadas, com clipe salvo e material com gêmeo populado.
	require.NoError(t, repo.Upsert(w.ctx, w.mat, twin, disc, 84))
	comClipe := covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	semClipe := covCInsertDet(t, w, w.mat, w.station, covCDay(1), "in_slot")
	retratada := covCInsertDet(t, w, w.mat, w.station, covCDay(2), "in_slot")
	outroMat := covCInsertDet(t, w, w.mat, w.station, covCDay(60*24), "in_slot") // fora do Until
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET evidence_key='ev/a.m4a', evidence_status='available' WHERE id = ANY($1)`,
		[]uuid.UUID{comClipe, retratada, outroMat})
	require.NoError(t, err)
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET retracted_at=now() WHERE id=$1`, retratada)
	require.NoError(t, err)
	_ = semClipe

	got, err := repo.ListEligibleDetections(w.ctx, EligibleTwinFilter{CampaignID: &w.camp, MaterialID: &w.mat, StationID: &w.station})
	require.NoError(t, err)
	var gotIDs []uuid.UUID
	for _, e := range got {
		gotIDs = append(gotIDs, e.ID)
		assert.Equal(t, "ev/a.m4a", e.EvidenceKey)
	}
	assert.ElementsMatch(t, []uuid.UUID{comClipe, outroMat}, gotIDs, "sem clipe e retratada ficam de fora")
	assert.True(t, got[0].DetectedAt.After(got[1].DetectedAt), "mais recente primeiro")

	until := covCDay(24 * 10)
	since := covCDay(-1)
	win, err := repo.ListEligibleDetections(w.ctx, EligibleTwinFilter{CampaignID: &w.camp, Since: &since, Until: &until})
	require.NoError(t, err)
	require.Len(t, win, 1)
	assert.Equal(t, comClipe, win[0].ID)
	limited, err := repo.ListEligibleDetections(w.ctx, EligibleTwinFilter{CampaignID: &w.camp, Limit: 1})
	require.NoError(t, err)
	assert.Len(t, limited, 1)

	// Populate sem fingerprints não acha gêmeos: no-op sem erro.
	require.NoError(t, repo.PopulateForMaterial(w.ctx, w.mat))
	// Erros de banco sobem.
	require.Error(t, repo.PopulateForMaterial(covCCancelled(), w.mat))
	require.Error(t, repo.Upsert(covCCancelled(), w.mat, twin, nil, 0))
	_, _, err = repo.Get(covCCancelled(), w.mat, twin)
	require.Error(t, err)
	_, err = repo.ListForMaterial(covCCancelled(), w.mat)
	require.Error(t, err)
	_, err = repo.ListEligibleDetections(covCCancelled(), EligibleTwinFilter{})
	require.Error(t, err)
}
