# Version Disambiguation by Coverage — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Atribuir a veiculação ao CORTE que realmente tocou (15s vs 30s do mesmo cliente), usando a cobertura do clipe de evidência contra cada master — em vez do critério de DURAÇÃO ("o mais longo ganha"), que hoje conta 15s como 30s em 55% dos casos.

**Architecture:** A desambiguação §18.2.2 deixa de decidir por duração na confirmação (onde não há áudio pra decidir) e passa a ser CORRIGIDA no estágio de audit/evidence (onde o clipe existe). O audit re-fingerprinta o clipe contra o master atribuído E contra o(s) corte(s) irmão(s) do mesmo cliente; o corte com maior cobertura ganha. Se o vencedor por duração não é o mais coberto, troca-se a atribuição (retrai o errado, restaura o certo).

**Tech Stack:** Go 1.26 (`workers/`), Postgres (migrations golang-migrate), NATS core, React (`frontend/`). Testes Go pure-func sem DB (rodam local no Windows do dev); testes DB-gated SKIPAM local.

---

## ⚠️ Contexto — ONDE está o problema (não fuçar em lugar errado)

Isto foi **provado** numa investigação (2026-06-16/17). Não re-investigar o que já foi descartado:

- **NÃO é o matching.** O matcher detecta o spot certo. Provado: mesmo stream que o fornecedor.
- **NÃO é o threshold** (`min_hashes` é ~7 nas emissoras ativas, não 18) **nem cooldown** (veiculações são espaçadas).
- **NÃO dá pra usar "duração do match" / cobertura ao vivo.** A state machine **confirma em ~2s e PARA** (vai pro cooldown e descarta o resto). A "JANELA DE MATCH" gravada (`match_start_offset_ms` / `match_end_offset_ms`) são **dois offsets de alinhamento ~constantes** (~0-2s no master), **não** um intervalo — por isso o display mostra `-2.0s`. Não há informação ao vivo de "quanto do spot tocou".
- **O bug É a desambiguação por duração.** [`disambiguation.go::evaluateDedup`](../../workers/internal/supervisor/disambiguation.go) decide **só por `DurationSeconds`** (o mais longo ganha; empate por menor `short_id`). Quando o 15s toca, ele cobre o pedaço compartilhado do master de 30s o suficiente pra o state machine do 30s **também** confirmar → os dois confirmam → o 30s ganha por ser mais longo → o 15s é retraído. Resultado medido em 3 dias: corte 77 (15s) retraído **86/156 (55%)**, corte 78 (30s) retraído **0**.
- **O fix do audit (commits `179e95c`+`862bda3`) AMPLIFICOU a visibilidade:** antes, o 30s mal-atribuído (clipe com só 15s de conteúdo) tinha cobertura de audit baixa → `audit_rejected` → ficava escondido. O bypass de score≥30 passou a deixá-lo visível. Isso é correto pro recall, mas expôs a má-atribuição.

### O sinal que FUNCIONA (provado às cegas)

Cobertura do **clipe de evidência** (multi-variante, prod-fiel) contra cada master. Teste cego com 2 censuras reais do ASAAS (cmd `audit-extent` + `diagnose_censura.py`, dois pipelines independentes, mesma resposta):

| Veiculação (ground truth) | cobertura vs **78 (30s)** | cobertura vs **77 (15s)** |
|---|---|---|
| **30s real** | **0.60** | 0.15 |
| **15s real** | 0.11 | **0.70** |

Margem **4-5×**, robusta a qualidade (os cortes têm conteúdo majoritariamente diferente; compartilham só uma vinheta/abertura — o que faz o 30s confirmar falso, mas a cobertura separa limpo). **Regra:** o corte cujo master é MAIS coberto pelo clipe é o que tocou.

