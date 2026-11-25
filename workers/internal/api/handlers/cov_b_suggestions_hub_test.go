package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/hub"
	"radiocheck/internal/users"
)

// covBHubRoute descreve uma rota do handler do hub para os testes em tabela.
type covBHubRoute struct {
	name string
	call func(h *SuggestionsHubHandler, w http.ResponseWriter, r *http.Request)
	// build monta a requisição já com os parâmetros de rota válidos.
	build func(t *testing.T) *http.Request
}

func covBHubRoutes() []covBHubRoute {
	const aid = "6650bbbbbbbbbbbbbbbbbbbb"
	jsonReq := func(body string, params map[string]string) func(t *testing.T) *http.Request {
		return func(t *testing.T) *http.Request {
			return covBReq("POST", "/x", body, params, nil)
		}
	}
	imageReq := func(params map[string]string) func(t *testing.T) *http.Request {
		return func(t *testing.T) *http.Request {
			return covBMultipartReq(t, "/x", nil, params, nil,
				covBFile{Field: "file", Filename: "print.png", ContentType: "image/png", Data: covBPNG})
		}
	}
	return []covBHubRoute{
		{"create", (*SuggestionsHubHandler).Create,
			jsonReq(`{"title":"T","description":"D","type":"bug","requester_priority":"alta"}`, nil)},
		{"list", (*SuggestionsHubHandler).List, jsonReq("", nil)},
		{"get", (*SuggestionsHubHandler).Get, jsonReq("", map[string]string{"id": idHub})},
		{"add comment", (*SuggestionsHubHandler).AddComment, jsonReq(`{"body":"oi"}`, map[string]string{"id": idHub})},
		{"stage attachment", (*SuggestionsHubHandler).StageAttachment, imageReq(nil)},
		{"upload attachment legado", (*SuggestionsHubHandler).UploadAttachment, imageReq(map[string]string{"id": idHub})},
		{"proxy attachment", (*SuggestionsHubHandler).ProxyAttachment, jsonReq("", map[string]string{"aid": aid})},
		{"mark read", (*SuggestionsHubHandler).MarkRead, jsonReq("", map[string]string{"id": idHub})},
		{"unread count", (*SuggestionsHubHandler).UnreadCount, jsonReq("", nil)},
	}
}

func covBAsAna(r *http.Request) *http.Request { return comoAna(r) }

func TestCovB_HubHandler_ErroDoHubTraduzidoParaAPessoa(t *testing.T) {
	cases := []struct {
		name       string
		hubStatus  int
		hubMsg     string
		wantStatus int
		wantMsg    string
	}{
		{"404 passa com a mensagem do hub", 404, "Sugestão não encontrada", 404, "Sugestão não encontrada"},
		{"422 passa com a mensagem do hub", 422, "Título longo demais", 422, "Título longo demais"},
		{"409 passa", 409, "Já existe", 409, "Já existe"},
		{"400 sem mensagem usa a genérica", 400, "", 400, "a Central de Clientes recusou o pedido"},
		{"404 sem mensagem usa a genérica", 404, "", 404, "sugestão não encontrada"},
		{"401 vira 503 (a chave é problema nosso, não da pessoa)", 401, "x", 503, "a chave do hub foi recusada — avise o suporte"},
		{"429 vira 503 pedindo para tentar de novo", 429, "x", 503, "a Central de Clientes está ocupada, tente de novo em instantes"},
		{"500 vira 502 sem repassar o corpo do hub", 500, "stack trace secreto", 502, "a Central de Clientes respondeu 500"},
		{"403 vira 502", 403, "x", 502, "a Central de Clientes respondeu 403"},
	}
	for _, rt := range covBHubRoutes() {
		for _, c := range cases {
			t.Run(rt.name+"/"+c.name, func(t *testing.T) {
				p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
					responde(w, c.hubStatus, map[string]any{"error": map[string]string{"code": "x", "message": c.hubMsg}})
				})
				w := httptest.NewRecorder()
				rt.call(handlerComHub(p), w, covBAsAna(rt.build(t)))
				assert.Equal(t, c.wantStatus, w.Code, w.Body.String())
				assert.JSONEq(t, `{"error":"`+c.wantMsg+`"}`, w.Body.String())
				assert.NotContains(t, w.Body.String(), "stack trace")
			})
		}
	}
}

