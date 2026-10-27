package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

var principalTeste = &Principal{UserID: uuid.New(), Email: "dereck@teste.local", Nome: "Dereck", TokenID: uuid.New()}

// servidorDeTeste injeta o principal como o middleware real faria.
func servidorDeTeste(t *testing.T) *httptest.Server {
	h := SoPost(NovoHandlerMCP(ConfigServidor{Versao: "teste"}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(ComPrincipal(r.Context(), principalTeste)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type respostaRPC struct {
	Status int
	Header http.Header
	Result map[string]any
	Error  map[string]any
}

func rpc(t *testing.T, srv *httptest.Server, host, versao, corpo string) respostaRPC {
	t.Helper()
	return rpcComCabecalhos(t, srv, host, versao, corpo, nil)
}

// rpcComCabecalhos é o rpc com cabeçalhos extras (a spec 2026-07-28 exige
// Mcp-Method/Mcp-Name além do Mcp-Protocol-Version).
func rpcComCabecalhos(t *testing.T, srv *httptest.Server, host, versao, corpo string, cabecalhos map[string]string) respostaRPC {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(corpo))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range cabecalhos {
		req.Header.Set(k, v)
	}
	if versao != "" {
		req.Header.Set("Mcp-Protocol-Version", versao)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	bruto, _ := io.ReadAll(res.Body)
	out := respostaRPC{Status: res.StatusCode, Header: res.Header}
	if res.StatusCode == http.StatusOK {
		var env struct {
			Result map[string]any `json:"result"`
			Error  map[string]any `json:"error"`
		}
		require.NoError(t, json.Unmarshal(bruto, &env), "corpo: %s", bruto)
		out.Result, out.Error = env.Result, env.Error
	}
	return out
}

func TestServidor_InitializeSemSessao(t *testing.T) {
	srv := servidorDeTeste(t)
	r := rpc(t, srv, "", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"teste","version":"1"}}}`)
	require.Equal(t, http.StatusOK, r.Status)
	require.Equal(t, "2025-06-18", r.Result["protocolVersion"])
	require.Equal(t, "emonitor", r.Result["serverInfo"].(map[string]any)["name"])
	require.Contains(t, r.Result["instructions"], "E-monitor")
	require.Empty(t, r.Header.Get("Mcp-Session-Id"), "stateless: nenhuma sessão")
}

func TestServidor_QuemSouEuSemInitialize(t *testing.T) {
	srv := servidorDeTeste(t)
	for _, versao := range []string{"2025-06-18", "2025-11-25"} {
		r := rpc(t, srv, "", versao, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"quem_sou_eu","arguments":{}}}`)
		require.Equal(t, http.StatusOK, r.Status, versao)
		require.Nil(t, r.Error, versao)
		sc := r.Result["structuredContent"].(map[string]any)
		require.Equal(t, "dereck@teste.local", sc["usuario"].(map[string]any)["email"])
		texto := r.Result["content"].([]any)[0].(map[string]any)["text"].(string)
		var doTexto map[string]any
		require.NoError(t, json.Unmarshal([]byte(texto), &doTexto))
		require.Equal(t, sc, doTexto, "o mesmo JSON no texto e no structuredContent (contrato §3.3)")
	}
}

