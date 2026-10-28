package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

// chamarNoServidor liga um cliente MCP em memória ao servidor e chama a tool.
// Devolve o texto do primeiro conteúdo e se veio isError.
func chamarNoServidor(t *testing.T, s *sdk.Server, nome string, args any) (string, bool) {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := s.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "teste", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: nome, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

// cfgTeste é o ConfigServidor das tools de leitura sobre o banco de teste.
func cfgTeste(pool *pgxpool.Pool) ConfigServidor {
	return ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: NovaLeitura(pool)}
}

// chamarTool chama a tool do servidor real e devolve o JSON do resultado.
// Falha o teste se vier isError.
func chamarTool(t *testing.T, cfg ConfigServidor, nome string, args any) map[string]any {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), nome, args)
	require.False(t, erro, "%s devolveu erro: %s", nome, texto)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &out), texto)
	return out
}

// chamarToolErro espera isError e devolve o texto.
func chamarToolErro(t *testing.T, cfg ConfigServidor, nome string, args any) string {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), nome, args)
	require.True(t, erro, "%s deveria falhar, devolveu: %s", nome, texto)
	return texto
}

// chamarHandler chama um handler HTTP como um admin chamaria pela tela:
// claims de admin no contexto e os parâmetros de rota do chi. Exige 200.
func chamarHandler(t *testing.T, h http.HandlerFunc, alvo string, rota map[string]string, admin uuid.UUID) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, alvo, nil)
	rctx := chi.NewRouteContext()
	for k, v := range rota {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.WithClaims(ctx, &auth.Claims{UserID: admin, Role: "admin"})
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

// itens devolve out["items"] como lista de objetos.
func itens(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	bruto, ok := out["items"].([]any)
	require.True(t, ok, "sem items: %v", out)
	res := make([]map[string]any, len(bruto))
	for i, v := range bruto {
		res[i] = v.(map[string]any)
	}
	return res
}

// pag devolve out["page"].
func pag(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	p, ok := out["page"].(map[string]any)
	require.True(t, ok, "sem page: %v", out)
	return p
}

// ids extrai o campo "id" (ou o id de um ref em campo) de cada item.
func ids(t *testing.T, xs []map[string]any, campo string) []string {
	t.Helper()
	out := make([]string, len(xs))
	for i, x := range xs {
		if campo == "id" {
			out[i] = x["id"].(string)
			continue
		}
		out[i] = x[campo].(map[string]any)["id"].(string)
	}
	return out
}
