package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Testes da fatia 3 — `user.password_changed` (§9.4 / decisão D8).

func TestHubSync_Senha_TrocaParaQuemEVinculado(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"hu-1","email":"x@y.com","password":"senha-nova-123"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.NotNil(t, r.ExternalID)
	require.Equal(t, id.String(), *r.ExternalID)

	// O que prova a troca não é o 200: é o hash do banco aceitar a senha nova.
	var hash string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&hash))
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("senha-nova-123")))
}

func TestHubSync_Senha_NaoGuardaOClaro(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"hu-1","password":"senha-em-claro-999"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	// §9.4, mitigação 2: a senha não aparece em log, erro, telemetria — nem na
	// resposta. O que fica no banco é o hash, e hash não é a senha.
	require.NotContains(t, rec.Body.String(), "senha-em-claro-999")
	var hash string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&hash))
	require.NotContains(t, hash, "senha-em-claro-999")
}

func TestHubSync_Senha_UsaCusto10ComoORestoDoSistema(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	id := criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"hu-1","password":"senha-nova-123"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var hash string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&hash))
	custo, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	// Divergir do custo usado em me.go, users.go e no JIT criaria contas com
	// força de hash diferente conforme o caminho pelo qual a senha foi definida
	// — e ninguém saberia disso olhando a tabela.
	require.Equal(t, 10, custo)
}

func TestHubSync_Senha_NaoTrocaDeContaLocalNaoVinculada(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	id := criaUsuario(t, ctx, pool, nil) // sem hub_id
	var antes string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&antes))
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"hu-qualquer","password":"senha-nova-123"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	// Coexistência (D5): o login local de quem nunca veio do hub é dele. O hub
	// não troca a senha de conta que não gerencia.
	require.Equal(t, http.StatusOK, rec.Code)
	var depois string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&depois))
	require.Equal(t, antes, depois)
}

func TestHubSync_Senha_HubIdInexistenteNaoEErro(t *testing.T) {
	_, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"ninguem","password":"senha-nova-123"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	// A pessoa ainda não clicou; o JIT vai criá-la com senha aleatória no
	// primeiro acesso. Não há o que trocar, e 500 mandaria o evento para a DLQ
	// por um caso normal.
	require.Equal(t, http.StatusOK, rec.Code)
	var r syncResposta
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.Nil(t, r.ExternalID)
}

func TestHubSync_Senha_SemSenhaE400(t *testing.T) {
	_, pool := poolDeTeste(t)
	h := NewHubSyncHandler(pool, hubConfigurado())

	corpo := `{"eventId":"e1","event":"user.password_changed","data":{"hubUserId":"hu-1"}}`
	rec, req := pedidoSync(corpo, chaveDeTeste)
	h.Receive(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	// A mensagem não diz que o que faltou foi a SENHA — "missing_password" num
	// log de acesso já é mais do que se precisa saber sobre a requisição.
	require.NotContains(t, rec.Body.String(), "password")
}

func TestHubSync_Senha_SemChaveE401(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	corpo := `{"eventId":"e1","event":"user.password_changed","data":{` +
		`"hubUserId":"hu-1","password":"x"}}`
	rec, req := pedidoSync(corpo, "pk_errada")
	h.Receive(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
