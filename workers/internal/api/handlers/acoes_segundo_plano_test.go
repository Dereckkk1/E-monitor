package handlers

// acoes_segundo_plano_test.go — prova que o campo EmSegundoPlano dos handlers
// chega às ações: com um executor síncrono, a recategorização que em produção é
// uma goroutine já terminou quando a resposta volta (sem Eventually).

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestRegraHTTP_EmSegundoPlanoInjetadoRodaAntesDeResponder(t *testing.T) {
	c := novoCenarioRegra(t)
	h := c.handlerRegras()
	h.EmSegundoPlano = mcptest.Sincrono
	r := roteadorRegras(h)

	rec := chamaRegra(t, r, http.MethodPost, "/campaigns/"+c.campanha.String()+"/distribution-rules",
		c.corpoRegra(c.tipoA, "08:00", "12:00", 1))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	p, b := c.categoria(t)
	require.Equal(t, "in_slot", p, "sem espera: o gatilho rodou dentro da chamada")
	require.Equal(t, "in_slot", b)
}

func TestMaterialTipoHTTP_EmSegundoPlanoInjetadoRodaAntesDeResponder(t *testing.T) {
	c := novoCenarioTipo(t)
	c.h.EmSegundoPlano = mcptest.Sincrono
	mcptest.Regra(t, c.ctx, c.pool, c.campanha, c.tipoB, c.emissora, "2026-06-01", "2026-06-30", 127, "08:00", "12:00", 1)

	rec := c.patchTipo(t, c.material, `{"type_id":"`+c.tipoB.String()+`"}`)

	require.Equal(t, http.StatusNoContent, rec.Code)
	p, _ := c.categoria(t)
	require.Equal(t, "in_slot", p, "sem espera: o gatilho rodou dentro da chamada")
}
