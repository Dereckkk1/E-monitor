package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covCNewCampaign cria uma campanha extra do cliente do mundo.
func covCNewCampaign(t *testing.T, w *covCWorld, name string, start, end time.Time, stations ...uuid.UUID) *Campaign {
	t.Helper()
	if stations == nil {
		stations = []uuid.UUID{}
	}
	c, err := NewCampaigns(w.pool).Create(w.ctx, CreateCampaignInput{
		Name: name, ClientID: w.client, StartDate: start, EndDate: end, TargetStations: stations,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM campaigns WHERE id = $1`, c.ID)
	})
	return c
}

func TestCovC_Campaigns_GetEClientNameFor(t *testing.T) {
	w := covCSeedWorld(t, "camp-get")
	repo := NewCampaigns(w.pool)

	got, err := repo.Get(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, "Campanha camp-get", got.Name)
	assert.Equal(t, "programada", got.Status, "campanha nasce programada")
	assert.Nil(t, got.FixedCPM)
	assert.Nil(t, got.HubCode)

	_, err = repo.Get(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	name, err := repo.ClientNameFor(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, "Cliente Teste camp-get", name)
	_, err = repo.ClientNameFor(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Campaigns_ListPaged_FiltrosEOrdenacao(t *testing.T) {
	w := covCSeedWorld(t, "camp-paged")
	repo := NewCampaigns(w.pool)
	camps := NewCampaigns(w.pool)
	ativa := covCNewCampaign(t, w, "Campanha Ativa Paged", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	conc := covCNewCampaign(t, w, "Campanha Concluida Paged", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	canc := covCNewCampaign(t, w, "Campanha Cancelada Paged", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, camps.UpdateStatus(w.ctx, ativa.ID, "ativa"))
	require.NoError(t, camps.UpdateStatus(w.ctx, conc.ID, "concluida"))
	require.NoError(t, camps.UpdateStatus(w.ctx, canc.ID, "cancelada"))
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, nil))

	scope := []uuid.UUID{w.client}
	ids := func(cs []Campaign) []uuid.UUID {
		out := make([]uuid.UUID, 0, len(cs))
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}

	// Ordem do ciclo de vida: ativa → programada → concluída → cancelada.
	rows, total, err := repo.ListPaged(w.ctx, "", "", scope, nil, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Equal(t, []uuid.UUID{ativa.ID, w.camp, conc.ID, canc.ID}, ids(rows))
	for _, c := range rows {
		if c.ID == w.camp {
			assert.Equal(t, 1, c.MaterialCount, "chip 'sem material' depende deste contador")
		} else {
			assert.Zero(t, c.MaterialCount)
		}
	}

	// Competência YYYY-MM: sobreposição de mês.
	rows, total, err = repo.ListPaged(w.ctx, "", "2026-04", scope, nil, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []uuid.UUID{conc.ID}, ids(rows))

	// Competência inválida é erro de entrada.
	_, _, err = repo.ListPaged(w.ctx, "", "abril/2026", scope, nil, 1, 20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid competence")

	// Texto livre (nome da campanha, sem acento/caixa) e deep-link por id.
	rows, total, err = repo.ListPaged(w.ctx, "CONCLUIDA paged", "", scope, nil, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []uuid.UUID{conc.ID}, ids(rows))
	rows, total, err = repo.ListPaged(w.ctx, "", "", scope, &canc.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []uuid.UUID{canc.ID}, ids(rows))

	// Paginação: 2 por página, total preservado.
	rows, total, err = repo.ListPaged(w.ctx, "", "", scope, nil, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Equal(t, []uuid.UUID{conc.ID, canc.ID}, ids(rows))

	// Carteira de outro cliente e carteira vazia: nenhuma linha.
	_, total, err = repo.ListPaged(w.ctx, "", "", []uuid.UUID{uuid.New()}, nil, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	_, total, err = repo.ListPaged(w.ctx, "", "", []uuid.UUID{}, nil, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
}

func TestCovC_Campaigns_ListFiltered(t *testing.T) {
	w := covCSeedWorld(t, "camp-filt")
	repo := NewCampaigns(w.pool)
	ativa := covCNewCampaign(t, w, "Campanha Ativa Filt", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, repo.UpdateStatus(w.ctx, ativa.ID, "ativa"))
	scope := []uuid.UUID{w.client}

	all, err := repo.ListFiltered(w.ctx, nil, scope)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, ativa.ID, all[0].ID, "ativa vem antes da programada")

	only, err := repo.ListFiltered(w.ctx, []string{"programada"}, scope)
	require.NoError(t, err)
	require.Len(t, only, 1)
	assert.Equal(t, w.camp, only[0].ID)

	two, err := repo.ListFiltered(w.ctx, []string{"programada", "ativa"}, scope)
	require.NoError(t, err)
	assert.Len(t, two, 2)

	none, err := repo.ListFiltered(w.ctx, []string{"cancelada"}, scope)
	require.NoError(t, err)
	assert.Empty(t, none)

	// List() é o ListFiltered sem filtro: contém as duas.
	everything, err := repo.List(w.ctx)
	require.NoError(t, err)
	seen := map[uuid.UUID]bool{}
	for _, c := range everything {
		seen[c.ID] = true
	}
	assert.True(t, seen[ativa.ID] && seen[w.camp])
}

func TestCovC_Campaigns_CancelCampaign(t *testing.T) {
	w := covCSeedWorld(t, "camp-cancel")
	repo := NewCampaigns(w.pool)

	// Inexistente → ErrNoRows.
	_, _, err := repo.CancelCampaign(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	changed, prev, err := repo.CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "programada", prev)
	got, err := repo.Get(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, "cancelada", got.Status)
	var cancelledAt *time.Time
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT cancelled_at FROM campaigns WHERE id=$1`, w.camp).Scan(&cancelledAt))
	require.NotNil(t, cancelledAt, "congela o déficit na data do cancelamento")

	// Já cancelada: estado terminal, nada muda (o caller devolve 409).
	changed, prev, err = repo.CancelCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, "cancelada", prev)

	// Concluída também é terminal.
	conc := covCNewCampaign(t, w, "Campanha Concluida Cancel", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, repo.UpdateStatus(w.ctx, conc.ID, "concluida"))
	changed, prev, err = repo.CancelCampaign(w.ctx, conc.ID)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, "concluida", prev)
}

