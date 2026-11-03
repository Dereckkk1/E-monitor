package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// insTocadaEm semeia uma tocada (mais a projeção canônica) num instante exato,
// no estado pedido: "" = aprovada; ignored | retracted | audit_rejected.
func insTocadaEm(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	camp, mat, st uuid.UUID, quando time.Time, estado string) uuid.UUID {
	t.Helper()
	var det uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category, ignored_at, retracted_at, evidence_status)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot',
		        CASE WHEN $5 = 'ignored' THEN now() END,
		        CASE WHEN $5 = 'retracted' THEN now() END,
		        CASE WHEN $5 = 'audit_rejected' THEN 'audit_rejected' ELSE 'available' END)
		RETURNING id`, st, mat, camp, quando, estado).Scan(&det))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'in_slot')`, det, quando, camp, mat)
	require.NoError(t, err)
	return det
}

func TestDetections_ApprovedNear(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Perto "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Perto "+uuid.NewString()[:6])
	outraSt := insSeedStationNoProfile(t, ctx, pool, "Perto outra "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "perto")
	_, outroMat := insSeedTypeAndMaterial(t, ctx, pool, cli, "perto-outro")
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	outraCamp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	base := time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)
	repo := NewDetections(pool)

	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(3*time.Minute), "")
	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(-10*time.Minute), "") // na borda da janela
	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(11*time.Minute), "")  // fora da janela
	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(2*time.Minute), "ignored")
	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(4*time.Minute), "retracted")
	insTocadaEm(t, ctx, pool, camp, mat, st, base.Add(5*time.Minute), "audit_rejected")
	insTocadaEm(t, ctx, pool, camp, mat, outraSt, base.Add(1*time.Minute), "") // outra emissora
	insTocadaEm(t, ctx, pool, camp, outroMat, st, base.Add(1*time.Minute), "") // outro material
	insTocadaEm(t, ctx, pool, outraCamp, mat, st, base.Add(1*time.Minute), "") // outra campanha

	got, err := repo.ApprovedNear(ctx, camp, st, mat, base, 10*time.Minute, 5)
	require.NoError(t, err)
	require.Equal(t, instantesUTC([]time.Time{base.Add(3 * time.Minute), base.Add(-10 * time.Minute)}), instantesUTC(got),
		"só as aprovadas da mesma campanha × emissora × material, da mais próxima para a mais distante")

	limitado, err := repo.ApprovedNear(ctx, camp, st, mat, base, 10*time.Minute, 1)
	require.NoError(t, err)
	require.Len(t, limitado, 1)

	nenhuma, err := repo.ApprovedNear(ctx, camp, st, mat, base.Add(2*time.Hour), 10*time.Minute, 5)
	require.NoError(t, err)
	require.Empty(t, nenhuma)
}

// A tocada atribuída a duas campanhas conta para as DUAS (a projeção manda),
// e a que ainda não tem projeção cai na campanha e no material da linha base.
func TestDetections_ApprovedNear_ProjecaoEBase(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Proj "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Proj "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "proj")
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	base := time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)
	repo := NewDetections(pool)

	multi := insTocadaEm(t, ctx, pool, a, mat, st, base, "")
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, multi, base, b, mat)
	require.NoError(t, err)
	semProjecao := insTocadaEm(t, ctx, pool, a, mat, st, base.Add(time.Minute), "")
	_, err = pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1`, semProjecao)
	require.NoError(t, err)

	emA, err := repo.ApprovedNear(ctx, a, st, mat, base, 10*time.Minute, 5)
	require.NoError(t, err)
	require.Equal(t, instantesUTC([]time.Time{base, base.Add(time.Minute)}), instantesUTC(emA))
	emB, err := repo.ApprovedNear(ctx, b, st, mat, base, 10*time.Minute, 5)
	require.NoError(t, err)
	require.Equal(t, instantesUTC([]time.Time{base}), instantesUTC(emB), "a atribuição à campanha b conta para ela")
}

func TestDetections_HasPartitionFor(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	ok, err := repo.HasPartitionFor(ctx, time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.True(t, ok, "o mês de outubro/2026 tem partição")
	ok, err = repo.HasPartitionFor(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok, "o mês corrente tem partição")

	ok, err = repo.HasPartitionFor(ctx, time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.False(t, ok, "antes da primeira partição o INSERT estouraria")
	ok, err = repo.HasPartitionFor(ctx, time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.False(t, ok, "depois da última partição também")
}

// O limite superior é aberto: o primeiro instante do mês seguinte já é da
// partição seguinte; e o limite do mês cai no fuso do banco (UTC), não em SP.
func TestDetections_HasPartitionFor_Bordas(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)
	var primeiro, ultimoLimite time.Time
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT (regexp_match(pg_get_expr(c.relpartbound, c.oid), 'FROM \(''([^'']+)''\)'))[1]::timestamptz
		FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'detections'::regclass
		ORDER BY 1 LIMIT 1`).Scan(&primeiro))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT (regexp_match(pg_get_expr(c.relpartbound, c.oid), 'TO \(''([^'']+)''\)'))[1]::timestamptz
		FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'detections'::regclass
		ORDER BY 1 DESC LIMIT 1`).Scan(&ultimoLimite))

	ok, err := repo.HasPartitionFor(ctx, primeiro)
	require.NoError(t, err)
	require.True(t, ok, "o início da primeira partição é dela")
	ok, err = repo.HasPartitionFor(ctx, primeiro.Add(-time.Nanosecond*1000))
	require.NoError(t, err)
	require.False(t, ok, "um instante antes do início não é")
	ok, err = repo.HasPartitionFor(ctx, ultimoLimite.Add(-time.Microsecond))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.HasPartitionFor(ctx, ultimoLimite)
	require.NoError(t, err)
	require.False(t, ok, "o limite superior é aberto")
}

// instantesUTC normaliza os instantes lidos do banco (que chegam no fuso local
// do driver) para comparar o MOMENTO, não a *Location.
func instantesUTC(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.UTC().Format(time.RFC3339)
	}
	return out
}
