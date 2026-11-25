package postsale

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- funcoes puras ----------------------------------------------------------

func TestCovZ_FirstName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Ana Maria Souza", "Ana"},
		{"  Joao  ", "Joao"},
		{"Cher", "Cher"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, firstName(c.in), "%q", c.in)
	}
}

func TestCovZ_NewToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := NewToken()
		require.NoError(t, err)
		assert.Len(t, tok, 43, "32 bytes em base64url sem padding")
		assert.NotContains(t, tok, "=")
		assert.NotContains(t, tok, "+")
		assert.NotContains(t, tok, "/")
		assert.False(t, seen[tok], "tokens nao se repetem")
		seen[tok] = true
	}
}

func TestCovZ_TodaySaoPaulo(t *testing.T) {
	got := todaySaoPaulo()
	assert.Equal(t, time.UTC, got.Location())
	assert.Equal(t, 0, got.Hour())
	assert.Equal(t, 0, got.Minute())
	// Em relacao ao "hoje" UTC o dia de Sao Paulo difere no maximo 1 dia (UTC-3).
	diff := time.Now().UTC().Sub(got)
	assert.Less(t, diff, 48*time.Hour)
	assert.Greater(t, diff, -24*time.Hour)
}

func TestCovZ_DefaultMessages(t *testing.T) {
	intro := DefaultIntroMessage("Cliente Teste")
	assert.Contains(t, intro, "Cliente Teste")
	assert.False(t, strings.HasPrefix(intro, " "))

	assert.Contains(t, DefaultCheckingText(97), "97% de entrega")
	assert.Contains(t, DefaultCheckingText(100), "100% de entrega")
}

func TestCovZ_OverallDeliveryPct(t *testing.T) {
	assert.Equal(t, 100, overallDeliveryPct(nil), "sem plano nao ha deficit a relatar")
	assert.Equal(t, 100, overallDeliveryPct([]StationRow{{Programmed: 0, Identified: 5}}))
	assert.Equal(t, 50, overallDeliveryPct([]StationRow{{Programmed: 10, Identified: 5}}))
	assert.Equal(t, 75, overallDeliveryPct([]StationRow{
		{Programmed: 10, Identified: 10},
		{Programmed: 10, Identified: 5},
		{Programmed: 20, Identified: 15},
	}), "soma identificado / soma programado, nao a media das linhas")
}

func TestCovZ_Service_LinkAndMailEnabled(t *testing.T) {
	svc := New(Config{BaseURL: "https://painel.example.com///", MailEnabled: true})
	assert.Equal(t, "https://painel.example.com/pos-venda/abc%2Fdef", svc.Link("abc/def"), "token e escapado no path e a barra final da base some")
	assert.False(t, svc.MailEnabled(), "habilitado mas sem mailer = desligado")

	assert.NotNil(t, svc.pending, "mapa inicializado no construtor")
	assert.False(t, New(Config{}).MailEnabled())
}

func TestCovZ_Service_SetAssets(t *testing.T) {
	svc := New(Config{})
	report, camp := uuid.New(), uuid.New()

	require.Error(t, svc.SetAssetsBytes(context.Background(), report, camp, []byte("mapa"), nil))
	require.Error(t, svc.SetAssetsBytes(context.Background(), report, camp, nil, []byte("ind")))
	_, ok := svc.takePending(report, camp)
	assert.False(t, ok, "captura incompleta nao fica guardada")

	require.NoError(t, svc.SetAssetsBytes(context.Background(), report, camp, []byte("mapa"), []byte("ind")))
	up, ok := svc.takePending(report, camp)
	require.True(t, ok)
	assert.Equal(t, []byte("mapa"), up.MapPNG)
	assert.Equal(t, []byte("ind"), up.InsightsPNG)

	// clearPending limpa so as capturas do relatorio dado.
	other := uuid.New()
	require.NoError(t, svc.SetAssets(context.Background(), report, camp, capture()))
	require.NoError(t, svc.SetAssets(context.Background(), other, camp, capture()))
	svc.clearPending(report)
	_, ok = svc.takePending(report, camp)
	assert.False(t, ok)
	_, ok = svc.takePending(other, camp)
	assert.True(t, ok)
}

// --- repositorio ------------------------------------------------------------

