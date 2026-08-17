package catalog

import (
	"context"
	"errors"
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

// TestRestoreDisplacedShorterCut_LostRaceRestoresNothing trava a corrida entre o
// SELECT que escolhe o candidato e o UPDATE que o des-retrata.
//
// O caminho deixou de ser um statement único (UPDATE ... RETURNING) pra o
// advisory lock da célula-dia poder ser tomado ANTES de qualquer lock de linha.
// Isso abriu uma janela: entre o SELECT e o UPDATE, outro caminho pode
// des-retratar a mesma linha. Quando isso acontece, o UPDATE guardado por
// `retracted_at IS NOT NULL` casa ZERO linhas e a função TEM que devolver
// (nil, 0, nil) — o mesmo que o ErrNoRows do statement único devolvia. É esse
// nil que faz o evidence/service.go cair no recoverRejectedByCoverage em vez de
// logar uma restauração que não foi dele e incrementar restored_on_reject.
//
// A INTERLEAVAÇÃO É REAL, não simulada: a conexão B segura o advisory lock da
// célula-dia, o que trava a chamada exatamente entre o SELECT do candidato e o
// UPDATE (lockCellDayKeys roda depois de um e antes do outro). Com a chamada
// parada ali, B des-retrata a linha e commita, liberando o lock. Não há sleep
// esperando "dar tempo": o teste espera o backend aparecer BLOQUEADO em
// pg_locks antes de mexer na linha.
func TestRestoreDisplacedShorterCut_LostRaceRestoresNothing(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "RaceCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioRace")
	m15 := restSeedMaterial(t, ctx, pool, client, "RaceSpot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "RaceSpot30", 30)
	cov := 0.66

	ts := time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC)
	ts15 := ts.Add(-5 * time.Second)
	det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
	det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts15, true, "available", &cov)

	// A projeção canônica é o que torna o rollback OBSERVÁVEL: com ela, um
	// refechamento que commitasse reescreveria a categoria de 'in_slot' pra
	// 'bonus' (célula sem regra ⇒ meta 0 ⇒ tudo excedente). Se no fim a
	// categoria continuar 'in_slot', nada foi escrito.
	if _, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'in_slot')`, det15, ts15, camp, m15); err != nil {
		t.Fatalf("seed projeção: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1 AND detected_at = $2`, det15, ts15)
	})

	// B segura o advisory lock da célula-dia do det15 — a MESMA chave que o
	// mutateApprovedSet vai pedir.
	key := cellDayLockKey(camp, st, dayStartSP(ts15))
	bTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin B: %v", err)
	}
	defer bTx.Rollback(ctx)
	if _, err := bTx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("B advisory lock: %v", err)
	}

	type result struct {
		id  *uuid.UUID
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, _, err := repo.RestoreDisplacedShorterCut(ctx, det30, ts, st)
		done <- result{id: got, err: err}
	}()

	// Espera a chamada ficar BLOQUEADA no advisory lock. Isso prova que o SELECT
	// do candidato já rodou (e viu a linha retratada) e que o UPDATE ainda não
	// rodou — é exatamente a janela da corrida.
	deadline := time.Now().Add(10 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted
			  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`,
		).Scan(&n); err != nil {
			t.Fatalf("pg_locks: %v", err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		// Sem o bloqueio confirmado a interleavação não é garantida e o teste
		// passaria pelo motivo errado (candidato nem chegou a ser escolhido).
		t.Fatal("a chamada não bloqueou no advisory lock — corrida não foi induzida")
	}

	// AGORA, com a chamada parada entre o SELECT e o UPDATE: outro caminho
	// des-retrata a linha e commita, liberando o lock.
	if _, err := bTx.Exec(ctx,
		`UPDATE detections SET retracted_at = NULL WHERE id = $1 AND detected_at = $2`,
		det15, ts15); err != nil {
		t.Fatalf("B des-retrata: %v", err)
	}
	if err := bTx.Commit(ctx); err != nil {
		t.Fatalf("commit B: %v", err)
	}

	var res result
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("RestoreDisplacedShorterCut não retornou depois de liberado o lock")
	}

	if res.err != nil {
		t.Fatalf("perder a corrida não é erro, veio: %v", res.err)
	}
	if res.id != nil {
		t.Fatalf("devolveu %v: não pode reivindicar uma restauração que outro caminho fez "+
			"(o chamador perde o fallthrough pro recoverRejectedByCoverage)", *res.id)
	}

	// Rollback: nada foi escrito pela chamada.
	var baseCat, projCat string
	if err := pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`, det15, ts15,
	).Scan(&baseCat); err != nil {
		t.Fatalf("read categoria base: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id = $1 AND detected_at = $2 AND campaign_id = $3`,
		det15, ts15, camp,
	).Scan(&projCat); err != nil {
		t.Fatalf("read categoria da projeção: %v", err)
	}
	if baseCat != "in_slot" || projCat != "in_slot" {
		t.Errorf("a transação deveria ter sofrido rollback inteira; categorias viraram base=%q proj=%q",
			baseCat, projCat)
	}
}

