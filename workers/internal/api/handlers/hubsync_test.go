package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
	"radiocheck/internal/hub"
)

const chaveDeTeste = "pk_chave_desta_plataforma"

func hubConfigurado() *hub.Client {
	return hub.New("https://api-clientes.emidiastec.com.br", chaveDeTeste)
}

func pedidoSync(corpo, chave string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/hub/sync", strings.NewReader(corpo))
	if chave != "" {
		req.Header.Set("X-Hub-Platform-Key", chave)
	}
	return httptest.NewRecorder(), req
}

// ── Sem DB: a porta ──────────────────────────────────────────────────────

func TestHubSync_SemChave(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`{"eventId":"1","event":"user.deactivate","data":{}}`, "")
	h.Receive(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHubSync_ChaveErrada(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`{"eventId":"1","event":"user.deactivate","data":{}}`, "pk_outra")
	h.Receive(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// A chave é conferida ANTES do corpo: responder 400 a quem não se autenticou
// contaria a quem sonda que o endpoint existe e o que ele espera.
func TestHubSync_CorpoInvalidoSemChaveAindaE401(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`isto nao e json`, "pk_outra")
	h.Receive(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHubSync_IntegracaoDesligadaRecusa(t *testing.T) {
	// hub.New("","") => Configured() falso. Sem chave configurada não há como
	// autenticar ninguém, e aceitar seria pior que recusar.
	h := NewHubSyncHandler(nil, hub.New("", ""))
	rec, req := pedidoSync(`{"eventId":"1","event":"user.deactivate","data":{}}`, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHubSync_CorpoInvalidoComChaveE400(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`isto nao e json`, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHubSync_SemEventIdE400(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`{"event":"user.deactivate","data":{}}`, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Evento que esta versão não implementa é ACEITO. É o que permite o hub subir
// antes deste lado sem enterrar na DLQ eventos que não têm defeito nenhum.
//
// Este teste já quebrou DUAS vezes de propósito: o exemplo era `user.upsert` até
// a fatia 2, e `user.password_changed` até a fatia 3. Nas duas, implementar o
// evento fez o teste falhar — e isso é o mecanismo, não um acidente: implementar
// TEM de tirar o evento do ramo `default`, senão o hub marca como sincronizado
// algo que não foi.
//
// Os quatro eventos do §9.3 agora existem, então o exemplo passou a ser um nome
// que o RFC não define. Se um dia ele virar evento de verdade, este teste falha
// de novo — e será, de novo, a coisa certa acontecendo.
func TestHubSync_EventoDesconhecidoEAceito(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`{"eventId":"1","event":"user.inventado_no_futuro","data":{}}`, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.True(t, r.OK)
	require.Nil(t, r.ExternalID)
}

func TestHubSync_DeactivateSemHubUserIdE400(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoSync(`{"eventId":"1","event":"user.deactivate","data":{}}`, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Com Postgres real (regra 4.8: banco vazio dá falso verde) ────────────

func poolDeTeste(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	dbtest.GuardOrSkip(t, ctx, pool)
	_, err = pool.Exec(ctx, `TRUNCATE users, clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// `admin` com `client_id` NULO, e não `viewer`: o schema real tem o CHECK
// `users_client_role_consistency` (viewer EXIGE client_id; admin/operator exigem
// client_id nulo). A primeira versão deste helper criava um viewer órfão e só o
// Postgres de verdade reprovou — banco vazio ou mock teria aceitado, que é a
// regra 4.8 do CLAUDE.md cobrada na prática.
//
// O papel é indiferente para a desativação: o handler casa por `hub_id`.
func criaUsuario(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hubID *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, name, hub_id, is_active)
		 VALUES ($1, 'x', 'admin', 'Fulano', $2, TRUE) RETURNING id`,
		"u"+uuid.NewString()[:8]+"@teste.com", hubID).Scan(&id)
	require.NoError(t, err)
	return id
}

func ativoNoBanco(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var a bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1`, id).Scan(&a))
	return a
}

func TestHubSync_DesativaEDevolveOIdLocal(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hub-user-123"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoSync(
		`{"eventId":"e1","event":"user.deactivate","data":{"hubUserId":"hub-user-123","email":"u@teste.com"}}`,
		chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.True(t, r.OK)
	// O externalId é o que o hub grava em userPlatformIdentities (§5.6).
	require.NotNil(t, r.ExternalID)
	require.Equal(t, id.String(), *r.ExternalID)
	require.False(t, ativoNoBanco(t, ctx, pool, id))
}

func TestHubSync_RepetirEIdempotente(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hub-user-123"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())
	corpo := `{"eventId":"e1","event":"user.deactivate","data":{"hubUserId":"hub-user-123"}}`

	rec1, req1 := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec1, req1)
	rec2, req2 := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec2, req2)

	// O §9.2 aceita "ignorar repetido"; aqui a operação é idempotente por
	// natureza, então a segunda entrega responde igual à primeira.
	require.Equal(t, http.StatusOK, rec2.Code)
	require.JSONEq(t, rec1.Body.String(), rec2.Body.String())
	require.False(t, ativoNoBanco(t, ctx, pool, id))
}

func TestHubSync_HubIdInexistenteNaoEErro(t *testing.T) {
	_, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoSync(
		`{"eventId":"e1","event":"user.deactivate","data":{"hubUserId":"ninguem"}}`,
		chaveDeTeste)
	h.Receive(rec, req)

	// A pessoa nunca entrou por aqui (o provisionamento é JIT). Não há o que
	// desativar, e insistir não faria aparecer — 200 com externalId nulo encerra
	// o evento em vez de mandá-lo para oito tentativas e a DLQ.
	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.True(t, r.OK)
	require.Nil(t, r.ExternalID)
}

func TestHubSync_NaoCasaPorEmail(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	// Usuário local SEM hub_id, com o mesmo e-mail que vem no payload.
	id := criaUsuario(t, ctx, pool, nil)
	var email string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, id).Scan(&email))
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoSync(
		`{"eventId":"e1","event":"user.deactivate","data":{"hubUserId":"hub-x","email":"`+email+`"}}`,
		chaveDeTeste)
	h.Receive(rec, req)

	// Casar por e-mail desativaria a pessoa errada quando dois sistemas têm o
	// mesmo endereço em pessoas diferentes. O e-mail vem para diagnóstico.
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, ativoNoBanco(t, ctx, pool, id))
}

func TestUpsertCliente_CNPJComMascaraCasaComCNPJSemMascara(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	// O cliente que JA existe aqui, com o CNPJ em digitos puros e sem hub_id.
	var existente string
	if err := pool.QueryRow(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1,$2) RETURNING id`,
		"Acme", "12345678000190").Scan(&existente); err != nil {
		t.Fatalf("semear: %v", err)
	}

	// O hub manda o MESMO CNPJ, com mascara — e como um humano digitou la.
	rec, req := pedidoSync(`{"eventId":"e1","event":"client.upsert","data":{
		"hubClientId":"hub-1","name":"Acme","cnpj":"12.345.678/0001-90"}}`, chaveDeTeste)
	h.Receive(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// ⚠️ O defeito: hoje nasce um SEGUNDO cliente, e o hub_id vai para ele.
	if n != 1 {
		t.Fatalf("esperava 1 cliente, achei %d — a mascara criou duplicata", n)
	}

	var hubID *string
	if err := pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id=$1`, existente).Scan(&hubID); err != nil {
		t.Fatal(err)
	}
	if hubID == nil || *hubID != "hub-1" {
		t.Errorf("o hub_id nao foi carimbado no cliente que ja existia: %v", hubID)
	}
}

func TestUpsertCliente_CNPJSemMascaraCasaComCNPJComMascara(t *testing.T) {
	// O sentido INVERSO: o cadastro local e que tem mascara.
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	if _, err := pool.Exec(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1,$2)`, "Acme", "12.345.678/0001-90"); err != nil {
		t.Fatal(err)
	}

	rec, req := pedidoSync(`{"eventId":"e1","event":"client.upsert","data":{
		"hubClientId":"hub-1","name":"Acme","cnpj":"12345678000190"}}`, chaveDeTeste)
	h.Receive(rec, req)

	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n)
	if n != 1 {
		t.Fatalf("esperava 1 cliente, achei %d — a normalizacao so funciona num sentido", n)
	}
}

func TestUpsertCliente_CNPJDiferenteNaoJunta(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	if _, err := pool.Exec(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1,$2)`, "Acme", "12345678000190"); err != nil {
		t.Fatal(err)
	}

	rec, req := pedidoSync(`{"eventId":"e1","event":"client.upsert","data":{
		"hubClientId":"hub-1","name":"Outra","cnpj":"99.999.999/9999-99"}}`, chaveDeTeste)
	h.Receive(rec, req)

	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n)
	// ⚠️ Normalizar NAO pode passar a juntar quem e diferente. Este e o teste
	// que impede o conserto de virar um defeito pior que o original: juntar
	// cliente errado e a unica falha aqui que vaza dado de um para outro.
	if n != 2 {
		t.Fatalf("esperava 2 clientes, achei %d — a normalizacao juntou quem nao devia", n)
	}
}

func TestUpsertCliente_CNPJVazioNaoJuntaComNinguem(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	// Dois clientes locais SEM cnpj. Se a normalizacao tratar "" como valor,
	// eles casariam entre si e com qualquer evento sem cnpj.
	if _, err := pool.Exec(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1,NULL), ($2,'')`, "Um", "Dois"); err != nil {
		t.Fatal(err)
	}

	rec, req := pedidoSync(`{"eventId":"e1","event":"client.upsert","data":{
		"hubClientId":"hub-1","name":"Terceiro"}}`, chaveDeTeste)
	h.Receive(rec, req)

	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n)
	if n != 3 {
		t.Fatalf("esperava 3 clientes, achei %d — cnpj vazio virou chave de juncao", n)
	}
}

// TestUpsertCliente_ExistenteSemCNPJNaoCasaComEventoComCNPJ exercita o
// COALESCE(cnpj,”) do lado do SQL.
//
// O irmao acima (CNPJVazioNaoJuntaComNinguem) prova a guarda do lado GO: quando
// o EVENTO nao traz cnpj, a consulta nem roda. Este prova o outro sentido, que
// so o SQL defende: o evento TRAZ cnpj e quem esta no banco tem NULL. Sem o
// COALESCE, `regexp_replace(NULL, ...)` devolve NULL, a comparacao vira NULL
// (nem verdadeiro nem falso) e a linha simplesmente nao casa — que por sorte e
// o resultado certo. Mas e por sorte, e sorte nao se testa: um dia alguem troca
// o operador por `IS NOT DISTINCT FROM` e o NULL passa a casar com tudo.
func TestUpsertCliente_ExistenteSemCNPJNaoCasaComEventoComCNPJ(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	if _, err := pool.Exec(ctx,
		`INSERT INTO clients (name, cnpj) VALUES ($1, NULL)`, "Sem CNPJ"); err != nil {
		t.Fatal(err)
	}

	rec, req := pedidoSync(`{"eventId":"e1","event":"client.upsert","data":{
		"hubClientId":"hub-1","name":"Acme","cnpj":"12.345.678/0001-90"}}`, chaveDeTeste)
	h.Receive(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM clients`).Scan(&n)
	if n != 2 {
		t.Fatalf("esperava 2 clientes, achei %d — o NULL casou com um cnpj real", n)
	}

	// E o que ja estava la continua sem vinculo: o hub_id foi para o novo.
	var vinculado int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM clients WHERE name='Sem CNPJ' AND hub_id IS NULL`).Scan(&vinculado)
	if vinculado != 1 {
		t.Errorf("o cliente sem cnpj foi carimbado indevidamente")
	}
}
