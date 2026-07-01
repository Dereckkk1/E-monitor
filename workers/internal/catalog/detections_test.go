package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDetections_Create_CategorizesOrphan(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Now().AddDate(0, 0, -1),
		EndDate:        time.Now().AddDate(0, 0, 30),
		TargetStations: []uuid.UUID{},
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "abc-cat-orphan",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM", Band: "FM", StreamURL: "http://example.com",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85, VariantUsed: 0, RateUsed: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sem regras criadas → deve ser orphan
	var category string
	err = pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&category)
	if err != nil {
		t.Fatalf("read category: %v", err)
	}
	if category != "orphan" {
		t.Errorf("category = %q, want orphan", category)
	}
}

// seedAirtimeFixture spins up the minimal set of rows needed to exercise the
// paginated airtime-report queries: one client, one campaign, one station,
// one material. Returns ctx + pool + the IDs so the test can insert
// detections referencing them. Cleanup is registered via t.Cleanup.
func seedAirtimeFixture(t *testing.T, materialTitle string) (
	ctx context.Context, pool *pgxpool.Pool,
	campaignID, materialID, stationID uuid.UUID,
) {
	t.Helper()
	ctx, pool = newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "T-airtime-" + materialTitle})
	if err != nil {
		t.Fatalf("seed client: %v", err)
	}
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C-airtime-" + materialTitle, ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -7),
		EndDate:   time.Now().AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: materialTitle, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "airtime-" + materialTitle,
	})
	if err != nil {
		t.Fatalf("seed material: %v", err)
	}
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Airtime FM", Band: "FM", StreamURL: "http://example.com/airtime-" + materialTitle,
	})
	if err != nil {
		t.Fatalf("seed station: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})
	return ctx, pool, cmp.ID, mat.ID, stat.ID
}

// TestDetections_MarkAmbiguous_RetractsAndExcludes prova a invariante
// `ambiguous ⟺ retracted`: MarkAmbiguous seta evidence_status='ambiguous' E
// retracted_at, tirando a linha do conjunto aprovado (catalog.ApprovedDetectionsFilter),
// e é idempotente (COALESCE não move o timestamp na 2a chamada).
func TestDetections_MarkAmbiguous_RetractsAndExcludes(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "Ambiguous")
	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: time.Now(), Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatal(err)
	}

	countApproved := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM detections d WHERE d.id=$1 AND d.detected_at=$2 AND `+ApprovedDetectionsFilter,
			det.ID, det.DetectedAt).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countApproved() != 1 {
		t.Fatalf("pré-condição: detecção recém-criada deveria estar aprovada, got %d", countApproved())
	}

	if err := dets.MarkAmbiguous(ctx, det.ID, det.DetectedAt); err != nil {
		t.Fatal(err)
	}

	var ev string
	var retracted *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT evidence_status, retracted_at FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&ev, &retracted); err != nil {
		t.Fatal(err)
	}
	if ev != "ambiguous" {
		t.Fatalf("evidence_status=%q, want ambiguous", ev)
	}
	if retracted == nil {
		t.Fatalf("retracted_at deve estar setado (ambiguous ⟹ retracted)")
	}
	if countApproved() != 0 {
		t.Fatalf("detecção ambígua deve sair do conjunto aprovado, got %d", countApproved())
	}

	// Idempotente: 2a chamada não move retracted_at.
	first := *retracted
	if err := dets.MarkAmbiguous(ctx, det.ID, det.DetectedAt); err != nil {
		t.Fatal(err)
	}
	var second time.Time
	if err := pool.QueryRow(ctx,
		`SELECT retracted_at FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if !second.Equal(first) {
		t.Fatalf("retracted_at moveu na 2a MarkAmbiguous: %v -> %v", first, second)
	}
}

func TestDetections_ListPaged_BasicPaging(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ListPaged-basic")
	dets := NewDetections(pool)
	// 25 detections, 1h apart, all in the campaign window.
	for i := 0; i < 25; i++ {
		_, err := dets.Create(ctx, CreateDetectionInput{
			StationID: statID, CommercialID: matID, CampaignID: campID,
			DetectedAt:         time.Now().Add(-time.Duration(i) * time.Hour),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
			Confidence: 0.95, HashCount: 100,
			TemporalCoverage: 0.85,
		})
		if err != nil {
			t.Fatalf("seed detection %d: %v", i, err)
		}
	}

	res, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignID: &campID, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPaged page 1: %v", err)
	}
	if len(res.Data) != 10 {
		t.Errorf("page 1 data len = %d, want 10", len(res.Data))
	}
	if res.Total != 25 {
		t.Errorf("total = %d, want 25", res.Total)
	}
	if res.TotalPages != 3 {
		t.Errorf("total_pages = %d, want 3", res.TotalPages)
	}

	res2, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignID: &campID, Page: 3, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPaged page 3: %v", err)
	}
	if len(res2.Data) != 5 {
		t.Errorf("last page len = %d, want 5", len(res2.Data))
	}
}