// shortIDOf lê o short_id (SERIAL) de um material já semeado.
func shortIDOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, materialID uuid.UUID) int32 {
	t.Helper()
	var sid int32
	if err := pool.QueryRow(ctx, `SELECT short_id FROM materials WHERE id = $1`, materialID).Scan(&sid); err != nil {
		t.Fatalf("read short_id: %v", err)
	}
	return sid
}

func TestFindSiblingDetectionInWindow(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "FindSibCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioFindSib")
	m15 := restSeedMaterial(t, ctx, pool, client, "FSpot15", 15)
	sid15 := shortIDOf(t, ctx, pool, m15)
	cov := 0.66

	t.Run("acha a row do corte na janela e reporta estado retraido+available", func(t *testing.T) {
		ts := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
		det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-5*time.Second), true, "available", &cov)

		got, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if got == nil || got.ID != det15 {
			t.Fatalf("achou %v, queria %v", got, det15)
		}
		if !got.Retracted || got.EvidenceStatus != "available" {
			t.Errorf("estado errado: retracted=%v status=%q", got.Retracted, got.EvidenceStatus)
		}
	})

	t.Run("retorna nil quando nao ha row do corte na janela", func(t *testing.T) {
		ts := time.Date(2026, 6, 14, 15, 0, 0, 0, time.UTC)
		restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-120*time.Second), true, "available", &cov)

		got, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if got != nil {
			t.Fatalf("nao deveria achar (fora da janela), achou %v", got.ID)
		}
	})
}

func TestClearRetraction(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "ClearCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioClear")
	m15 := restSeedMaterial(t, ctx, pool, client, "CSpot15", 15)
	cov := 0.7
	ts := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	det := restSeedDet(t, ctx, pool, camp, m15, st, ts, true, "available", &cov)

	if err := repo.ClearRetraction(ctx, det, ts); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if detIsRetracted(t, ctx, pool, det) {
		t.Error("row deveria estar des-retratada")
	}
}

// detRow lê commercial_id, campaign_id, evidence_status de uma detection.
func detRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	var cmc, cmp uuid.UUID
	var ev string
	if err := pool.QueryRow(ctx,
		`SELECT commercial_id, campaign_id, evidence_status FROM detections WHERE id = $1`, id,
	).Scan(&cmc, &cmp, &ev); err != nil {
		t.Fatalf("read det row: %v", err)
	}
	return cmc, cmp, ev
}

