package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestNovaLeitura_TodosOsRepositorios(t *testing.T) {
	L := NovaLeitura(nil)
	require.NotNil(t, L.Clientes)
	require.NotNil(t, L.PMMTarget)
	require.NotNil(t, L.Campanhas)
	require.NotNil(t, L.MateriaisCampanha)
	require.NotNil(t, L.Regras)
	require.NotNil(t, L.Ajustes)
	require.NotNil(t, L.Precos)
	require.NotNil(t, L.Materiais)
	require.NotNil(t, L.TiposMaterial)
	require.NotNil(t, L.Emissoras)
	require.NotNil(t, L.Veiculacoes)
	require.NotNil(t, L.Grade)
	require.NotNil(t, L.Notificacoes)
	require.NotNil(t, L.Usuarios)
	require.NotNil(t, L.BoasVindas)
}

func TestFixturesDeCatalogo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Fixture")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Fixture", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot fixture")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha Fixture", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, mat, est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "10:00"))

	L := NovaLeitura(pool)
	rows, err := L.Grade.ListByCampaign(ctx, camp, mcptest.Dia("2026-10-10"), mcptest.Dia("2026-10-10"))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 2, rows[0].Expected)
	require.EqualValues(t, 1, rows[0].InSlot)
}
