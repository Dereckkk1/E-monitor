package acoes

// campanhas_ciclo_test.go — cancelar, apagar, CPM fixo e preços (Fatia 4, Task 10).

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func (c *cenario) campanhaNova(t *testing.T, nome string) uuid.UUID {
	t.Helper()
	return mcptest.Campanha(t, c.ctx, c.pool, c.cliente, nome, "2026-06-01", "2026-06-30", c.emissora)
}

func TestCancelarCampanha_CancelaParaOsWorkersEDevolveACampanha(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Cancelar")

	out, err := c.srv.CancelarCampanha(c.ctx, id)

	require.NoError(t, err)
	require.Equal(t, id, out.ID)
	require.Equal(t, "cancelada", out.Status)
	var quando *string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT cancelled_at::text FROM campaigns WHERE id=$1`, id).Scan(&quando))
	require.NotNil(t, quando)
	require.Equal(t, 1, sf.Contagem("StopWorkers", id))
}

func TestCancelarCampanha_SemSupervisorSoGrava(t *testing.T) {
	c := novoCenario(t)
	out, err := c.srv.CancelarCampanha(c.ctx, c.campanhaNova(t, "Cancelar sem sup"))
	require.NoError(t, err)
	require.Equal(t, "cancelada", out.Status)
}

func TestCancelarCampanha_JaCanceladaEhEstadoTerminalEInexistenteNaoEncontrado(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Cancelar 2x")
	_, err := c.srv.CancelarCampanha(c.ctx, id)
	require.NoError(t, err)

	_, err = c.srv.CancelarCampanha(c.ctx, id)
	r := recusaDe(t, err)
	require.Equal(t, CodigoEstadoTerminal, r.Codigo)
	require.Equal(t, "cancelada", r.Dados["estado_atual"])
	require.Equal(t, 1, sf.Contagem("StopWorkers", id), "a recusa não derruba worker de novo")

	_, err = c.srv.CancelarCampanha(c.ctx, uuid.New())
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}

func (c *cenario) existeCampanha(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM campaigns WHERE id=$1`, id).Scan(&n))
	return n == 1
}

func TestApagarCampanha_CancelaDerrubaOsWorkersEApaga(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Apagar")

	require.NoError(t, c.srv.ApagarCampanha(c.ctx, id))

	require.False(t, c.existeCampanha(t, id))
	require.Equal(t, 1, sf.Contagem("StopWorkers", id))
}

func TestApagarCampanha_InexistenteEhRecusaComAlvoInexistente(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := uuid.New()

	err := c.srv.ApagarCampanha(c.ctx, id)

	r := recusaDe(t, err)
	require.Equal(t, CodigoNaoEncontrado, r.Codigo)
	require.ErrorIs(t, err, ErrAlvoInexistente)
	require.Zero(t, sf.Contagem("StopWorkers", id))
}

func TestApagarCampanha_ProjecaoDeOutraCampanhaRecusaAntesDeCancelar(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	outra := c.campanhaNova(t, "Irmã")
	var detID uuid.UUID
	var quando = c.quando
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT id, detected_at FROM detections WHERE id=$1`, c.tocada).Scan(&detID, &quando))
	require.NoError(t, catalog.NewDetectionCampaigns(c.pool).InsertProjections(c.ctx, detID, quando,
		[]catalog.Projection{{CampaignID: outra, CommercialID: c.material, Category: "bonus"}}))

	err := c.srv.ApagarCampanha(c.ctx, c.campanha)

	r := recusaDe(t, err)
	require.Equal(t, CodigoCampanhaComProjecoes, r.Codigo)
	require.Equal(t, 1, r.Dados["foreign_projections"])
	require.Zero(t, sf.Contagem("StopWorkers", c.campanha), "recusa ANTES de cancelar")
	require.True(t, c.existeCampanha(t, c.campanha))
}

// exigeIntacta: a campanha segue lá, no estado em que estava, sem chamada ao supervisor.
func (c *cenario) exigeCampanhaIntacta(t *testing.T, id uuid.UUID, estadoAntes string, sf *mcptest.SupervisorFalso) {
	t.Helper()
	var estado string
	var canceladaEm *time.Time
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT status, cancelled_at FROM campaigns WHERE id=$1`, id).Scan(&estado, &canceladaEm))
	require.Equal(t, estadoAntes, estado)
	require.Nil(t, canceladaEm)
	require.Empty(t, sf.Chamadas(), "nenhuma chamada ao supervisor")
}

