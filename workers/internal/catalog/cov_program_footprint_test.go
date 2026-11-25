package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── ProgramacaoDeCampanha.Gravar ───────────────────────────────────────────

func covCProgramaInput(w *covCWorld, nome string, st2 uuid.UUID) ProgramacaoInput {
	ini := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	fim := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	return ProgramacaoInput{
		Campanha: CreateCampaignInput{
			ClientID: w.client, Name: nome, StartDate: ini, EndDate: fim,
			TargetStations: []uuid.UUID{w.station, st2}, HubCode: "  EH-PROG01 ",
		},
		Regras: []CreateDistributionRuleInput{
			{TypeID: w.typeID, StationIDs: []uuid.UUID{w.station}, StartDate: ini, EndDate: fim,
				WeekdayMask: 62, TimeStart: "07:00", TimeEnd: "19:00", PlaysPerDay: 3},
			{TypeID: w.typeID, StationIDs: []uuid.UUID{st2}, StartDate: ini, EndDate: fim,
				WeekdayMask: 127, TimeStart: "07:00", TimeEnd: "19:00", PlaysPerDay: 2},
		},
		Precos: []EntradaPreco{
			{StationID: w.station, Mode: PricingModeConsolidated, ConsolidatedValue: covCF(1200)},
			{StationID: st2, Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 15}}},
		},
	}
}

func covCCampanhaPorNome(t *testing.T, w *covCWorld, nome string) int {
	t.Helper()
	var n int
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT count(*) FROM campaigns WHERE name = $1`, nome).Scan(&n))
	return n
}

func TestCovC_Programacao_Gravar_CampanhaRegrasEPrecosNumaTransacao(t *testing.T) {
	w := covCSeedWorld(t, "prog-ok")
	st2 := covCSeedStation(t, w, "Rádio Teste FM Programa Dois")
	repo := NewProgramacaoDeCampanha(w.pool)
	nome := "Campanha Programada " + uuid.NewString()[:8]

	out, err := repo.Gravar(w.ctx, covCProgramaInput(w, nome, st2))
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM campaign_station_type_pricing WHERE campaign_id = $1`, out.Campanha.ID)
		w.pool.Exec(w.ctx, `DELETE FROM campaign_station_pricing WHERE campaign_id = $1`, out.Campanha.ID)
		w.pool.Exec(w.ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, out.Campanha.ID)
		w.pool.Exec(w.ctx, `DELETE FROM campaigns WHERE id = $1`, out.Campanha.ID)
	})

	assert.Equal(t, nome, out.Campanha.Name)
	assert.Equal(t, "programada", out.Campanha.Status)
	require.NotNil(t, out.Campanha.HubCode)
	assert.Equal(t, "EH-PROG01", *out.Campanha.HubCode, "código do hub é aparado")
	require.Len(t, out.Regras, 2)
	for _, r := range out.Regras {
		assert.Equal(t, out.Campanha.ID, r.CampaignID, "as regras nascem com o id da campanha recém-criada")
	}

	prices, err := NewPricing(w.pool).ListByCampaign(w.ctx, out.Campanha.ID)
	require.NoError(t, err)
	require.Len(t, prices, 2)
	byStation := map[uuid.UUID]StationPricing{}
	for _, p := range prices {
		byStation[p.StationID] = p
	}
	assert.Equal(t, PricingModeConsolidated, byStation[w.station].Mode)
	assert.Equal(t, PricingModePerInsertion, byStation[st2].Mode)
	require.Len(t, byStation[st2].PerType, 1)
	assert.InDelta(t, 15, byStation[st2].PerType[0].UnitValue, 0.001)

	// O que ficou persistido bate com o retorno.
	rules, err := NewDistributionRules(w.pool).ListByCampaign(w.ctx, out.Campanha.ID)
	require.NoError(t, err)
	assert.Len(t, rules, 2)
}