func TestCovB_HubHandler_SemUsuarioOuSemEmailE500SemChamarOHub(t *testing.T) {
	loaders := map[string]autorLoader{
		"usuário não existe": usuariosFalsos{},
		"sem e-mail":         usuariosFalsos{idAna: {ID: idAna, Email: "   ", Name: "Ana"}},
		"usuário nulo":       usuariosFalsos{idAna: nil},
	}
	for _, rt := range covBHubRoutes() {
		for lname, loader := range loaders {
			t.Run(rt.name+"/"+lname, func(t *testing.T) {
				p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
				h := NewSuggestionsHubHandler(hub.New(p.srv.URL, "chave"), loader)
				w := httptest.NewRecorder()
				rt.call(h, w, covBAsAna(rt.build(t)))
				assert.Equal(t, http.StatusInternalServerError, w.Code, "falha de banco não pode virar 401 (expulsaria a pessoa pro login)")
				assert.JSONEq(t, `{"error":"erro interno"}`, w.Body.String())
				assert.Empty(t, p.pedidos, "o hub não pode ser chamado sem autor identificado")
			})
		}
	}
}

func TestCovB_HubHandler_SemSessao401EmTodasAsRotas(t *testing.T) {
	for _, rt := range covBHubRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
			w := httptest.NewRecorder()
			rt.call(handlerComHub(p), w, rt.build(t)) // sem claims
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Empty(t, p.pedidos)
		})
	}
}

func TestCovB_HubHandler_IdMalformadoNaoSaiDeCasa(t *testing.T) {
	bad := []string{"../importar", "ZZZ", "6650aaaaaaaaaaaaaaaaaaa", "6650aaaaaaaaaaaaaaaaaaaaa", "6650AAAAAAAAAAAAAAAAAAAA", ""}
	routes := map[string]struct {
		call  func(h *SuggestionsHubHandler, w http.ResponseWriter, r *http.Request)
		param string
	}{
		"add comment": {(*SuggestionsHubHandler).AddComment, "id"},
		"upload":      {(*SuggestionsHubHandler).UploadAttachment, "id"},
		"proxy":       {(*SuggestionsHubHandler).ProxyAttachment, "aid"},
		"mark read":   {(*SuggestionsHubHandler).MarkRead, "id"},
	}
	for name, rt := range routes {
		for _, id := range bad {
			t.Run(name+"/"+id, func(t *testing.T) {
				p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 200, sugestaoDoHub) })
				w := httptest.NewRecorder()
				rt.call(handlerComHub(p), w, covBAsAna(covBReq("POST", "/x", `{"body":"oi"}`, map[string]string{rt.param: id}, nil)))
				assert.Equal(t, http.StatusNotFound, w.Code)
				assert.Empty(t, p.pedidos)
			})
		}
	}
}