func TestDetections_ListPaged_QFilter(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "Cha cha cha 30s")
	dets := NewDetections(pool)
	_, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}

	res, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignID: &campID, Q: "cha", Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPaged q='cha': %v", err)
	}
	if res.Total != 1 {
		t.Errorf("q='cha' total = %d, want 1", res.Total)
	}

	res2, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignID: &campID, Q: "xyz-impossivel", Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPaged q='xyz': %v", err)
	}
	if res2.Total != 0 {
		t.Errorf("q='xyz' total = %d, want 0", res2.Total)
	}
}

func TestDetections_AggregateByMaterial(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "Aggregate-A")
	dets := NewDetections(pool)
	// 5 detections of matA.
	for i := 0; i < 5; i++ {
		_, err := dets.Create(ctx, CreateDetectionInput{
			StationID: statID, CommercialID: matID, CampaignID: campID,
			DetectedAt:         time.Now().Add(-time.Duration(i) * time.Minute),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
			Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		if err != nil {
			t.Fatalf("seed matA detection %d: %v", i, err)
		}
	}
	// matB linked to the same campaign + station.
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID:          uuid.MustParse(mustClientIDFromCampaign(t, pool, campID)),
		Title:             "Aggregate-B",
		DurationSeconds:   60,
		MasterStoragePath: "/tmp", MasterSHA256: "aggregate-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE commercial_id = $1", matB.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID)
	})
	for i := 0; i < 2; i++ {
		_, err := dets.Create(ctx, CreateDetectionInput{
			StationID: statID, CommercialID: matB.ID, CampaignID: campID,
			DetectedAt:         time.Now().Add(-time.Duration(10+i) * time.Minute),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 60000,
			Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		if err != nil {
			t.Fatalf("seed matB detection %d: %v", i, err)
		}
	}

	res, err := dets.AggregateByMaterial(ctx, AggregateFilter{CampaignID: campID})
	if err != nil {
		t.Fatalf("AggregateByMaterial: %v", err)
	}
	if res.TotalDetections != 7 {
		t.Errorf("total = %d, want 7", res.TotalDetections)
	}
	if res.DistinctMaterials != 2 {
		t.Errorf("distinct = %d, want 2", res.DistinctMaterials)
	}
	if len(res.Data) == 0 || res.Data[0].Count != 5 {
		t.Errorf("top row count = %v, want 5", func() any {
			if len(res.Data) == 0 {
				return "empty"
			}
			return res.Data[0].Count
		}())
	}
}

// mustClientIDFromCampaign fetches the client_id of a campaign row. Used by
// the aggregate test to seed a second material under the same client.
func mustClientIDFromCampaign(t *testing.T, pool *pgxpool.Pool, campID uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT client_id::text FROM campaigns WHERE id = $1`, campID).Scan(&s); err != nil {
		t.Fatalf("read client_id: %v", err)
	}
	return s
}

func TestDetections_SetAuditCoverage(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "SetAuditCoverage")
	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}

	if err := dets.SetAuditCoverage(ctx, det.ID, det.DetectedAt, 0.42); err != nil {
		t.Fatalf("SetAuditCoverage: %v", err)
	}

	var got float64
	if err := pool.QueryRow(ctx,
		`SELECT audit_coverage FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&got); err != nil {
		t.Fatalf("read audit_coverage: %v", err)
	}
	if got != 0.42 {
		t.Errorf("audit_coverage = %v, want 0.42", got)
	}
}