func TestReattributeRejectedDetection(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "ReatCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioReat")
	m15 := restSeedMaterial(t, ctx, pool, client, "RSpot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "RSpot30", 30)

	t.Run("reatribui a row audit_rejected do 30s pro 15s, status missing", func(t *testing.T) {
		ts := time.Date(2026, 6, 16, 15, 0, 0, 0, time.UTC)
		det := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)

		if err := repo.ReattributeRejectedDetection(ctx, det, ts, m15, camp, st, 0.79); err != nil {
			t.Fatalf("reattribute: %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det)
		if cmc != m15 {
			t.Errorf("commercial_id = %v, queria %v", cmc, m15)
		}
		if ev != "missing" {
			t.Errorf("evidence_status = %q, queria \"missing\"", ev)
		}
	})

	t.Run("nao mexe numa row que NAO esta audit_rejected (guard G3)", func(t *testing.T) {
		ts := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)
		det := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "available", nil)

		err := repo.ReattributeRejectedDetection(ctx, det, ts, m15, camp, st, 0.79)
		if !errors.Is(err, ErrReattributeNoRow) {
			t.Fatalf("queria ErrReattributeNoRow, veio %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det)
		if cmc != m30 || ev != "available" {
			t.Errorf("row nao deveria ter mudado: cmc=%v ev=%q", cmc, ev)
		}
	})
}

// TestRejectRecoveryScenarios trava as duas recuperações que o orquestrador v2c
// dispara, exercitando os helpers de catalog na mesma ordem (sem auditor):
//
//	A) corte suprimido (sem row) -> ReattributeRejectedDetection re-aponta a row rejeitada.
//	B) irmão deslocado de MESMA duração (Ep.2 05-09) -> FindSiblingDetectionInWindow
//	   acha a row retraída+available e ClearRetraction restaura (sem duplicar).
func TestRejectRecoveryScenarios(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "RecCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioRec")
	m15 := restSeedMaterial(t, ctx, pool, client, "RecSpot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "RecSpot30", 30)
	mJingle := restSeedMaterial(t, ctx, pool, client, "RecJingle", 30) // mesma duração do m30
	sid15 := shortIDOf(t, ctx, pool, m15)
	sidJingle := shortIDOf(t, ctx, pool, mJingle)
	cov := 0.66

	t.Run("A: 30s rejeitado + 15s sem row -> reatribui (sem row irma)", func(t *testing.T) {
		ts := time.Date(2026, 6, 18, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)

		// orquestrador: FindSiblingDetectionInWindow(15s) == nil -> RecoveryReattribute
		existing, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if decideEv := evDecide(existing); decideEv != "reattribute" {
			t.Fatalf("decisao = %s, queria reattribute", decideEv)
		}
		if err := repo.ReattributeRejectedDetection(ctx, det30, ts, m15, camp, st, 0.79); err != nil {
			t.Fatalf("reattribute: %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det30)
		if cmc != m15 || ev != "missing" {
			t.Errorf("row deveria ser 15s/missing, veio cmc=%v ev=%q", cmc, ev)
		}
	})

	t.Run("B: jingle MESMA duracao retraido+available -> restaura, sem duplicar", func(t *testing.T) {
		ts := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
		detJingle := restSeedDet(t, ctx, pool, camp, mJingle, st, ts.Add(-5*time.Second), true, "available", &cov)

		// v2b NÃO acharia (mesma duração); v2c acha pela cobertura + janela:
		existing, err := repo.FindSiblingDetectionInWindow(ctx, sidJingle, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if existing == nil || existing.ID != detJingle {
			t.Fatalf("achou %v, queria %v", existing, detJingle)
		}
		if evDecide(existing) != "restore" {
			t.Fatalf("decisao = %s, queria restore", evDecide(existing))
		}
		if err := repo.ClearRetraction(ctx, existing.ID, existing.DetectedAt); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if detIsRetracted(t, ctx, pool, detJingle) {
			t.Error("jingle deveria estar des-retratado")
		}
		// o 30s rejeitado NÃO é reatribuído nesse ramo (sem duplicar):
		_, _, ev := detRow(t, ctx, pool, det30)
		if ev != "audit_rejected" {
			t.Errorf("det30 deveria continuar audit_rejected, veio %q", ev)
		}
		_ = sid15
	})
}

// evDecide espelha a decisão pura do pacote evidence (decideRejectRecovery) sem
// importar evidence (evita ciclo): nil->reattribute, retraida+available->restore, senão skip.
func evDecide(existing *SiblingDetectionRow) string {
	switch {
	case existing == nil:
		return "reattribute"
	case existing.Retracted && existing.EvidenceStatus == "available":
		return "restore"
	default:
		return "skip"
	}
}
