package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestListarSugestoes_JuntaAsPaginas(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("pagina")
		n, _ := strconv.Atoi(p) // número no JSON, como o hub manda
		jsonDe(w, 200, map[string]any{
			"itens":  []map[string]any{{"id": "id-" + p, "numero": 1, "origem": "hub", "tipo": "ideia", "status": "nova"}},
			"pagina": n, "paginas": 2, "total": 2, "naoLidas": 0,
		})
	})
	l, err := New(h.srv.URL, "k").ListarSugestoes(context.Background(), autorAna, "concluida")

	require.NoError(t, err)
	require.Len(t, l, 2)
	require.Equal(t, "id-1", l[0].ID)
	require.Equal(t, "id-2", l[1].ID)
	require.Equal(t, "concluida", h.pedidos[0].URL.Query().Get("status"))
	require.Equal(t, "/api/platform/sugestoes", h.pedidos[0].URL.Path)
}

func TestCriarSugestao_MandaOCorpoDoContrato(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 201, map[string]any{"sugestao": map[string]any{"id": "6650aaaaaaaaaaaaaaaaaaaa", "numero": 42, "origem": "e-monitor"}})
	})
	tela := "Campanhas"
	s, err := New(h.srv.URL, "k").CriarSugestao(context.Background(), autorAna, NovaSugestaoHub{
		ChaveEnvio: "chave-123456", Tipo: "ideia", Titulo: "T", Descricao: "D",
		TelaAlvo: &tela, UrgenciaAutor: "alta", AnexoIDs: []string{"a1"},
	})

	require.NoError(t, err)
	require.Equal(t, 42, s.Numero)
	require.Equal(t, http.MethodPost, h.pedidos[0].Method)
	require.JSONEq(t, `{"chaveEnvio":"chave-123456","tipo":"ideia","titulo":"T","descricao":"D",
		"telaAlvo":"Campanhas","urgenciaAutor":"alta","anexoIds":["a1"]}`, h.corpos[0])
}

func TestDetalheSugestao_NaoDecodificaNotas(t *testing.T) {
	// Se o hub um dia vazar `notas`, este lado não tem onde guardá-las.
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 200, map[string]any{"sugestao": map[string]any{"id": "x1", "notas": "segredo do gestor"}})
	})
	s, err := New(h.srv.URL, "k").DetalheSugestao(context.Background(), autorAna, "x1")
	require.NoError(t, err)
	b, _ := json.Marshal(s)
	require.NotContains(t, string(b), "segredo")
}

func TestDetalheSugestao_IdComBarraNaoVazaDoCaminho(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) { jsonDe(w, 404, map[string]any{}) })
	_, err := New(h.srv.URL, "k").DetalheSugestao(context.Background(), autorAna, "../importar")
	e, _ := AsError(err)
	require.Equal(t, 404, e.Status)
	require.Equal(t, "/api/platform/sugestoes/..%2Fimportar", h.pedidos[0].URL.RawPath)
}

func TestComentarSugestao_E_Lida(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/platform/sugestoes/x1/lida" {
			jsonDe(w, 200, map[string]any{"naoLidas": 0})
			return
		}
		jsonDe(w, 201, map[string]any{"sugestao": map[string]any{"id": "x1"}})
	})
	c := New(h.srv.URL, "k")
	_, err := c.ComentarSugestao(context.Background(), autorAna, "x1", NovoComentarioHub{ChaveEnvio: "chave-abcdefgh", Corpo: "oi"})
	require.NoError(t, err)
	require.Equal(t, "/api/platform/sugestoes/x1/comentarios", h.pedidos[0].URL.Path)
	require.JSONEq(t, `{"chaveEnvio":"chave-abcdefgh","corpo":"oi"}`, h.corpos[0])

	n, err := c.MarcarSugestaoLida(context.Background(), autorAna, "x1")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestImportarSugestao_SoAceitaAsDuasAcoes(t *testing.T) {
	acao := "importada"
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/platform/sugestoes/importar", r.URL.Path)
		jsonDe(w, 200, map[string]any{"acao": acao, "sugestaoId": "s1", "numero": 77})
	})
	c := New(h.srv.URL, "k")
	r, err := c.ImportarSugestao(context.Background(), autorAna, SugestaoImportada{IDOriginal: "u1", NumeroOriginal: 17})
	require.NoError(t, err)
	require.Equal(t, 77, r.Numero)
	require.Contains(t, h.corpos[0], `"idOriginal":"u1"`)
	require.Contains(t, h.corpos[0], `"anexoIds":[]`)
	require.Contains(t, h.corpos[0], `"comentarios":[]`)

	acao = "ignorado" // 200 com ação desconhecida NÃO é sucesso
	_, err = c.ImportarSugestao(context.Background(), autorAna, SugestaoImportada{IDOriginal: "u2"})
	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
}
