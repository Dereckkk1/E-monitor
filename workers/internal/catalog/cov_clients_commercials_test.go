package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covCNewClient(t *testing.T, w *covCWorld, in CreateClientInput) *Client {
	t.Helper()
	c, err := NewClients(w.pool).Create(w.ctx, in)
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM clients WHERE id = $1`, c.ID) })
	return c
}

func TestCovC_Clients_ListByIDsEList(t *testing.T) {
	w := covCSeedWorld(t, "cli-list")
	repo := NewClients(w.pool)
	tok := "ZzCovC" + uuid.NewString()[:6]
	b := covCNewClient(t, w, CreateClientInput{Name: tok + " Beta"})
	a := covCNewClient(t, w, CreateClientInput{Name: tok + " Alfa"})
	_, err := repo.SetActive(w.ctx, b.ID, false)
	require.NoError(t, err)

	// Ordena por nome; ids inexistentes são omitidos sem erro; inativos continuam resolvíveis.
	got, err := repo.ListByIDs(w.ctx, []uuid.UUID{b.ID, uuid.New(), a.ID})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, a.ID, got[0].ID)
	assert.Equal(t, b.ID, got[1].ID)
	assert.False(t, got[1].IsActive)

	empty, err := repo.ListByIDs(w.ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	all, err := repo.List(w.ctx)
	require.NoError(t, err)
	seen := map[uuid.UUID]bool{}
	for _, c := range all {
		seen[c.ID] = true
	}
	assert.True(t, seen[a.ID] && seen[b.ID], "List devolve ativos E inativos")
}

func TestCovC_Clients_ListPaged_BuscaSemAcentoEPaginacao(t *testing.T) {
	w := covCSeedWorld(t, "cli-paged")
	repo := NewClients(w.pool)
	tok := "zzcovcpg" + uuid.NewString()[:6]
	city := "São José"
	covCNewClient(t, w, CreateClientInput{Name: tok + " Um", City: &city})
	covCNewClient(t, w, CreateClientInput{Name: tok + " Dois"})
	covCNewClient(t, w, CreateClientInput{Name: tok + " Tres"})

	// Busca acento-insensitiva: a consulta é UM trecho contíguo de "nome cidade UF ...".
	rows, total, err := repo.ListPaged(w.ctx, tok+" um sao jose", 1, 10, false)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, tok+" Um", rows[0].Name)

	// page/pageSize inválidos caem nos defaults (1 / 20).
	rows, total, err = repo.ListPaged(w.ctx, tok, 0, 0, false)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, rows, 3)

	// Paginação de 2 em 2.
	rows, total, err = repo.ListPaged(w.ctx, tok, 2, 2, false)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, rows, 1)
}

func TestCovC_Clients_UpdateGetSetActiveNaoEncontrado(t *testing.T) {
	w := covCSeedWorld(t, "cli-nf")
	repo := NewClients(w.pool)
	ghost := uuid.New()

	_, err := repo.Get(w.ctx, ghost)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.Update(w.ctx, ghost, UpdateClientInput{Name: "x"})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.SetActive(w.ctx, ghost, false)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.ErrorIs(t, repo.Delete(w.ctx, ghost), pgx.ErrNoRows)
	_, err = repo.GetWebhookConfig(w.ctx, ghost)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.UpdateWebhookConfig(w.ctx, ghost, UpdateWebhookInput{URL: strPtr("https://example.com/hook")})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Update troca todos os campos e o rótulo de público-alvo.
	label := "Mulheres 25-49"
	updated, err := repo.Update(w.ctx, w.client, UpdateClientInput{
		Name: "Cliente Teste Renomeado", ContactEmail: strPtr("contato@example.com"),
		ContactName: strPtr("Pessoa Teste"), Phone: strPtr("11999990000"),
		CNPJ: strPtr("12.345.678/0001-90"), CEP: strPtr("01001-000"),
		City: strPtr("São Paulo"), State: strPtr("SP"), TargetLabel: &label,
	})
	require.NoError(t, err)
	assert.Equal(t, "Cliente Teste Renomeado", updated.Name)
	require.NotNil(t, updated.TargetLabel)
	assert.Equal(t, label, *updated.TargetLabel)
	assert.Equal(t, "contato@example.com", *updated.ContactEmail)

	// Desativar é reversível.
	off, err := repo.SetActive(w.ctx, w.client, false)
	require.NoError(t, err)
	assert.False(t, off.IsActive)
	on, err := repo.SetActive(w.ctx, w.client, true)
	require.NoError(t, err)
	assert.True(t, on.IsActive)
}

func TestCovC_Clients_DeleteComDependenteEContagem(t *testing.T) {
	w := covCSeedWorld(t, "cli-dep")
	repo := NewClients(w.pool)

	// O mundo tem 1 campanha e 1 material do cliente.
	d, err := repo.CountDependents(w.ctx, w.client)
	require.NoError(t, err)
	assert.Equal(t, 1, d.Campaigns)
	assert.Equal(t, 1, d.Materials)
	assert.Equal(t, 0, d.Users)
	assert.Equal(t, 2, d.Total())

	require.ErrorIs(t, repo.Delete(w.ctx, w.client), ErrClientHasDependents)

	// Cliente sem vínculos é apagado de verdade.
	solo := covCNewClient(t, w, CreateClientInput{Name: "Cliente Teste Solo"})
	d, err = repo.CountDependents(w.ctx, solo.ID)
	require.NoError(t, err)
	assert.Zero(t, d.Total())
	require.NoError(t, repo.Delete(w.ctx, solo.ID))
	_, err = repo.Get(w.ctx, solo.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Clients_Webhook(t *testing.T) {
	w := covCSeedWorld(t, "cli-hook")
	repo := NewClients(w.pool)

	// Sem URL configurada: nil, nil (= webhooks desligados para o cliente).
	cfg, err := repo.GetWebhookConfig(w.ctx, w.client)
	require.NoError(t, err)
	assert.Nil(t, cfg)

	cfg, err = repo.UpdateWebhookConfig(w.ctx, w.client, UpdateWebhookInput{
		URL: strPtr("https://example.com/hook"), Secret: strPtr("segredo-teste"),
		Enabled: func() *bool { b := true; return &b }(),
		Events:  []string{"detection.confirmed", "detection.retracted"},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "https://example.com/hook", cfg.URL)
	assert.Equal(t, "segredo-teste", cfg.Secret)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, []string{"detection.confirmed", "detection.retracted"}, cfg.Events)

	// Atualização parcial: só o enabled muda, o resto fica.
	off := false
	cfg, err = repo.UpdateWebhookConfig(w.ctx, w.client, UpdateWebhookInput{Enabled: &off})
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, "https://example.com/hook", cfg.URL)
	assert.Equal(t, "segredo-teste", cfg.Secret)
	assert.Equal(t, []string{"detection.confirmed", "detection.retracted"}, cfg.Events)

	// URL vazia ('') equivale a "sem URL" → config nil.
	cfg, err = repo.UpdateWebhookConfig(w.ctx, w.client, UpdateWebhookInput{URL: strPtr("")})
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestCovC_Clients_OutroComMesmoCNPJ(t *testing.T) {
	w := covCSeedWorld(t, "cli-cnpj")
	repo := NewClients(w.pool)
	// CNPJ único por execução: 14 dígitos derivados do UUID.
	digits := ""
	for _, r := range uuid.NewString() {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	for len(digits) < 14 {
		digits += "7"
	}
	digits = digits[:14]
	masked := digits[:2] + "." + digits[2:5] + "." + digits[5:8] + "/" + digits[8:12] + "-" + digits[12:]

	dono := covCNewClient(t, w, CreateClientInput{Name: "Cliente Teste CNPJ A", CNPJ: &masked})

	// Compara só dígitos: o mesmo CNPJ sem máscara acha o cliente.
	got, err := repo.OutroComMesmoCNPJ(w.ctx, &digits, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dono.ID, got.ID)

	// "Exceto" exclui o próprio cliente.
	got, err = repo.OutroComMesmoCNPJ(w.ctx, &masked, dono.ID)
	require.NoError(t, err)
	assert.Nil(t, got)

	// nil e CNPJ incompleto não se comparam.
	got, err = repo.OutroComMesmoCNPJ(w.ctx, nil, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)
	curto := "123.456"
	got, err = repo.OutroComMesmoCNPJ(w.ctx, &curto, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)

	// Com duplicata, prefere o ATIVO.
	dup := covCNewClient(t, w, CreateClientInput{Name: "Cliente Teste CNPJ B", CNPJ: &digits})
	_, err = repo.SetActive(w.ctx, dono.ID, false)
	require.NoError(t, err)
	got, err = repo.OutroComMesmoCNPJ(w.ctx, &digits, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dup.ID, got.ID, "ativo vem antes do inativo")
}

func TestCovC_Clients_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "cli-dberr")
	repo := NewClients(w.pool)
	ctx := covCCancelled()
	id := w.client

	_, err := repo.Create(ctx, CreateClientInput{Name: "x"})
	require.Error(t, err)
	_, err = repo.Update(ctx, id, UpdateClientInput{Name: "x"})
	require.Error(t, err)
	require.Error(t, repo.Delete(ctx, id))
	_, err = repo.CountDependents(ctx, id)
	require.Error(t, err)
	_, err = repo.SetActive(ctx, id, true)
	require.Error(t, err)
	_, err = repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.ListByIDs(ctx, []uuid.UUID{id})
	require.Error(t, err)
	_, err = repo.List(ctx)
	require.Error(t, err)
	_, _, err = repo.ListPaged(ctx, "", 1, 10, true)
	require.Error(t, err)
	_, err = repo.GetWebhookConfig(ctx, id)
	require.Error(t, err)
	_, err = repo.UpdateWebhookConfig(ctx, id, UpdateWebhookInput{})
	require.Error(t, err)
	cnpj := "12345678000195"
	_, err = repo.OutroComMesmoCNPJ(ctx, &cnpj, id)
	require.Error(t, err)
}

// ─── Commercials (tabela legada) ────────────────────────────────────────────

func covCNewCommercial(t *testing.T, w *covCWorld, title string, dur float64, ready bool) *Commercial {
	t.Helper()
	repo := NewCommercials(w.pool)
	cut := "corte-1"
	com, err := repo.Create(w.ctx, CreateCommercialInput{
		CampaignID: w.camp, Title: title, CutLabel: &cut, DurationSeconds: dur,
		MasterStoragePath: "/tmp/covc-com-" + title, MasterSHA256: "covc-" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, com.ID)
		w.pool.Exec(w.ctx, `DELETE FROM commercials WHERE id = $1`, com.ID)
	})
	if ready {
		_, err := w.pool.Exec(w.ctx, `UPDATE commercials SET fingerprint_status='ready' WHERE id = $1`, com.ID)
		require.NoError(t, err)
	}
	return com
}

func TestCovC_Commercials_CRUDEListagens(t *testing.T) {
	w := covCSeedWorld(t, "com-crud")
	repo := NewCommercials(w.pool)

	pend := covCNewCommercial(t, w, "Spot Pendente", 30, false)
	ready := covCNewCommercial(t, w, "Spot Pronto", 15, true)
	assert.Equal(t, "pending", pend.FingerprintStatus)
	require.NotNil(t, pend.CutLabel)
	assert.Equal(t, "corte-1", *pend.CutLabel)
	assert.Empty(t, pend.TargetStations)

	got, err := repo.Get(w.ctx, pend.ID)
	require.NoError(t, err)
	assert.Equal(t, "Spot Pendente", got.Title)
	_, err = repo.Get(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	list, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, pend.ID, list[0].ID, "ordena por criação")

	// ListReadyByCampaigns: só 'ready', independente de target_stations; vazio → nil.
	rdy, err := repo.ListReadyByCampaigns(w.ctx, []uuid.UUID{w.camp})
	require.NoError(t, err)
	require.Len(t, rdy, 1)
	assert.Equal(t, ready.ID, rdy[0].ID)
	none, err := repo.ListReadyByCampaigns(w.ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, none)

	// Por emissora: precisa estar em target_stations explicitamente.
	forSt, err := repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, w.station)
	require.NoError(t, err)
	assert.Empty(t, forSt, "target_stations vazio = inativo")
	require.NoError(t, repo.UpdateStations(w.ctx, ready.ID, []uuid.UUID{w.station}))
	forSt, err = repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, w.station)
	require.NoError(t, err)
	require.Len(t, forSt, 1)
	assert.Equal(t, ready.ID, forSt[0].ID)
	nilCamps, err := repo.ListReadyByCampaignsForStation(w.ctx, nil, w.station)
	require.NoError(t, err)
	assert.Nil(t, nilCamps)

	// UpdateStations(nil) limpa (vira lista vazia, não NULL).
	require.NoError(t, repo.UpdateStations(w.ctx, ready.ID, nil))
	got, err = repo.Get(w.ctx, ready.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.TargetStations)
	assert.Empty(t, got.TargetStations)
}

func TestCovC_Commercials_ListReadyPorEmissoraExcluiBackfillados(t *testing.T) {
	w := covCSeedWorld(t, "com-backfill")
	repo := NewCommercials(w.pool)
	com := covCNewCommercial(t, w, "Spot Legado", 30, true)
	require.NoError(t, repo.UpdateStations(w.ctx, com.ID, []uuid.UUID{w.station}))

	got, err := repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, w.station)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// Um material com o MESMO id (backfill) sai desse caminho: carrega pelo de materiais.
	_, err = w.pool.Exec(w.ctx, `
		INSERT INTO materials (id, client_id, title, duration_seconds, master_storage_path, master_sha256)
		VALUES ($1, $2, 'Backfill', 30, '/tmp/x', $3)`, com.ID, w.client, "covc-"+uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() { w.pool.Exec(w.ctx, `DELETE FROM materials WHERE id = $1`, com.ID) })
	got, err = repo.ListReadyByCampaignsForStation(w.ctx, []uuid.UUID{w.camp}, w.station)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCovC_Commercials_Delete(t *testing.T) {
	w := covCSeedWorld(t, "com-del")
	repo := NewCommercials(w.pool)

	// Inexistente → ErrNoRows.
	_, _, err := repo.Delete(w.ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Com histórico de detecções → recusa e preserva a linha.
	withHist := covCNewCommercial(t, w, "Spot Com Historico", 30, true)
	_, err = w.pool.Exec(w.ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence, hash_count, category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.9, 10, 'bonus')`,
		w.station, withHist.ID, w.camp, covCDay(0))
	require.NoError(t, err)
	_, _, err = repo.Delete(w.ctx, withHist.ID)
	require.ErrorIs(t, err, ErrCommercialHasDetections)
	_, err = repo.Get(w.ctx, withHist.ID)
	require.NoError(t, err, "recusa não apaga")

	// Sem histórico: apaga e devolve campanha + caminho do master (pro caller limpar o disco).
	clean := covCNewCommercial(t, w, "Spot Limpo", 30, true)
	camp, path, err := repo.Delete(w.ctx, clean.ID)
	require.NoError(t, err)
	assert.Equal(t, w.camp, camp)
	assert.Equal(t, "/tmp/covc-com-Spot Limpo", path)
	_, err = repo.Get(w.ctx, clean.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Commercials_LookupForDedup(t *testing.T) {
	w := covCSeedWorld(t, "com-dedup")
	repo := NewCommercials(w.pool)

	// Short id desconhecido → ErrNoRows.
	_, err := repo.LookupForDedup(w.ctx, 2_000_000_000)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Legado pronto: janela de dedup default (5s) e duração arredondada.
	com := covCNewCommercial(t, w, "Spot Dedup", 14.6, true)
	info, err := repo.LookupForDedup(w.ctx, com.ShortID)
	require.NoError(t, err)
	assert.Equal(t, com.ID, info.CommercialID)
	assert.Equal(t, w.client, info.ClientID)
	assert.Equal(t, 15, info.DurationSeconds)
	assert.Equal(t, 5, info.DedupWindowSeconds)

	// Janela por campanha sobrescreve o default.
	_, err = w.pool.Exec(w.ctx, `UPDATE campaigns SET dedup_window_seconds = 9 WHERE id = $1`, w.camp)
	require.NoError(t, err)
	info, err = repo.LookupForDedup(w.ctx, com.ShortID)
	require.NoError(t, err)
	assert.Equal(t, 9, info.DedupWindowSeconds)

	// Legado ainda 'pending' não é achado.
	pend := covCNewCommercial(t, w, "Spot Dedup Pendente", 30, false)
	_, err = repo.LookupForDedup(w.ctx, pend.ShortID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Fallback para materiais: só se 'ready' e ligado a campanha programada/ativa.
	mat := covCSeedMaterial(t, w, "Material Dedup", 29.5)
	var matShort int32
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT short_id FROM materials WHERE id=$1`, mat).Scan(&matShort))
	_, err = repo.LookupForDedup(w.ctx, matShort)
	require.ErrorIs(t, err, pgx.ErrNoRows, "material pendente e sem vínculo")
	_, err = w.pool.Exec(w.ctx, `UPDATE materials SET fingerprint_status='ready' WHERE id=$1`, mat)
	require.NoError(t, err)
	_, err = repo.LookupForDedup(w.ctx, matShort)
	require.ErrorIs(t, err, pgx.ErrNoRows, "ready mas sem campaign_materials")
	require.NoError(t, NewCampaignMaterials(w.pool).Link(w.ctx, w.camp, mat, []uuid.UUID{w.station}))
	info, err = repo.LookupForDedup(w.ctx, matShort)
	require.NoError(t, err)
	assert.Equal(t, mat, info.CommercialID, "id polimórfico: devolve o UUID do material")
	assert.Equal(t, 30, info.DurationSeconds, "29.5 arredonda para 30")
	assert.Equal(t, 9, info.DedupWindowSeconds)

	// Campanha concluída não conta.
	require.NoError(t, NewCampaigns(w.pool).UpdateStatus(w.ctx, w.camp, "concluida"))
	_, err = repo.LookupForDedup(w.ctx, matShort)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestCovC_Commercials_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "com-dberr")
	repo := NewCommercials(w.pool)
	ctx := covCCancelled()
	id := uuid.New()

	_, err := repo.Get(ctx, id)
	require.Error(t, err)
	_, err = repo.ListByCampaign(ctx, w.camp)
	require.Error(t, err)
	_, err = repo.ListReadyByCampaigns(ctx, []uuid.UUID{w.camp})
	require.Error(t, err)
	_, err = repo.ListReadyByCampaignsForStation(ctx, []uuid.UUID{w.camp}, w.station)
	require.Error(t, err)
	_, err = repo.LookupForDedup(ctx, 1)
	require.Error(t, err)
	require.Error(t, repo.UpdateStations(ctx, id, nil))
	_, _, err = repo.Delete(ctx, id)
	require.Error(t, err)
	_, err = repo.Create(ctx, CreateCommercialInput{CampaignID: w.camp, Title: "x"})
	require.Error(t, err)
}
