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
)

// supervisorFalso faz as vezes do supervisor de workers (UpdateStations/Reload).
type supervisorFalso struct{}

func (supervisorFalso) UpdateStations(uuid.UUID, []uuid.UUID) error { return nil }
func (supervisorFalso) Reload(uuid.UUID) error                      { return nil }

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