func TestCovC_Programacao_Gravar_SemPrecos(t *testing.T) {
	w := covCSeedWorld(t, "prog-nopreco")
	st2 := covCSeedStation(t, w, "Rádio Teste FM Programa Sem Preco")
	repo := NewProgramacaoDeCampanha(w.pool)
	nome := "Campanha Sem Preco " + uuid.NewString()[:8]
	in := covCProgramaInput(w, nome, st2)
	in.Precos = nil
	in.Regras = nil

	out, err := repo.Gravar(w.ctx, in)
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM campaigns WHERE id = $1`, out.Campanha.ID) })
	assert.Empty(t, out.Regras)
	prices, err := NewPricing(w.pool).ListByCampaign(w.ctx, out.Campanha.ID)
	require.NoError(t, err)
	assert.Empty(t, prices)
}

func TestCovC_Programacao_Gravar_FalhaDesfazTudo(t *testing.T) {
	w := covCSeedWorld(t, "prog-rollback")
	st2 := covCSeedStation(t, w, "Rádio Teste FM Programa Rollback")
	repo := NewProgramacaoDeCampanha(w.pool)

	cases := map[string]func(in *ProgramacaoInput){
		// tipo de material inexistente → FK da regra falha DEPOIS de a campanha ter sido inserida
		"regra com tipo inexistente": func(in *ProgramacaoInput) { in.Regras[1].TypeID = uuid.New() },
		// tipo inexistente no preço → falha no último passo, depois de campanha + regras
		"preço com tipo inexistente": func(in *ProgramacaoInput) {
			in.Precos[1].PerType = []TypePricing{{TypeID: uuid.New(), UnitValue: 1}}
		},
		// cliente inexistente → falha logo no primeiro INSERT
		"cliente inexistente": func(in *ProgramacaoInput) { in.Campanha.ClientID = uuid.New() },
	}
	for nome, mutate := range cases {
		t.Run(nome, func(t *testing.T) {
			campNome := "Campanha Rollback " + uuid.NewString()[:8]
			in := covCProgramaInput(w, campNome, st2)
			mutate(&in)
			out, err := repo.Gravar(w.ctx, in)
			require.Error(t, err)
			assert.Nil(t, out)
			assert.Zero(t, covCCampanhaPorNome(t, w, campNome), "falha no meio não deixa campanha pela metade")
		})
	}

	_, err := repo.Gravar(covCCancelled(), covCProgramaInput(w, "x", st2))
	require.Error(t, err)
}

// ─── Footprints ─────────────────────────────────────────────────────────────

func TestCovC_Campaigns_Footprint(t *testing.T) {
	w := covCSeedWorld(t, "fp-camp")
	repo := NewCampaigns(w.pool)

	empty, err := repo.Footprint(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Equal(t, CampaignFootprint{}, empty)

	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))
	_, err = NewDistributionRules(w.pool).Create(w.ctx, CreateDistributionRuleInput{
		CampaignID: w.camp, TypeID: w.typeID, StationIDs: []uuid.UUID{w.station},
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "23:59", PlaysPerDay: 1,
	})
	require.NoError(t, err)
	_, err = NewPricing(w.pool).Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station,
		Mode: PricingModeConsolidated, ConsolidatedValue: covCF(10)})
	require.NoError(t, err)
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")

	fp, err := repo.Footprint(w.ctx, w.camp)
	require.NoError(t, err)
	assert.True(t, fp.HasDetections)
	assert.True(t, fp.HasProjections)
	assert.False(t, fp.HasManualBatches)
	assert.False(t, fp.HasPostSale)
	assert.Equal(t, 1, fp.MaterialLinks)
	assert.Equal(t, 1, fp.Rules)
	assert.Equal(t, 0, fp.Overrides)
	assert.Equal(t, 1, fp.Prices)

	// Campanha inexistente: tudo zerado, sem erro.
	ghost, err := repo.Footprint(w.ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, CampaignFootprint{}, ghost)
	_, err = repo.Footprint(covCCancelled(), w.camp)
	require.Error(t, err)
}

func TestCovC_Clients_Materials_Footprint(t *testing.T) {
	w := covCSeedWorld(t, "fp-cli")
	clients := NewClients(w.pool)
	covCHealthUnused := covCSeedMaterial(t, w, "Material Footprint", 15)
	_ = covCHealthUnused

	fp, err := clients.Footprint(w.ctx, w.client)
	require.NoError(t, err)
	assert.Equal(t, 1, fp.Dependents.Campaigns)
	assert.Equal(t, 2, fp.Dependents.Materials)
	assert.False(t, fp.WebhookConfigured)
	assert.Zero(t, fp.APIKeys)

	_, err = clients.UpdateWebhookConfig(w.ctx, w.client, UpdateWebhookInput{URL: strPtr("https://example.com/h")})
	require.NoError(t, err)
	v := 10
	_, _, err = NewClientStationPMM(w.pool).BulkUpsert(w.ctx, w.client, []TargetPMMEntry{{StationID: w.station, PMMTarget: &v}})
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM client_station_pmm WHERE client_id = $1`, w.client) })
	fp, err = clients.Footprint(w.ctx, w.client)
	require.NoError(t, err)
	assert.True(t, fp.WebhookConfigured)
	assert.Equal(t, 1, fp.PMMRows)

	// Cliente inexistente: tudo zerado.
	ghost, err := clients.Footprint(w.ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, ClientFootprint{}, ghost)

	// Usuários ativos da carteira (user_clients é o que conta).
	user := covCUser(t, w)
	_, err = w.pool.Exec(w.ctx, `INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2)`, user, w.client)
	require.NoError(t, err)
	users, err := clients.ActiveUsers(w.ctx, w.client)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, user, users[0].ID)
	assert.False(t, users[0].OtherActiveClient, "só este cliente na carteira")
	// Dá um segundo cliente ATIVO à carteira: agora ele segue entrando pelo outro.
	outro := covCNewClient(t, w, CreateClientInput{Name: "Cliente Teste Carteira Dois"})
	_, err = w.pool.Exec(w.ctx, `INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2)`, user, outro.ID)
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM user_clients WHERE client_id = $1`, outro.ID) })
	users, err = clients.ActiveUsers(w.ctx, w.client)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.True(t, users[0].OtherActiveClient)
	// ...mas se o outro cliente for desativado, volta a depender só deste.
	_, err = clients.SetActive(w.ctx, outro.ID, false)
	require.NoError(t, err)
	users, err = clients.ActiveUsers(w.ctx, w.client)
	require.NoError(t, err)
	assert.False(t, users[0].OtherActiveClient)
	// Usuário desativado sai da lista.
	_, err = w.pool.Exec(w.ctx, `UPDATE users SET is_active = false WHERE id = $1`, user)
	require.NoError(t, err)
	users, err = clients.ActiveUsers(w.ctx, w.client)
	require.NoError(t, err)
	assert.Empty(t, users)

	// Material.
	mats := NewMaterials(w.pool)
	mfp, err := mats.Footprint(w.ctx, w.mat)
	require.NoError(t, err)
	assert.False(t, mfp.HasDetections)
	covCInsertDet(t, w, w.mat, w.station, covCDay(0), "in_slot")
	mfp, err = mats.Footprint(w.ctx, w.mat)
	require.NoError(t, err)
	assert.True(t, mfp.HasDetections)
	assert.True(t, mfp.HasProjections)

	// Erros de banco.
	_, err = clients.Footprint(covCCancelled(), w.client)
	require.Error(t, err)
	_, err = clients.ActiveUsers(covCCancelled(), w.client)
	require.Error(t, err)
	_, err = mats.Footprint(covCCancelled(), w.mat)
	require.Error(t, err)
}

// ─── Leituras do MCP ────────────────────────────────────────────────────────

func TestCovC_MCPLeituras_ClientesCampanhasEmissoras(t *testing.T) {
	w := covCSeedWorld(t, "mcp")
	clients := NewClients(w.pool)
	camps := NewCampaigns(w.pool)
	stations := NewStations(w.pool)

	// HubIDs: só clientes com ponte aparecem.
	empty, err := clients.HubIDs(w.ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	none, err := clients.HubIDs(w.ctx, []uuid.UUID{w.client})
	require.NoError(t, err)
	assert.Empty(t, none)
	hub := "hub-" + uuid.NewString()
	_, err = w.pool.Exec(w.ctx, `UPDATE clients SET hub_id = $2 WHERE id = $1`, w.client, hub)
	require.NoError(t, err)
	got, err := clients.HubIDs(w.ctx, []uuid.UUID{w.client, uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]string{w.client: hub}, got)

	// ListByHubCode: lista (não único), da mais nova para a mais velha.
	code := "EH-" + uuid.NewString()[:6]
	code = "EH-MCP" + code[3:]
	old := covCNewCampaign(t, w, "Campanha Hub Velha", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	nova := covCNewCampaign(t, w, "Campanha Hub Nova", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	for _, c := range []*Campaign{old, nova} {
		_, err := w.pool.Exec(w.ctx, `UPDATE campaigns SET hub_code = $2 WHERE id = $1`, c.ID, code)
		require.NoError(t, err)
	}
	list, err := camps.ListByHubCode(w.ctx, code)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, nova.ID, list[0].ID)
	assert.Equal(t, old.ID, list[1].ID)
	noMatch, err := camps.ListByHubCode(w.ctx, "EH-INEXISTENTE")
	require.NoError(t, err)
	assert.NotNil(t, noMatch)
	assert.Empty(t, noMatch)

	// CancelledAtFor: só canceladas, com a data de cancelamento.
	cancelled, err := camps.CancelledAtFor(w.ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, cancelled)
	_, _, err = camps.CancelCampaign(w.ctx, nova.ID)
	require.NoError(t, err)
	cancelled, err = camps.CancelledAtFor(w.ctx, []uuid.UUID{old.ID, nova.ID})
	require.NoError(t, err)
	require.Len(t, cancelled, 1)
	assert.WithinDuration(t, time.Now(), cancelled[nova.ID], time.Minute)

	// Cidades de cobertura: ordenadas por distância e depois por nome.
	cities, err := stations.CoverageCities(w.ctx, w.station)
	require.NoError(t, err)
	assert.NotNil(t, cities)
	assert.Empty(t, cities)
	for _, c := range []struct {
		ibge int
		name string
		km   float64
	}{{1, "Zeta", 5}, {2, "Alfa", 5}, {3, "Beta", 1}} {
		_, err := w.pool.Exec(w.ctx, `
			INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, 'SP', $4, false, -23, -46)`, w.station, c.ibge, c.name, c.km)
		require.NoError(t, err)
	}
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM station_coverage_cities WHERE station_id = $1`, w.station) })
	cities, err = stations.CoverageCities(w.ctx, w.station)
	require.NoError(t, err)
	require.Len(t, cities, 3)
	assert.Equal(t, []string{"Beta", "Alfa", "Zeta"}, []string{cities[0].City, cities[1].City, cities[2].City})

	// Classe Anatel não cruzada = indeterminada, nunca zero.
	an, err := stations.Anatel(w.ctx, w.station)
	require.NoError(t, err)
	require.NotNil(t, an)
	assert.Nil(t, an.CoverageKm)
	_, err = stations.Anatel(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Erros de banco.
	_, err = clients.HubIDs(covCCancelled(), []uuid.UUID{w.client})
	require.Error(t, err)
	_, err = camps.ListByHubCode(covCCancelled(), code)
	require.Error(t, err)
	_, err = camps.CancelledAtFor(covCCancelled(), []uuid.UUID{nova.ID})
	require.Error(t, err)
	_, err = stations.CoverageCities(covCCancelled(), w.station)
	require.Error(t, err)
	_, err = stations.Anatel(covCCancelled(), w.station)
	require.Error(t, err)
}