> Já existe instrumentação (commit `0ea2d31`): `audit.Result.MatchExtent`, log de `extent` no audit, e `cmd/audit-extent` (modo DB `--short-id` e modo offline `--master-file`). O extent (frame máximo) **satura** e NÃO discrimina — usar **cobertura** (`Result.Coverage`), não extent, pra decidir.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `migrations/0038_detection_audit_coverage.up.sql` / `.down.sql` | Persistir `audit_coverage` no detection (pra irmãos compararem + pro display) | Create |
| `workers/internal/catalog/detections.go` | Gravar/ler `audit_coverage`; query de irmãos; helper de un-retract | Modify |
| `workers/internal/audit/auditor.go` | (já tem `Coverage`/`MatchExtent`) — nada novo; reusar `AuditEvidence` | Reuse |
| `workers/internal/evidence/service.go` | Após audit: gravar coverage; cross-check de irmãos; corrigir atribuição | Modify |
| `workers/internal/evidence/disambig_coverage.go` | Lógica pura de decisão (qual corte ganha por cobertura) — testável sem DB | Create |
| `workers/internal/evidence/disambig_coverage_test.go` | Testes pure-func da decisão | Create |
| `workers/internal/supervisor/disambiguation.go` | `DedupActionSuppress` → publicar-e-retrair (garante que os 2 rows existam pra correção) | Modify |
| `frontend/src/pages/DetectionDetailPage.jsx` | Corrigir a "Janela de match" (`-2.0s`); mostrar cobertura/extent do audit | Modify |

---

## Phase 0 — Display fix da "Janela de match" (independente, frontend-only)

