package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestClients_HubIDs(t *testing.T) {
	ctx, pool := newTestDB(t)
	com := insSeedClient(t, ctx, pool, "Com ponte "+uuid.NewString()[:6])
	sem := insSeedClient(t, ctx, pool, "Sem ponte "+uuid.NewString()[:6])
	hubID := "hub-" + uuid.NewString()
	_, err := pool.Exec(ctx, `UPDATE clients SET hub_id = $1 WHERE id = $2`, hubID, com)
	require.NoError(t, err)

	got, err := NewClients(pool).HubIDs(ctx, []uuid.UUID{com, sem})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{com: hubID}, got)

	vazio, err := NewClients(pool).HubIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, vazio)
}

func TestCampaigns_ListByHubCodeECancelledAtFor(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Hub code "+uuid.NewString()[:6])
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-11-01", "2026-11-30")
	c := insSeedCampaign(t, ctx, pool, cli, "2026-12-01", "2026-12-31")
	_, err := pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = ANY($1)`, []uuid.UUID{a, b})
	require.NoError(t, err)

	repo := NewCampaigns(pool)
	got, err := repo.ListByHubCode(ctx, "EH-2346AB")
	require.NoError(t, err)
	require.Len(t, got, 2, "o código não é único: dois PIs podem dividi-lo")
	require.Equal(t, b, got[0].ID, "mais nova primeiro")

	nenhuma, err := repo.ListByHubCode(ctx, "EH-ZZZZZZ")
	require.NoError(t, err)
	require.Empty(t, nenhuma)

	_, err = pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelada', cancelled_at = now() WHERE id = $1`, c)
	require.NoError(t, err)
	canc, err := repo.CancelledAtFor(ctx, []uuid.UUID{a, c})
	require.NoError(t, err)
	require.Len(t, canc, 1)
	require.WithinDuration(t, time.Now(), canc[c], time.Minute)
}

func TestStations_AnatelECoverageCities(t *testing.T) {
	ctx, pool := newTestDB(t)
	st := insSeedStationNoProfile(t, ctx, pool, "Anatel "+uuid.NewString()[:6])
	repo := NewStations(pool)

	vazio, err := repo.Anatel(ctx, st)
	require.NoError(t, err)
	require.Nil(t, vazio.Class, "sem cruzamento: tudo null, nunca zero")
	require.Nil(t, vazio.CoverageKm)

	_, err = pool.Exec(ctx, `
		UPDATE stations SET anatel_class = 'A4', anatel_coverage_km = 24.0, anatel_reach_km = 36.0,
		       anatel_match_tier = 'exact', anatel_plan_city = 'Goiânia', anatel_plan_state = 'GO',
		       anatel_matched_at = now()
		WHERE id = $1`, st)
	require.NoError(t, err)
	a, err := repo.Anatel(ctx, st)
	require.NoError(t, err)
	require.Equal(t, "A4", *a.Class)
	require.InDelta(t, 24.0, *a.CoverageKm, 0.001)
	require.InDelta(t, 36.0, *a.ReachKm, 0.001)
	require.Equal(t, "exact", *a.MatchTier)

	_, err = repo.Anatel(ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	_, err = pool.Exec(ctx, `
		INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
		VALUES ($1, 5208707, 'Goiânia', 'GO', 0, true, -16.68, -49.25),
		       ($1, 5201405, 'Aparecida de Goiânia', 'GO', 18.2, false, -16.82, -49.24)`, st)
	require.NoError(t, err)
	cs, err := repo.CoverageCities(ctx, st)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	require.Equal(t, "Goiânia", cs[0].City, "da mais perto para a mais longe")
	require.True(t, cs[0].IsHome)
}

func TestDetections_Attributions(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Atrib "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Atrib "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "atrib")
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-10-02", "2026-10-31")
	insSeedDetection(t, ctx, pool, a, mat, st, "in_slot", "2026-10-10")
	var det uuid.UUID
	var quando time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id, detected_at FROM detections WHERE campaign_id = $1`, a).Scan(&det, &quando))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, det, quando, b, mat)
	require.NoError(t, err)

	got, err := NewDetections(pool).Attributions(ctx, det, quando)
	require.NoError(t, err)
	require.Len(t, got, 2)
	cats := map[uuid.UUID]string{got[0].CampaignID: got[0].Category, got[1].CampaignID: got[1].Category}
	require.Equal(t, map[uuid.UUID]string{a: "in_slot", b: "bonus"}, cats)
	require.Equal(t, "atrib", got[0].CommercialName)
}

func TestDetections_ListPaged_FiltrosDoMCP(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Filtros "+uuid.NewString()[:6])
	st1 := insSeedStationNoProfile(t, ctx, pool, "F1 "+uuid.NewString()[:6])
	st2 := insSeedStationNoProfile(t, ctx, pool, "F2 "+uuid.NewString()[:6])
	_, mat1 := insSeedTypeAndMaterial(t, ctx, pool, cli, "f-mat1")
	_, mat2 := insSeedTypeAndMaterial(t, ctx, pool, cli, "f-mat2")
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	insSeedDetection(t, ctx, pool, camp, mat1, st1, "in_slot", "2026-10-10")
	insSeedDetection(t, ctx, pool, camp, mat1, st2, "bonus", "2026-10-11")
	insSeedDetection(t, ctx, pool, camp, mat2, st1, "out_slot", "2026-10-12")
	repo := NewDetections(pool)
	base := ListPagedFilter{CampaignIDs: []uuid.UUID{camp}, Page: 1, PageSize: 50}

	f := base
	f.StationID = &st1
	r, err := repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 2, r.Total)

	f = base
	f.MaterialID = &mat1
	r, err = repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 2, r.Total)

	f = base
	f.Categories = []string{"bonus"}
	r, err = repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 1, r.Total)

	r, err = repo.ListPaged(ctx, base)
	require.NoError(t, err)
	require.Equal(t, 3, r.Total, "sem os filtros novos, nada muda para a tela")

	// O filtro de categoria lê a PROJEÇÃO (detection_attributions): uma tocada
	// canônica in_slot na campanha A com projeção bonus na campanha B aparece
	// só como a linha de B quando se pede "bonus".
	a2 := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b2 := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	insSeedDetection(t, ctx, pool, a2, mat1, st1, "in_slot", "2026-10-13")
	var det uuid.UUID
	var quando time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id, detected_at FROM detections WHERE campaign_id = $1`, a2).Scan(&det, &quando))
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, det, quando, b2, mat1)
	require.NoError(t, err)
	r, err = repo.ListPaged(ctx, ListPagedFilter{
		CampaignIDs: []uuid.UUID{a2, b2}, Categories: []string{"bonus"}, Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, r.Total)
	require.Len(t, r.Data, 1)
	require.Equal(t, b2, r.Data[0].CampaignID)
}

func TestDetections_Approved(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Aprov "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Aprov "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "aprov")
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-10-10")
	var det uuid.UUID
	var quando time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, detected_at FROM detections WHERE campaign_id = $1`, camp).Scan(&det, &quando))
	repo := NewDetections(pool)

	ok, err := repo.Approved(ctx, det, quando)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.Approved(ctx, det, quando.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, ok, "detected_at é a chave de poda: outro instante não casa")

	_, err = pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, det)
	require.NoError(t, err)
	ok, err = repo.Approved(ctx, det, quando)
	require.NoError(t, err)
	require.False(t, ok, "ignorada sai do conjunto aprovado")
}