func TestCovZ_Repo_CreateDraftAndUpdateContent(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{
		ClientID: seed.ClientID, Title: "  Titulo com espacos  ", IntroMessage: "ola", CreatedBy: &seed.AdminID,
	})
	require.NoError(t, err)
	assert.Equal(t, "Titulo com espacos", rep.Title, "titulo e aparado")
	assert.Equal(t, "ola", rep.IntroMessage)
	assert.Equal(t, "draft", rep.Status)

	require.NoError(t, repo.UpdateContent(ctx, rep.ID, "  Novo  ", "intro nova", "  https://drive.example.com/x  "))
	got, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, "Novo", got.Title)
	assert.Equal(t, "intro nova", got.IntroMessage)
	assert.Equal(t, "https://drive.example.com/x", got.AttachmentsURL)

	assert.ErrorIs(t, repo.UpdateContent(ctx, uuid.New(), "t", "i", ""), ErrNotFound)

	// Cliente inexistente: a FK recusa e o erro vem contextualizado.
	_, err = repo.CreateDraft(ctx, CreateDraftInput{ClientID: uuid.New(), Title: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "criar draft")
}

func TestCovZ_Repo_GetNotFoundAndUpdateClient(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	_, err := repo.Get(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	other := covZSecondClient(t, ctx, pool, "Cliente Teste Outro")

	require.NoError(t, repo.UpdateClient(ctx, rep.ID, other))
	got, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, other, got.ClientID)
	assert.Equal(t, "Cliente Teste Outro", got.ClientName)

	assert.ErrorIs(t, repo.UpdateClient(ctx, uuid.New(), other), ErrNotFound)

	// Depois de enviado o documento e do cliente que o recebeu: nao troca mais.
	require.NoError(t, repo.MarkSent(ctx, rep.ID, []byte(`{"x":1}`)))
	assert.ErrorIs(t, repo.UpdateClient(ctx, rep.ID, seed.ClientID), ErrNotFound)
	assert.ErrorIs(t, repo.UpdateContent(ctx, rep.ID, "t", "i", ""), ErrNotFound)

	// MarkSent so vale uma vez.
	assert.ErrorIs(t, repo.MarkSent(ctx, rep.ID, []byte(`{"x":2}`)), ErrAlreadySent)
}

func TestCovZ_Repo_ReplaceBlocks_RoundTripAndOrdering(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)
	second := covZSecondCampaign(t, ctx, pool, seed, "Campanha ZZ Segunda")

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)

	valor, impactos := 1234.5, int64(0) // zero e um override legitimo, nao "ausente"
	city := "Cidade Teste"
	row := StationRow{StationID: seed.Acima, Name: "Radio Teste FM", City: &city, Programmed: 10, Identified: 8, Deficit: 2, Kind: KindCompensation, Note: "compensado"}
	err = repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{
		{CampaignID: second, From: date(2026, 6, 1), To: date(2026, 6, 10), Position: 2},
		{
			CampaignID: seed.CampaignID, From: date(2026, 6, 11), To: date(2026, 6, 30), Position: 1,
			CheckingText: "texto do admin", CheckingRows: []StationRow{row}, CheckingEdited: true,
			KPIOverrides: KPIOverrides{ValorEntregue: &valor, Impactos: &impactos},
		},
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, got.Blocks, 2)
	// Ordenado por position: o de position 1 vem antes do de position 2.
	assert.Equal(t, seed.CampaignID, got.Blocks[0].CampaignID)
	assert.Equal(t, second, got.Blocks[1].CampaignID)

	b := got.Blocks[0]
	assert.Equal(t, "texto do admin", b.CheckingText)
	assert.True(t, b.CheckingEdited)
	require.Len(t, b.CheckingRows, 1)
	assert.Equal(t, seed.Acima, b.CheckingRows[0].StationID)
	assert.Equal(t, 8, b.CheckingRows[0].Identified)
	assert.Equal(t, "compensado", b.CheckingRows[0].Note)
	require.NotNil(t, b.CheckingRows[0].City)
	assert.Equal(t, city, *b.CheckingRows[0].City)
	require.NotNil(t, b.KPIOverrides.ValorEntregue)
	assert.InDelta(t, 1234.5, *b.KPIOverrides.ValorEntregue, 1e-9)
	require.NotNil(t, b.KPIOverrides.Impactos)
	assert.Equal(t, int64(0), *b.KPIOverrides.Impactos)
	assert.Nil(t, b.KPIOverrides.Bonificacao, "campo nao informado continua nulo")
	assert.True(t, b.KPIOverrides.Any())
	assert.False(t, got.Blocks[1].KPIOverrides.Any())
	assert.Equal(t, "2026-06-11", b.From.Format("2006-01-02"))

	// Sem Position explicita, vale a ordem em que os blocos foram passados.
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{
		{CampaignID: second, From: date(2026, 6, 1), To: date(2026, 6, 5)},
		{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 5)},
	}))
	got, err = repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, got.Blocks, 2)
	assert.Equal(t, 0, got.Blocks[0].Position)
	assert.Equal(t, 1, got.Blocks[1].Position)
	assert.Equal(t, second, got.Blocks[0].CampaignID)
	assert.Empty(t, got.Blocks[0].CheckingRows, "checking_rows nulo vira lista vazia gravada")
}

