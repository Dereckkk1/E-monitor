package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type linhaUsuario struct {
	ExternalID string  `json:"externalId"`
	Email      string  `json:"email"`
	Name       string  `json:"name"`
	Active     bool    `json:"active"`
	HubUserID  *string `json:"hubUserId"`
	Role       string  `json:"role"`
}

type respostaUsuarios struct {
	Users      []linhaUsuario `json:"users"`
	NextCursor *string        `json:"nextCursor"`
}

func pedidoUsuarios(query, chave string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/hub/users"+query, nil)
	if chave != "" {
		req.Header.Set("X-Hub-Platform-Key", chave)
	}
	return httptest.NewRecorder(), req
}

func TestHubUsers_SemChaveE401(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoUsuarios("", "")
	h.ListUsers(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHubUsers_ChaveErradaE401(t *testing.T) {
	h := NewHubSyncHandler(nil, hubConfigurado())
	rec, req := pedidoUsuarios("", "pk_outra")
	h.ListUsers(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHubUsers_ListaIdentidadeEVinculo(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	comVinculo := criaUsuario(t, ctx, pool, &hubID)
	semVinculo := criaUsuario(t, ctx, pool, nil)
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoUsuarios("", chaveDeTeste)
	h.ListUsers(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var r respostaUsuarios
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	require.Len(t, r.Users, 2)

	porID := map[string]linhaUsuario{}
	for _, u := range r.Users {
		porID[u.ExternalID] = u
	}
	// `hubUserId` nulo é informação, não ausência de dado: é ele que diz ao §9.6
	// quem ainda não está vinculado.
	require.NotNil(t, porID[comVinculo.String()].HubUserID)
	require.Equal(t, "hu-1", *porID[comVinculo.String()].HubUserID)
	require.Nil(t, porID[semVinculo.String()].HubUserID)
	require.True(t, porID[comVinculo.String()].Active)
}

func TestHubUsers_NaoDevolveAutorizacao(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	hubID := "hu-1"
	criaUsuario(t, ctx, pool, &hubID)
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoUsuarios("", chaveDeTeste)
	h.ListUsers(rec, req)

	// O escopo é estreito de propósito: identidade e vínculo, nada de
	// `client_id` nem permissões. Isso é do E-monitor e o hub nunca toca (D9).
	// Uma listagem generosa viraria, com o tempo, uma API de exportação de base
	// que ninguém decidiu criar.
	corpo := rec.Body.String()
	require.NotContains(t, corpo, "client_id")
	require.NotContains(t, corpo, "clientId")
	require.NotContains(t, corpo, "password")
}

func TestHubUsers_IgnoraApagados(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	id := criaUsuario(t, ctx, pool, nil)
	_, err := pool.Exec(ctx, `UPDATE users SET deleted_at = NOW() WHERE id=$1`, id)
	require.NoError(t, err)
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoUsuarios("", chaveDeTeste)
	h.ListUsers(rec, req)

	var r respostaUsuarios
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	// Conta apagada não é "sumiu da plataforma" nem "está lá": ela não existe.
	// Devolvê-la faria a reconciliação tentar corrigir um fantasma.
	require.Len(t, r.Users, 0)
}

func TestHubUsers_PaginaPorCursor(t *testing.T) {
	ctx, pool := poolDeTeste(t)
	for i := 0; i < 3; i++ {
		criaUsuario(t, ctx, pool, nil)
	}
	h := NewHubSyncHandler(pool, hubConfigurado())

	rec, req := pedidoUsuarios("?limit=2", chaveDeTeste)
	h.ListUsers(rec, req)
	var p1 respostaUsuarios
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p1))
	require.Len(t, p1.Users, 2)
	require.NotNil(t, p1.NextCursor)

	rec2, req2 := pedidoUsuarios("?limit=2&cursor="+*p1.NextCursor, chaveDeTeste)
	h.ListUsers(rec2, req2)
	var p2 respostaUsuarios
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &p2))
	require.Len(t, p2.Users, 1)
	// Sem próxima página: é isto que encerra o laço da reconciliação. Um cursor
	// que nunca fica nulo faria o job noturno girar para sempre.
	require.Nil(t, p2.NextCursor)

	// E nenhuma linha repetida entre as páginas — o motivo de paginar por `id`
	// e não por `created_at`.
	vistos := map[string]bool{}
	for _, u := range append(p1.Users, p2.Users...) {
		require.False(t, vistos[u.ExternalID], "linha repetida entre paginas")
		vistos[u.ExternalID] = true
	}
	require.Len(t, vistos, 3)
}
