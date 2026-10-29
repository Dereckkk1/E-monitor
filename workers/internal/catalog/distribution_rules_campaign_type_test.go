package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// RecategorizeForCampaignType refaz só o TIPO pedido, na campanha toda. É o que
// a edição de regra que TROCA o tipo usa para curar o tipo ANTIGO (o
// RecategorizeForRule só enxerga o tipo novo). Sem regra nenhuma, uma tocada
// gravada como in_slot cai para bonus quando o tipo dela é recategorizado — e
// a do outro tipo fica como estava.
func TestRecategorizeForCampaignType_SoRefazOTipoPedido(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "rct "+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	st := insSeedStationNoProfile(t, ctx, pool, "RCT est "+uuid.NewString()[:6])
	tipoA, matA := insSeedTypeAndMaterial(t, ctx, pool, cli, "rct-a")
	_, matB := insSeedTypeAndMaterial(t, ctx, pool, cli, "rct-b")
	tocadaA := hvTocada(t, ctx, pool, camp, matA, st, hvQuando("2026-10-05", "10:00"), "")
	tocadaB := hvTocada(t, ctx, pool, camp, matB, st, hvQuando("2026-10-05", "11:00"), "")
	repo := NewDistributionRules(pool)

	require.NoError(t, repo.RecategorizeForCampaignType(ctx, camp, tipoA))

	cat := func(id uuid.UUID) string {
		var c string
		require.NoError(t, pool.QueryRow(ctx, `SELECT category FROM detection_campaigns WHERE detection_id = $1`, id).Scan(&c))
		return c
	}
	require.Equal(t, "bonus", cat(tocadaA), "o tipo pedido foi recalculado (sem regra → bonus)")
	require.Equal(t, "in_slot", cat(tocadaB), "o outro tipo não é tocado")
}

func TestRecategorizeForCampaignType_CampanhaInexistenteEhErro(t *testing.T) {
	ctx, pool := newTestDB(t)
	require.Error(t, NewDistributionRules(pool).RecategorizeForCampaignType(ctx, uuid.New(), uuid.New()))
}
