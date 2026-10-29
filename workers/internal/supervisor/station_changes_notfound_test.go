package supervisor

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Campanha inexistente em UpdateStations devolve um erro que EMBRULHA o sentinela
// ErrCampaignNotFound (quem chama usa errors.Is, não casa texto) e mantém a
// mensagem de sempre.
func TestUpdateStations_CampanhaInexistenteEmbrulhaOSentinela(t *testing.T) {
	f := newReconcileFixture(t)
	id := uuid.New()

	err := f.s.UpdateStations(id, nil)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCampaignNotFound), "o erro tem de embrulhar ErrCampaignNotFound: %v", err)
	require.Contains(t, err.Error(), "campaign not found")
	require.Contains(t, err.Error(), id.String())
}
