package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestDedupSuppressions_Record grava uma supressão e lê de volta, incluindo as
// duas confianças — o sinal que importa (suppressed_confidence > kept_confidence
// = provável veiculação real morta por corte mais fraco).
func TestDedupSuppressions_Record(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDedupSuppressions(pool)
	station := uuid.New()
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM dedup_suppressions WHERE station_id=$1`, station) })

	require.NoError(t, repo.Record(ctx, DedupSuppression{
		StationID: station, SuppressedShortID: 15, KeptShortID: 30,
		SuppressedDuration: 15, KeptDuration: 30,
		SuppressedConfidence: 0.79, KeptConfidence: 0.16,
		BroadcastStart: time.Now().Add(-time.Minute).UTC(),
		DetectedAt:     time.Now().UTC(),
		Reason:         "shorter_cut",
	}))

	var suppShort, keptShort int32
	var suppConf, keptConf float64
	var reason string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT suppressed_short_id, kept_short_id, suppressed_confidence, kept_confidence, reason
		FROM dedup_suppressions WHERE station_id=$1`, station).Scan(
		&suppShort, &keptShort, &suppConf, &keptConf, &reason))
	require.Equal(t, int32(15), suppShort)
	require.Equal(t, int32(30), keptShort)
	require.InDelta(t, 0.79, suppConf, 0.0001)
	require.InDelta(t, 0.16, keptConf, 0.0001)
	require.Equal(t, "shorter_cut", reason)
}
