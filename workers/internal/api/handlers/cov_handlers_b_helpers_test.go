package handlers

// Helpers dos testes de cobertura "cov_b_*" (fatia B do pacote handlers).
// Todos os nomes têm prefixo covB para não colidir com os da fatia A (covA).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
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
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/storage"
	"radiocheck/internal/supervisor"
)

// covBDB abre o banco de teste (com guarda), zera users/clients/stations e as
// tabelas que dependem delas. Pula o teste quando TEST_DATABASE_URL não existe.
func covBDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `TRUNCATE suggestions RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// covBClaims cria claims de um papel; clientIDs só faz sentido para viewer.
func covBClaims(role string, uid uuid.UUID, clientIDs ...uuid.UUID) *auth.Claims {
	c := &auth.Claims{UserID: uid, Role: role}
	if len(clientIDs) > 0 {
		c.ClientIDs = clientIDs
		first := clientIDs[0]
		c.ClientID = &first
	}
	return c
}

// covBReq monta uma requisição com parâmetros de rota do chi e (opcional) claims.
func covBReq(method, target, body string, params map[string]string, claims *auth.Claims) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return covBWithCtx(req, params, claims)
}

func covBWithCtx(req *http.Request, params map[string]string, claims *auth.Claims) *http.Request {
	ctx := req.Context()
	if len(params) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	if claims != nil {
		ctx = auth.ContextWithClaims(ctx, claims)
	}
	return req.WithContext(ctx)
}

// covBServe executa o handler e devolve o recorder.
func covBServe(h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// covBRecorder devolve um ResponseRecorder novo.
func covBRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// covBJSON decodifica o corpo da resposta num mapa.
func covBJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "corpo: %s", rec.Body.String())
	return m
}

// covBJSONArray decodifica o corpo da resposta como lista de objetos.
func covBJSONArray(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var m []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "corpo: %s", rec.Body.String())
	return m
}

// covBFile é uma parte de arquivo de um formulário multipart.
type covBFile struct {
	Field       string
	Filename    string
	ContentType string
	Data        []byte
}

// covBMultipart monta o corpo multipart (campos + arquivos) e o Content-Type.
func covBMultipart(t *testing.T, fields map[string]string, files ...covBFile) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+f.Field+`"; filename="`+f.Filename+`"`)
		if f.ContentType != "" {
			h.Set("Content-Type", f.ContentType)
		}
		part, err := mw.CreatePart(h)
		require.NoError(t, err)
		_, err = part.Write(f.Data)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	return buf, mw.FormDataContentType()
}

// covBMultipartReq cria uma requisição POST multipart já com rota e claims.
func covBMultipartReq(t *testing.T, target string, fields map[string]string, params map[string]string,
	claims *auth.Claims, files ...covBFile) *http.Request {
	t.Helper()
	body, ct := covBMultipart(t, fields, files...)
	req := httptest.NewRequest(http.MethodPost, target, body)
	req.Header.Set("Content-Type", ct)
	return covBWithCtx(req, params, claims)
}

// covBPNG são 8 bytes de assinatura PNG: o que o handler vê é só o Content-Type.
var covBPNG = []byte("\x89PNG\r\n\x1a\n-corpo-de-teste")

// ── S3 falso ────────────────────────────────────────────────────────────

// covBS3 é um servidor S3 mínimo (path-style) em memória: PUT/GET/DELETE.
type covBS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	ctypes  map[string]string
	failPut bool
	failGet bool
	srv     *httptest.Server
}

// covBNewS3 sobe o servidor e devolve o storage.Client apontado para ele.
func covBNewS3(t *testing.T) (*storage.Client, *covBS3) {
	t.Helper()
	f := &covBS3{objects: map[string][]byte{}, ctypes: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	cli, err := storage.New(context.Background(), f.srv.URL, f.srv.URL, "covb-bucket", "us-east-1", "ak", "sk")
	require.NoError(t, err)
	return cli, f
}

func (f *covBS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/covb-bucket/")
	switch r.Method {
	case http.MethodPut:
		if f.failPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.objects[key] = b
		f.ctypes[key] = r.Header.Get("Content-Type")
		w.Header().Set("ETag", `"covb"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok || f.failGet {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`))
			return
		}
		if ct := f.ctypes[key]; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// put grava direto no fake (sem passar pelo SDK).
func (f *covBS3) put(key string, data []byte, ctype string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	f.ctypes[key] = ctype
}

func (f *covBS3) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

func (f *covBS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	return out
}

// ── supervisor falso ────────────────────────────────────────────────────

// covBSup implementa as interfaces de supervisor que os handlers consomem.
type covBSup struct {
	ws      []supervisor.WorkerStatus
	backoff map[uuid.UUID]uint32
}

func (s *covBSup) WorkerStatuses() []supervisor.WorkerStatus { return s.ws }
func (s *covBSup) BackoffStations() map[uuid.UUID]uint32 {
	if s.backoff == nil {
		return map[uuid.UUID]uint32{}
	}
	return s.backoff
}

// ── NATS falso ──────────────────────────────────────────────────────────

// covBNATS devolve uma conexão NATS real (nats.go) contra um servidor mínimo
// em 127.0.0.1 que só fala INFO/PING/PONG. Basta para IsConnected() == true.
func covBNATS(t *testing.T) *nats.Conn {
	t.Helper()
	const crlf = "\r\n"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.WriteString(c, `INFO {"server_id":"covb","version":"2.10.0","proto":1,"max_payload":1048576,"headers":true}`+crlf)
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					if strings.HasPrefix(sc.Text(), "PING") {
						_, _ = io.WriteString(c, "PONG"+crlf)
					}
				}
			}(c)
		}
	}()
	nc, err := nats.Connect("nats://"+ln.Addr().String(), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close(); _ = ln.Close() })
	return nc
}

// covBListener abre uma porta TCP local que aceita (e descarta) conexões.
func covBListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// covBDeadAddr devolve um host:porta local onde ninguém escuta.
func covBDeadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// covBClosedPool devolve um pool válido já fechado (todo uso falha).
func covBClosedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	require.NoError(t, err)
	pool.Close()
	return pool
}
