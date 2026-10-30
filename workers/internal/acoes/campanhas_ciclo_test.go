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

func (c *cenario) contaPrecos(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx, `SELECT count(*) FROM campaign_station_pricing WHERE campaign_id=$1`, c.campanha).Scan(&n))
	return n
}
