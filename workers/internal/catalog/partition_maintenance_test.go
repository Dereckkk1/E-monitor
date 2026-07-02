package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnsureMonthPartitions_CreatesAndIdempotent cobre a função de manutenção de
// partições (audit E1): detections, stream_health_events e detection_campaigns
// são PARTITION BY RANGE mas a 0001 criou só 12 meses e nenhum job cria novas —
// por volta de ~abr/2027 todo INSERT falharia. ensure_month_partitions(N) cria
// as partições faltantes N meses à frente, idempotente.
func TestEnsureMonthPartitions_CreatesAndIdempotent(t *testing.T) {
	ctx, pool := newTestDB(t)

	const monthsAhead = 20 // além do horizonte de 12 meses da 0001

	// Cria as partições faltantes até current_month + monthsAhead.
	_, err := pool.Exec(ctx, `SELECT ensure_month_partitions($1::int)`, monthsAhead)
	require.NoError(t, err)
	// Idempotente: 2ª chamada não erra (to_regclass pula as existentes).
	_, err = pool.Exec(ctx, `SELECT ensure_month_partitions($1::int)`, monthsAhead)
	require.NoError(t, err)

	// Nome do mês-alvo (current_month + monthsAhead), computado pelo mesmo
	// critério da função (date_trunc('month', CURRENT_DATE)).
	var yyyymm string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT to_char(date_trunc('month', CURRENT_DATE) + make_interval(months => $1::int), 'YYYY_MM')`,
		monthsAhead).Scan(&yyyymm))

	for _, prefix := range []string{"detections", "stream_health", "detection_campaigns"} {
		partName := prefix + "_" + yyyymm
		var present bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, partName).Scan(&present))
		require.True(t, present, "partição %s deve existir após ensure_month_partitions", partName)
	}
}
