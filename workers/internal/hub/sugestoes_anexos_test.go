package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// bucketFalso faz o papel do GCS: guarda o que chegou e com que headers.
type bucketFalso struct {
	srv     *httptest.Server
	headers []http.Header
	corpo   []byte
	status  int
}

func novoBucket(t *testing.T) *bucketFalso {
	b := &bucketFalso{status: 200}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.headers = append(b.headers, r.Header.Clone())
		if r.Method == http.MethodPut {
			b.corpo, _ = io.ReadAll(r.Body)
			w.WriteHeader(b.status)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func TestSubirAnexo_TresPassos(t *testing.T) {
	b := novoBucket(t)
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/platform/sugestoes/anexos/upload-url":
			jsonDe(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": b.srv.URL + "/obj?assinado=1"})
		case "/api/platform/sugestoes/anexos/an1/confirm":
			jsonDe(w, 200, map[string]any{"anexo": map[string]any{"id": "an1"}})
		default:
			w.WriteHeader(500)
		}
	})

	id, err := New(h.srv.URL, "chave-x").SubirAnexoSugestao(context.Background(), autorAna, "print.png", "image/png", []byte("12345"))

	require.NoError(t, err)
	require.Equal(t, "an1", id)
	require.JSONEq(t, `{"nome":"print.png","mimeType":"image/png","tamanho":5}`, h.corpos[0])
	require.Equal(t, []byte("12345"), b.corpo)
	require.Equal(t, "image/png", b.headers[0].Get("Content-Type"))
	// O bucket NUNCA vê a chave nem o autor.
	require.Empty(t, b.headers[0].Get("X-Hub-Platform-Key"))
	require.Empty(t, b.headers[0].Get("X-Hub-Autor-Email"))
	require.Len(t, h.pedidos, 2) // upload-url + confirm
}

func TestSubirAnexo_BucketRecusaENaoConfirma(t *testing.T) {
	b := novoBucket(t)
	b.status = 403
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 201, map[string]any{"anexoId": "an1", "uploadUrl": b.srv.URL + "/obj"})
	})
	_, err := New(h.srv.URL, "k").SubirAnexoSugestao(context.Background(), autorAna, "p.png", "image/png", []byte("x"))

	e, _ := AsError(err)
	require.Equal(t, http.StatusBadGateway, e.Status)
	require.Len(t, h.pedidos, 1) // não houve confirm
}

func TestAbrirAnexo_Segue302SemHeaders(t *testing.T) {
	b := novoBucket(t)
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/platform/sugestoes/anexos/an1/arquivo", r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("inline"))
		http.Redirect(w, r, b.srv.URL+"/obj?assinado=1", http.StatusFound)
	})

	corpo, ct, err := New(h.srv.URL, "chave-x").AbrirAnexoSugestao(context.Background(), autorAna, "an1")

	require.NoError(t, err)
	defer corpo.Close()
	dados, _ := io.ReadAll(corpo)
	require.Equal(t, "PNGBYTES", string(dados))
	require.Equal(t, "image/png", ct)
	require.Empty(t, b.headers[0].Get("X-Hub-Platform-Key"), "a chave vazou para o bucket no redirect")
	require.Empty(t, b.headers[0].Get("X-Hub-Autor-Email"))
}

func TestAbrirAnexo_404DoHubPassa(t *testing.T) {
	h := novoHubFalso(t, func(w http.ResponseWriter, r *http.Request) {
		jsonDe(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Anexo não encontrado"}})
	})
	_, _, err := New(h.srv.URL, "k").AbrirAnexoSugestao(context.Background(), autorAna, "an1")
	e, _ := AsError(err)
	require.Equal(t, 404, e.Status)
}
