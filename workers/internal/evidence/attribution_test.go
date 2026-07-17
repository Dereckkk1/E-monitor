package evidence

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"radiocheck/internal/db"
)

func attrTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// TestResolveAttribution_ReusedBackfillPrefersActiveCampaign: the legacy
// commercial points to a concluded campaign, but the live campaign_materials
// link points to an active one. Attribution must pick the ACTIVE campaign.
func TestResolveAttribution_ReusedBackfillPrefersActiveCampaign(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-client') RETURNING id`).Scan(&clientID))
	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-active', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'attr-spot', 30, '/tmp/a.mp3', $3, 'ready')
		RETURNING short_id`, id, concluded, sha).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'attr-spot', 30, '/tmp/a.mp3', $4, 'ready')`,
		id, shortID, clientID, sha)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, active, id, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, concluded, active)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	detectedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, station, detectedAt)
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, active, gotCamp, "must attribute to the active campaign, not the concluded one")
}

// TestResolveAttribution_PureLegacyCommercialFallback: a commercial with no
// material row resolves via the legacy commercials fallback.
func TestResolveAttribution_PureLegacyCommercialFallback(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-legacy-client') RETURNING id`).Scan(&clientID))
	var camp uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-legacy', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&camp))
	id := uuid.New()
	sha := "sha-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'legacy-spot', 30, '/tmp/l.mp3', $3, 'ready')
		RETURNING short_id`, id, camp, sha).Scan(&shortID))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, camp)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, uuid.New(),
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, camp, gotCamp)
}

// TestResolveAttribution_LateNightLastDayAttributesToActiveCampaign: uma tocada
// às 23:30 BRT do ÚLTIMO dia da campanha (end_date). Em UTC isso é 02:30 do dia
// seguinte. Se a query resolve `detectedAt::date` em sessão UTC, cai no dia
// seguinte, falha o BETWEEN start_date..end_date e escorrega pro fallback legacy
// (que aponta pra campanha concluída) — a classe de má-atribuição noturna do
// audit 2026-07-02 (D1). Com o cast em America/Sao_Paulo a data local é o
// end_date e a atribuição vai pra campanha ativa correta.
func TestResolveAttribution_LateNightLastDayAttributesToActiveCampaign(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-tz-client') RETURNING id`).Scan(&clientID))
	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-tz-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-tz-active', '2026-07-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	sha := "sha-tz-" + id.String()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'attr-tz-spot', 30, '/tmp/tz.mp3', $3, 'ready')
		RETURNING short_id`, id, concluded, sha).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'attr-tz-spot', 30, '/tmp/tz.mp3', $4, 'ready')`,
		id, shortID, clientID, sha)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, active, id, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, concluded, active)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	// 2026-07-31 23:30 BRT == 2026-08-01 02:30 UTC (BRT = UTC-3).
	detectedAt := time.Date(2026, 8, 1, 2, 30, 0, 0, time.UTC)
	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, station, detectedAt)
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, active, gotCamp,
		"tocada 23:30 BRT do end_date deve cair na campanha ativa (data local SP), não no fallback legacy")
}

// TestResolveAllAttributions_LateNightLastDayKeepsProjections: o fan-out F-119
// tem o mesmo cast `detectedAt::date`; uma tocada às 23:30 BRT do end_date de
// duas campanhas ativas deve devolver as 2 projeções, não zero.
func TestResolveAllAttributions_LateNightLastDayKeepsProjections(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('fanout-tz-client') RETURNING id`).Scan(&clientID))
	station := uuid.New()
	sha := "fanout-tz-sha-" + uuid.New().String()

	var campA, campB uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'fanout-tz-A', '2026-07-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campA))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'fanout-tz-B', '2026-07-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campB))

	matA, matB := uuid.New(), uuid.New()
	for _, m := range []uuid.UUID{matA, matB} {
		_, err := pool.Exec(ctx, `
			INSERT INTO materials (id, client_id, title, duration_seconds,
			                       master_storage_path, master_sha256, fingerprint_status)
			VALUES ($1, $2, 'fanout-tz-spot', 30, '/tmp/ftz.mp3', $3, 'ready')`,
			m, clientID, sha)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campA, matA, station)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campB, matB, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id IN ($1,$2)`, matA, matB)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA, matB)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA, campB)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	// 2026-07-31 23:30 BRT == 2026-08-01 02:30 UTC.
	detectedAt := time.Date(2026, 8, 1, 2, 30, 0, 0, time.UTC)
	got, err := resolveAllAttributions(ctx, pool, matA, station, detectedAt)
	require.NoError(t, err)
	require.Len(t, got, 2, "tocada 23:30 BRT do end_date deve manter as 2 projeções (data local SP)")
}

// TestResolveAllAttributions_TwoCampaignsSameMaster: o mesmo áudio (master_sha256
// idêntico) subido como 2 materiais, cada um linkado a uma campanha ativa
// targetando a MESMA estação → resolveAllAttributions devolve uma projeção por
// campanha (cada uma com o seu material). É o fan-out do F-119.
func TestResolveAllAttributions_TwoCampaignsSameMaster(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('fanout-client') RETURNING id`).Scan(&clientID))
	station := uuid.New()
	sha := "fanout-sha-" + uuid.New().String()

	var campA, campB uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'fanout-A', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campA))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'fanout-B', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campB))

	matA, matB := uuid.New(), uuid.New()
	for _, m := range []uuid.UUID{matA, matB} {
		_, err := pool.Exec(ctx, `
			INSERT INTO materials (id, client_id, title, duration_seconds,
			                       master_storage_path, master_sha256, fingerprint_status)
			VALUES ($1, $2, 'fanout-spot', 30, '/tmp/f.mp3', $3, 'ready')`,
			m, clientID, sha)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campA, matA, station)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campB, matB, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id IN ($1,$2)`, matA, matB)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA, matB)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA, campB)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	detectedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	got, err := resolveAllAttributions(ctx, pool, matA, station, detectedAt)
	require.NoError(t, err)
	require.Len(t, got, 2, "uma projeção por campanha que roda o áudio na emissora")

	byCamp := map[uuid.UUID]uuid.UUID{}
	for _, p := range got {
		byCamp[p.CampaignID] = p.CommercialID
	}
	require.Equal(t, matA, byCamp[campA], "campA usa o material dela")
	require.Equal(t, matB, byCamp[campB], "campB usa o material dela")
}