func TestApagarCampanha_ComLoteManualRecusaSemMexerEmNada(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Com lote")
	var antes string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT status FROM campaigns WHERE id=$1`, id).Scan(&antes))
	_, err := c.pool.Exec(c.ctx,
		`INSERT INTO manual_proof_batches (campaign_id, station_id, proof_pdf_key, proof_pdf_size) VALUES ($1,$2,'k',1)`, id, c.emissora)
	require.NoError(t, err)

	err = c.srv.ApagarCampanha(c.ctx, id)

	require.Equal(t, CodigoCampanhaComLoteManual, recusaDe(t, err).Codigo)
	c.exigeCampanhaIntacta(t, id, antes, sf)
}

func TestApagarCampanha_EmPosVendaRecusaSemMexerEmNada(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Em pós-venda")
	var antes string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT status FROM campaigns WHERE id=$1`, id).Scan(&antes))
	mcptest.PosVenda(t, c.ctx, c.pool, c.cliente, id, "Fechamento", "2026-06-01", "2026-06-30", false, nil)

	err := c.srv.ApagarCampanha(c.ctx, id)

	require.Equal(t, CodigoCampanhaEmPosVenda, recusaDe(t, err).Codigo)
	c.exigeCampanhaIntacta(t, id, antes, sf)
}

// supervisorQueSemeiaProjecao injeta uma projeção alheia quando os workers são
// derrubados — isto é, DEPOIS do cancelamento e ANTES do Delete —, simulando a
// corrida que a checagem prévia não pega.
type supervisorQueSemeiaProjecao struct {
	*mcptest.SupervisorFalso
	semeia func()
}

func (s supervisorQueSemeiaProjecao) StopWorkersForCampaign(id uuid.UUID) {
	s.SupervisorFalso.StopWorkersForCampaign(id)
	s.semeia()
}