func TestDetections_FindCutWithSiblings(t *testing.T) {
	ctx, pool := newTestDB(t)
	mats := NewMaterials(pool)
	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "T-siblings"})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	cli2, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "T-siblings-other"})
	if err != nil {
		t.Fatalf("client2: %v", err)
	}
	a, _ := mats.Create(ctx, CreateMaterialInput{ClientID: cli.ID, Title: "Cut 30s", DurationSeconds: 30, MasterStoragePath: "/tmp", MasterSHA256: "sib-a"})
	b, _ := mats.Create(ctx, CreateMaterialInput{ClientID: cli.ID, Title: "Cut 15s", DurationSeconds: 15, MasterStoragePath: "/tmp", MasterSHA256: "sib-b"})
	pending, _ := mats.Create(ctx, CreateMaterialInput{ClientID: cli.ID, Title: "Cut pending", DurationSeconds: 20, MasterStoragePath: "/tmp", MasterSHA256: "sib-pending"})
	other, _ := mats.Create(ctx, CreateMaterialInput{ClientID: cli2.ID, Title: "Other client", DurationSeconds: 15, MasterStoragePath: "/tmp", MasterSHA256: "sib-other"})
	// a and b are ready; pending stays non-ready; other is a different client.
	if _, err := pool.Exec(ctx, `UPDATE materials SET fingerprint_status='ready' WHERE id = ANY($1)`,
		[]uuid.UUID{a.ID, b.ID, other.ID}); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE id = ANY($1)", []uuid.UUID{a.ID, b.ID, pending.ID, other.ID})
		pool.Exec(ctx, "DELETE FROM clients WHERE id = ANY($1)", []uuid.UUID{cli.ID, cli2.ID})
	})

	dets := NewDetections(pool)
	self, sibs, err := dets.FindCutWithSiblings(ctx, a.ID)
	if err != nil {
		t.Fatalf("FindCutWithSiblings: %v", err)
	}
	if self.ShortID != a.ShortID || self.DurationSeconds != 30 {
		t.Errorf("self = %+v, want short_id=%d dur=30", self, a.ShortID)
	}
	// Only b qualifies: same client + ready. pending is not ready; other is a
	// different client.
	if len(sibs) != 1 || sibs[0].ShortID != b.ShortID || sibs[0].DurationSeconds != 15 {
		t.Fatalf("siblings = %+v, want exactly [short_id=%d dur=15]", sibs, b.ShortID)
	}

	// A non-material UUID (legacy commercial / unknown) yields no self, no
	// siblings, and no error — the caller leaves attribution unchanged.
	self2, sibs2, err := dets.FindCutWithSiblings(ctx, uuid.New())
	if err != nil {
		t.Fatalf("FindCutWithSiblings(random): %v", err)
	}
	if self2.ID != uuid.Nil || len(sibs2) != 0 {
		t.Errorf("random uuid: self=%+v sibs=%+v, want zero self and empty siblings", self2, sibs2)
	}
}

func TestDetections_RetractByID(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "RetractByID")
	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: time.Now(), Confidence: 1.0, HashCount: 100, TemporalCoverage: 1.0,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	at := time.Now().UTC()
	if err := dets.RetractByID(ctx, det.ID, det.DetectedAt, at); err != nil {
		t.Fatalf("RetractByID: %v", err)
	}
	var retracted *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT retracted_at FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&retracted); err != nil {
		t.Fatalf("read: %v", err)
	}
	if retracted == nil {
		t.Fatal("retracted_at ainda NULL após RetractByID")
	}
	first := *retracted
	// idempotente: 2a chamada não sobrescreve (WHERE retracted_at IS NULL).
	if err := dets.RetractByID(ctx, det.ID, det.DetectedAt, at.Add(time.Hour)); err != nil {
		t.Fatalf("RetractByID 2a: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT retracted_at FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&retracted); err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if !retracted.Equal(first) {
		t.Errorf("retracted_at mudou na 2a chamada: %v -> %v (deveria ser no-op)", first, *retracted)
	}
}

