package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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

func multipartComImagem(t *testing.T, mime string, dados []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="print.png"`}
	h["Content-Type"] = []string{mime}
	part, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, _ = part.Write(dados)
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

func TestHubHandler_AddComment(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)
	h.naoLidas.guarda("ana@t.local", 9)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":" oi ","attachment_ids":["an1"],"client_key":"fala-123456"}`))
	h.AddComment(w, comParam(comoAna(req), "id", idHub))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "/api/platform/sugestoes/"+idHub+"/comentarios", p.pedidos[0].URL.Path)
	require.JSONEq(t, `{"chaveEnvio":"fala-123456","corpo":"oi","anexoIds":["an1"]}`, p.corpos[0])
	_, ainda := h.naoLidas.pega("ana@t.local")
	require.False(t, ainda, "comentar esquece o cache do badge")
}

func TestHubHandler_AddComment_SoImagemVale_VazioNao(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)

	w := httptest.NewRecorder()
	h.AddComment(w, comParam(comoAna(httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":"  "}`))), "id", idHub))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Empty(t, p.pedidos)

	w = httptest.NewRecorder()
	h.AddComment(w, comParam(comoAna(httptest.NewRequest("POST", "/x", strings.NewReader(`{"body":"","attachment_ids":["an1"]}`))), "id", idHub))
	require.Equal(t, http.StatusCreated, w.Code)
}

// bucket + porta que respondem o fluxo de três passos.
func portaComBucket(t *testing.T) (*portaFalsa, *[]byte) {
	recebido := &[]byte{}
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			*recebido, _ = io.ReadAll(r.Body)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("BYTES-DO-GCS"))
	}))
	t.Cleanup(bucket.Close)
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/platform/sugestoes/anexos/upload-url":
			responde(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": bucket.URL + "/obj"})
		case strings.HasSuffix(r.URL.Path, "/confirm"):
			responde(w, 200, map[string]any{"anexo": map[string]any{"id": "an1"}})
		case strings.HasSuffix(r.URL.Path, "/arquivo"):
			http.Redirect(w, r, bucket.URL+"/obj?assinado=1", http.StatusFound)
		default:
			responde(w, 201, sugestaoDoHub)
		}
	})
	return p, recebido
}

func TestHubHandler_StageAttachment(t *testing.T) {
	p, recebido := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "image/png", []byte("PNG!"))
	req := httptest.NewRequest("POST", "/suggestions/attachments", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).StageAttachment(w, comoAna(req))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.JSONEq(t, `{"id":"an1"}`, w.Body.String())
	require.Equal(t, []byte("PNG!"), *recebido)
	require.JSONEq(t, `{"nome":"print.png","mimeType":"image/png","tamanho":4}`, p.corpos[0])
}

func TestHubHandler_StageAttachment_RecusaNaoImagem(t *testing.T) {
	p, _ := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "application/pdf", []byte("%PDF"))
	req := httptest.NewRequest("POST", "/suggestions/attachments", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).StageAttachment(w, comoAna(req))
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	require.Empty(t, p.pedidos)
}

func TestHubHandler_UploadAttachmentLegado_ViraFalaComAImagem(t *testing.T) {
	p, _ := portaComBucket(t)
	corpo, ct := multipartComImagem(t, "image/png", []byte("PNG!"))
	req := httptest.NewRequest("POST", "/x", corpo)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	handlerComHub(p).UploadAttachment(w, comParam(comoAna(req), "id", idHub))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	ultimo := p.pedidos[len(p.pedidos)-1]
	require.Equal(t, "/api/platform/sugestoes/"+idHub+"/comentarios", ultimo.URL.Path)
	require.Contains(t, p.corpos[len(p.corpos)-1], `"anexoIds":["an1"]`)
}

func TestHubHandler_ProxyAttachment(t *testing.T) {
	p, _ := portaComBucket(t)
	aid := "6650bbbbbbbbbbbbbbbbbbbb"
	w := httptest.NewRecorder()
	handlerComHub(p).ProxyAttachment(w, comParam(comoAna(httptest.NewRequest("GET", "/x", nil)), "aid", aid))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "BYTES-DO-GCS", w.Body.String())
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, `inline; filename="`+aid+`"`, w.Header().Get("Content-Disposition"))
	require.Equal(t, "private, max-age=300", w.Header().Get("Cache-Control"))
}

func TestHubHandler_UnreadCount_CacheEEsquece(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"naoLidas": 2})
	})
	h := handlerComHub(p)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.UnreadCount(w, comoAna(httptest.NewRequest("GET", "/suggestions/unread-count", nil)))
		require.JSONEq(t, `{"count":2}`, w.Body.String())
	}
	require.Len(t, p.pedidos, 1, "três pedidos, uma ida ao hub")

	w := httptest.NewRecorder()
	h.MarkRead(w, comParam(comoAna(httptest.NewRequest("POST", "/x", nil)), "id", idHub))
	require.Equal(t, http.StatusNoContent, w.Code)

	w = httptest.NewRecorder()
	h.UnreadCount(w, comoAna(httptest.NewRequest("GET", "/suggestions/unread-count", nil)))
	require.Len(t, p.pedidos, 3, "marcar lida esquece o cache: lida + nova contagem")
}

func TestHubHandler_UnreadCount_ErroDoHubNaoViraZero(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	w := httptest.NewRecorder()
	handlerComHub(p).UnreadCount(w, comoAna(httptest.NewRequest("GET", "/x", nil)))
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, w.Body.String(), `"count"`)
}

func TestHubHandler_TriagemFoiParaOHub(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {})
	h := handlerComHub(p)
	for _, f := range []http.HandlerFunc{h.Gone} {
		w := httptest.NewRecorder()
		f(w, comoAna(httptest.NewRequest("PATCH", "/suggestions/"+idHub, strings.NewReader(`{"status":"concluida"}`))))
		require.Equal(t, http.StatusGone, w.Code)
		require.Contains(t, w.Body.String(), "Central de Clientes")
	}
	require.Empty(t, p.pedidos)
}
