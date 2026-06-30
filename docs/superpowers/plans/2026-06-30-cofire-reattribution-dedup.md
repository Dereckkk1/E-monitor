# Co-fire Reattribution Dedup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (inline) ou subagent-driven-development. Steps usam checkbox (`- [ ]`).

**Goal:** Impedir que o §18.2.2-v2 pass-path (`reattributeByCoverage`) crie contagem dobrada quando o cut vencedor já tem detecção na janela — retratando a row irmã redundante em vez de reatribuí-la.

**Architecture:** Adiciona um guard no `reattributeByCoverage` que reusa o classificador `decideRejectRecovery` (já testado, usado pelo reject-path) + `FindSiblingDetectionInWindow`/`ClearRetraction` (existentes) + um novo `RetractByID`. Sem renomear nada no reject-path (caminho de billing provado em prod — blast radius mínimo). Gated pela flag `DISAMBIG_BY_COVERAGE` já existente.

**Tech Stack:** Go, pgx, prometheus client. Pacotes `workers/internal/evidence` e `workers/internal/catalog`.

**Decisão de escopo registrada:** o spec mencionava renomear `decideRejectRecovery→decideCoverageRecovery`. Optamos por **não renomear** — reusar a função como está (com comentário) evita tocar o reject-path provado. Menor risco pra régua da regra 6.

---

### Task 1: `RetractByID` em catalog.Detections

**Files:**
- Modify: `workers/internal/catalog/detections.go` (adicionar método perto de `ClearRetraction`, ~linha 1261)
- Test: `workers/internal/catalog/detections_test.go` (novo `TestDetections_RetractByID`)

- [ ] **Step 1: Escrever o teste que falha**

Em `detections_test.go`:
```go
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
```

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_RetractByID -v`
Expected: FAIL — `dets.RetractByID undefined`.

- [ ] **Step 3: Implementar `RetractByID`**

Em `detections.go`, logo após `ClearRetraction` (~linha 1261):
```go
// RetractByID retrata uma detection (retracted_at = $3) por id+detected_at, só
// se ainda não estava retraída (idempotente). Simétrico ao ClearRetraction.
// Usado pelo co-fire guard do §18.2.2-v2 pass-path pra descartar a row irmã que
// é a MESMA veiculação do cut vencedor. NÃO mexe em detection_campaigns: a
// projeção é gateada pelo retracted_at da row base (view daily_play_summary CTE
// `actual`), então a retração in-place basta. detected_at no WHERE pra partition
// pruning.
func (d *Detections) RetractByID(ctx context.Context, id uuid.UUID, detectedAt, at time.Time) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET retracted_at = $3
		 WHERE id = $1 AND detected_at = $2 AND retracted_at IS NULL`,
		id, detectedAt, at)
	return err
}
```

- [ ] **Step 4: Rodar pra ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_RetractByID -v`
Expected: PASS (ou SKIP se não houver DB de teste configurado — mesma condição dos outros testes de pool).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(detections): RetractByID idempotente p/ co-fire dedup"
```

---

### Task 2: Co-fire guard no `reattributeByCoverage`

**Files:**
- Modify: `workers/internal/evidence/service.go` (`reattributeByCoverage`, inserir após o check `best.ShortID == self.ShortID` ~linha 600, antes do `resolveAttribution` ~linha 605)

- [ ] **Step 1: Adicionar comentário de uso compartilhado no classificador**

Em `workers/internal/evidence/reject_recovery.go`, na doc de `decideRejectRecovery` (~linha 31), acrescentar a linha:
```go
// decideRejectRecovery escolhe o ramo a partir da row existente do irmão vencedor
// na janela (nil = não existe). Pré-condição do caller: o vencedor != corte
// atribuído e supera a margem de cobertura. Pura/testável.
//
// COMPARTILHADA: além do reject-path, o co-fire guard do PASS-path
// (reattributeByCoverage) também a usa — lá `self` está APROVADA, então os ramos
// Skip/Restore retratam `self` em vez de só pular.
```

- [ ] **Step 2: Inserir o guard**

