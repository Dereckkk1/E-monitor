package postsale

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRepo_DraftLifecycle(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{
		ClientID: seed.ClientID,
		Title:    "Pós-venda · Teste",
	})
	require.NoError(t, err)
	require.Equal(t, "draft", rep.Status)
	require.Nil(t, rep.SentAt)

	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID,
		From:       date(2026, 6, 1),
		To:         date(2026, 6, 30),
	}}))

	loaded, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Blocks, 1)
	require.Equal(t, seed.CampaignID, loaded.Blocks[0].CampaignID)
	require.Equal(t, seed.ClientID, loaded.ClientID)
	require.NotEmpty(t, loaded.ClientName)

	// Trocar os blocos substitui, não acumula: o passo 2 do wizard é uma
	// seleção completa, então desmarcar uma campanha precisa removê-la.
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []BlockRow{{
		CampaignID: seed.CampaignID,
		From:       date(2026, 6, 5),
		To:         date(2026, 6, 20),
	}}))
	loaded, err = repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Blocks, 1)
	require.Equal(t, "2026-06-05", loaded.Blocks[0].From.Format("2006-01-02"))

	// A listagem enxerga o draft com as contagens zeradas de envio. Recorte por
	// cliente pra não depender do que outros testes deixaram no banco.
	page, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, PerPage: 100})
	require.NoError(t, err)
	var found bool
	for _, it := range page.Items {
		if it.ID == rep.ID {
			found = true
			require.Equal(t, 1, it.Campaigns)
			require.Equal(t, 0, it.Recipients)
			require.Equal(t, 0, it.Opened)
			require.NotNil(t, it.PeriodFrom, "período coberto vem dos blocos")
			require.Equal(t, "2026-06-05", it.PeriodFrom.Format("2006-01-02"))
			require.Equal(t, "2026-06-20", it.PeriodTo.Format("2006-01-02"))
		}
	}
	require.True(t, found, "draft não apareceu na listagem")
	require.Equal(t, page.Counts.All, page.Total, "sem filtro de estado, total = todos")
	require.GreaterOrEqual(t, page.Counts.Draft, 1)

	// Competência: o mês do período acha; um mês fora dele, não.
	hit, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Month: "2026-06"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, hit.Total, 1, "junho intersecta 05/06–20/06")

	miss, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, Month: "2026-09"})
	require.NoError(t, err)
	require.Zero(t, miss.Total, "setembro não intersecta o período do bloco")

	// Paginação: página 1 respeita o teto, página além do fim vem vazia.
	first, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, PerPage: 1, Page: 1})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)

	beyond, err := repo.List(ctx, ListFilter{ClientID: &seed.ClientID, PerPage: 1, Page: 999})
	require.NoError(t, err)
	require.Empty(t, beyond.Items)
	require.Equal(t, first.Total, beyond.Total, "total não muda com a página")
}

func TestRepo_ResolveToken(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "T"})
	require.NoError(t, err)

	recs, err := repo.CreateRecipients(ctx, rep.ID, []RecipientInput{
		{Email: "cliente@empresa.com", Name: "Cliente", Token: "tok-" + rep.ID.String()},
	})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	tok := recs[0].Token

	// Draft não resolve: o link só vale depois do publish.
	_, err = repo.ResolveToken(ctx, tok)
	require.ErrorIs(t, err, ErrNotFound, "draft não pode resolver")

	require.NoError(t, repo.MarkSent(ctx, rep.ID, []byte(`{"version":1}`)))

	res, err := repo.ResolveToken(ctx, tok)
	require.NoError(t, err)
	require.JSONEq(t, `{"version":1}`, string(res.Payload))
	require.Equal(t, 0, res.OpenCount)

	// Publicar de novo é 409, não um segundo envio silencioso.
	require.ErrorIs(t, repo.MarkSent(ctx, rep.ID, []byte(`{"version":1}`)), ErrAlreadySent)

	// Abertura é contabilizada.
	require.NoError(t, repo.TouchOpen(ctx, res.RecipientID))
	again, err := repo.ResolveToken(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, 1, again.OpenCount)

	// Revogado some pra sempre — e é indistinguível de token inexistente.
	require.NoError(t, repo.RevokeRecipient(ctx, res.RecipientID, seed.AdminID))
	_, err = repo.ResolveToken(ctx, tok)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.ResolveToken(ctx, "nao-existe")
	require.ErrorIs(t, err, ErrNotFound)

	// Revogar duas vezes não é erro.
	require.NoError(t, repo.RevokeRecipient(ctx, res.RecipientID, seed.AdminID))
}

// Depois de enviado, o conteúdo não muda mais: o texto que o cliente leu é o
// texto que fica.
func TestRepo_UpdateContent_SoEmDraft(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)
	repo := NewRepo(pool)

	rep, err := repo.CreateDraft(ctx, CreateDraftInput{ClientID: seed.ClientID, Title: "Antes"})
	require.NoError(t, err)
	require.NoError(t, repo.UpdateContent(ctx, rep.ID, "Depois", "oi",
		"  https://drive.google.com/drive/folders/abc  "))

	loaded, err := repo.Get(ctx, rep.ID)
	require.NoError(t, err)
	require.Equal(t, "Depois", loaded.Title)
	require.Equal(t, "https://drive.google.com/drive/folders/abc", loaded.AttachmentsURL,
		"link dos anexos grava sem espaço nas pontas")

	require.NoError(t, repo.MarkSent(ctx, rep.ID, []byte(`{}`)))
	require.ErrorIs(t, repo.UpdateContent(ctx, rep.ID, "Tarde demais", "x", ""), ErrNotFound)
}

