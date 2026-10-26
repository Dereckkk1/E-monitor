package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

var autorAna = Autor{Email: " Ana@T.local ", Nome: "Ana Paula Ávila"}

// hubFalso responde uma rota só e guarda o que recebeu.
type hubFalso struct {
	srv     *httptest.Server
	pedidos []*http.Request
	corpos  []string
}

func novoHubFalso(t *testing.T, responde func(w http.ResponseWriter, r *http.Request)) *hubFalso {
	t.Helper()
	h := &hubFalso{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.pedidos = append(h.pedidos, r)
		h.corpos = append(h.corpos, string(b))
		responde(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func jsonDe(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestPorta_LevaChaveEAutorNosHeaders(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 200, map[string]any{"naoLidas": 3})
	})
	c := New(h.srv.URL, "chave-x")

	n, err := c.SugestoesNaoLidas(context.Background(), autorAna)

	require.NoError(t, err)
	require.Equal(t, 3, n)
	r := h.pedidos[0]
	require.Equal(t, "/api/platform/sugestoes/nao-lidas", r.URL.Path)
	require.Equal(t, "chave-x", r.Header.Get("X-Hub-Platform-Key"))
	require.Equal(t, "ana@t.local", r.Header.Get("X-Hub-Autor-Email"))
	// encodeURIComponent do lado de lá: espaço vira %20, NUNCA "+".
	require.Equal(t, "Ana%20Paula%20%C3%81vila", r.Header.Get("X-Hub-Autor-Nome"))
}

func TestPorta_ErroDoHubViraErroComAMensagemDeLa(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 422, map[string]any{"error": map[string]string{"code": "campo_invalido", "message": "O título passa de 180 caracteres"}})
	})
	_, err := New(h.srv.URL, "k").CriarSugestao(context.Background(), autorAna, NovaSugestaoHub{ChaveEnvio: "chave-123456"})

	e, ok := AsError(err)
	require.True(t, ok)
	require.Equal(t, 422, e.Status)
	require.Equal(t, "O título passa de 180 caracteres", e.Message)
}

func TestPorta_ChaveRecusadaVira503ComFraseDeSuporte(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 401, map[string]any{"error": map[string]string{"code": "invalid_platform_key", "message": "chave de plataforma inválida"}})
	})
	_, err := New(h.srv.URL, "k").SugestoesNaoLidas(context.Background(), autorAna)

	e, _ := AsError(err)
	require.Equal(t, http.StatusServiceUnavailable, e.Status)
	require.Contains(t, e.Message, "chave do hub foi recusada")
}

func TestPorta_HubForaDoArNaoViraSucesso(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {})
	url := h.srv.URL
	h.srv.Close()

	_, err := New(url, "k").DetalheSugestao(context.Background(), autorAna, "6650aaaaaaaaaaaaaaaaaaaa")

	e, ok := AsError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, e.Status)
	require.Contains(t, e.Message, "tente de novo")
}

func TestPorta_200SemOCampoEsperadoEErro(t *testing.T) {
	// Zero não é ausência: um 200 sem `naoLidas` (envelope novo, proxy, SPA)
	// não pode apagar o badge.
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) { jsonDe(w, 200, map[string]any{}) })
	_, err := New(h.srv.URL, "k").SugestoesNaoLidas(context.Background(), autorAna)
	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
}

func TestPorta_SemConfiguracaoNaoSaiDeCasa(t *testing.T) {
	_, err := New("", "").SugestoesNaoLidas(context.Background(), autorAna)
	require.ErrorIs(t, err, ErrNotConfigured)
}
