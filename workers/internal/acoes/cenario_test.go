package acoes

// cenario_test.go — fixture comum dos testes de ação: um cliente, uma emissora,
// dois tipos, um material do tipo A, uma campanha de junho/2026 e UMA tocada às
// 10:30 (SP) de quarta, 10/06, gravada como bonus.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

type cenario struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	cliente  uuid.UUID
	emissora uuid.UUID
	tipoA    uuid.UUID
	tipoB    uuid.UUID
	material uuid.UUID
	campanha uuid.UUID
	tocada   uuid.UUID
	quando   time.Time
	srv      *Servico
}

// sincrono roda o gatilho assíncrono na hora: o teste observa o efeito sem espera.
func sincrono(f func()) { f() }

func novoCenario(t *testing.T) *cenario {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	c := &cenario{ctx: ctx, pool: pool}
	c.cliente = mcptest.Cliente(t, ctx, pool, "Cliente Ação")
	c.emissora = mcptest.Emissora(t, ctx, pool, "Rádio Ação", 1000)
	c.tipoA = mcptest.TipoMaterial(t, ctx, pool, "Spot A")
	c.tipoB = mcptest.TipoMaterial(t, ctx, pool, "Spot B")
	c.material = mcptest.Material(t, ctx, pool, c.cliente, c.tipoA, "Spot Ação")
	c.campanha = mcptest.Campanha(t, ctx, pool, c.cliente, "Campanha Ação", "2026-06-01", "2026-06-30", c.emissora)
	mcptest.Vincular(t, ctx, pool, c.campanha, c.material, c.emissora)
	c.quando = mcptest.Quando("2026-06-10", "10:30")
	c.tocada = mcptest.Veiculacao(t, ctx, pool, c.campanha, c.material, c.emissora, "bonus", c.quando)
	c.srv = NovoServico(pool, nil, nil, nil)
	c.srv.EmSegundoPlano = sincrono
	return c
}

// categoria lê a categoria da projeção canônica e da tocada-base.
func (c *cenario) categoria(t *testing.T) (projecao, base string) {
	t.Helper()
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT dc.category, d.category
		   FROM detection_campaigns dc
		   JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		  WHERE dc.detection_id = $1`, c.tocada).Scan(&projecao, &base))
	return projecao, base
}

func (c *cenario) exigeCategoria(t *testing.T, esperada string) {
	t.Helper()
	p, b := c.categoria(t)
	require.Equal(t, esperada, p, "categoria da projeção")
	require.Equal(t, esperada, b, "categoria da tocada-base")
}

func dia(s string) time.Time { return mcptest.Dia(s) }

// regraTarde é uma regra geral do tipo informado na emissora do cenário.
func (c *cenario) entradaRegra(tipo uuid.UUID, ini, fim string, porDia int16) EntradaRegra {
	return EntradaRegra{
		TipoID: tipo, EmissoraIDs: []uuid.UUID{c.emissora}, MaterialIDs: []uuid.UUID{},
		Nome: "Regra", Inicio: dia("2026-06-01"), Fim: dia("2026-06-30"),
		MascaraDias: 127, HoraInicio: ini, HoraFim: fim, InsercoesPorDia: porDia,
	}
}

func (c *cenario) contaRegras(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM distribution_rules WHERE campaign_id = $1`, c.campanha).Scan(&n))
	return n
}
