package acoes

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestDefinirAtivoCliente(t *testing.T) {
	c := novoCenario(t)
	out, err := c.srv.DefinirAtivoCliente(c.ctx, c.cliente, false)
	require.NoError(t, err)
	require.False(t, out.IsActive)
	out, err = c.srv.DefinirAtivoCliente(c.ctx, c.cliente, true)
	require.NoError(t, err)
	require.True(t, out.IsActive)

	_, err = c.srv.DefinirAtivoCliente(c.ctx, uuid.New(), false)
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}

func TestApagarCliente(t *testing.T) {
	c := novoCenario(t)
	livre := mcptest.Cliente(t, c.ctx, c.pool, "Cliente Livre")
	require.NoError(t, c.srv.ApagarCliente(c.ctx, livre))
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, c.srv.ApagarCliente(c.ctx, livre)).Codigo)

	r := recusaDe(t, c.srv.ApagarCliente(c.ctx, c.cliente))
	require.Equal(t, CodigoClienteComVinculos, r.Codigo)
	require.Equal(t, map[string]any{"campanhas": 1, "materiais": 1, "usuarios": 0}, r.Dados)
}

func TestApagarMaterial(t *testing.T) {
	c := novoCenario(t)
	// vinculado a campanha: recusa própria (não "material não encontrado").
	r := recusaDe(t, c.srv.ApagarMaterial(c.ctx, c.material))
	require.Equal(t, CodigoMaterialVinculado, r.Codigo)
	require.Equal(t, "material vinculado a campanha", r.Mensagem)

	solto := mcptest.Material(t, c.ctx, c.pool, c.cliente, c.tipoA, "Solto")
	require.NoError(t, c.srv.ApagarMaterial(c.ctx, solto))

	err := c.srv.ApagarMaterial(c.ctx, solto)
	require.ErrorIs(t, err, ErrAlvoInexistente)
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}