// Depois do cancelamento NADA pode voltar como Recusa (diria "nada aconteceu"
// com a campanha já cancelada): é erro interno, mas ainda reconhecível por errors.Is.
func TestApagarCampanha_ProjecaoQueApareceDepoisDoCancelamentoEhErroInternoNaoRecusa(t *testing.T) {
	c := novoCenario(t)
	sf := &mcptest.SupervisorFalso{}
	outra := c.campanhaNova(t, "Irmã tardia")
	c.srv.Supervisor = supervisorQueSemeiaProjecao{sf, func() {
		mcptest.Projecao(t, c.ctx, c.pool, c.tocada, c.quando, outra, c.material, "bonus")
	}}

	err := c.srv.ApagarCampanha(c.ctx, c.campanha)

	require.Error(t, err)
	var r *Recusa
	require.False(t, errors.As(err, &r), "depois do cancelamento não existe Recusa: %v", err)
	require.ErrorIs(t, err, catalog.ErrCampaignHasForeignProjections)
	var estado string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT status FROM campaigns WHERE id=$1`, c.campanha).Scan(&estado))
	require.Equal(t, "cancelada", estado, "o cancelamento ficou gravado")
}

func TestDefinirCPMFixo_GravaLimpaERecusa(t *testing.T) {
	c := novoCenario(t)
	cpm := 12.5

	out, err := c.srv.DefinirCPMFixo(c.ctx, c.campanha, &cpm)
	require.NoError(t, err)
	require.NotNil(t, out.FixedCPM)
	require.InDelta(t, 12.5, *out.FixedCPM, 0.001)

	out, err = c.srv.DefinirCPMFixo(c.ctx, c.campanha, nil)
	require.NoError(t, err)
	require.Nil(t, out.FixedCPM)

	neg := -0.01
	_, err = c.srv.DefinirCPMFixo(c.ctx, c.campanha, &neg)
	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "fixed_cpm must be >= 0", r.Mensagem)

	_, err = c.srv.DefinirCPMFixo(c.ctx, uuid.New(), &cpm)
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)
}

// NUMERIC(12,2) não comporta 1e10: é entrada inválida, não erro do banco.
func TestDefinirCPMFixo_AcimaDoLimiteDaColunaEhRecusa(t *testing.T) {
	c := novoCenario(t)

	grande := 1e10
	_, err := c.srv.DefinirCPMFixo(c.ctx, c.campanha, &grande)
	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)

	quaseLa := 9999999999.99
	out, err := c.srv.DefinirCPMFixo(c.ctx, c.campanha, &quaseLa)
	require.NoError(t, err)
	require.NotNil(t, out.FixedCPM)
}

func TestDefinirPrecos_GravaVariasEmissorasNumaTransacao(t *testing.T) {
	c := novoCenario(t)
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Outra preço", 0)
	valor := 900.0

	out, err := c.srv.DefinirPrecos(c.ctx, c.campanha, []catalog.EntradaPreco{
		{StationID: c.emissora, Mode: catalog.PricingModeConsolidated, ConsolidatedValue: &valor},
		{StationID: outra, Mode: catalog.PricingModePerInsertion, PerType: []catalog.TypePricing{{TypeID: c.tipoA, UnitValue: 30}}},
	})

	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, c.emissora, out[0].StationID)
	require.Equal(t, catalog.PricingModeConsolidated, out[0].Mode)
	require.Equal(t, outra, out[1].StationID)
	require.Equal(t, []catalog.TypePricing{{TypeID: c.tipoA, UnitValue: 30}}, out[1].PerType)
}

func TestDefinirPrecos_EntradaInvalidaNaoGravaNada(t *testing.T) {
	c := novoCenario(t)
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Outra preço 2", 0)
	valor := 900.0

	_, err := c.srv.DefinirPrecos(c.ctx, c.campanha, []catalog.EntradaPreco{
		{StationID: c.emissora, Mode: catalog.PricingModeConsolidated, ConsolidatedValue: &valor},
		{StationID: outra, Mode: "xx"},
	})

	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, catalog.ErrPricingInvalidMode.Error(), r.Mensagem, "a rota devolve este texto no 422")
	require.ErrorIs(t, err, catalog.ErrPricingInvalidMode)
	require.Zero(t, c.contaPrecos(t))
}

// Atomicidade: a 2ª entrada passa na validação mas o banco recusa (tipo
// inexistente) — a 1ª não pode ter ficado gravada.
func TestDefinirPrecos_FalhaDoBancoNaSegundaEntradaDesfazAPrimeira(t *testing.T) {
	c := novoCenario(t)
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Outra preço 3", 0)
	valor := 900.0

	_, err := c.srv.DefinirPrecos(c.ctx, c.campanha, []catalog.EntradaPreco{
		{StationID: c.emissora, Mode: catalog.PricingModeConsolidated, ConsolidatedValue: &valor},
		{StationID: outra, Mode: catalog.PricingModePerInsertion, PerType: []catalog.TypePricing{{TypeID: uuid.New(), UnitValue: 1}}},
	})

	r := recusaDe(t, err)
	require.Equal(t, CodigoEntradaInvalida, r.Codigo)
	require.Equal(t, "tipo de material inexistente", r.Mensagem)
	require.Zero(t, c.contaPrecos(t), "nenhuma emissora gravada")
}

func TestDefinirPrecos_LoteVazioRepetidoOuTipoRepetidoEhEntradaInvalida(t *testing.T) {
	c := novoCenario(t)
	valor := 10.0
	cons := catalog.EntradaPreco{StationID: c.emissora, Mode: catalog.PricingModeConsolidated, ConsolidatedValue: &valor}
	porTipo := func(tipos ...uuid.UUID) catalog.EntradaPreco {
		e := catalog.EntradaPreco{StationID: c.emissora, Mode: catalog.PricingModePerInsertion}
		for _, ty := range tipos {
			e.PerType = append(e.PerType, catalog.TypePricing{TypeID: ty, UnitValue: 1})
		}
		return e
	}

	for nome, lote := range map[string][]catalog.EntradaPreco{
		"vazio":             {},
		"nil":               nil,
		"emissora repetida": {cons, cons},
		"tipo repetido":     {porTipo(c.tipoA, c.tipoA)},
	} {
		_, err := c.srv.DefinirPrecos(c.ctx, c.campanha, lote)
		require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo, nome)
	}
	require.Zero(t, c.contaPrecos(t), "nada gravado")
}

// O resultado segue a ordem da entrada, qualquer que seja a ordem em que as
// emissoras foram escritas (por station_id, contra deadlock).
func TestDefinirPrecos_ResultadoNaOrdemDaEntrada(t *testing.T) {
	c := novoCenario(t)
	outra := mcptest.Emissora(t, c.ctx, c.pool, "Outra ordem", 0)
	valor := 5.0
	a := catalog.EntradaPreco{StationID: c.emissora, Mode: catalog.PricingModeConsolidated, ConsolidatedValue: &valor}
	b := catalog.EntradaPreco{StationID: outra, Mode: catalog.PricingModePerInsertion, PerType: []catalog.TypePricing{{TypeID: c.tipoA, UnitValue: 3}, {TypeID: c.tipoB, UnitValue: 4}}}

	for _, lote := range [][]catalog.EntradaPreco{{a, b}, {b, a}} {
		out, err := c.srv.DefinirPrecos(c.ctx, c.campanha, lote)
		require.NoError(t, err)
		require.Len(t, out, 2)
		require.Equal(t, lote[0].StationID, out[0].StationID)
		require.Equal(t, lote[1].StationID, out[1].StationID)
		for _, sp := range out {
			if sp.StationID == outra {
				require.Len(t, sp.PerType, 2)
				require.Nil(t, sp.ConsolidatedValue)
			} else {
				require.Empty(t, sp.PerType)
				require.NotNil(t, sp.ConsolidatedValue)
			}
		}
	}
}

func (c *cenario) contaPrecos(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM campaign_station_pricing WHERE campaign_id=$1`, c.campanha).Scan(&n))
	return n
}