func TestCovC_MCPLeituras_Deteccoes(t *testing.T) {
	w := covCSeedWorld(t, "mcp-det")
	dets := NewDetections(w.pool)
	at := covCDay(0)
	id := covCInsertDet(t, w, w.mat, w.station, at, "in_slot")

	attrs, err := dets.Attributions(w.ctx, id, at)
	require.NoError(t, err)
	require.Len(t, attrs, 1)
	assert.Equal(t, w.camp, attrs[0].CampaignID)
	assert.Equal(t, "Campanha mcp-det", attrs[0].CampaignName)
	assert.Equal(t, "Material mcp-det", attrs[0].CommercialName)
	assert.Equal(t, "in_slot", attrs[0].Category)
	none, err := dets.Attributions(w.ctx, uuid.New(), at)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	ok, err := dets.Approved(w.ctx, id, at)
	require.NoError(t, err)
	assert.True(t, ok)
	_, err = w.pool.Exec(w.ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, id)
	require.NoError(t, err)
	ok, err = dets.Approved(w.ctx, id, at)
	require.NoError(t, err)
	assert.False(t, ok, "retratada sai do conjunto aprovado")
	ok, err = dets.Approved(w.ctx, uuid.New(), at)
	require.NoError(t, err)
	assert.False(t, ok)

	// ApprovedNear: tocadas aprovadas da mesma (campanha, emissora, material) na janela, mais próxima primeiro.
	near1 := covCInsertDet(t, w, w.mat, w.station, covCDay(10), "in_slot")
	covCInsertDet(t, w, w.mat, w.station, covCDay(30), "in_slot")
	_ = near1
	list, err := dets.ApprovedNear(w.ctx, w.camp, w.station, w.mat, covCDay(11), 5*time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, list, 1, "só a de +10min cai em ±5min de +11min; a retratada e a de +30 ficam fora")
	assert.True(t, list[0].Equal(covCDay(10)))
	wide, err := dets.ApprovedNear(w.ctx, w.camp, w.station, w.mat, covCDay(11), time.Hour, 1)
	require.NoError(t, err)
	assert.Len(t, wide, 1, "o limite corta")
	assert.True(t, wide[0].Equal(covCDay(10)), "a mais próxima vem primeiro")

	// Partições: 2026-06 existe; 1999 não.
	has, err := dets.HasPartitionFor(w.ctx, at)
	require.NoError(t, err)
	assert.True(t, has)
	has, err = dets.HasPartitionFor(w.ctx, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, has)

	ctx := covCCancelled()
	_, err = dets.Attributions(ctx, id, at)
	require.Error(t, err)
	_, err = dets.Approved(ctx, id, at)
	require.Error(t, err)
	_, err = dets.ApprovedNear(ctx, w.camp, w.station, w.mat, at, time.Minute, 1)
	require.Error(t, err)
	_, err = dets.HasPartitionFor(ctx, at)
	require.Error(t, err)
}