func TestCovZ_Repo_ReplaceBlocks_FailureKeepsPreviousBlocks(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 5)}}))

	// O 2o bloco aponta para campanha inexistente: a transacao inteira desfaz,
	// inclusive o DELETE inicial.
	err = repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{
		{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 9)},
		{CampaignID: uuid.New(), From: date(2026, 6, 1), To: date(2026, 6, 9)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gravar bloco")

	got, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, got.Blocks, 1)
	assert.Equal(t, "2026-06-05", got.Blocks[0].To.Format("2006-01-02"), "bloco original preservado")
}

func TestCovZ_Repo_BlocksRejectMalformedJSON(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 5)}}))

	cases := []struct {
		column string
		want   string
	}{
		{"checking_rows", "checking_rows inválido"},
		{"assets", "assets inválido"},
		{"kpi_overrides", "kpi_overrides inválido"},
	}
	for _, c := range cases {
		t.Run(c.column, func(t *testing.T) {
			// JSON valido para o jsonb mas com formato errado para o tipo Go.
			bad := `"string-onde-devia-ter-estrutura"`
			_, err := pool.Exec(ctx, `UPDATE post_sale_report_campaigns SET `+c.column+` = $2::jsonb WHERE report_id = $1`, rep.ID, bad)
			require.NoError(t, err)
			_, err = repo.Get(ctx, rep.ID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			// restaura
			_, err = pool.Exec(ctx, `UPDATE post_sale_report_campaigns SET checking_rows='[]'::jsonb, assets='{}'::jsonb, kpi_overrides='{}'::jsonb WHERE report_id = $1`, rep.ID)
			require.NoError(t, err)
		})
	}
}

func TestCovZ_Repo_RecipientsLifecycle(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	recs, err := repo.CreateRecipients(ctx, rep.ID, []RecipientInput{
		{Email: "zeta@example.com", Name: "Zeta", Token: "tok-zeta-covz"},
		{Email: "alfa@example.com", Name: "Alfa", Token: "tok-alfa-covz"},
	})
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "pending", recs[0].EmailStatus)
	assert.Equal(t, 0, recs[0].OpenCount)
	assert.Nil(t, recs[0].OpenedAt)

	list, err := repo.Recipients(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "alfa@example.com", list[0].Email, "ordenado por email")

	// Token duplicado e recusado (UNIQUE) com o email no erro.
	_, err = repo.CreateRecipients(ctx, rep.ID, []RecipientInput{{Email: "dup@example.com", Token: "tok-alfa-covz"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "criar destinatário dup@example.com")

	alfa := list[0]

	// MarkEmail: erro longo e truncado em 500; sucesso limpa o erro.
	require.NoError(t, repo.MarkEmail(ctx, alfa.ID, "failed", strings.Repeat("x", 800)))
	list, err = repo.Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", list[0].EmailStatus)
	require.NotNil(t, list[0].EmailError)
	assert.Len(t, *list[0].EmailError, 500)
	require.NoError(t, repo.MarkEmail(ctx, alfa.ID, "sent", ""))
	list, err = repo.Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, "sent", list[0].EmailStatus)
	assert.Nil(t, list[0].EmailError)

	// TouchOpen: primeira abertura fixa opened_at, as seguintes so contam.
	require.NoError(t, repo.TouchOpen(ctx, alfa.ID))
	list, _ = repo.Recipients(ctx, rep.ID)
	first := list[0].OpenedAt
	require.NotNil(t, first)
	assert.Equal(t, 1, list[0].OpenCount)
	require.NoError(t, repo.TouchOpen(ctx, alfa.ID))
	list, _ = repo.Recipients(ctx, rep.ID)
	assert.Equal(t, 2, list[0].OpenCount)
	assert.True(t, first.Equal(*list[0].OpenedAt), "opened_at nao muda depois da primeira abertura")
}

