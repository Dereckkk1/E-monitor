package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Testes da fatia 2 — `client.upsert` e `user.upsert`. Os helpers
// (poolDeTeste, criaUsuario, pedidoSync, hubConfigurado) vivem em hubsync_test.go.

// ── client.upsert ───────────────────────────────────────────────────────────

// `criaClienteCom` e nao `criaCliente`: o hubsso_test.go ja define um helper com
// esse nome no MESMO pacote, e com outra assinatura (sem nome nem CNPJ). Renomear
// o meu custa nada; mexer no dele mudaria testes de SSO que nao sao meus.
func criaClienteCom(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, cnpj *string, hubID *string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, cnpj, hub_id) VALUES ($1,$2,$3) RETURNING id`,
		nome, cnpj, hubID).Scan(&id))
	return id
}

func TestHubSync_ClientUpsert_CriaQuandoNaoExiste(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"client.upsert","data":{` +
		`"hubClientId":"hc-1","name":"ACME","cnpj":"11.111.111/0001-11","city":"SP","active":true}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.NotNil(t, r.ExternalID)

	// É este registro que destrava o `client_not_provisioned` do §8.1 — o erro
	// que hoje barra todo usuário cuja empresa não tem hub_id carimbado.
	var nome, cidade string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name, city FROM clients WHERE hub_id = 'hc-1'`).Scan(&nome, &cidade))
	require.Equal(t, "ACME", nome)
	require.Equal(t, "SP", cidade)
}

func TestHubSync_ClientUpsert_AtualizaPeloHubId(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hc-1"
	id := criaClienteCom(t, ctx, pool, "Nome Velho", nil, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"client.upsert","data":{"hubClientId":"hc-1","name":"Nome Novo"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var nome string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM clients WHERE id=$1`, id).Scan(&nome))
	require.Equal(t, "Nome Novo", nome)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients`).Scan(&n))
	require.Equal(t, 1, n) // atualizou, não duplicou
}

func TestHubSync_ClientUpsert_CarimbaPeloCNPJ(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	cnpj := "22.222.222/0001-22"
	id := criaClienteCom(t, ctx, pool, "ACME Local", &cnpj, nil) // sem hub_id
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"client.upsert","data":{` +
		`"hubClientId":"hc-9","name":"ACME","cnpj":"22.222.222/0001-22"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Vinculou o que já existia em vez de criar um segundo — é o pior caso que a
	// §9.5 descreve: dois registros vivos para a mesma empresa, divergindo.
	var hub *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id=$1`, id).Scan(&hub))
	require.NotNil(t, hub)
	require.Equal(t, "hc-9", *hub)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients`).Scan(&n))
	require.Equal(t, 1, n)
}

func TestHubSync_ClientUpsert_NaoCasaPorNome(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	criaClienteCom(t, ctx, pool, "ACME", nil, nil) // mesmo nome, sem CNPJ, sem hub_id
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"client.upsert","data":{"hubClientId":"hc-1","name":"ACME"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Nome é rótulo, CNPJ é identidade. Casar por nome repetiria o defeito que a
	// §9.5 registrou no importador: cliente renomeado virava cliente novo, em
	// silêncio. Aqui prefere-se um registro a mais, visível, a um vínculo errado.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestHubSync_ClientUpsert_SemNomeE400(t *testing.T) {
	_, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())
	corpo := `{"eventId":"e1","event":"client.upsert","data":{"hubClientId":"hc-1"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── user.upsert ─────────────────────────────────────────────────────────────

func TestHubSync_UserUpsert_AtualizaPeloHubId(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.upsert","data":{` +
		`"hubUserId":"hu-1","email":"x@y.com","name":"Nome Novo","active":true}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var nome string
	require.NoError(t, pool.QueryRow(ctx, `SELECT name FROM users WHERE id=$1`, id).Scan(&nome))
	require.Equal(t, "Nome Novo", nome)
}

func TestHubSync_UserUpsert_ReativaComActiveTrue(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	id := criaUsuario(t, ctx, pool, &hubID)
	_, err := pool.Exec(ctx, `UPDATE users SET is_active=FALSE WHERE id=$1`, id)
	require.NoError(t, err)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.upsert","data":{` +
		`"hubUserId":"hu-1","email":"x@y.com","active":true}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Reativação chega como upsert com active:true, não como evento próprio (§9.3).
	require.True(t, ativoNoBanco(t, ctx, pool, id))
}

func TestHubSync_UserUpsert_VinculaPeloEmailEmVezDeDuplicar(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	id := criaUsuario(t, ctx, pool, nil) // conta local, sem hub_id
	var email string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, id).Scan(&email))
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.upsert","data":{` +
		`"hubUserId":"hu-7","email":"` + email + `","name":"Vinculada"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Mesma escada do acharOuCriar do SSO: quem já tem conta local ganha o
	// vínculo em vez de uma segunda conta.
	var hub *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM users WHERE id=$1`, id).Scan(&hub))
	require.NotNil(t, hub)
	require.Equal(t, "hu-7", *hub)
}

func TestHubSync_UserUpsert_NaoCriaContaNova(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.upsert","data":{` +
		`"hubUserId":"hu-1","email":"ninguem@aqui.com","name":"X"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	// Decisão consciente da fatia 2: o upsert NÃO cria conta. Ela nasce no
	// primeiro clique (JIT), e pré-criar povoaria as telas de operação com gente
	// que talvez nunca apareça. Acrescentar a criação depois é uma linha; apagar
	// contas criadas por engano em produção não é.
	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.Nil(t, r.ExternalID)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n))
	require.Equal(t, 0, n)
}

func TestHubSync_UserUpsert_NaoRoubaVinculoDeOutroHubId(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	outro := "hu-OUTRO"
	id := criaUsuario(t, ctx, pool, &outro)
	var email string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, id).Scan(&email))
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.upsert","data":{` +
		`"hubUserId":"hu-NOVO","email":"` + email + `"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// O degrau do e-mail exige `hub_id IS NULL`. Sem essa guarda, um evento
	// reapontaria para outra pessoa a identidade de uma conta já vinculada.
	var hub *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM users WHERE id=$1`, id).Scan(&hub))
	require.NotNil(t, hub)
	require.Equal(t, "hu-OUTRO", *hub)
}