// A spec 2026-07-28 é a que a Plura e o hub negociam hoje com o claude.ai.
// Se este teste falhar porque a versão exige outro formato de pedido, ajuste
// O PEDIDO conforme mcp/streamable_test.go do SDK — nunca o servidor.
//
// O que a 2026-07-28 (SEP-2575) exige do pedido, conforme o go-sdk v1.8.0
// (streamable.go servePOST + streamable_headers.go validateMcpHeaders, e o
// helper newProtocolBody de streamable_test.go):
//   - params._meta["io.modelcontextprotocol/protocolVersion"] igual ao
//     cabeçalho Mcp-Protocol-Version (senão 400 com erro JSON-RPC);
//   - cabeçalho Mcp-Method igual ao method do corpo (e Mcp-Name em tools/call);
//   - clientInfo/clientCapabilities no _meta, como o cliente do SDK sempre manda.
//
// Sem initialize: na 2026-07-28 cada pedido carrega a própria identidade.
func TestServidor_ListaNaSpec20260728(t *testing.T) {
	srv := servidorDeTeste(t)
	r := rpcComCabecalhos(t, srv, "", "2026-07-28",
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{`+
			`"io.modelcontextprotocol/protocolVersion":"2026-07-28",`+
			`"io.modelcontextprotocol/clientInfo":{"name":"teste","version":"1"},`+
			`"io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"Mcp-Method": "tools/list"})
	require.Equal(t, http.StatusOK, r.Status)
	var nomes []string
	for _, tl := range r.Result["tools"].([]any) {
		nomes = append(nomes, tl.(map[string]any)["name"].(string))
	}
	require.Equal(t, ToolsEsperadas, nomes)
}

// D10: em prod o cloudflared chega por localhost:8080 com Host público.
func TestServidor_HostPublicoPorLoopbackNaoLeva403(t *testing.T) {
	srv := servidorDeTeste(t)
	r := rpc(t, srv, "api.e-monitor.online", "2025-06-18", `{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, r.Status)
}

func TestServidor_SoPost(t *testing.T) {
	srv := servidorDeTeste(t)
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequest(m, srv.URL, nil)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode, m)
	}
}

func TestServidor_CorpoAcimaDe1MB(t *testing.T) {
	srv := servidorDeTeste(t)
	grande := `{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"x":"` + strings.Repeat("a", 1<<20) + `"}}`
	// Com o Accept certo: sem ele o SDK devolve 400 ANTES de ler o corpo e o
	// teste passaria sem nunca tocar no limite. O SDK tem limite próprio de
	// 4 MiB; o 413 em 1 MiB só vem do MaxBytesReader do SoPost.
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(grande))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
}

// Inventário: o conjunto de tools é travado e toda tool declara anotações.
func TestInventario(t *testing.T) {
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(ConfigServidor{Versao: "teste"}, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "inventario", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	var nomes []string
	for _, tl := range res.Tools {
		nomes = append(nomes, tl.Name)
		require.NotNil(t, tl.Annotations, "%s sem anotações", tl.Name)
		require.Nil(t, tl.OutputSchema, "%s: contrato §3.3 é sem outputSchema", tl.Name)
		require.NotEmpty(t, tl.Description, tl.Name)
	}
	require.Equal(t, ToolsEsperadas, nomes)
}

// I-1: um router que esqueça o Autenticador.Middleware não pode servir dado.
// O handler recusa antes do SDK: 500, e a tool nunca é alcançada (se fosse,
// devolveria 200 com isError "sem_identidade").
func TestServidor_SemPrincipalRecusaAntesDoSDK(t *testing.T) {
	srv := httptest.NewServer(SoPost(NovoHandlerMCP(ConfigServidor{Versao: "teste"})))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodPost, srv.URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"quem_sou_eu","arguments":{}}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	corpo, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Contains(t, string(corpo), "MCP sem autenticação montada")
	require.NotContains(t, string(corpo), "sem_identidade", "a tool não pode ter sido alcançada")
}

func TestServidor_QuemSouEuNaSpec20260728(t *testing.T) {
	srv := servidorDeTeste(t)
	r := rpcComCabecalhos(t, srv, "", "2026-07-28",
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"_meta":{`+
			`"io.modelcontextprotocol/protocolVersion":"2026-07-28",`+
			`"io.modelcontextprotocol/clientInfo":{"name":"teste","version":"1"},`+
			`"io.modelcontextprotocol/clientCapabilities":{}},`+
			`"name":"quem_sou_eu","arguments":{}}}`,
		map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "quem_sou_eu"})
	require.Equal(t, http.StatusOK, r.Status)
	require.Nil(t, r.Error)
	sc := r.Result["structuredContent"].(map[string]any)
	require.Equal(t, "dereck@teste.local", sc["usuario"].(map[string]any)["email"])
}

func TestServidor_NotificacaoDa202(t *testing.T) {
	srv := servidorDeTeste(t)
	r := rpc(t, srv, "", "2025-06-18", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	require.Equal(t, http.StatusAccepted, r.Status)
}

// M-3: structuredContent tem de ser objeto JSON.
func TestResultado_ExigeObjeto(t *testing.T) {
	_, _, err := resultado([]string{"a"})
	require.ErrorContains(t, err, "mcp: resultado precisa ser objeto JSON")
	_, _, err = resultado("texto")
	require.Error(t, err)
	res, _, err := resultado(map[string]any{"ok": true})
	require.NoError(t, err)
	require.NotNil(t, res.StructuredContent)
}

func TestInstrucoes_TemGlossario(t *testing.T) {
	require.Contains(t, Instrucoes, "Glossário: veiculação = uma tocada detectada")
	require.Contains(t, Instrucoes, "Impactos = PMM × (in_slot + bonus).")
}