func TestCovC_Campaigns_CountByStatus(t *testing.T) {
	w := covCSeedWorld(t, "camp-count")
	repo := NewCampaigns(w.pool)
	before, err := repo.CountByStatus(w.ctx)
	require.NoError(t, err)

	a := covCNewCampaign(t, w, "Campanha Count A", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	b := covCNewCampaign(t, w, "Campanha Count B", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, repo.UpdateStatus(w.ctx, a.ID, "ativa"))
	require.NoError(t, repo.UpdateStatus(w.ctx, b.ID, "ativa"))

	after, err := repo.CountByStatus(w.ctx)
	require.NoError(t, err)
	assert.Equal(t, before["ativa"]+2, after["ativa"])
	assert.Equal(t, before["programada"]+0, after["programada"], "as duas foram de programada a ativa; a do mundo continua programada")
}

func TestCovC_Campaigns_UpdateBasicFixedCPMETargets(t *testing.T) {
	w := covCSeedWorld(t, "camp-upd")
	repo := NewCampaigns(w.pool)

	_, err := repo.UpdateBasic(w.ctx, uuid.New(), UpdateBasicInput{Name: "x", StartDate: time.Now(), EndDate: time.Now()})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.UpdateFixedCPM(w.ctx, uuid.New(), covCF(10))
	require.ErrorIs(t, err, pgx.ErrNoRows)

	code := "  EH-ABC123  "
	upd, err := repo.UpdateBasic(w.ctx, w.camp, UpdateBasicInput{
		Name:      "Campanha Renomeada",
		StartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		HubCode:   &code,
	})
	require.NoError(t, err)
	assert.Equal(t, "Campanha Renomeada", upd.Name)
	assert.Equal(t, "2026-07-01", upd.StartDate.Format("2006-01-02"))
	require.NotNil(t, upd.HubCode)
	assert.Equal(t, "EH-ABC123", *upd.HubCode, "código é aparado")
	assert.Equal(t, "programada", upd.Status, "UpdateBasic não mexe no estado")

	// HubCode nil não mexe; vazio/espaços apaga.
	keep, err := repo.UpdateBasic(w.ctx, w.camp, UpdateBasicInput{Name: "Campanha Renomeada",
		StartDate: upd.StartDate, EndDate: upd.EndDate})
	require.NoError(t, err)
	require.NotNil(t, keep.HubCode)
	blank := "   "
	cleared, err := repo.UpdateBasic(w.ctx, w.camp, UpdateBasicInput{Name: "Campanha Renomeada",
		StartDate: upd.StartDate, EndDate: upd.EndDate, HubCode: &blank})
	require.NoError(t, err)
	assert.Nil(t, cleared.HubCode)

	// CPM fixo: seta e limpa.
	withCPM, err := repo.UpdateFixedCPM(w.ctx, w.camp, covCF(12.5))
	require.NoError(t, err)
	require.NotNil(t, withCPM.FixedCPM)
	assert.InDelta(t, 12.5, *withCPM.FixedCPM, 0.0001)
	noCPM, err := repo.UpdateFixedCPM(w.ctx, w.camp, nil)
	require.NoError(t, err)
	assert.Nil(t, noCPM.FixedCPM)

	// Emissoras-alvo: troca, e nil vira lista vazia (não NULL).
	st2 := covCSeedStation(t, w, "Rádio Teste FM Alvo")
	require.NoError(t, repo.UpdateTargetStations(w.ctx, w.camp, []uuid.UUID{w.station, st2}))
	got, err := repo.Get(w.ctx, w.camp)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{w.station, st2}, got.TargetStations)
	require.NoError(t, repo.UpdateTargetStations(w.ctx, w.camp, nil))
	got, err = repo.Get(w.ctx, w.camp)
	require.NoError(t, err)
	assert.NotNil(t, got.TargetStations)
	assert.Empty(t, got.TargetStations)
}