func TestRepo_ActiveClientUsers_SoAtivos(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	seedClientUser(t, ctx, pool, seed.ClientID, "ativo@empresa.com", true)
	seedClientUser(t, ctx, pool, seed.ClientID, "inativo@empresa.com", false)

	people, err := NewRepo(pool).ActiveClientUsers(ctx, seed.ClientID)
	require.NoError(t, err)
	require.Len(t, people, 1)
	require.Equal(t, "ativo@empresa.com", people[0].Email)
	require.NotNil(t, people[0].UserID)
}

// A cópia interna é do TIME: só admin/operator ativo, não excluído e com o
// opt-in ligado. As asserções são por pertinência (e não por tamanho da lista)
// porque a query é global — qualquer admin do banco de teste entraria na conta.
func TestRepo_InternalRecipients_SoAtivosComFlag(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	quer := seedInternalAdmin(t, ctx, pool, "pv-quer@hubradios.com", true, true)
	seedInternalAdmin(t, ctx, pool, "pv-naoquer@hubradios.com", true, false)
	seedInternalAdmin(t, ctx, pool, "pv-inativo@hubradios.com", false, true)
	excluido := seedInternalAdmin(t, ctx, pool, "pv-excluido@hubradios.com", true, true)
	_, err := pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, excluido)
	require.NoError(t, err)

	// Usuário do cliente com a flag ligada NÃO vira destinatário interno: ele já
	// recebe pelo próprio cliente, e a flag é um controle do time.
	viewer := seedClientUser(t, ctx, pool, seed.ClientID, "pv-viewer@empresa.com", true)
	_, err = pool.Exec(ctx,
		`UPDATE users SET receive_post_sale_emails = TRUE WHERE id = $1`, viewer)
	require.NoError(t, err)

	people, err := NewRepo(pool).InternalRecipients(ctx)
	require.NoError(t, err)

	byEmail := map[string]RecipientInput{}
	for _, p := range people {
		byEmail[p.Email] = p
	}
	require.Contains(t, byEmail, "pv-quer@hubradios.com")
	require.NotNil(t, byEmail["pv-quer@hubradios.com"].UserID)
	require.Equal(t, quer, *byEmail["pv-quer@hubradios.com"].UserID)

	for _, fora := range []string{
		"pv-naoquer@hubradios.com",  // opt-in desligado
		"pv-inativo@hubradios.com",  // conta desativada
		"pv-excluido@hubradios.com", // excluído
		"pv-viewer@empresa.com",     // não é do time
	} {
		require.NotContains(t, byEmail, fora)
	}
}

// Usuário de agência (carteira multi-cliente) tem que receber o pós-venda de
// TODO cliente da carteira, não só do principal.
//
// Sem isso a feature multi-cliente vira regressão: hoje uma agência atendida
// por 5 logins recebe 5 pós-vendas; consolidada num login só, receberia 1.
// Silenciosamente — o preview do wizard lê a mesma query.
func TestRepo_ActiveClientUsers_IncluiCarteiraSecundaria(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	// Segundo cliente, do qual a agência é secundária.
	var outro uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('Cliente Secundário') RETURNING id`).Scan(&outro))
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", outro) }) //nolint:errcheck

	agencia := seedClientUser(t, ctx, pool, seed.ClientID, "agencia@empresa.com", true)
	_, err := pool.Exec(ctx,
		`INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2)`, agencia, outro)
	require.NoError(t, err)

	people, err := NewRepo(pool).ActiveClientUsers(ctx, outro)
	require.NoError(t, err)
	require.Len(t, people, 1, "agência tem que receber o pós-venda do cliente secundário")
	require.Equal(t, "agencia@empresa.com", people[0].Email)
}

// A disjunção com InternalRecipients não pode depender de client_id: ao passar
// a ler user_clients, um admin com linha na tabela viraria destinatário DUAS
// vezes. O filtro por role é o que mantém os conjuntos separados.
func TestRepo_ActiveClientUsers_IgnoraAdminComVinculo(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	admin := seedInternalAdmin(t, ctx, pool, "pv-admin-vinculado@hubradios.com", true, true)
	// Estado que nenhum handler cria, mas que o repo não impede.
	_, err := pool.Exec(ctx,
		`INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2)`, admin, seed.ClientID)
	require.NoError(t, err)

	people, err := NewRepo(pool).ActiveClientUsers(ctx, seed.ClientID)
	require.NoError(t, err)
	for _, p := range people {
		require.NotEqual(t, "pv-admin-vinculado@hubradios.com", p.Email,
			"admin não é destinatário de cliente — receberia duplicado")
	}
}
