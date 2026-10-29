package acoes

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func (c *cenario) emissorasDoVinculo(t *testing.T, material uuid.UUID) (emissoras []uuid.UUID, existe bool) {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT count(*) FROM campaign_materials WHERE campaign_id=$1 AND material_id=$2`, c.campanha, material).Scan(&n))
	if n == 0 {
		return nil, false
	}
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT target_stations FROM campaign_materials WHERE campaign_id=$1 AND material_id=$2`, c.campanha, material).Scan(&emissoras))
	return emissoras, true
}

func TestVincularMaterial_GravaERecarregaACampanhaUmaVez(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	outro := mcptest.Material(t, c.ctx, c.pool, c.cliente, c.tipoA, "Outro spot")

	require.NoError(t, c.srv.VincularMaterial(c.ctx, c.campanha, outro, []uuid.UUID{c.emissora}))

	est, ok := c.emissorasDoVinculo(t, outro)
	require.True(t, ok)
	require.Equal(t, []uuid.UUID{c.emissora}, est)
	require.Equal(t, []mcptest.ChamadaSupervisor{{Op: "Reload", Campanha: c.campanha}}, sf.Chamadas())
	// O passado não muda: nada recategoriza ao vincular.
	c.exigeCategoria(t, "bonus")
}

func TestVincularMaterial_RepetirTrocaAsEmissoras(t *testing.T) {
	c, _, _ := novoCenarioCampanha(t)

	require.NoError(t, c.srv.VincularMaterial(c.ctx, c.campanha, c.material, nil))

	est, ok := c.emissorasDoVinculo(t, c.material)
	require.True(t, ok)
	require.Empty(t, est, "o vínculo do cenário tinha a emissora; o upsert sem lista a esvaziou")
}

func TestVinculos_SemSupervisorSoGravam(t *testing.T) {
	c := novoCenario(t) // Supervisor nil
	outro := mcptest.Material(t, c.ctx, c.pool, c.cliente, c.tipoA, "Outro spot")

	require.NoError(t, c.srv.VincularMaterial(c.ctx, c.campanha, outro, nil))
	require.NoError(t, c.srv.TrocarEmissorasMaterial(c.ctx, c.campanha, outro, []uuid.UUID{c.emissora}))
	est, _ := c.emissorasDoVinculo(t, outro)
	require.Equal(t, []uuid.UUID{c.emissora}, est)
	require.NoError(t, c.srv.DesvincularMaterial(c.ctx, c.campanha, outro))
	_, ok := c.emissorasDoVinculo(t, outro)
	require.False(t, ok)
}

// O banco já foi mutado: a falha do reload é só logada, a ação devolve sucesso.
func TestVinculos_FalhaDoReloadNaoFalhaAAcao(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	sf.ErroReload = errors.New("nats caiu")
	outro := mcptest.Material(t, c.ctx, c.pool, c.cliente, c.tipoA, "Outro spot")

	require.NoError(t, c.srv.VincularMaterial(c.ctx, c.campanha, outro, nil))
	require.NoError(t, c.srv.TrocarEmissorasMaterial(c.ctx, c.campanha, outro, []uuid.UUID{c.emissora}))
	require.NoError(t, c.srv.DesvincularMaterial(c.ctx, c.campanha, outro))

	require.Equal(t, 3, sf.Contagem("Reload", c.campanha))
}

func TestVinculos_DesvincularEtrocarInexistenteNaoEhErroEAindaRecarrega(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	inexistente := uuid.New()

	require.NoError(t, c.srv.TrocarEmissorasMaterial(c.ctx, c.campanha, inexistente, nil))
	require.NoError(t, c.srv.DesvincularMaterial(c.ctx, c.campanha, inexistente))

	require.Equal(t, 2, sf.Contagem("Reload", c.campanha))
}

func TestDesvincularMaterial_NaoMudaOPassado(t *testing.T) {
	c, _, _ := novoCenarioCampanha(t)

	require.NoError(t, c.srv.DesvincularMaterial(c.ctx, c.campanha, c.material))

	_, ok := c.emissorasDoVinculo(t, c.material)
	require.False(t, ok)
	c.exigeCategoria(t, "bonus") // a tocada gravada segue na campanha
}

func TestTrocarTipoMaterial_RecategorizaAsCampanhasDoMaterial(t *testing.T) {
	c := novoCenario(t)
	// A regra é do tipo B; o material (e a tocada) é do tipo A → a tocada é bonus.
	_, err := c.srv.CriarRegra(c.ctx, c.campanha, c.entradaRegra(c.tipoB, "08:00", "12:00", 1))
	require.NoError(t, err)
	c.exigeCategoria(t, "bonus")

	require.NoError(t, c.srv.TrocarTipoMaterial(c.ctx, c.material, &c.tipoB))

	// Sem espera: o EmSegundoPlano do cenário é síncrono. O material agora casa a regra do tipo B.
	c.exigeCategoria(t, "in_slot")
	m, gerr := c.srv.Materiais.Get(c.ctx, c.material)
	require.NoError(t, gerr)
	require.Equal(t, c.tipoB, *m.TypeID)
}

func TestTrocarTipoMaterial_NilLimpaOTipoESemRegrasNaoRecategoriza(t *testing.T) {
	c := novoCenario(t)
	c.srv.Regras = nil
	disparos := 0
	c.srv.EmSegundoPlano = func(f func()) { disparos++; f() }

	require.NoError(t, c.srv.TrocarTipoMaterial(c.ctx, c.material, nil))

	m, err := c.srv.Materiais.Get(c.ctx, c.material)
	require.NoError(t, err)
	require.Nil(t, m.TypeID)
	require.Zero(t, disparos, "sem Regras no Servico a recategorização é pulada")
}

func TestTrocarTipoMaterial_TipoInexistenteViraRecusa(t *testing.T) {
	c := novoCenario(t)
	disparos := 0
	c.srv.EmSegundoPlano = func(f func()) { disparos++; f() }
	tipoFalso := uuid.New()

	err := c.srv.TrocarTipoMaterial(c.ctx, c.material, &tipoFalso)

	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Contains(t, r.Mensagem, "tipo de material")
	require.Zero(t, disparos, "falhou antes de gravar: não recategoriza")
}

// A rota sempre respondeu 204 para material inexistente (o UPDATE não acha
// linha); quem precisa saber confere antes.
func TestTrocarTipoMaterial_MaterialInexistenteNaoEhErro(t *testing.T) {
	c := novoCenario(t)
	require.NoError(t, c.srv.TrocarTipoMaterial(c.ctx, uuid.New(), &c.tipoB))
}