func TestCovC_Campaigns_EmissorasDasAtivas(t *testing.T) {
	w := covCSeedWorld(t, "camp-active-st")
	repo := NewCampaigns(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Ativa Dois")
	stIdle := covCSeedStation(t, w, "Rádio Teste FM Parada")
	covCTarget(t, w, w.station, st2)

	// Programada não conta como "coberta por ativa".
	idsOf := func(list []uuid.UUID) map[uuid.UUID]bool {
		m := map[uuid.UUID]bool{}
		for _, id := range list {
			m[id] = true
		}
		return m
	}
	covered, err := repo.StationsCoveredByActive(w.ctx)
	require.NoError(t, err)
	assert.False(t, idsOf(covered)[w.station])
	forSt, err := repo.ActiveCampaignsForStation(w.ctx, w.station)
	require.NoError(t, err)
	assert.Empty(t, forSt)

	require.NoError(t, repo.UpdateStatus(w.ctx, w.camp, "ativa"))
	covered, err = repo.StationsCoveredByActive(w.ctx)
	require.NoError(t, err)
	cm := idsOf(covered)
	assert.True(t, cm[w.station] && cm[st2])
	assert.False(t, cm[stIdle])

	forSt, err = repo.ActiveCampaignsForStation(w.ctx, st2)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{w.camp}, forSt)
	forIdle, err := repo.ActiveCampaignsForStation(w.ctx, stIdle)
	require.NoError(t, err)
	assert.Empty(t, forIdle)
}

func TestCovC_Campaigns_PromoteScheduledLifecycle(t *testing.T) {
	w := covCSeedWorld(t, "camp-promote")
	repo := NewCampaigns(w.pool)
	hoje := time.Now().UTC().Truncate(24 * time.Hour)
	dia := func(off int) time.Time { return hoje.AddDate(0, 0, off) }

	// Janela larga (±400 dias) evita qualquer sensibilidade a virada de dia.
	programadaQueComeca := covCNewCampaign(t, w, "Promote Programada", dia(-400), dia(400))
	ativaQueTerminou := covCNewCampaign(t, w, "Promote Terminou", dia(-400), dia(-300))
	ativaCedo := covCNewCampaign(t, w, "Promote Cedo", dia(300), dia(400))
	concluidaReaberta := covCNewCampaign(t, w, "Promote Reaberta", dia(-400), dia(400))
	concluidaDeVerdade := covCNewCampaign(t, w, "Promote Concluida", dia(-400), dia(-300))
	canceladaIntocada := covCNewCampaign(t, w, "Promote Cancelada", dia(-400), dia(400))
	set := func(c *Campaign, st string) { require.NoError(t, repo.UpdateStatus(w.ctx, c.ID, st)) }
	set(ativaQueTerminou, "ativa")
	set(ativaCedo, "ativa")
	set(concluidaReaberta, "concluida")
	set(concluidaDeVerdade, "concluida")
	set(canceladaIntocada, "cancelada")

	activated, ended, demoted, err := repo.PromoteScheduledLifecycle(w.ctx)
	require.NoError(t, err)
	has := func(list []uuid.UUID, id uuid.UUID) bool {
		for _, x := range list {
			if x == id {
				return true
			}
		}
		return false
	}
	assert.True(t, has(activated, programadaQueComeca.ID), "programada que já começou vira ativa")
	assert.True(t, has(activated, concluidaReaberta.ID), "concluída com fim futuro é recuperada (RECOVERY)")
	assert.True(t, has(ended, ativaQueTerminou.ID), "ativa que terminou conclui")
	assert.True(t, has(demoted, ativaCedo.ID), "ativa com início futuro volta a programada (DEMOTE)")
	assert.False(t, has(activated, concluidaDeVerdade.ID) || has(ended, concluidaDeVerdade.ID))
	assert.False(t, has(activated, canceladaIntocada.ID) || has(ended, canceladaIntocada.ID) || has(demoted, canceladaIntocada.ID))

	status := func(c *Campaign) string {
		got, err := repo.Get(w.ctx, c.ID)
		require.NoError(t, err)
		return got.Status
	}
	assert.Equal(t, "ativa", status(programadaQueComeca))
	assert.Equal(t, "ativa", status(concluidaReaberta))
	assert.Equal(t, "concluida", status(ativaQueTerminou))
	assert.Equal(t, "programada", status(ativaCedo))
	assert.Equal(t, "concluida", status(concluidaDeVerdade))
	assert.Equal(t, "cancelada", status(canceladaIntocada))

	// Idempotente: a segunda rodada não move nenhuma das nossas.
	a2, e2, d2, err := repo.PromoteScheduledLifecycle(w.ctx)
	require.NoError(t, err)
	for _, c := range []*Campaign{programadaQueComeca, ativaQueTerminou, ativaCedo, concluidaReaberta, concluidaDeVerdade, canceladaIntocada} {
		assert.False(t, has(a2, c.ID) || has(e2, c.ID) || has(d2, c.ID), c.Name)
	}
}

