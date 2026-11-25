package handlers

// cov_a_bugs_test.go — achados da cobertura que NÃO foram consertados (a regra
// do trabalho é só testar). Cada teste descreve o comportamento CORRETO e fica
// pulado até alguém corrigir o código de produção; ao corrigir, remova o Skip.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

// admin_monitoring.go:821-873 (BlockIP) só checa que o campo não é vazio: texto
// qualquer entra em blocked_ips e na lista em memória. Um IP digitado errado
// (ou colado com espaço/ponto-e-vírgula) bloqueia nada e ainda polui a tabela;
// o correto é validar com net.ParseIP e responder 400.
func TestCovA_Bug_BlockIPAceitaTextoQueNaoEhIP(t *testing.T) {
	t.Skip("BUG: admin_monitoring.go:827-831 — BlockIP aceita qualquer texto não-vazio como IP (200 e grava em blocked_ips); deveria validar net.ParseIP e responder 400")
	_, pool, h := covAMon(t)
	rec := covAServe(covAMonRouter(h), covAJSON(t, http.MethodPost, "/block-ip", map[string]string{"ip": "isto-nao-e-ip"}))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM blocked_ips`).Scan(&n))
	assert.Zero(t, n)
}

// Regressão: Revoke apagava a chave só pelo keyID e ignorava o clientID da URL —
// pelo path de um cliente dava para revogar a chave de outro.
func TestCovA_RevogarChaveRespeitaOClienteDaURL(t *testing.T) {
	ctx, pool := covADB(t)
	h := NewAPIKeysHandler(pool)
	r := covARouter(nil, func(r chi.Router) {
		r.Post("/clients/{clientID}/api-keys", h.Create)
		r.Delete("/clients/{clientID}/api-keys/{keyID}", h.Revoke)
	})
	a := mcptest.Cliente(t, ctx, pool, "Cliente A")
	b := mcptest.Cliente(t, ctx, pool, "Cliente B")
	rec := covAServe(r, httptest.NewRequest(http.MethodPost, "/clients/"+b.String()+"/api-keys", nil))
	id := covADecode(t, rec)["id"].(string)

	covAServe(r, httptest.NewRequest(http.MethodDelete, "/clients/"+a.String()+"/api-keys/"+id, nil))

	var revogada int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE id=$1 AND revoked_at IS NOT NULL`, id).Scan(&revogada))
	assert.Zero(t, revogada, "a chave do cliente B não pode ser revogada pelo path do cliente A")
}

// detections.go:126 (List legado): quando não há linhas o repo devolve slice
// nil e a resposta sai {"data":null}; as outras listas (commercials, regras,
// vínculos) normalizam para []. Clientes que fazem data.map(...) quebram.
func TestCovA_Bug_ListaLegadaDeVeiculacoesDevolveNullQuandoVazia(t *testing.T) {
	t.Skip(`BUG: detections.go:126 — GET /detections (sem ?page) sem resultados responde {"data":null}; deveria ser {"data":[]} como as demais listas`)
	c := covANewDetCen(t)
	rec := c.get(c.adminR(), "/detections?campaign_id="+c.campanha.String()+"&start_date=2030-01-01T00:00:00Z")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []any{}, covADecode(t, rec)["data"])
}
