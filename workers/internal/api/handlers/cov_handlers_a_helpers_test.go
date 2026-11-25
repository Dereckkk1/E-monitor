package handlers

// cov_handlers_a_helpers_test.go — helpers compartilhados pelos testes cov_a_*
// (cobertura dos handlers de admin, campanhas, clientes, veiculações...).
// Todo identificador novo aqui leva o prefixo `covA` para não colidir com o
// outro agente que mexe no mesmo pacote (prefixo `covB`).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/auth"
	"radiocheck/internal/db"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/storage"
)

// covADB devolve um banco limpo (clients/users/stations zerados) já protegido
// pela guarda do dbtest. Pula o teste quando não há TEST_DATABASE_URL.
func covADB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	return mcptest.DBCatalogo(t)
}

// covAClosedPool devolve um pool já fechado: qualquer query nele falha, o que
// permite exercitar os ramos "500 internal error" dos handlers sem mock.
func covAClosedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	return pool
}

// covAClaims monta claims de teste. Para viewer, os ids viram a carteira e o
// primeiro vira o ClientID principal.
func covAClaims(role string, userID uuid.UUID, clientIDs ...uuid.UUID) *auth.Claims {
	c := &auth.Claims{UserID: userID, Role: role}
	if len(clientIDs) > 0 {
		first := clientIDs[0]
		c.ClientID = &first
		c.ClientIDs = clientIDs
	}
	return c
}

// covAWith injeta claims no contexto do request (o que o RequireJWT faria).
func covAWith(r *http.Request, c *auth.Claims) *http.Request {
	if c == nil {
		return r
	}
	return r.WithContext(auth.WithClaims(r.Context(), c))
}

// covAJSON cria um request com corpo JSON. corpo string vai cru (para testar
// JSON malformado).
func covAJSON(t *testing.T, method, target string, corpo any) *http.Request {
	t.Helper()
	var rd io.Reader = http.NoBody
	switch v := corpo.(type) {
	case nil:
	case string:
		rd = strings.NewReader(v)
	default:
		b, err := json.Marshal(v)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, rd)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// covAServe roda o handler/roteador e devolve o gravador.
func covAServe(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// covADecode lê o corpo JSON da resposta num map.
func covADecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "corpo: %s", rec.Body.String())
	return m
}

// covARouter monta um chi.Router com as rotas informadas, já com os claims
// (se houver) injetados em todo request.
func covARouter(claims *auth.Claims, register func(r chi.Router)) chi.Router {
	r := chi.NewRouter()
	if claims != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, covAWith(req, claims))
			})
		})
	}
	register(r)
	return r
}

// covAPart é uma parte de arquivo de um multipart de teste.
type covAPart struct {
	Field       string
	Filename    string
	ContentType string
	Data        []byte
}

// covAMultipart monta um request multipart com campos de texto e arquivos.
func covAMultipart(t *testing.T, method, target string, campos map[string]string, partes ...covAPart) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range campos {
		require.NoError(t, mw.WriteField(k, v))
	}
	for _, p := range partes {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, p.Field, p.Filename))
		if p.ContentType != "" {
			h.Set("Content-Type", p.ContentType)
		}
		w, err := mw.CreatePart(h)
		require.NoError(t, err)
		_, err = w.Write(p.Data)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// covAS3 é um S3 falso (path-style) em httptest: guarda os objetos em memória.
// Basta para o storage.Client real (aws-sdk) fazer Put/Get/Head contra ele.
type covAS3 struct {
	mu      sync.Mutex
	objs    map[string]covAObj
	FailPut bool // PUT devolve 403 (o SDK não repete 403, então o teste não demora)
	FailGet bool
	srv     *httptest.Server
}

type covAObj struct {
	Data        []byte
	ContentType string
}

// covANewS3 sobe o S3 falso e devolve (storage real apontando pra ele, o falso).
func covANewS3(t *testing.T) (*storage.Client, *covAS3) {
	t.Helper()
	f := &covAS3{objs: map[string]covAObj{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	cli, err := storage.New(context.Background(), f.srv.URL, f.srv.URL, "bucket-teste", "us-east-1", "AKIATESTE", "segredo-teste")
	require.NoError(t, err)
	return cli, f
}

// Objeto devolve o objeto gravado sob a chave (sem o prefixo do bucket).
func (f *covAS3) Objeto(key string) (covAObj, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objs[key]
	return o, ok
}

// Poe grava um objeto direto, sem passar pelo handler.
func (f *covAS3) Poe(key string, data []byte, contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objs[key] = covAObj{Data: data, ContentType: contentType}
}

// Quantos devolve o número de objetos gravados.
func (f *covAS3) Quantos() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objs)
}

func (f *covAS3) serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/bucket-teste/")
	negado := func() {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>negado</Message></Error>`))
	}
	switch r.Method {
	case http.MethodPut:
		if f.FailPut {
			negado()
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.Poe(key, b, r.Header.Get("Content-Type"))
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		if f.FailGet {
			negado()
			return
		}
		o, ok := f.Objeto(key)
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`))
			}
			return
		}
		if o.ContentType != "" {
			w.Header().Set("Content-Type", o.ContentType)
		} else {
			w.Header()["Content-Type"] = nil // sem Content-Type e sem sniffing do Go
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(o.Data)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.Data)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// covAWAV gera um WAV PCM mono 8 kHz de `segundos` de silêncio: pequeno e
// legível pelo ffprobe.
func covAWAV(segundos int) []byte {
	const taxa = 8000
	n := segundos * taxa * 2 // 16 bits mono
	var b bytes.Buffer
	le32 := func(v uint32) { b.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}) }
	le16 := func(v uint16) { b.Write([]byte{byte(v), byte(v >> 8)}) }
	b.WriteString("RIFF")
	le32(uint32(36 + n))
	b.WriteString("WAVEfmt ")
	le32(16)
	le16(1) // PCM
	le16(1) // mono
	le32(taxa)
	le32(taxa * 2)
	le16(2)
	le16(16)
	b.WriteString("data")
	le32(uint32(n))
	b.Write(make([]byte, n))
	return b.Bytes()
}

// covADecodeInto lê o corpo JSON da resposta numa estrutura qualquer.
func covADecodeInto(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), dst), "corpo: %s", rec.Body.String())
}

// covATrim aparta espaços/quebras de linha do corpo (json.Encoder termina com \n).
func covATrim(s string) string { return strings.TrimSpace(s) }

// covAJoin junta strings com vírgula (formato dos filtros CSV de campanhas).
func covAJoin(partes []string) string { return strings.Join(partes, ",") }