func TestCovC_Campaigns_DeleteEBloqueios(t *testing.T) {
	w := covCSeedWorld(t, "camp-del")
	repo := NewCampaigns(w.pool)

	n, err := repo.CountForeignProjections(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Zero(t, n)
	lote, posVenda, err := repo.DeleteBlockers(w.ctx, w.camp)
	require.NoError(t, err)
	assert.False(t, lote)
	assert.False(t, posVenda)

	// Com histórico: DeleteSemHistorico recusa e preserva tudo.
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	require.ErrorIs(t, repo.DeleteSemHistorico(w.ctx, w.camp), ErrCampaignHasHistory)
	_, err = repo.Get(w.ctx, w.camp)
	require.NoError(t, err, "recusa não apaga")

	// Delete não-estrito varre detecções + comerciais legados junto.
	require.NoError(t, repo.Delete(w.ctx, w.camp))
	_, err = repo.Get(w.ctx, w.camp)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	var left int
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT count(*) FROM detections WHERE campaign_id=$1`, w.camp).Scan(&left))
	assert.Zero(t, left)

	// DeleteSemHistorico de campanha limpa funciona; de inexistente dá ErrNoRows.
	limpa := covCNewCampaign(t, w, "Campanha Limpa Del", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	require.NoError(t, repo.DeleteSemHistorico(w.ctx, limpa.ID))
	require.ErrorIs(t, repo.DeleteSemHistorico(w.ctx, limpa.ID), pgx.ErrNoRows)
}

func TestCovC_Campaigns_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "camp-dberr")
	repo := NewCampaigns(w.pool)
	ctx := covCCancelled()
	id := w.camp

	_, err := repo.Create(ctx, CreateCampaignInput{ClientID: w.client, Name: "x"})
	require.Error(t, err)
	_, err = repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.ClientNameFor(ctx, id)
	require.Error(t, err)
	_, err = repo.List(ctx)
	require.Error(t, err)
	_, err = repo.ListFiltered(ctx, []string{"ativa"}, nil)
	require.Error(t, err)
	_, _, err = repo.ListPaged(ctx, "", "", nil, nil, 1, 10)
	require.Error(t, err)
	require.Error(t, repo.UpdateStatus(ctx, id, "ativa"))
	_, _, err = repo.CancelCampaign(ctx, id)
	require.Error(t, err)
	_, err = repo.CountByStatus(ctx)
	require.Error(t, err)
	_, _, _, err = repo.PromoteScheduledLifecycle(ctx)
	require.Error(t, err)
	_, err = repo.UpdateBasic(ctx, id, UpdateBasicInput{})
	require.Error(t, err)
	_, err = repo.UpdateFixedCPM(ctx, id, nil)
	require.Error(t, err)
	require.Error(t, repo.UpdateTargetStations(ctx, id, nil))
	require.Error(t, repo.Delete(ctx, id))
	require.Error(t, repo.DeleteSemHistorico(ctx, id))
	_, err = repo.CountForeignProjections(ctx, id)
	require.Error(t, err)
	_, _, err = repo.DeleteBlockers(ctx, id)
	require.Error(t, err)
	_, err = repo.StationsCoveredByActive(ctx)
	require.Error(t, err)
	_, err = repo.ActiveCampaignsForStation(ctx, w.station)
	require.Error(t, err)
	_, err = repo.PendentesDeHub(ctx, 5)
	require.Error(t, err)
	_, err = repo.PendentesDoCliente(ctx, w.client)
	require.Error(t, err)
	require.Error(t, repo.MarcarHubNotificada(ctx, id, "EH-X"))
	require.Error(t, repo.MarcarTentativaDeHub(ctx, id))
}