Em `service.go`, entre o `return false` do `best.ShortID == self.ShortID` (linha 600) e o comentário `// A sibling covers materially more.` (linha 602):
```go
	// §18.2.2-v2 co-fire guard. O irmão vencedor pode JÁ ter a tocada própria na
	// janela: quando um material curto (subset de um spot longo) toca, o spot casa
	// fraco na região compartilhada e cairia aqui. Reatribuir criaria contagem
	// dobrada (incidente PILECCO 2026-06-30 — ver
	// docs/superpowers/specs/2026-06-30-cofire-reattribution-dedup-design.md).
	// Reusa o classificador do reject-path; no pass-path `self` está APROVADA, então
	// Skip/Restore retratam `self`.
	existing, ferr := s.detections.FindSiblingDetectionInWindow(ctx, best.ShortID, stationID, detectedAt, recoverRejWindowSeconds)
	if ferr != nil {
		s.log.Warn("evidence: co-fire — winner row lookup failed",
			zap.String("detection_id", detectionID.String()), zap.Error(ferr))
		return false
	}
	switch decideRejectRecovery(existing) {
	case RecoveryRestore:
		// v1 retraiu a tocada real do vencedor; restaura e retrata a duplicata.
		if cerr := s.detections.ClearRetraction(ctx, existing.ID, existing.DetectedAt); cerr != nil {
			s.log.Warn("evidence: co-fire — restore winner failed",
				zap.String("detection_id", detectionID.String()), zap.Error(cerr))
			return false
		}
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: co-fire — retract duplicate failed",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return false
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: co-fire — restored winner, retracted duplicate (§18.2.2 v2)",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", best.ShortID))
		return true
	case RecoverySkip:
		// Vencedor já tem tocada presente → esta row é a mesma veiculação. Retrata.
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: co-fire — retract duplicate failed",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return false
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: co-fire — winner already counted, retracted duplicate (§18.2.2 v2)",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", best.ShortID))
		return true
	case RecoveryReattribute:
		// Vencedor SEM row → reatribuição legítima (15s-contado-como-30s). Cai pro
		// fluxo normal abaixo (resolveAttribution + ReattributeDetection).
	}
```

- [ ] **Step 3: Compilar (cross-compile linux, igual ao Dockerfile)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem erros (todos os símbolos — `RetractByID`, `decideRejectRecovery`, `RecoveryRestore/Skip/Reattribute`, `recoverRejWindowSeconds`, `metrics.MatchDisambiguation` — resolvem).

- [ ] **Step 4: Rodar testes dos dois pacotes**

Run: `cd workers && go test ./internal/evidence/... ./internal/catalog/...`
Expected: PASS. `TestDecideRejectRecovery` (pacote evidence) cobre a árvore de decisão usada pelo guard; `TestDetections_RetractByID` cobre a retração.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/evidence/service.go workers/internal/evidence/reject_recovery.go
git commit -m "fix(disambig): co-fire guard no pass-path — retrata duplicata em vez de reatribuir (§18.2.2 v2)"
```

---

### Task 3: Régua regra 6 (gate de deploy)

- [ ] **Step 1: Cross-compile de TODOS os cmd/**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: exit 0.

- [ ] **Step 2: `go vet` + suíte completa**

Run: `cd workers && go vet ./... && go test ./...`
Expected: sem regressão. Falhas conhecidas/flaky (ex.: `internal/catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC) NÃO contam — confirmar que estão em pacotes não tocados.

- [ ] **Step 3: Checagens de boot da API**

Verificar que não há métrica nova no `MustRegister` (só um novo *label value* em `MatchDisambiguation`, que não precisa registro) → sem risco de panic no init. Sem migration nova → sem shadow test. Sem mudança de frontend/lockfile.

---

### Task 4: Reparo de dado (sistema todo — Dereck executa em prod)

Ver §6 do spec. Read-only blast-radius primeiro, depois retração com preview→COMMIT. Claude escreve, Dereck executa (regra 7). Mantém a row de MAIOR coverage por (estação, material, segundo).

---

## Self-Review

- **Cobertura do spec:** guard no pass-path (Task 2) ✓; reject-path inalterado ✓ (decisão registrada); `RetractByID` (Task 1) ✓; flag/kill-switch ✓ (reusa DISAMBIG_BY_COVERAGE — o guard só roda dentro de `reattributeByCoverage`, que só é chamado quando a flag está on); reparo de dado (Task 4) ✓; régua regra 6 (Task 3) ✓.
- **Placeholders:** nenhum — todo passo tem código/comando real.
- **Consistência de tipos:** `RetractByID(ctx, id, detectedAt, at)` definido na Task 1 e chamado idêntico na Task 2; `FindSiblingDetectionInWindow`/`ClearRetraction`/`decideRejectRecovery`/`RecoveryRestore|Skip|Reattribute`/`recoverRejWindowSeconds` existem no pacote (verificados no código). `metrics.MatchDisambiguation` é CounterVec (label value novo não exige registro).
- **Risco no reject-path:** zero mudança funcional (só 1 comentário). Guard 100% isolado no pass-path.
