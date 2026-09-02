package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Fakes, sem banco: o middleware é lógica de autenticação, e amarrá-lo a um
// pgxpool faria o teste PULAR sem TEST_DATABASE_URL — que é exatamente como um
// teste de segurança some sem ninguém notar.
type chaveFake struct{ boa string }

func (c chaveFake) ChaveConfere(apresentada string) bool {
	return c.boa != "" && apresentada == c.boa
}

type resolverFake struct {
	porHub map[string]uuid.UUID
	erro   error
	visto  []string
}

func (r *resolverFake) LocalPorHubID(_ context.Context, hubClientID string) (uuid.UUID, error) {
	r.visto = append(r.visto, hubClientID)
	if r.erro != nil {
		return uuid.Nil, r.erro
	}
	id, ok := r.porHub[hubClientID]
	if !ok {
		return uuid.Nil, ErrHubClientNaoLigado
	}
	return id, nil
}

func monta(t *testing.T, chave string, res *resolverFake) (http.Handler, *[]uuid.UUID) {
	t.Helper()
	var visto []uuid.UUID
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto = ClientScopesFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	return RequireHubKeyScoped(chaveFake{boa: chave}, res)(final), &visto
}

func req(hubKey, hubClient string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/hub/insights", nil)
	if hubKey != "" {
		r.Header.Set("X-Hub-Platform-Key", hubKey)
	}
	if hubClient != "" {
		r.Header.Set("X-Hub-Client-Id", hubClient)
	}
	return r
}

func TestHubKey_InjetaEscopoDoClienteResolvido(t *testing.T) {
	local := uuid.New()
	res := &resolverFake{porHub: map[string]uuid.UUID{"hub-abc": local}}
	h, visto := monta(t, "chave-boa", res)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("chave-boa", "hub-abc"))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []uuid.UUID{local}, *visto,
		"o handler tem que enxergar exatamente um cliente — o resolvido")
	require.Equal(t, []string{"hub-abc"}, res.visto,
		"o resolver recebe o id DO HUB, não o local")
}

func TestHubKey_ChaveErradaNaoChegaNoResolver(t *testing.T) {
	res := &resolverFake{porHub: map[string]uuid.UUID{"hub-abc": uuid.New()}}
	h, _ := monta(t, "chave-boa", res)

	for _, chave := range []string{"", "chave-errada"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req(chave, "hub-abc"))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.Contains(t, rec.Body.String(), "invalid_platform_key")
	}
	// A ordem é o ponto: conferir a chave ANTES de tocar o banco é o que
	// impede um não-autenticado de descobrir quais ids existem pelo tempo de
	// resposta. Mesma ordem de HubSync.Receive.
	require.Empty(t, res.visto, "o resolver não pode ser chamado sem chave válida")
}

func TestHubKey_SemHeaderDeCliente(t *testing.T) {
	res := &resolverFake{porHub: map[string]uuid.UUID{}}
	h, _ := monta(t, "chave-boa", res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("chave-boa", ""))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "missing_hub_client_id")
	require.Empty(t, res.visto)
}

func TestHubKey_ClienteNaoLigado(t *testing.T) {
	res := &resolverFake{porHub: map[string]uuid.UUID{}}
	h, visto := monta(t, "chave-boa", res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("chave-boa", "hub-desconhecido"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "client_not_linked")
	require.Nil(t, *visto, "o handler não pode ter rodado")
}

func TestHubKey_ErroDoBancoNaoViraAcessoTotal(t *testing.T) {
	// MUTAÇÃO guardada: se alguém trocar o `return` por um `next.ServeHTTP` no
	// caminho de erro, o handler roda com scopes nil — que o sistema trata como
	// "sem escopo = vê tudo". É a BOLA do audit de 2026-07-21.
	res := &resolverFake{erro: errors.New("connection refused")}
	h, visto := monta(t, "chave-boa", res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("chave-boa", "hub-abc"))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Nil(t, *visto)
}

func TestHubKey_ClaimsSaoDeViewerComUmClienteSo(t *testing.T) {
	// ScopeAllows é o que os handlers usam para o 404 anti-oráculo. Este teste
	// prova que ele diz "não" para qualquer cliente que não seja o resolvido.
	local := uuid.New()
	outro := uuid.New()
	res := &resolverFake{porHub: map[string]uuid.UUID{"hub-abc": local}}
	var permitiuProprio, permitiuAlheio, permitiuNil bool
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		permitiuProprio = ScopeAllows(r.Context(), local)
		permitiuAlheio = ScopeAllows(r.Context(), outro)
		permitiuNil = ScopeAllows(r.Context(), uuid.Nil)
	})
	RequireHubKeyScoped(chaveFake{boa: "chave-boa"}, res)(final).
		ServeHTTP(httptest.NewRecorder(), req("chave-boa", "hub-abc"))

	require.True(t, permitiuProprio)
	require.False(t, permitiuAlheio)
	require.False(t, permitiuNil)
}
