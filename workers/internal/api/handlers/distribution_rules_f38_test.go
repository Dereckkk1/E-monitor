package handlers

// distribution_rules_f38_test.go — os dois defeitos da rota de regra corrigidos
// na extração para internal/acoes (plano da Fatia 3, decisão F3-8):
//
//	(a) PUT/DELETE de uma regra exige que ela seja DA campanha da URL (404);
//	(b) o PUT que TROCA o tipo recategoriza também o tipo ANTIGO.
//
// Antes: (a) editava/apagava a regra de outra campanha com 204 e recategorizava
// a campanha errada; (b) deixava o tipo antigo com a categoria velha até o
// projrecon curar (até 48 h).

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestRegraHTTP_F38a_PutDeRegraDeOutraCampanhaDa404ESemEfeito(t *testing.T) {
	c := novoCenarioRegra(t)
	outra := mcptest.Campanha(t, c.ctx, c.pool, c.cliente, "Outra", "2026-06-01", "2026-06-30", c.emissora)
	alheia := mcptest.Regra(t, c.ctx, c.pool, outra, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	h := c.handlerRegras()
	r := roteadorRegras(h)

	rec := chamaRegra(t, r, http.MethodPut,
		"/campaigns/"+c.campanha.String()+"/distribution-rules/"+alheia.String(),
		c.corpoRegra(c.tipoA, "14:00", "18:00", 9))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "not found")
	atual, err := h.Repo.Get(c.ctx, alheia)
	require.NoError(t, err)
	require.Equal(t, "08:00", atual.TimeStart, "a regra da outra campanha não foi tocada")
	require.EqualValues(t, 1, atual.PlaysPerDay)
}

func TestRegraHTTP_F38a_DeleteDeRegraDeOutraCampanhaDa404ESemEfeito(t *testing.T) {
	c := novoCenarioRegra(t)
	outra := mcptest.Campanha(t, c.ctx, c.pool, c.cliente, "Outra", "2026-06-01", "2026-06-30", c.emissora)
	alheia := mcptest.Regra(t, c.ctx, c.pool, outra, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	h := c.handlerRegras()
	r := roteadorRegras(h)

	rec := chamaRegra(t, r, http.MethodDelete,
		"/campaigns/"+c.campanha.String()+"/distribution-rules/"+alheia.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	_, err := h.Repo.Get(c.ctx, alheia)
	require.NoError(t, err, "a regra da outra campanha continua lá")
}

func TestRegraHTTP_F38a_RegraInexistenteDa404(t *testing.T) {
	c := novoCenarioRegra(t)
	r := roteadorRegras(c.handlerRegras())
	alvo := "/campaigns/" + c.campanha.String() + "/distribution-rules/" + uuid.NewString()

	require.Equal(t, http.StatusNotFound, chamaRegra(t, r, http.MethodPut, alvo, c.corpoRegra(c.tipoA, "08:00", "12:00", 1)).Code)
	require.Equal(t, http.StatusNotFound, chamaRegra(t, r, http.MethodDelete, alvo, nil).Code)
}

func TestRegraHTTP_F38b_TrocaDeTipoRecategorizaOTipoAntigo(t *testing.T) {
	c := novoCenarioRegra(t)
	regra := mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoA, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)
	h := c.handlerRegras()
	require.NoError(t, h.Repo.RecategorizeForRule(c.ctx, regra))
	c.esperaCategoria(t, "in_slot")
	r := roteadorRegras(h)

	// Mesma faixa e mesma meta; só o TIPO muda de A (o do material da tocada) para B.
	rec := chamaRegra(t, r, http.MethodPut,
		"/campaigns/"+c.campanha.String()+"/distribution-rules/"+regra.String(),
		c.corpoRegra(c.tipoB, "08:00", "12:00", 1))

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	// O tipo A ficou sem regra: a tocada vira bonificação (antes ficava in_slot).
	c.esperaCategoria(t, "bonus")
}
