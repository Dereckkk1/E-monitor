package mcp

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
	"radiocheck/internal/webhook"
)

// supervisorFalso faz as vezes do supervisor de workers (UpdateStations/Reload/StopWorkersForCampaign).
type supervisorFalso struct{}

func (supervisorFalso) UpdateStations(uuid.UUID, []uuid.UUID) error { return nil }
func (supervisorFalso) Reload(uuid.UUID) error                      { return nil }
func (supervisorFalso) StopWorkersForCampaign(uuid.UUID)            {}

// TestModulo_RepassaDepsParaEscritas: Supervisor, Hub e Ligacoes de DepsModulo
// chegam ao acoes.Servico das tools de escrita, e a Leitura é a mesma das
// tools de leitura (previews e resultados no formato do obter_*).
func TestModulo_RepassaDepsParaEscritas(t *testing.T) {
	sup := supervisorFalso{}
	h := hub.New("https://hub.teste", "chave")
	lig := catalog.NewLigacoes(nil)
	m := NovoModulo(DepsModulo{
		Segredo: []byte(mcptest.SegredoTeste), Portal: "https://portal.teste", Log: zap.NewNop(),
		Supervisor: sup, Hub: h, Ligacoes: lig,
	})
	require.NotNil(t, m)
	E := m.cfg.Escritas
	require.NotNil(t, E, "o módulo monta as dependências da escrita")
	require.NotNil(t, E.Acoes)
	require.Equal(t, acoes.SupervisorDeCampanha(sup), E.Acoes.Supervisor)
	require.Equal(t, acoes.AvisoHub(h), E.Acoes.Hub)
	require.Same(t, lig, E.Acoes.Ligacoes)
	require.Equal(t, []byte(mcptest.SegredoTeste), E.Segredo)
	require.Same(t, m.cfg.Leitura, E.Leitura, "a escrita monta previews com a MESMA Leitura das tools obter_*")
}

// BoasVindas e Webhooks de DepsModulo chegam ao Servico; um *webhook.Outbox nil
// (typed-nil) NÃO entra — TestarWebhook diria "indisponível" em vez de dar panic.
func TestModulo_RepassaBoasVindasEWebhooksETrataNilTipado(t *testing.T) {
	bv := acoes.BoasVindasDoServico(nil)
	var outbox *webhook.Outbox // nil tipado, como deliverer.Outbox() sem entregador
	m := NovoModulo(DepsModulo{
		Segredo: []byte(mcptest.SegredoTeste), Portal: "https://portal.teste", Log: zap.NewNop(),
		BoasVindas: bv, Webhooks: outbox,
	})
	require.NotNil(t, m)
	require.Equal(t, bv, m.cfg.Escritas.Acoes.BoasVindas)
	require.Nil(t, m.cfg.Escritas.Acoes.Webhooks, "nil tipado não pode virar interface não-nil")

	real := webhook.NewOutbox(nil, nil, zap.NewNop())
	m = NovoModulo(DepsModulo{
		Segredo: []byte(mcptest.SegredoTeste), Portal: "https://portal.teste", Log: zap.NewNop(),
		Webhooks: real,
	})
	require.NotNil(t, m)
	require.Equal(t, webhook.OutboxEnqueuer(real), m.cfg.Escritas.Acoes.Webhooks)
}

// PosVenda de DepsModulo chega às Escritas (o mesmo serviço das rotas); sem ele fica nil
// de verdade e reenviar_pos_venda recusa com pos_venda_indisponivel.
func TestModulo_RepassaPosVenda(t *testing.T) {
	svc := postsale.New(postsale.Config{Repo: postsale.NewRepo(nil), Log: zap.NewNop()})
	m := NovoModulo(DepsModulo{
		Segredo: []byte(mcptest.SegredoTeste), Portal: "https://portal.teste", Log: zap.NewNop(),
		PosVenda: svc,
	})
	require.NotNil(t, m)
	require.Same(t, svc, m.cfg.Escritas.PosVenda)

	m = NovoModulo(DepsModulo{Segredo: []byte(mcptest.SegredoTeste), Log: zap.NewNop()})
	require.NotNil(t, m)
	require.Nil(t, m.cfg.Escritas.PosVenda)
}

// Sem supervisor nem hub (dev, teste): as interfaces ficam nil — um
// *hub.Client nil dentro da interface passaria no `!= nil` e daria panic.
func TestModulo_SemSupervisorNemHubEscritasSegueSoComBanco(t *testing.T) {
	m := NovoModulo(DepsModulo{Segredo: []byte(mcptest.SegredoTeste), Log: zap.NewNop()})
	require.NotNil(t, m)
	require.NotNil(t, m.cfg.Escritas)
	require.NotNil(t, m.cfg.Escritas.Acoes)
	require.Nil(t, m.cfg.Escritas.Acoes.Supervisor)
	require.Nil(t, m.cfg.Escritas.Acoes.Hub)
	require.Nil(t, m.cfg.Escritas.Acoes.Ligacoes)
}