// ─── Lote manual: erros ─────────────────────────────────────────────────────

func TestCovC_Detections_CreateManualBatch_ValidacaoEErros(t *testing.T) {
	w := covCSeedWorld(t, "batch-err")
	dets := NewDetections(w.pool)
	user := covCUser(t, w)
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, w.mat, []uuid.UUID{w.station}))

	// Lote sem linhas é recusado (evita manual_proof_batches órfão).
	_, err := dets.CreateManualBatch(w.ctx, CreateManualBatchInput{CampaignID: w.camp, StationID: w.station, ManualBy: user})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entries must not be empty")

	// Falha no meio (usuário inexistente → FK de manual_by) desfaz o lote inteiro, inclusive o comprovante.
	batchID := uuid.New()
	_, err = dets.CreateManualBatch(w.ctx, CreateManualBatchInput{
		CampaignID: w.camp, StationID: w.station, ManualBy: uuid.New(), ProofBatchID: &batchID,
		ProofPDFKey: "proofs/x.pdf", ProofPDFSize: 1,
		Entries: []ManualBatchEntry{{CommercialID: w.mat, DetectedAt: covCDay(0)}},
	})
	require.Error(t, err)
	var n int
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT count(*) FROM manual_proof_batches WHERE id = $1`, batchID).Scan(&n))
	assert.Zero(t, n)

	// Lote sem comprovante: nota em branco vira NULL, e a cota do dia é disputada pelo lote todo.
	out, err := dets.CreateManualBatch(w.ctx, CreateManualBatchInput{
		CampaignID: w.camp, StationID: w.station, ManualBy: user,
		Entries: []ManualBatchEntry{
			{CommercialID: w.mat, DetectedAt: covCDay(0), Note: "   "},
			{CommercialID: w.mat, DetectedAt: covCDay(5), Note: " ok "},
		},
	})
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Nil(t, out[0].ManualNote)
	require.NotNil(t, out[1].ManualNote)
	assert.Equal(t, "ok", *out[1].ManualNote)
	assert.Nil(t, out[0].ProofBatchID)

	// Sem lote de comprovante a chave não resolve.
	_, err = dets.ProofKeyForDetection(w.ctx, out[0].ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Validação: erro do banco vira mensagem por linha, não pânico.
	errs := dets.ValidateBatchLinks(covCCancelled(), w.camp, w.station, []ManualBatchEntry{{CommercialID: w.mat}})
	require.Len(t, errs, 1)
	assert.Equal(t, "erro ao validar vínculo", errs[0].Message)
	require.Error(t, func() error {
		_, e := dets.CreateManualBatch(covCCancelled(), CreateManualBatchInput{
			CampaignID: w.camp, StationID: w.station, ManualBy: user,
			Entries: []ManualBatchEntry{{CommercialID: w.mat, DetectedAt: covCDay(0)}}})
		return e
	}())
}

// ─── Ligações / hub / integrações: erros e bordas ──────────────────────────

func TestCovC_Ligacoes_BordasEErros(t *testing.T) {
	w := covCSeedWorld(t, "lig")
	l := NewLigacoes(w.pool)

	// hubClientID vazio nunca liga.
	_, err := l.LigarLocal(w.ctx, w.client, "", nil)
	require.Error(t, err)
	// Cliente inexistente: ErrNoRows do SELECT ... FOR UPDATE.
	_, err = l.LigarLocal(w.ctx, uuid.New(), "hub-x", nil)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	// Fatos de cliente inexistente.
	_, err = l.Fatos(w.ctx, uuid.New(), "hub-x", "", "")
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Liga (sem ator → auditoria "system") e reaperta: idempotente.
	hub := "hub-" + uuid.NewString()
	mudou, err := l.LigarLocal(w.ctx, w.client, hub, nil)
	require.NoError(t, err)
	assert.True(t, mudou)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM audit_log WHERE resource_id = $1 AND action = 'client.hub_link'`, w.client)
	})
	mudou, err = l.LigarLocal(w.ctx, w.client, hub, nil)
	require.NoError(t, err)
	assert.False(t, mudou)
	var tipo string
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT actor_type FROM audit_log WHERE resource_id = $1 AND action = 'client.hub_link' ORDER BY created_at DESC LIMIT 1`,
		w.client).Scan(&tipo))
	assert.Equal(t, "system", tipo)
	// Já ligado a OUTRO código: recusa.
	_, err = l.LigarLocal(w.ctx, w.client, "hub-outro", nil)
	require.ErrorIs(t, err, ErrEscolhidoDeOutro)

	// Fatos: o hub aponta pro próprio escolhido; id lixo e id desconhecido caem em "sem ponte".
	f, err := l.Fatos(w.ctx, w.client, hub, w.client.String(), "")
	require.NoError(t, err)
	assert.True(t, f.HubApontaEscolhido)
	assert.Nil(t, f.Detentor, "o escolhido não é detentor de si mesmo")
	f, err = l.Fatos(w.ctx, w.client, "", "lixo", "12.345.678/0001-90")
	require.NoError(t, err)
	assert.True(t, f.HubSemPonte)
	assert.Equal(t, "12.345.678/0001-90", f.CNPJNoHub)
	f, err = l.Fatos(w.ctx, w.client, "", uuid.NewString(), "")
	require.NoError(t, err)
	assert.True(t, f.HubSemPonte, "id válido mas inexistente aqui")

	// Cliente com campanha NÃO é vazio.
	vazio, err := l.ClienteVazio(w.ctx, w.client)
	require.NoError(t, err)
	assert.False(t, vazio)
	solo := covCNewClient(t, w, CreateClientInput{Name: "Cliente Teste Vazio"})
	vazio, err = l.ClienteVazio(w.ctx, solo.ID)
	require.NoError(t, err)
	assert.True(t, vazio)

	ctx := covCCancelled()
	_, err = l.ClienteVazio(ctx, w.client)
	require.Error(t, err)
	_, err = l.Fatos(ctx, w.client, "h", "", "")
	require.Error(t, err)
	_, err = l.LigarLocal(ctx, w.client, "h", nil)
	require.Error(t, err)
}

func TestCovC_HubClientsEIntegracoes_Erros(t *testing.T) {
	w := covCSeedWorld(t, "hub-int")
	hc := NewHubClients(w.pool)
	clients := NewClients(w.pool)

	_, err := hc.LocalPorHubID(w.ctx, "hub-que-nao-existe-"+uuid.NewString())
	require.Error(t, err)
	_, err = hc.LocalPorHubID(covCCancelled(), "x")
	require.Error(t, err)

	// Integrações: sem nada cadastrado devolve listas vazias e total 0.
	deliveries, total, err := clients.ListWebhookDeliveries(w.ctx, w.client, "", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, deliveries)
	assert.Zero(t, total)
	keys, err := clients.ListAPIKeys(w.ctx, w.client)
	require.NoError(t, err)
	assert.Empty(t, keys)
	_, _, err = clients.ListWebhookDeliveries(covCCancelled(), w.client, "", 10, 0)
	require.Error(t, err)
	_, err = clients.ListAPIKeys(covCCancelled(), w.client)
	require.Error(t, err)
}

// ─── Busca de emissoras: sugestões ──────────────────────────────────────────

func TestCovC_Stations_Suggest_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "suggest")
	repo := NewStations(w.pool)
	ctx := covCCancelled()
	_, err := repo.Suggest(ctx, SuggestInput{Q: "radio"})
	require.Error(t, err)
	_, err = repo.suggestStations(ctx, "radio", "")
	require.Error(t, err)
	_, err = repo.suggestCities(ctx, "radio", "FM")
	require.Error(t, err)
	_, err = repo.suggestStates(ctx, "SP", "FM")
	require.Error(t, err)

	// Entradas que não geram consulta: devolvem lista vazia, não-nil, sem erro.
	short, err := repo.Suggest(w.ctx, SuggestInput{Q: " a "})
	require.NoError(t, err)
	assert.NotNil(t, short.Stations)
	assert.Empty(t, short.Stations)
	assert.Empty(t, short.Cities)
	assert.Empty(t, short.States)
	notUF, err := repo.suggestStates(w.ctx, "sao paulo", "")
	require.NoError(t, err)
	assert.NotNil(t, notUF)
	assert.Empty(t, notUF)
	noTok, err := repo.suggestCities(w.ctx, "   ", "")
	require.NoError(t, err)
	assert.Empty(t, noTok)
}