Risco zero, sem backend. Pode shipar antes de tudo. Hoje [DetectionDetailPage.jsx:336](../../frontend/src/pages/DetectionDetailPage.jsx#L336) calcula `dur = match_end_offset_ms - match_start_offset_ms`, que dá negativo porque os offsets não são um intervalo.

### Task 0.1: Não exibir duração sem sentido na Janela de match

**Files:**
- Modify: `frontend/src/pages/DetectionDetailPage.jsx:336` e o bloco `dd-window-display` (~380-388)

- [ ] **Step 1: Localizar o uso de `dur`** — `grep -n "const dur" frontend/src/pages/DetectionDetailPage.jsx` e onde `dur` é renderizado (provável `DURAÇÃO {dur/1000}s` ou similar logo abaixo do `dd-window-range`).

- [ ] **Step 2: Remover o cálculo/exibição da "duração" derivada dos offsets.** Trocar a label enganosa. Os offsets de alinhamento (`match_start_offset_ms`/`match_end_offset_ms`) ficam como estão (informativo: onde no master alinhou), mas SEM a linha "DURAÇÃO -2.0s". Substituir o bloco da duração por:

```jsx
// REMOVER: const dur = (detection.match_end_offset_ms ?? 0) - (detection.match_start_offset_ms ?? 0)
// e a linha que renderiza "DURAÇÃO {dur}s".
// A "Janela de match" passa a mostrar só os offsets de alinhamento, sem duração derivada
// (eles NÃO são um intervalo — são o offset de alinhamento no master no 1º match e na confirmação).
```

- [ ] **Step 3: Build do frontend local** — `cd frontend && npm run build` (NÃO rodar `npm install` no Windows — quebra o lockfile do CF Pages, ver CLAUDE.md §5). Esperado: build OK.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/DetectionDetailPage.jsx
git commit -m "fix(detections): remove duração sem sentido (-2.0s) da Janela de match — offsets de alinhamento não são intervalo"
```

> O display "rico" (mostrar a cobertura real do audit / quanto do master casou) entra na Task 4.3, depois que `audit_coverage` for persistido.

---

## Phase 1 — Persistir `audit_coverage` no detection

Necessário pra (a) o irmão comparar cobertura e (b) o display rico. Aditivo, sem mudança de comportamento.

### Task 1.1: Migration — coluna `audit_coverage`

**Files:**
- Create: `migrations/0038_detection_audit_coverage.up.sql`
- Create: `migrations/0038_detection_audit_coverage.down.sql`

- [ ] **Step 1: Escrever a migration up** (ler `docs/operations/migrations.md` antes — leitura obrigatória)

```sql
-- 0038_detection_audit_coverage.up.sql
ALTER TABLE detections
  ADD COLUMN IF NOT EXISTS audit_coverage NUMERIC(5,4);
COMMENT ON COLUMN detections.audit_coverage IS
  'Cobertura do §9.9 audit (frames do master casados no clipe / total). Usada pela desambiguação por cobertura (§18.2.2 v2) e pelo /detections/:id.';
```

- [ ] **Step 2: Escrever a migration down**

```sql
-- 0038_detection_audit_coverage.down.sql
ALTER TABLE detections DROP COLUMN IF EXISTS audit_coverage;
```

- [ ] **Step 3: Aplicar local/teste** — `migrate -path migrations -database "$TEST_DATABASE_URL" up` (ou via `./scripts/deploy.sh` em prod no rollout). Esperado: sem erro.

- [ ] **Step 4: Commit**

```bash
git add migrations/0038_detection_audit_coverage.up.sql migrations/0038_detection_audit_coverage.down.sql
git commit -m "feat(db): coluna detections.audit_coverage (migration 0038)"
```

### Task 1.2: Gravar a cobertura do audit no detection

**Files:**
- Modify: `workers/internal/catalog/detections.go` (a função `UpdateEvidence` ou um novo `SetAuditResult`)
- Modify: `workers/internal/evidence/service.go::runAuditOrReject` (passar `result.Coverage`)

- [ ] **Step 1: Teste (DB-gated)** em `detections_test.go`: inserir um detection, chamar o novo setter com coverage 0.42, reler e conferir. (DB-gated SKIPa local; roda em prod/banco de teste.)

```go
func TestSetAuditCoverage(t *testing.T) {
    // ... cria detection pending ...
    err := repo.SetAuditCoverage(ctx, detectionID, detectedAt, 0.42)
    if err != nil { t.Fatal(err) }
    got := /* SELECT audit_coverage ... */
    if got != 0.42 { t.Errorf("audit_coverage=%v want 0.42", got) }
}
```

- [ ] **Step 2: Implementar `SetAuditCoverage`** em `detections.go`:

```go
func (d *Detections) SetAuditCoverage(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, coverage float64) error {
    _, err := d.db.Exec(ctx,
        `UPDATE detections SET audit_coverage = $1 WHERE id = $2 AND detected_at = $3`,
        coverage, detectionID, detectedAt)
    return err
}
```
(Nota: `detections` é particionada por `detected_at` — incluir `detected_at` no WHERE pra partition pruning, padrão do resto do arquivo.)

- [ ] **Step 3: Chamar no audit que PASSA** — em `runAuditOrReject`, no ramo `result.Passed` (após `audit passed`), chamar `s.detections.SetAuditCoverage(ctx, detectionID, detectedAt, result.Coverage)`. (Não bloquear o upload se falhar — logar warn.)

- [ ] **Step 4: Rodar** — `go -C workers build ./...` + `go -C workers test ./internal/catalog/ ./internal/evidence/` (DB-gated SKIPa local; confirmar build).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/evidence/service.go
git commit -m "feat(evidence): persiste audit_coverage no detection ao passar o §9.9"
```

---

## Phase 2 — Decisão pura: qual corte ganha por cobertura

Lógica pura (sem DB/NATS) → 100% testável local no Windows. Esta é a peça que captura a regra provada.

### Task 2.1: `chooseByCoverage`

**Files:**
- Create: `workers/internal/evidence/disambig_coverage.go`
- Create: `workers/internal/evidence/disambig_coverage_test.go`

- [ ] **Step 1: Escrever os testes** (RED). Casos: 15s ganha do 30s; 30s ganha do 15s; empate dentro da margem cai pro mais longo (comportamento atual preservado); irmão sem cobertura medida não decide.

```go
package evidence

import "testing"

// CutCoverage descreve um corte candidato e sua cobertura do clipe.
type CutCoverage struct {
    ShortID         int32
    DurationSeconds int
    Coverage        float64 // cobertura do clipe contra ESTE master (audit)
}

func TestChooseByCoverage_ShorterCutWinsWhenItCoversMore(t *testing.T) {
    // 15s airing: 77 cobre 0.70, 78 cobre 0.15 → 77 ganha.
    got := chooseByCoverage(
        CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.15},
        CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.70},
    )
    if got != 77 { t.Fatalf("winner=%d want 77 (covers more)", got) }
}

func TestChooseByCoverage_LongerCutWinsWhenItCoversMore(t *testing.T) {
    got := chooseByCoverage(
        CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.60},
        CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.15},
    )
    if got != 78 { t.Fatalf("winner=%d want 78 (covers more)", got) }
}

func TestChooseByCoverage_TieWithinMarginFallsBackToDuration(t *testing.T) {
    // Coberturas próximas (dentro da margem) → preserva o comportamento atual: mais longo ganha.
    got := chooseByCoverage(
        CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.50},
        CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.48},
    )
    if got != 78 { t.Fatalf("winner=%d want 78 (tie → longer)", got) }
}
```

- [ ] **Step 2: Rodar e ver falhar** — `go -C workers test ./internal/evidence/ -run TestChooseByCoverage -v`. Esperado: FAIL (`chooseByCoverage` não existe).

- [ ] **Step 3: Implementar**

```go
package evidence

// coverageMargin: o vencedor por cobertura precisa cobrir pelo menos esta fração
// a MAIS que o perdedor pra sobrepor o critério de duração. Abaixo disso (quase
// empate, ex.: 30s real cobrindo ambos quase igual) cai pro mais longo, como antes.
// Calibrado pela margem provada (4-5×); 1.5 é folgado e seguro.
const coverageMargin = 1.5

// chooseByCoverage devolve o short_id do corte que tocou: o de MAIOR cobertura do
// clipe, se a vantagem passar de coverageMargin; senão, o de maior duração
// (desempate estável por menor short_id), preservando §18.2.2 original.
func chooseByCoverage(a, b CutCoverage) int32 {
    hi, lo := a, b
    if b.Coverage > a.Coverage {
        hi, lo = b, a
    }
    if lo.Coverage <= 0 || hi.Coverage >= lo.Coverage*coverageMargin {
        return hi.ShortID
    }
    // quase empate → duração (e menor short_id no empate exato)
    if a.DurationSeconds != b.DurationSeconds {
        if a.DurationSeconds > b.DurationSeconds {
            return a.ShortID
        }
        return b.ShortID
    }
    if a.ShortID < b.ShortID {
        return a.ShortID
    }
    return b.ShortID
}
```

- [ ] **Step 4: Rodar e ver passar** — `go -C workers test ./internal/evidence/ -run TestChooseByCoverage -v`. Esperado: PASS (todos).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/evidence/disambig_coverage.go workers/internal/evidence/disambig_coverage_test.go
git commit -m "feat(evidence): chooseByCoverage — corte que tocou = maior cobertura do clipe (TDD)"
```

---

## Phase 3 — Cross-check de irmãos + correção da atribuição

Integra a Phase 2 no fluxo real: ao auditar um detection, conferir os irmãos e corrigir se o vencedor por duração não for o mais coberto.

### Task 3.1: Garantir que o irmão "perdedor" exista como row (suppress → retract)

Hoje, se o corte mais longo confirma ANTES do mais curto, o curto é **suprimido** (`DedupActionSuppress`, não vira row). A correção precisa que os 2 rows existam. Mudança: o supervisor passa a **publicar e retrair** o perdedor por duração em vez de suprimir.

**Files:**
- Modify: `workers/internal/supervisor/disambiguation.go::SubmitDetection` (caso `DedupActionSuppress`)

- [ ] **Step 1: Teste (pure-func)** em `disambiguation_test.go`: hoje já há testes de `evaluateDedup`. Adicionar/ajustar pra documentar que o caminho de suppress deve resultar em "publica e marca retraído" no `SubmitDetection`. (A decisão `evaluateDedup` continua igual; muda só a AÇÃO no `case DedupActionSuppress`.) Como `SubmitDetection` toca NATS/DB, testar via o teste de integração existente do supervisor (DB-gated) OU refatorar a ação pra uma função pura `actionFor(...)` testável. Mínimo: teste pure-func garantindo que suppress não significa "sumir".

- [ ] **Step 2: Implementar** — no `case DedupActionSuppress` de `SubmitDetection`, em vez de só logar e descartar: publicar o candidato (`s.publishConfirmed(ctx, original)`) e imediatamente retraí-lo contra o vencedor (`s.retract(ctx, det, "shorter_cut_pending_audit", conflict.Detection.CommercialShortID)`), e `s.dedupBuffer.Add(newEntry)`. Assim os DOIS cortes viram row (um retraído), prontos pra correção do audit.

```go
case DedupActionSuppress:
    metrics.MatchDisambiguation.WithLabelValues("suppressed").Inc()
    // Publica E retrai (em vez de descartar): a correção por cobertura no audit
    // (§18.2.2 v2) precisa que ambos os cortes existam como row pra poder trocar
    // a atribuição. O perdedor por duração nasce retraído; o audit reverte se ele
    // for, na real, o corte que tocou (cobre mais).
    s.publishConfirmed(ctx, original)
    s.retract(ctx, det, "shorter_cut_pending_audit", conflict.Detection.CommercialShortID)
    s.dedupBuffer.Add(newEntry)
```

- [ ] **Step 3: Rodar** — `go -C workers build ./...` + testes do supervisor (DB-gated SKIPa local; confirmar build + os pure-func de `evaluateDedup` seguem verdes).

- [ ] **Step 4: Commit**

```bash
git add workers/internal/supervisor/disambiguation.go workers/internal/supervisor/disambiguation_test.go
git commit -m "fix(disambig): suppress vira publish+retract — ambos os cortes viram row pra correção por cobertura"
```

### Task 3.2: Query de detections-irmãos no mesmo break

**Files:**
- Modify: `workers/internal/catalog/detections.go`

- [ ] **Step 1: Teste (DB-gated)** em `detections_test.go`: inserir 2 detections do mesmo `client_id` (via `commercial_id` que resolve pro cliente), mesma station, dentro de ±N seg; `FindSiblingDetections` deve retornar o outro. (SKIPa local.)

- [ ] **Step 2: Implementar `FindSiblingDetections`** — dado (detectionID, stationID, commercialID, detectedAt), retorna detections de OUTROS cortes do MESMO cliente, mesma station, `detected_at` dentro de ±`siblingWindowSeconds` (ex.: 90s), com seu `audit_coverage` e a duração. Resolver cliente via `commercials/materials.client_id` (mesmo padrão do `LookupForDedup`).

```sql
SELECT d.id, d.detected_at, d.commercial_id, COALESCE(d.audit_coverage, -1) AS cov, m.duration_seconds, m.short_id
FROM detections d
JOIN materials m ON m.id = d.commercial_id
WHERE d.station_id = $1
  AND d.detected_at BETWEEN $2::timestamptz - ($3 || ' seconds')::interval
                        AND $2::timestamptz + ($3 || ' seconds')::interval
  AND d.id <> $4
  AND m.client_id = (SELECT client_id FROM materials WHERE id = $5)
  AND m.id <> $5;
-- (UNION com commercials pra legado, espelhando markDetectionRetracted)
```

- [ ] **Step 3: Build + commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(catalog): FindSiblingDetections — cortes irmãos do mesmo cliente no mesmo break"
```

### Task 3.3: Correção por cobertura no audit (o coração do fix)

**Files:**
- Modify: `workers/internal/evidence/service.go::runAuditOrReject` (após o audit passar e gravar coverage)

- [ ] **Step 1: Teste (DB-gated, integração)** em `evidence/service_test.go`: cenário 15s-virou-30s. Inserir o detection 78 (atribuído, retraiu 77), 77 retraído. Auditar 78 com clipe que cobre 77>78. Esperar: 78 vira `audit_rejected` (ou retraído) e 77 fica `retracted_at = NULL` (restaurado). (SKIPa local; roda em banco de teste.)

- [ ] **Step 2: Implementar a correção.** Após gravar `audit_coverage` do detection X (cut atual), buscar irmãos via `FindSiblingDetections`. Pra cada irmão Y que JÁ tem `audit_coverage`, re-auditar o clipe de X contra o master de Y **não é preciso** — Y já tem a cobertura DELE contra o master DELE (gravada quando Y foi auditado com o MESMO break). Comparar via `chooseByCoverage(X, Y)`:
  - Se vencedor == X.ShortID e Y está available → retrair Y (`markDetectionRetracted(Y)`), métrica `disambiguation{action="retracted_by_coverage"}`.
  - Se vencedor == Y.ShortID e X está available → retrair X (e, se Y estava retraído por duração, **un-retrair** Y via novo `ClearRetraction(Y)`), métrica `action="reattributed_by_coverage"`.
  - Margem/empate: `chooseByCoverage` já cai pra duração — nesse caso não mexe (preserva o atual).

```go
// pseudo, dentro de runAuditOrReject após SetAuditCoverage(X, result.Coverage):
sibs, _ := s.detections.FindSiblingDetections(ctx, detectionID, stationID, commercialID, detectedAt)
for _, y := range sibs {
    if y.Coverage < 0 { continue } // irmão ainda não auditado; ele decide quando chegar
    winner := chooseByCoverage(
        CutCoverage{ShortID: xShortID, DurationSeconds: xDur, Coverage: result.Coverage},
        CutCoverage{ShortID: y.ShortID, DurationSeconds: y.DurationSeconds, Coverage: y.Coverage},
    )
    switch {
    case winner == xShortID && y.Available():
        s.retractSibling(ctx, y, "lower_coverage")
    case winner == y.ShortID:
        s.detections.ClearRetraction(ctx, y) // restaura o corte certo
        // X (este detection) perde: marca como overruled
        s.detections.MarkRetractedBy(ctx, detectionID, detectedAt, y.ShortID, "lower_coverage")
        return true // não sobe evidência do corte errado
    }
}
```

> **Idempotência:** quem é auditado por ÚLTIMO faz a comparação (o primeiro vê `y.Coverage < 0` e não age). `ClearRetraction`/`markDetectionRetracted` são idempotentes (WHERE no estado atual). Cobrir o caso "ambos auditados quase juntos" com `ClearRetraction` só agindo se `retracted_at IS NOT NULL`.

- [ ] **Step 3: Implementar `ClearRetraction`** em `detections.go` (UPDATE `retracted_at = NULL WHERE id=$ AND detected_at=$ AND retracted_at IS NOT NULL`). Teste DB-gated.

- [ ] **Step 4: Build + rodar** — `go -C workers build ./...`; testes DB-gated rodam no banco de teste.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/evidence/service.go workers/internal/catalog/detections.go workers/internal/evidence/service_test.go
git commit -m "feat(disambig): correção por cobertura no audit — 15s não vira 30s (§18.2.2 v2)"
```

---

## Phase 4 — Display rico + validação + rollout

### Task 4.1: Feature flag

- [ ] Env `DISAMBIG_BY_COVERAGE=true|false` (default `false` no primeiro deploy). Lido no `cmd/api/main.go`, plumbado pro evidence service. Com `false`, a correção da Task 3.3 não roda (comportamento = hoje). Permite shipar o código e ligar quando validado. Commit.

### Task 4.2: Validar com as censuras (audit-extent) + métricas

- [ ] **Sem deploy de comportamento:** rodar `cmd/audit-extent --short-id 77` e `--short-id 78` nas censuras conhecidas (na VM, DB real). Confirmar que a cobertura separa (15s → 77 alto, 78 baixo). Já validado offline; confirmar contra DB real.
- [ ] **Pós-ligar a flag:** métrica `radiocheck_match_disambiguation_total{action="reattributed_by_coverage"}` deve subir; rodar a query de retração do ASAAS (77 retraído deve CAIR de 55%): comparar `audit_coverage` dos 78 mantidos — devem ser altos (>~0.4); 78 com cobertura baixa devem ter virado 77.

### Task 4.3: Display rico no /detections/:id (depende da Phase 1)

- [ ] Mostrar `detection.audit_coverage` (e, opcional, o corte atribuído + "confirmado por cobertura") no painel, no lugar da duração removida na Task 0.1. Build do frontend (sem `npm install`). Commit.

### Task 4.4: Doc + memória

- [ ] Atualizar `docs/architecture/version-disambiguation.md`: o critério passou de duração para cobertura do clipe; documentar `coverageMargin`, o fluxo de correção, e o R-B (suppress→retract). `ultima-verificacao` = data do deploy.
- [ ] Atualizar a memória `recall-gap-root-cause-capture` com o desfecho da desambiguação.

---

## Riscos & decisões

- **R1 — flip downstream (webhook/UI).** O corte errado pode aparecer e depois ser retraído/trocado (segundos). É o mesmo R-A do §18.2.2 original (retração já causa flip). Mitigação: relatórios filtram `retracted_at IS NULL`; UI risca retraído.
- **R2 — dois irmãos auditados concorrentes.** Idempotência por "quem chega por último compara" + `ClearRetraction`/`retract` condicionais ao estado. Cobrir no teste de integração.
- **R3 — cliente com muitos cortes.** `FindSiblingDetections` é limitado por station + janela ±90s → só os cortes que confirmaram no mesmo break (1-2 na prática). Sem custo de re-fingerprint extra (reusa `audit_coverage` já gravada).
- **R4 — margem mal calibrada.** `coverageMargin=1.5` é folgado vs a margem provada (4-5×). Se um caso real ficar perto, cai pro comportamento atual (duração) — falha segura. Tunável.
- **R5 — caminho-core.** Mexe em supervisor + evidence. Mitigado por: feature flag (Task 4.1), TDD nas peças puras, e validação por métrica antes de confiar.

## Rollout

1. Migration 0038 (`./scripts/deploy.sh` — inclui migrate). 
2. Deploy do `api` com a flag `DISAMBIG_BY_COVERAGE=false` (código presente, comportamento inalterado).
3. Validar `audit-extent` + `audit_coverage` populando.
4. Ligar `DISAMBIG_BY_COVERAGE=true`, observar `disambiguation{action="reattributed_by_coverage"}` e a queda da retração do 77. Reverter a flag se algo estranho.
5. Phase 0 (display) shipa independente a qualquer momento.

## Self-review

- Cobertura do spec: diagnóstico documentado (contexto), sinal provado (cobertura), Phase 1 (persistir), Phase 2 (decisão pura, TDD), Phase 3 (cross-check + correção + suppress→retract), Phase 4 (flag, validação, display, doc). Display bug coberto na Phase 0 + 4.3. ✓
- Sem placeholders nos passos de código das peças puras (Phase 2) e do display (Phase 0). As integrações DB-gated (Phase 1/3) têm SQL e assinaturas concretas; o detalhe fino do wiring se resolve na execução (são DB-gated, validados em banco de teste).
- Consistência de nomes: `audit_coverage`, `chooseByCoverage`, `CutCoverage`, `FindSiblingDetections`, `ClearRetraction`, `coverageMargin`, flag `DISAMBIG_BY_COVERAGE` — usados consistentes entre tasks. ✓