func TestCovZ_Repo_RevokeRecipient(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	recs, err := repo.CreateRecipients(ctx, rep.ID, []RecipientInput{
		{Email: "a@example.com", Token: "tok-a-covz"},
		{Email: "b@example.com", Token: "tok-b-covz"},
	})
	require.NoError(t, err)

	// UUID zero vira NULL em revoked_by: a acao de seguranca nao falha por causa da auditoria.
	require.NoError(t, repo.RevokeRecipient(ctx, recs[0].ID, uuid.Nil))
	var by *uuid.UUID
	var at *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT revoked_by, revoked_at FROM post_sale_report_recipients WHERE id=$1`, recs[0].ID).Scan(&by, &at))
	assert.Nil(t, by)
	require.NotNil(t, at)

	// Com ator conhecido grava quem revogou; revogar de novo e idempotente e nao reescreve.
	require.NoError(t, repo.RevokeRecipient(ctx, recs[1].ID, seed.AdminID))
	require.NoError(t, pool.QueryRow(ctx, `SELECT revoked_by, revoked_at FROM post_sale_report_recipients WHERE id=$1`, recs[1].ID).Scan(&by, &at))
	require.NotNil(t, by)
	assert.Equal(t, seed.AdminID, *by)
	firstAt := *at
	require.NoError(t, repo.RevokeRecipient(ctx, recs[1].ID, uuid.Nil))
	require.NoError(t, pool.QueryRow(ctx, `SELECT revoked_at FROM post_sale_report_recipients WHERE id=$1`, recs[1].ID).Scan(&at))
	assert.True(t, firstAt.Equal(*at))

	assert.ErrorIs(t, repo.RevokeRecipient(ctx, uuid.New(), uuid.Nil), ErrNotFound)

	// Revogado nunca resolve, mesmo com o relatorio enviado.
	require.NoError(t, repo.MarkSent(ctx, rep.ID, []byte(`{"ok":true}`)))
	_, err = repo.ResolveToken(ctx, "tok-a-covz")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Repo_PayloadAndAssets(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 5)}}))

	// Draft nao tem payload congelado.
	_, err = repo.Payload(ctx, rep.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// Sem assets gravados nada resolve.
	_, err = repo.BundleKey(ctx, rep.ID, seed.CampaignID)
	assert.ErrorIs(t, err, ErrNotFound, "bloco sem bundle")
	_, err = repo.ImageKey(ctx, rep.ID, seed.CampaignID, AssetMap)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = repo.BundleKey(ctx, rep.ID, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound, "campanha que nao esta no relatorio")

	got, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	blockID := got.Blocks[0].ID

	// So o bundle e o mapa; sem o PNG de indicadores.
	require.NoError(t, repo.SetAssets(ctx, blockID, Assets{BundleZIP: "k/bundle.zip", MapPNG: "k/mapa.png"}))
	bundle, err := repo.BundleKey(ctx, rep.ID, seed.CampaignID)
	require.NoError(t, err)
	assert.Equal(t, "k/bundle.zip", bundle)
	mapKey, err := repo.ImageKey(ctx, rep.ID, seed.CampaignID, AssetMap)
	require.NoError(t, err)
	assert.Equal(t, "k/mapa.png", mapKey)
	_, err = repo.ImageKey(ctx, rep.ID, seed.CampaignID, AssetInsights)
	assert.ErrorIs(t, err, ErrNotFound, "indicadores nao foi gravado")
	_, err = repo.ImageKey(ctx, rep.ID, seed.CampaignID, AssetKind("outro"))
	assert.ErrorIs(t, err, ErrNotFound, "tipo de imagem desconhecido")

	require.NoError(t, repo.MarkSent(ctx, rep.ID, []byte(`{"title":"congelado"}`)))
	raw, err := repo.Payload(ctx, rep.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"title":"congelado"}`, string(raw))
	_, err = repo.Payload(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Repo_Briefs(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	cli, err := repo.ClientBrief(ctx, seed.ClientID)
	require.NoError(t, err)
	assert.NotEmpty(t, cli.Name)
	assert.Nil(t, cli.LogoURL)
	_, err = pool.Exec(ctx, `UPDATE clients SET logo_url='https://cdn.example.com/logo.png' WHERE id=$1`, seed.ClientID)
	require.NoError(t, err)
	cli, err = repo.ClientBrief(ctx, seed.ClientID)
	require.NoError(t, err)
	require.NotNil(t, cli.LogoURL)
	assert.Equal(t, "https://cdn.example.com/logo.png", *cli.LogoURL)
	_, err = repo.ClientBrief(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	camp, err := repo.CampaignBrief(ctx, seed.CampaignID)
	require.NoError(t, err)
	assert.Equal(t, seed.CampaignID, camp.ID)
	assert.Equal(t, seed.ClientID, camp.ClientID)
	assert.Equal(t, "2026-06-01", camp.StartDate.Format("2006-01-02"))
	assert.Equal(t, "2026-06-30", camp.EndDate.Format("2006-01-02"))
	assert.NotEmpty(t, camp.Status)
	_, err = repo.CampaignBrief(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Repo_TargetSuffix(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	set := func(label any) {
		_, err := pool.Exec(ctx, `UPDATE clients SET target_label=$2 WHERE id=$1`, seed.ClientID, label)
		require.NoError(t, err)
	}

	set(nil)
	got, err := repo.TargetSuffix(ctx, seed.CampaignID)
	require.NoError(t, err)
	assert.Equal(t, "", got, "sem rotulo nao ha sufixo")

	set("   ")
	got, err = repo.TargetSuffix(ctx, seed.CampaignID)
	require.NoError(t, err)
	assert.Equal(t, "", got, "so espacos tambem nao")

	set("  Mulheres 25-45 ")
	got, err = repo.TargetSuffix(ctx, seed.CampaignID)
	require.NoError(t, err)
	assert.Equal(t, " (Mulheres 25-45)", got)

	_, err = repo.TargetSuffix(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Repo_ListFiltersAndCounts(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	draft, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "Fechamento Alfa covz"})
	require.NoError(t, err)
	sent, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "Fechamento Beta covz"})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, sent.ID, []BlockRow{{CampaignID: seed.CampaignID, From: date(2026, 6, 1), To: date(2026, 6, 30)}}))
	require.NoError(t, repo.MarkSent(ctx, sent.ID, []byte(`{}`)))
	recs, err := repo.CreateRecipients(ctx, sent.ID, []RecipientInput{{Email: "a@example.com", Token: "tok-list-a"}, {Email: "b@example.com", Token: "tok-list-b"}})
	require.NoError(t, err)
	require.NoError(t, repo.TouchOpen(ctx, recs[0].ID))

	ids := func(p *ListPage) []uuid.UUID {
		var out []uuid.UUID
		for _, it := range p.Items {
			out = append(out, it.ID)
		}
		return out
	}

	all, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID})
	require.NoError(t, err)
	assert.Equal(t, 10, all.PerPage, "default de 10 por pagina")
	assert.Equal(t, 1, all.Page)
	assert.Equal(t, 2, all.Total)
	assert.Equal(t, ListCounts{All: 2, Sent: 1, Draft: 1}, all.Counts)
	assert.ElementsMatch(t, []uuid.UUID{draft.ID, sent.ID}, ids(all))

	// Busca por titulo (insensivel a caixa).
	byQ, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Q: "BETA covz"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{sent.ID}, ids(byQ))
	assert.Equal(t, 1, byQ.Counts.All)

	// Busca pelo nome do cliente casa os dois.
	var clientName string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM clients WHERE id=$1`, seed.ClientID).Scan(&clientName))
	byClient, err := repo.List(ctx, ListFilter{Q: strings.ToLower(clientName)})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, byClient.Total, 2)

	// Filtro de estado: o total acompanha o estado, mas Counts ignora esse filtro.
	onlySent, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Status: "sent"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{sent.ID}, ids(onlySent))
	assert.Equal(t, 1, onlySent.Total)
	assert.Equal(t, 2, onlySent.Counts.All)
	it := onlySent.Items[0]
	assert.Equal(t, 1, it.Campaigns)
	assert.Equal(t, 2, it.Recipients)
	assert.Equal(t, 1, it.Opened)
	require.NotNil(t, it.SentAt)

	onlyDraft, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Status: "draft"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{draft.ID}, ids(onlyDraft))
	assert.Nil(t, onlyDraft.Items[0].PeriodFrom, "rascunho sem bloco nao tem periodo")

	// Competencia: so o enviado (com bloco em junho) casa; rascunho sem bloco nunca casa.
	june, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Month: "2026-06"})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{sent.ID}, ids(june))

	// Pagina alem do fim: itens vazios, nunca nulos.
	beyond, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Page: 5, PerPage: 10})
	require.NoError(t, err)
	assert.NotNil(t, beyond.Items)
	assert.Empty(t, beyond.Items)
	assert.Equal(t, 2, beyond.Total)

	// Cliente sem relatorios.
	none := uuid.New()
	empty, err := repo.List(ctx, ListFilter{ClientID: &none})
	require.NoError(t, err)
	assert.Zero(t, empty.Total)
	assert.Equal(t, ListCounts{}, empty.Counts)
}

func TestCovZ_Repo_DatabaseErrorsAreWrapped(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewRepo(pool)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := repo.List(cancelled, ListFilter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contar")

	_, err = repo.Get(cancelled, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get")

	_, err = repo.ActiveClientUsers(cancelled, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destinatários ativos")

	_, err = repo.InternalRecipients(cancelled)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destinatários internos")

	_, err = repo.Recipients(cancelled, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destinatários")

	_, err = repo.ResolveToken(cancelled, "x")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "falha de banco nao pode se passar por 'nao encontrado'")
}

// --- servico: publicado, resolvido, reenviado --------------------------------

func TestCovZ_Service_ResolveCountsOpens(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc, rep, rc := covZPublished(t, ctx, pool, seed, &recordingMailer{}, &fakeStore{})

	raw, err := svc.Resolve(ctx, rc.Token)
	require.NoError(t, err)
	var p Payload
	require.NoError(t, json.Unmarshal(raw, &p))
	assert.Equal(t, "Pós-venda · Teste", p.Title)

	_, err = svc.Resolve(ctx, rc.Token)
	require.NoError(t, err)
	recs, err := svc.Repo().Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, recs[0].OpenCount, "cada abertura conta")
	assert.NotNil(t, recs[0].OpenedAt)

	_, err = svc.Resolve(ctx, "token-que-nao-existe")
	assert.ErrorIs(t, err, ErrNotFound)

	// Revogado deixa de abrir.
	require.NoError(t, svc.Repo().RevokeRecipient(ctx, rc.ID, seed.AdminID))
	_, err = svc.Resolve(ctx, rc.Token)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Service_OpenAssetFallbackContentTypeAndErrors(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	store := &covZEmptyCTStore{}
	svc, _, rc := covZPublished(t, ctx, pool, seed, &recordingMailer{}, store)

	// Storage sem content-type: o servico usa o do tipo pedido.
	zip, err := svc.OpenBundle(ctx, rc.Token, seed.CampaignID)
	require.NoError(t, err)
	defer zip.Body.Close()
	assert.Equal(t, "application/zip", zip.ContentType)
	img, err := svc.OpenImage(ctx, rc.Token, seed.CampaignID, AssetInsights)
	require.NoError(t, err)
	defer img.Body.Close()
	assert.Equal(t, "image/png", img.ContentType)
	body, err := io.ReadAll(img.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("fake-insights-png"), body)

	// Campanha que nao pertence ao relatorio.
	_, err = svc.OpenBundle(ctx, rc.Token, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.OpenImage(ctx, rc.Token, uuid.New(), AssetMap)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.OpenImage(ctx, "token-invalido", seed.CampaignID, AssetMap)
	assert.ErrorIs(t, err, ErrNotFound)

	// Objeto sumiu do bucket: erro de abertura contextualizado (nao "nao encontrado").
	store.objects = map[string][]byte{}
	_, err = svc.OpenBundle(ctx, rc.Token, seed.CampaignID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postsale: abrir")
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Service_PreviewFrozenVsDraft(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	_, err := svc.Preview(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	rep := createDraftWithBlock(t, ctx, svc, seed)
	draftView, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, draftView.Campaigns, 1)
	assert.False(t, draftView.Campaigns[0].HasBundle, "no preview o zip ainda nao existe")

	// Publicado: o preview devolve o CONGELADO (com bundle), nao recalcula.
	seedClientUser(t, ctx, pool, seed.ClientID, "cliente-prev@example.com", true)
	require.NoError(t, svc.SetAssets(ctx, rep.ID, seed.CampaignID, capture()))
	_, err = svc.Publish(ctx, rep.ID)
	require.NoError(t, err)

	frozen, err := svc.Preview(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, frozen.Campaigns, 1)
	assert.True(t, frozen.Campaigns[0].HasBundle)

	// Corrompe o JSON congelado: o preview acusa em vez de devolver lixo.
	_, err = pool.Exec(ctx, `UPDATE post_sale_reports SET payload_json = '"nao-e-payload"'::jsonb WHERE id=$1`, rep.ID)
	require.NoError(t, err)
	_, err = svc.Preview(ctx, rep.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload congelado ilegível")
}

func TestCovZ_Service_Resend(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	mail := &recordingMailer{}
	svc, rep, rc := covZPublished(t, ctx, pool, seed, mail, &fakeStore{})
	require.Equal(t, []string{"cliente-covz@example.com"}, mail.sent)

	// Reenvio usa o MESMO token e manda outro email.
	require.NoError(t, svc.Resend(ctx, rep.ID, rc.ID))
	assert.Equal(t, []string{"cliente-covz@example.com", "cliente-covz@example.com"}, mail.sent)
	recs, err := svc.Repo().Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, rc.Token, recs[0].Token)
	assert.Equal(t, "sent", recs[0].EmailStatus)

	// Falha de SMTP no reenvio e gravada no destinatario, nao devolvida como erro.
	mail.fail = map[string]bool{"cliente-covz@example.com": true}
	require.NoError(t, svc.Resend(ctx, rep.ID, rc.ID))
	recs, err = svc.Repo().Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", recs[0].EmailStatus)
	require.NotNil(t, recs[0].EmailError)
	assert.Contains(t, *recs[0].EmailError, "smtp: recusado")

	// Destinatario inexistente / relatorio inexistente.
	assert.ErrorIs(t, svc.Resend(ctx, rep.ID, uuid.New()), ErrNotFound)
	assert.ErrorIs(t, svc.Resend(ctx, uuid.New(), rc.ID), ErrNotFound)

	// Revogado nao ressuscita.
	mail.fail = nil
	before := len(mail.sent)
	require.NoError(t, svc.Repo().RevokeRecipient(ctx, rc.ID, seed.AdminID))
	assert.ErrorIs(t, svc.Resend(ctx, rep.ID, rc.ID), ErrNotFound)
	assert.Len(t, mail.sent, before, "nada foi enviado")

	// Rascunho nao tem o que reenviar.
	draft := createDraftWithBlock(t, ctx, svc, seed)
	assert.ErrorIs(t, svc.Resend(ctx, draft.ID, uuid.New()), ErrNotFound)
}

func TestCovZ_Service_ResendWithoutSMTPMarksDisabled(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc, rep, rc := covZPublished(t, ctx, pool, seed, nil, &fakeStore{})

	require.NoError(t, svc.Resend(ctx, rep.ID, rc.ID))
	recs, err := svc.Repo().Recipients(ctx, rep.ID)
	require.NoError(t, err)
	assert.Equal(t, "disabled", recs[0].EmailStatus, "sem SMTP nunca marca 'sent'")
}

func TestCovZ_Service_PublishGuards(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	svc := newTestService(t, pool, &recordingMailer{}, &fakeStore{})

	_, err := svc.Publish(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	// Sem campanha nenhuma nao ha o que publicar.
	empty, err := svc.Repo().CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "Vazio"})
	require.NoError(t, err)
	_, err = svc.Publish(ctx, empty.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sem campanhas")
}