// supervisorQueSemeiaTocada persiste uma tocada DA campanha quando os workers são derrubados —
// depois do cancelamento e antes do DELETE —, a corrida que a conferência prévia (feita em outro
// comando, antes de tudo) não pega: o pipeline gravando uma veiculação enquanto a campanha é apagada.
type supervisorQueSemeiaTocada struct {
	*mcptest.SupervisorFalso
	semeia func()
}

func (s supervisorQueSemeiaTocada) StopWorkersForCampaign(id uuid.UUID) {
	s.SupervisorFalso.StopWorkersForCampaign(id)
	s.semeia()
}

func (c *cenario) tocadasDaCampanha(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM detections WHERE campaign_id=$1`, id).Scan(&n))
	return n
}

// Com ExigirSemHistorico (o MCP), a veiculação que aparece depois da conferência e do cancelamento
// aborta o delete: nada é apagado, a campanha fica cancelada, e o erro é *ErroAposEscrita — nunca
// Recusa, porque o cancelamento já está gravado —, com o texto que diz isso à pessoa.
func TestApagarCampanhaCom_ExigirSemHistorico_TocadaQueApareceDepoisDoCancelamentoSobrevive(t *testing.T) {
	c := novoCenario(t)
	id := c.campanhaNova(t, "Sem histórico ainda")
	c.srv.Supervisor = supervisorQueSemeiaTocada{&mcptest.SupervisorFalso{}, func() {
		mcptest.Veiculacao(t, c.ctx, c.pool, id, c.material, c.emissora, "bonus", c.quando)
	}}

	err := c.srv.ApagarCampanhaCom(c.ctx, id, OpcoesApagarCampanha{ExigirSemHistorico: true})

	require.Error(t, err)
	var r *Recusa
	require.False(t, errors.As(err, &r), "depois do cancelamento não existe Recusa: %v", err)
	var pos *ErroAposEscrita
	require.ErrorAs(t, err, &pos)
	require.ErrorIs(t, err, catalog.ErrCampaignHasHistory)
	require.Contains(t, pos.Mensagem, "Sem histórico ainda")
	require.Contains(t, pos.Mensagem, "foi cancelada")
	require.Contains(t, pos.Mensagem, "NÃO foi apagada")
	require.NotContains(t, pos.Mensagem, "SQLSTATE")
	require.True(t, c.existeCampanha(t, id), "a campanha não foi apagada")
	require.Equal(t, 1, c.tocadasDaCampanha(t, id), "a veiculação que apareceu sobreviveu")
	var estado string
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT status FROM campaigns WHERE id=$1`, id).Scan(&estado))
	require.Equal(t, "cancelada", estado, "o cancelamento ficou gravado")
}

// A rota de apagar segue como sempre foi: apaga a campanha e as veiculações dela (é o que o
// ExigirSemHistorico existe para NÃO fazer no MCP).
func TestApagarCampanha_RotaContinuaApagandoAsTocadasDaCampanha(t *testing.T) {
	c := novoCenario(t)
	id := c.campanhaNova(t, "A rota apaga tudo")
	c.srv.Supervisor = supervisorQueSemeiaTocada{&mcptest.SupervisorFalso{}, func() {
		mcptest.Veiculacao(t, c.ctx, c.pool, id, c.material, c.emissora, "bonus", c.quando)
	}}

	require.NoError(t, c.srv.ApagarCampanha(c.ctx, id))

	require.False(t, c.existeCampanha(t, id))
	require.Zero(t, c.tocadasDaCampanha(t, id))
}

func TestApagarCampanhaCom_ExigirSemHistorico_SemHistoricoApagaComoSempre(t *testing.T) {
	c, _, sf := novoCenarioCampanha(t)
	id := c.campanhaNova(t, "Limpa")

	require.NoError(t, c.srv.ApagarCampanhaCom(c.ctx, id, OpcoesApagarCampanha{ExigirSemHistorico: true}))

	require.False(t, c.existeCampanha(t, id))
	require.Equal(t, 1, sf.Contagem("StopWorkers", id))
	// inexistente segue sendo a Recusa com ErrAlvoInexistente
	err := c.srv.ApagarCampanhaCom(c.ctx, uuid.New(), OpcoesApagarCampanha{ExigirSemHistorico: true})
	require.ErrorIs(t, err, ErrAlvoInexistente)
}
