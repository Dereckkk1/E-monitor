package hub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTipo_IdaEVolta(t *testing.T) {
	require.Equal(t, "ideia", TipoParaHub("feature"))
	require.Equal(t, "feature", TipoDoHub("ideia"))
	for _, t2 := range []string{"bug", "melhoria", "duvida"} {
		require.Equal(t, t2, TipoParaHub(t2))
		require.Equal(t, t2, TipoDoHub(t2))
	}
}

func TestEventoDoHub(t *testing.T) {
	require.Equal(t, "created", EventoDoHub("criada"))
	require.Equal(t, "status_changed", EventoDoHub("status"))
	require.Equal(t, "priority_changed", EventoDoHub("prioridade"))
	require.Equal(t, "feedback_given", EventoDoHub("resposta"))
	require.Equal(t, "reopened", EventoDoHub("reaberta"))
	// Os que o E-monitor não tinha passam com o nome do hub; a tela os nomeia.
	require.Equal(t, "retirada", EventoDoHub("retirada"))
	require.Equal(t, "sua_vez", EventoDoHub("sua_vez"))
}
