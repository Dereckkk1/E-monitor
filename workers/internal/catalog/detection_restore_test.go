package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// restSeedMaterial cria um material com duração explícita sob um cliente.
// (insSeedTypeAndMaterial fixa 30s; o restore-path precisa de 15s e 30s.)
func restSeedMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	clientID uuid.UUID, label string, dur float64) uuid.UUID {
	t.Helper()
	typeID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`,
		typeID, label+"-"+typeID.String()[:8]); err != nil {
		t.Fatalf("seed material_type: %v", err)
	}
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: label, TypeID: &typeID,
		DurationSeconds: dur, MasterStoragePath: "/tmp/" + label,
		MasterSHA256: "rest-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("seed material: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM material_types WHERE id = $1", typeID)
	})
	return mat.ID
}

// restSeedDet insere uma detection in_slot com estado explícito (retratada?,
// evidence_status, audit_coverage) num timestamp exato.
func restSeedDet(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID, materialID, stationID uuid.UUID, at time.Time,
	retracted bool, ev string, cov *float64) uuid.UUID {
	t.Helper()
	var retractedAt *time.Time
	if retracted {
		retractedAt = &at
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		    match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		    temporal_coverage, variant_used, rate_used, category,
		    retracted_at, evidence_status, audit_coverage)
		VALUES ($1,$2,$3,$4, 0,30000, 0.9,100, 0.85,0,0, 'in_slot', $5,$6,$7)
		RETURNING id`,
		stationID, materialID, campaignID, at, retractedAt, ev, cov,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	return id
}

func detIsRetracted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var ra *time.Time
	if err := pool.QueryRow(ctx, `SELECT retracted_at FROM detections WHERE id = $1`, id).Scan(&ra); err != nil {
		t.Fatalf("read retracted_at: %v", err)
	}
	return ra != nil
}

// TestRestoreDisplacedShorterCut trava a recuperação reject-path da §18.2.2 v2:
// quando o 30s reprova no audit, o 15s válido que a v1 retratou deve ser
// des-retratado — mas só quando ele de fato passou no próprio audit (available)
// e está na janela da veiculação.
func TestRestoreDisplacedShorterCut(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "RestoreCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioRestore")
	m15 := restSeedMaterial(t, ctx, pool, client, "Spot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "Spot30", 30)
	cov := 0.66

	// Cada subteste usa um dia distinto → janelas de ±60s nunca se cruzam.
	t.Run("recupera o 15s available que o 30s rejeitado deslocou", func(t *testing.T) {
		ts := time.Date(2026, 6, 10, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
		det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-5*time.Second), true, "available", &cov)

		got, _, err := repo.RestoreDisplacedShorterCut(ctx, det30, ts, st)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got == nil || *got != det15 {
			t.Fatalf("restaurou %v, queria %v", got, det15)
		}
		if detIsRetracted(t, ctx, pool, det15) {
			t.Error("det15 deveria estar des-retratada")
		}
	})

	t.Run("NÃO recupera quando o 15s irmão também reprovou no audit", func(t *testing.T) {
		ts := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
		det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-5*time.Second), true, "audit_rejected", nil)

		got, _, err := repo.RestoreDisplacedShorterCut(ctx, det30, ts, st)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != nil {
			t.Fatalf("não deveria restaurar (15s irrecuperável), restaurou %v", *got)
		}
		if !detIsRetracted(t, ctx, pool, det15) {
			t.Error("det15 deveria continuar retratada")
		}
	})

	t.Run("NÃO recupera o 15s fora da janela de ±60s", func(t *testing.T) {
		ts := time.Date(2026, 6, 12, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
		det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-120*time.Second), true, "available", &cov)

		got, _, err := repo.RestoreDisplacedShorterCut(ctx, det30, ts, st)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != nil {
			t.Fatalf("não deveria restaurar (fora da janela), restaurou %v", *got)
		}
		if !detIsRetracted(t, ctx, pool, det15) {
			t.Error("det15 deveria continuar retratada (fora da janela)")
		}
	})
}
