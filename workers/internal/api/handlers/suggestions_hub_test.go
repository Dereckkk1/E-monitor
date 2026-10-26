package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

// ── apoio: sem Postgres, de propósito (o handler não toca no banco daqui) ──

type usuariosFalsos map[uuid.UUID]*users.User

func (u usuariosFalsos) Get(_ context.Context, id uuid.UUID) (*users.User, error) {
	if x, ok := u[id]; ok {
		return x, nil
	}
	return nil, pgx.ErrNoRows
}

var idAna = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")

type portaFalsa struct {
	srv     *httptest.Server
	pedidos []*http.Request
	corpos  []string
}

func novaPortaFalsa(t *testing.T, responde func(w http.ResponseWriter, r *http.Request)) *portaFalsa {
	t.Helper()
	p := &portaFalsa{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.pedidos = append(p.pedidos, r)
		p.corpos = append(p.corpos, string(b))
		responde(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func responde(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handlerComHub(p *portaFalsa) *SuggestionsHubHandler {
	return NewSuggestionsHubHandler(hub.New(p.srv.URL, "chave-x"),
		usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local", Name: "Ana Paula"}})
}

func comoAna(r *http.Request) *http.Request {
	return r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: idAna, Role: "admin"}))
}

func comParam(r *http.Request, nome, valor string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(nome, valor)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

const idHub = "6650aaaaaaaaaaaaaaaaaaaa"

var sugestaoDoHub = map[string]any{"sugestao": map[string]any{
	"id": idHub, "numero": 42, "origem": "e-monitor", "tipo": "ideia", "titulo": "T",
	"descricao": "D", "status": "nova", "urgenciaAutor": "alta",
	"autor": map[string]any{"id": "u1", "nome": "Ana Paula"},
	"notas": "segredo do gestor",
}}

// ── criar ──

func TestHubHandler_Create(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)
	corpo := `{"title":" T ","description":" D ","type":"feature","target_screen":"Campanhas",
		"requester_priority":"alta","attachment_ids":["an1"],"client_key":"chave-do-form-1"}`
	w := httptest.NewRecorder()
	h.Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(corpo))))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "ana@t.local", p.pedidos[0].Header.Get("X-Hub-Autor-Email"))
	require.Equal(t, "Ana%20Paula", p.pedidos[0].Header.Get("X-Hub-Autor-Nome"))
	require.JSONEq(t, `{"chaveEnvio":"chave-do-form-1","tipo":"ideia","titulo":"T","descricao":"D",
		"telaAlvo":"Campanhas","urgenciaAutor":"alta","anexoIds":["an1"]}`, p.corpos[0])
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Equal(t, "feature", out["type"])
	require.Equal(t, float64(42), out["ref_num"])
	require.NotContains(t, w.Body.String(), "segredo")
}

func TestHubHandler_Create_ChaveInvalidaGanhaUmaNova(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"T","description":"D","type":"bug","requester_priority":"baixa","client_key":"x y"}`))))
	require.Equal(t, http.StatusCreated, w.Code)
	var b map[string]any
	_ = json.Unmarshal([]byte(p.corpos[0]), &b)
	require.Regexp(t, `^[A-Za-z0-9_-]{8,64}$`, b["chaveEnvio"])
}

func TestHubHandler_Create_ValidaAntesDeIrAoHub(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"","description":"D","type":"xpto","requester_priority":"alta"}`))))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_Create_HubForaDoArNuncaVira201(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	url := p.srv.URL
	p.srv.Close()
	h := NewSuggestionsHubHandler(hub.New(url, "k"), usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local"}})
	w := httptest.NewRecorder()
	h.Create(w, comoAna(httptest.NewRequest("POST", "/suggestions", strings.NewReader(
		`{"title":"T","description":"D","type":"bug","requester_priority":"alta"}`))))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), `"error"`)
	require.Contains(t, w.Body.String(), "tente de novo")
}

func TestHubHandler_SemHubConfiguradoResponde503(t *testing.T) {
	h := NewSuggestionsHubHandler(hub.New("", ""), usuariosFalsos{idAna: {ID: idAna, Email: "ana@t.local"}})
	w := httptest.NewRecorder()
	h.List(w, comoAna(httptest.NewRequest("GET", "/suggestions", nil)))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── listar e detalhe ──

func TestHubHandler_List(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"pagina": 1, "paginas": 1, "itens": []map[string]any{
			{"id": idHub, "numero": 7, "origem": "plura", "tipo": "ideia", "titulo": "T", "previa": "P",
				"status": "retirada", "urgenciaAutor": "media", "comentarios": 2, "naoLida": true,
				"autor": map[string]any{"id": "u1", "nome": "Ana"}}}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, comoAna(httptest.NewRequest("GET", "/suggestions?status=retirada", nil)))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "retirada", p.pedidos[0].URL.Query().Get("status"))
	var out struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	require.Equal(t, float64(7), out.Data[0]["ref_num"])
	require.Equal(t, "feature", out.Data[0]["type"])
	require.Equal(t, "plura", out.Data[0]["origin"])
	require.Equal(t, true, out.Data[0]["unread"])
}

func TestHubHandler_List_StatusForaDoEnumNaoVai(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"pagina": 1, "paginas": 1, "itens": []any{}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, comoAna(httptest.NewRequest("GET", "/suggestions?status=%24ne", nil)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, p.pedidos[0].URL.Query().Get("status"))
	require.JSONEq(t, `{"data":[]}`, w.Body.String())
}

func TestHubHandler_Get(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/suggestions/"+idHub, nil)), "id", idHub))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "/api/platform/sugestoes/"+idHub, p.pedidos[0].URL.Path)
	require.NotContains(t, w.Body.String(), "segredo")
	require.NotContains(t, w.Body.String(), "dev_notes")
}

func TestHubHandler_Get_404DoHubPassa(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Sugestão não encontrada"}})
	})
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "id", idHub))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.JSONEq(t, `{"error":"Sugestão não encontrada"}`, w.Body.String())
}

func TestHubHandler_IdMalformadoNemSaiDeCasa(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
	w := httptest.NewRecorder()
	handlerComHub(p).Get(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "id", "../importar"))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_SemClaims401(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	w := httptest.NewRecorder()
	handlerComHub(p).List(w, httptest.NewRequest("GET", "/suggestions", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Empty(t, p.pedidos)
}
