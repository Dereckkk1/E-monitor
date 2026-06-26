package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDetections_ValidateBatchLinks(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ValidateBatchLinks")
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	dets := NewDetections(pool)

	errs := dets.ValidateBatchLinks(ctx, campID, statID, []ManualBatchEntry{
		{CommercialID: matID, DetectedAt: time.Now()},
	})
	if len(errs) != 0 {
		t.Fatalf("linked material returned errors: %+v", errs)
	}

	errs2 := dets.ValidateBatchLinks(ctx, campID, statID, []ManualBatchEntry{
		{CommercialID: matID, DetectedAt: time.Now()},
		{CommercialID: uuid.New(), DetectedAt: time.Now()},
	})
	if len(errs2) != 1 || errs2[0].Index != 1 {
		t.Fatalf("unlinked material: errs=%+v, want exactly 1 at index 1", errs2)
	}
}

func TestDetections_CreateManualBatch_WithProof(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "CreateManualBatch-proof")
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "Batch-B", DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "batch-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID) })
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link A: %v", err)
	}
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matB.ID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link B: %v", err)
	}

	batchID := uuid.New()
	dets := NewDetections(pool)
	out, err := dets.CreateManualBatch(ctx, CreateManualBatchInput{
		CampaignID:   campID,
		StationID:    statID,
		ManualBy:     uuid.New(),
		BatchNote:    "comprovante da emissora 25/06",
		ProofBatchID: &batchID,
		ProofPDFKey:  "proofs/2026/06/25/" + statID.String() + "/" + batchID.String() + ".pdf",
		ProofPDFSize: 12345,
		Entries: []ManualBatchEntry{
			{CommercialID: matID, DetectedAt: time.Now().Add(-2 * time.Hour), Note: "tocada 1"},
			{CommercialID: matB.ID, DetectedAt: time.Now().Add(-1 * time.Hour)},
		},
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE proof_batch_id = $1", batchID)
		pool.Exec(ctx, "DELETE FROM manual_proof_batches WHERE id = $1", batchID)
	})
	if err != nil {
		t.Fatalf("CreateManualBatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("created %d detections, want 2", len(out))
	}
	for i, det := range out {
		if det.ProofBatchID == nil || *det.ProofBatchID != batchID {
			t.Errorf("detection %d proof_batch_id = %v, want %s", i, det.ProofBatchID, batchID)
		}
		if det.EvidenceStatus != "missing" {
			t.Errorf("detection %d evidence_status = %q, want missing", i, det.EvidenceStatus)
		}
		if det.ManualAt == nil {
			t.Errorf("detection %d manual_at is nil, want set", i)
		}
	}
	var projCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM detection_campaigns WHERE campaign_id = $1 AND detection_id = ANY($2)`,
		campID, []uuid.UUID{out[0].ID, out[1].ID}).Scan(&projCount); err != nil {
		t.Fatalf("count projections: %v", err)
	}
	if projCount != 2 {
		t.Errorf("detection_campaigns rows = %d, want 2", projCount)
	}
	var gotKey string
	if err := pool.QueryRow(ctx,
		`SELECT proof_pdf_key FROM manual_proof_batches WHERE id = $1`, batchID).Scan(&gotKey); err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if gotKey == "" {
		t.Errorf("batch proof_pdf_key empty")
	}
	resolved, err := dets.ProofKeyForDetection(ctx, out[0].ID)
	if err != nil {
		t.Fatalf("ProofKeyForDetection: %v", err)
	}
	if resolved != gotKey {
		t.Errorf("ProofKeyForDetection = %q, want %q", resolved, gotKey)
	}
}

func TestDetections_CreateManualBatch_NoProof(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "CreateManualBatch-noproof")
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	dets := NewDetections(pool)
	out, err := dets.CreateManualBatch(ctx, CreateManualBatchInput{
		CampaignID: campID, StationID: statID, ManualBy: uuid.New(),
		Entries: []ManualBatchEntry{{CommercialID: matID, DetectedAt: time.Now()}},
	})
	if err != nil {
		t.Fatalf("CreateManualBatch: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("created %d, want 1", len(out))
	}
	if out[0].ProofBatchID != nil {
		t.Errorf("proof_batch_id = %v, want nil (no PDF)", out[0].ProofBatchID)
	}
	// Projeção canônica detection_campaigns (F-119) é incondicional — vale também
	// sem PDF. Cobre regressão no branch sem comprovante.
	var projCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM detection_campaigns WHERE detection_id = $1`,
		out[0].ID).Scan(&projCount); err != nil {
		t.Fatalf("count projection: %v", err)
	}
	if projCount != 1 {
		t.Errorf("detection_campaigns rows = %d, want 1", projCount)
	}
	if _, err := dets.ProofKeyForDetection(ctx, out[0].ID); err == nil {
		t.Errorf("ProofKeyForDetection on no-proof detection: want error, got nil")
	}
}