func TestDetections_ReattributeDetection(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "Reattribute")
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "Reattribute-real-cut", DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "reattr-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID) })

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: time.Now(), Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}

	// Re-point the detection from matID to matB in the same campaign.
	if err := dets.ReattributeDetection(ctx, det.ID, det.DetectedAt, matB.ID, campID, statID); err != nil {
		t.Fatalf("ReattributeDetection: %v", err)
	}

	var gotCommercial, gotCampaign uuid.UUID
	var gotCategory string
	if err := pool.QueryRow(ctx,
		`SELECT commercial_id, campaign_id, category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&gotCommercial, &gotCampaign, &gotCategory); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if gotCommercial != matB.ID {
		t.Errorf("commercial_id = %s, want %s (reattributed)", gotCommercial, matB.ID)
	}
	if gotCampaign != campID {
		t.Errorf("campaign_id = %s, want %s", gotCampaign, campID)
	}
	if gotCategory == "" {
		t.Errorf("category was not recomputed (empty)")
	}
}

// TestDetections_ReattributeDetection_SyncsProjection prova o invariante que o
// incidente 2026-06-30 violou: ao reatribuir a tocada base pra um irmão (ex.:
// desambiguação por cobertura COPA↔CARVÃO), a projeção canônica em
// detection_campaigns DEVE acompanhar. Sem isso, a grade/relatórios (que lêem
// detection_campaigns) mostram tocada-fantasma do material errado com a
// categoria velha. Mesma campanha, só o material muda.
func TestDetections_ReattributeDetection_SyncsProjection(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ReattrProjSync")
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "ReattrProjSync-real-cut", DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "reattrprojsync-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID) })

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: time.Now(), Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}

	if err := dets.ReattributeDetection(ctx, det.ID, det.DetectedAt, matB.ID, campID, statID); err != nil {
		t.Fatalf("ReattributeDetection: %v", err)
	}

	var baseCategory string
	if err := pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&baseCategory); err != nil {
		t.Fatalf("read base: %v", err)
	}

	// A projeção canônica DEVE ter seguido a base (material + categoria).
	var projCommercial uuid.UUID
	var projCategory string
	if err := pool.QueryRow(ctx,
		`SELECT commercial_id, category FROM detection_campaigns
		 WHERE detection_id = $1 AND detected_at = $2 AND campaign_id = $3`,
		det.ID, det.DetectedAt, campID).Scan(&projCommercial, &projCategory); err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if projCommercial != matB.ID {
		t.Errorf("projeção commercial_id = %s, want %s (devia seguir a base reatribuída)", projCommercial, matB.ID)
	}
	if projCategory != baseCategory {
		t.Errorf("projeção category = %s, want %s (devia bater com a base)", projCategory, baseCategory)
	}
}

// TestDetections_ReattributeRejectedDetection_SyncsProjection cobre o caminho
// gêmeo (reject-path §18.2.2 v2c): reatribuir uma row audit_rejected pra um irmão
// também precisa levar a projeção canônica junto, senão regenera o fantasma do
// incidente 2026-06-30 pelo outro caminho.
func TestDetections_ReattributeRejectedDetection_SyncsProjection(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ReattrRejProjSync")
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "ReattrRejProjSync-real-cut", DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "reattrrejprojsync-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID) })

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: time.Now(), Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	// O reject-path só reatribui rows audit_rejected.
	if _, err := pool.Exec(ctx,
		`UPDATE detections SET evidence_status = 'audit_rejected' WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt); err != nil {
		t.Fatalf("mark audit_rejected: %v", err)
	}

	if err := dets.ReattributeRejectedDetection(ctx, det.ID, det.DetectedAt, matB.ID, campID, statID, 0.79); err != nil {
		t.Fatalf("ReattributeRejectedDetection: %v", err)
	}

	var baseCategory, evStatus string
	if err := pool.QueryRow(ctx,
		`SELECT category, evidence_status FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&baseCategory, &evStatus); err != nil {
		t.Fatalf("read base: %v", err)
	}
	if evStatus != "missing" {
		t.Errorf("evidence_status = %s, want missing (reatribuído)", evStatus)
	}

	var projCommercial uuid.UUID
	var projCategory string
	if err := pool.QueryRow(ctx,
		`SELECT commercial_id, category FROM detection_campaigns
		 WHERE detection_id = $1 AND detected_at = $2 AND campaign_id = $3`,
		det.ID, det.DetectedAt, campID).Scan(&projCommercial, &projCategory); err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if projCommercial != matB.ID {
		t.Errorf("projeção commercial_id = %s, want %s (devia seguir a base)", projCommercial, matB.ID)
	}
	if projCategory != baseCategory {
		t.Errorf("projeção category = %s, want %s (devia bater com a base)", projCategory, baseCategory)
	}
}

func TestDetections_Insert_CarveOut_OutDate(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-carve-insert",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Carve", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	// Regra específica de `mat`: só 1ª semana (1-7), seg-sex, 18-19h.
	repo := NewDistributionRules(pool)
	if _, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{mat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "18:00", TimeEnd: "19:00", PlaysPerDay: 1,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	dets := NewDetections(pool)
	// Toca 16/06 (semana 3) 18:30 BRT (21:30 UTC) — fora do período da regra → out_date.
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: time.Date(2026, 6, 16, 21, 30, 0, 0, time.UTC),
		Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	if err != nil {
		t.Fatalf("create detection: %v", err)
	}
	var cat string
	pool.QueryRow(ctx, `SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&cat)
	if cat != "out_date" {
		t.Errorf("insert carve-out: category = %q, want out_date", cat)
	}
}

func TestDetections_ListPaged_IgnoredExcluded(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ListPaged-ignored")
	dets := NewDetections(pool)
	d1, err := dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85,
	})
	if err != nil {
		t.Fatalf("seed d1: %v", err)
	}
	_, err = dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt:         time.Now().Add(-time.Hour),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85,
	})
	if err != nil {
		t.Fatalf("seed d2: %v", err)
	}
	if err := dets.Ignore(ctx, d1.ID, uuid.New()); err != nil {
		t.Fatalf("ignore d1: %v", err)
	}

	res, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignID: &campID, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPaged: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("total = %d, want 1 (ignored excluded)", res.Total)
	}
}