func TestCovB_HubHandler_CorpoInvalido(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)

	w := httptest.NewRecorder()
	h.Create(w, covBAsAna(covBReq("POST", "/x", `{"title":`, nil, nil)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"error":"invalid JSON"}`, w.Body.String())

	w = httptest.NewRecorder()
	h.AddComment(w, covBAsAna(covBReq("POST", "/x", `{"body":`, map[string]string{"id": idHub}, nil)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, p.pedidos)
}

func TestCovB_HubHandler_CreateNormalizaTelaAlvoEChave(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) { responde(w, 201, sugestaoDoHub) })
	h := handlerComHub(p)

	// tela só com espaços vira ausente; chave curta demais ganha uma nova (UUID).
	w := httptest.NewRecorder()
	h.Create(w, covBAsAna(covBReq("POST", "/x",
		`{"title":"T","description":"D","type":"melhoria","target_screen":"   ","requester_priority":"media","client_key":"curta"}`, nil, nil)))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, p.corpos[0], "telaAlvo")
	assert.NotContains(t, p.corpos[0], `"chaveEnvio":"curta"`)

	// o formulário reenviado com a MESMA chave válida repassa a mesma chave (idempotência no hub).
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		h.Create(w, covBAsAna(covBReq("POST", "/x",
			`{"title":"T","description":"D","type":"bug","requester_priority":"alta","client_key":"form-abc-123"}`, nil, nil)))
		require.Equal(t, http.StatusCreated, w.Code)
	}
	assert.Contains(t, p.corpos[1], `"chaveEnvio":"form-abc-123"`)
	assert.Contains(t, p.corpos[2], `"chaveEnvio":"form-abc-123"`)
}

func TestCovB_HubHandler_ChaveDeEnvio(t *testing.T) {
	assert.Equal(t, "abcdefgh", chaveDeEnvio("abcdefgh"))
	assert.Equal(t, "A_b-9xyz", chaveDeEnvio("A_b-9xyz"))
	for _, bad := range []string{"", "curta", "tem espaço aqui", "acentuação!", strings.Repeat("a", 65)} {
		got := chaveDeEnvio(bad)
		assert.NotEqual(t, bad, got, bad)
		_, err := uuid.Parse(got)
		assert.NoError(t, err, "chave inválida %q é trocada por um UUID", bad)
	}
}

func TestCovB_HubHandler_UploadsDeImagem(t *testing.T) {
	p, _ := portaComBucket(t)
	h := handlerComHub(p)
	post := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.StageAttachment(w, covBAsAna(r))
		return w
	}

	t.Run("corpo que não é multipart 400", func(t *testing.T) {
		w := post(covBReq("POST", "/x", `{"a":1}`, nil, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "envio inválido")
	})
	t.Run("sem campo file 400", func(t *testing.T) {
		w := post(covBMultipartReq(t, "/x", map[string]string{"outro": "x"}, nil, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "campo 'file' obrigatório")
	})
	t.Run("mime fora da lista 415 (svg, pdf, vazio)", func(t *testing.T) {
		for _, mime := range []string{"image/svg+xml", "application/pdf", ""} {
			w := post(covBMultipartReq(t, "/x", nil, nil, nil, covBFile{Field: "file", Filename: "a.bin", ContentType: mime, Data: []byte("x")}))
			assert.Equal(t, http.StatusUnsupportedMediaType, w.Code, "mime %q", mime)
		}
	})
	t.Run("mime em caixa alta e com espaços é aceito", func(t *testing.T) {
		w := post(covBMultipartReq(t, "/x", nil, nil, nil, covBFile{Field: "file", Filename: "a.jpg", ContentType: " IMAGE/JPEG ", Data: []byte("jpg")}))
		assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	})
	t.Run("imagem acima de 10MB 413", func(t *testing.T) {
		big := make([]byte, suggestionAttachmentMaxBytes+1)
		w := post(covBMultipartReq(t, "/x", nil, nil, nil, covBFile{Field: "file", Filename: "big.png", ContentType: "image/png", Data: big}))
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
	})
	t.Run("hub recusa o anexo: status do hub e nenhuma fala criada (rota legada)", func(t *testing.T) {
		recusa := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
			responde(w, 429, map[string]any{"error": map[string]string{"code": "rate", "message": "calma"}})
		})
		w := httptest.NewRecorder()
		handlerComHub(recusa).UploadAttachment(w, covBAsAna(covBMultipartReq(t, "/x", nil, map[string]string{"id": idHub}, nil,
			covBFile{Field: "file", Filename: "a.png", ContentType: "image/png", Data: covBPNG})))
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Len(t, recusa.pedidos, 1, "falhou no upload, então não chega a comentar")
	})
	t.Run("legado: upload ok mas comentário recusado devolve o erro do hub", func(t *testing.T) {
		bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer bucket.Close()
		porta := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/platform/sugestoes/anexos/upload-url":
				responde(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": bucket.URL + "/obj"})
			case strings.HasSuffix(r.URL.Path, "/confirm"):
				responde(w, 200, map[string]any{"anexo": map[string]any{"id": "an1"}})
			default: // comentário
				responde(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Sugestão não encontrada"}})
			}
		})
		w := httptest.NewRecorder()
		handlerComHub(porta).UploadAttachment(w, covBAsAna(covBMultipartReq(t, "/x", nil, map[string]string{"id": idHub}, nil,
			covBFile{Field: "file", Filename: "a.png", ContentType: "image/png", Data: covBPNG})))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.JSONEq(t, `{"error":"Sugestão não encontrada"}`, w.Body.String())
	})
}

func TestCovB_HubHandler_ProxyAttachmentSemContentType(t *testing.T) {
	// o bucket responde sem Content-Type: o proxy cai em octet-stream? O Go
	// sniffa o corpo, então o tipo nunca vem vazio; garantimos só os cabeçalhos fixos.
	p, _ := portaComBucket(t)
	aid := "6650bbbbbbbbbbbbbbbbbbbb"
	w := httptest.NewRecorder()
	handlerComHub(p).ProxyAttachment(w, covBAsAna(covBReq("GET", "/x", "", map[string]string{"aid": aid}, nil)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "BYTES-DO-GCS", w.Body.String())
	assert.NotEmpty(t, w.Header().Get("Content-Type"))
}

func TestCovB_HubHandler_MarkReadEUnreadCount(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		// POST /{id}/lida e GET /nao-lidas devolvem o mesmo contador.
		responde(w, 200, map[string]any{"naoLidas": 5})
	})
	h := handlerComHub(p)

	t.Run("unread: 1ª chamada vai ao hub e a 2ª sai do cache", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			h.UnreadCount(w, covBAsAna(covBReq("GET", "/x", "", nil, nil)))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.JSONEq(t, `{"count":5}`, w.Body.String())
		}
		assert.Len(t, p.pedidos, 1)
	})
	t.Run("mark read: 204 e o cache do badge é esquecido", func(t *testing.T) {
		_, ainda := h.naoLidas.pega("ana@t.local")
		require.True(t, ainda)
		w := httptest.NewRecorder()
		h.MarkRead(w, covBAsAna(covBReq("POST", "/x", "", map[string]string{"id": idHub}, nil)))
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		_, ainda = h.naoLidas.pega("ana@t.local")
		assert.False(t, ainda)
	})
}

func TestCovB_HubHandler_ListaEGone(t *testing.T) {
	p := novaPortaFalsa(t, func(w http.ResponseWriter, r *http.Request) {
		responde(w, 200, map[string]any{"itens": []any{}, "sugestoes": []any{}})
	})
	h := handlerComHub(p)

	t.Run("lista vazia do hub vira data [] (não null)", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.List(w, covBAsAna(covBReq("GET", "/x?status=retirada", "", nil, nil)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"data":[]}`, w.Body.String())
		assert.Contains(t, p.pedidos[0].URL.RawQuery, "retirada", "'retirada' é o único status extra repassado")
	})
	t.Run("PATCH, summary e URL assinada respondem 410 com o aviso", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.Gone(w, httptest.NewRequest("PATCH", "/x", nil))
		assert.Equal(t, http.StatusGone, w.Code)
		assert.JSONEq(t, `{"error":"`+triagemNoHub+`"}`, w.Body.String())
	})
}

func TestCovB_HubHandler_EscreveErroDoHub(t *testing.T) {
	t.Run("erro que não é do hub vira 500 genérico sem vazar o detalhe", func(t *testing.T) {
		w := httptest.NewRecorder()
		escreveErroDoHub(w, errors.New("dial tcp 10.0.0.5:443: segredo interno"))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.JSONEq(t, `{"error":"erro interno"}`, w.Body.String())
		assert.NotContains(t, w.Body.String(), "10.0.0.5")
	})
	t.Run("texto(nil) é string vazia", func(t *testing.T) {
		assert.Equal(t, "", texto(nil))
		s := "abc"
		assert.Equal(t, "abc", texto(&s))
	})
}

// garante que o usuário falso do pacote continua satisfazendo a interface.
var _ autorLoader = usuariosFalsos{}
var _ = users.User{}
