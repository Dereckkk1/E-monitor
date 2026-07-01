# Desambiguação de Gêmeos Acústicos — Plano 1 (backend core)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Atribuir gêmeos acústicos de mesma duração ao criativo que de fato tocou, medindo a cobertura do clipe no trecho que os distingue; marcar `ambiguous` quando o áudio não permite decidir.

**Architecture:** Arquitetura B (pós-audit). Estende o gancho `reattributeByCoverage` que já roda após o §9.9. Quando a cobertura-cheia empata E as durações são ~iguais (único caso que cobertura/duração não resolvem), computa cobertura discriminante (frames não-sobrepostos, reusando o overlap do `sharing`) e decide; senão marca `ambiguous`. Não toca no matcher em tempo real.

**Tech Stack:** Go (workers/), pgx/pgxpool, PostgreSQL (particionado), testes com DB limpo migrado.

**Spec:** [`docs/superpowers/specs/2026-07-01-acoustic-twin-disambiguation-design.md`](../specs/2026-07-01-acoustic-twin-disambiguation-design.md)

**Escopo deste plano:** só o backend core. A **fila de revisão (frontend)** e os **comandos de backfill** são planos separados que dependem deste.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `migrations/00NN_material_twin_discriminative.up/.down.sql` | Tabela das regiões discriminantes por par de gêmeos | Criar |
| `migrations/00NN_evidence_status_ambiguous.up/.down.sql` | Adiciona `'ambiguous'` ao CHECK de `detections.evidence_status` | Criar |
| `workers/internal/audit/auditor.go` | Nova `CoverageOnFrames` — cobertura restrita a frame-ranges | Modificar |
| `workers/internal/sharing/sharing.go` | Nova `ComputeTwinOverlap` — frame-ranges de overlap entre dois masters | Modificar |
| `workers/internal/catalog/twin_discriminative.go` | Repo da tabela `material_twin_discriminative` (upsert/get) | Criar |
| `workers/internal/evidence/twin_disambig.go` | Regra de decisão pura (`chooseTwinByDiscriminative`) + orquestração | Criar |
| `workers/internal/evidence/twin_disambig_test.go` | Testes da regra pura | Criar |
| `workers/internal/evidence/service.go` | Wire do passo discriminante no `reattributeByCoverage` + flag `disambigTwin` | Modificar |
| `workers/internal/catalog/detections.go` | `MarkAmbiguous` + exclusão de `ambiguous` do conjunto aprovado | Modificar |
| `workers/internal/metrics/metrics.go` | Labels novos em `MatchDisambiguation` | Modificar (sem código novo — labels são strings) |

**Ordem de dependência:** migrations → audit.CoverageOnFrames → sharing.ComputeTwinOverlap → catalog.twin_discriminative repo → evidence.twin_disambig (regra pura) → wire no service → ambiguous status. Cada tarefa commita sozinha.

---

## Task 1: Migration — tabela `material_twin_discriminative`

**Files:**
- Create: `migrations/00NN_material_twin_discriminative.up.sql`
- Create: `migrations/00NN_material_twin_discriminative.down.sql`

> Descubra o próximo N com `ls migrations/ | tail`. Ambas as migrations são aditivas (passam no shadow-test do deploy).

- [ ] **Step 1: Escreva a up.sql**

```sql
-- 00NN_material_twin_discriminative.up.sql
-- Regiões discriminantes por par de gêmeos acústicos (spec 2026-07-01).
-- disc_ranges = frame-ranges de material_id que NÃO se sobrepõem a twin_id.
-- disc_frames = total de frames discriminantes (denominador da cobertura).
-- disc_frames = 0 => par não separável pelo áudio (sempre ambíguo).
BEGIN;

CREATE TABLE material_twin_discriminative (
    material_id  UUID          NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    twin_id      UUID          NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    disc_ranges  int4range[]   NOT NULL,
    disc_frames  INT           NOT NULL,
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (material_id, twin_id)
);
CREATE INDEX idx_twin_disc_material ON material_twin_discriminative(material_id);

COMMIT;
```

- [ ] **Step 2: Escreva a down.sql**

```sql
-- 00NN_material_twin_discriminative.down.sql
BEGIN;
DROP TABLE material_twin_discriminative;
COMMIT;
```

- [ ] **Step 3: Aplique num DB limpo pra validar (não o dev populado)**

Run:
```bash
docker rm -f rc-testdb >/dev/null 2>&1; docker run -d --name rc-testdb --network docker_default -e POSTGRES_DB=radiocheck -e POSTGRES_USER=radiocheck -e POSTGRES_HOST_AUTH_METHOD=trust postgres:16-alpine >/dev/null; sleep 3
REPO=$(pwd -W); MSYS_NO_PATHCONV=1 docker run --rm --network docker_default -v "${REPO}/migrations:/migrations" migrate/migrate -path=/migrations -database "postgres://radiocheck@rc-testdb:5432/radiocheck?sslmode=disable" up
```
Expected: última linha `00NN/u material_twin_discriminative`, sem erro.

- [ ] **Step 4: Commit**

```bash
git add migrations/00NN_material_twin_discriminative.up.sql migrations/00NN_material_twin_discriminative.down.sql
git commit -m "feat(migrations): tabela material_twin_discriminative (gêmeos acústicos)"
```

---

## Task 2: Migration — `evidence_status='ambiguous'`

**Files:**
- Create: `migrations/00NN_evidence_status_ambiguous.up.sql`
- Create: `migrations/00NN_evidence_status_ambiguous.down.sql`

> Primeiro descubra o CHECK atual: `grep -rn "evidence_status" migrations/*.up.sql | grep -i check`. O ALTER precisa dropar e recriar o constraint com o novo valor.

- [ ] **Step 1: Escreva a up.sql** (ajuste o nome do constraint ao que o grep achou)

```sql
-- 00NN_evidence_status_ambiguous.up.sql
-- Adiciona 'ambiguous' aos valores válidos de detections.evidence_status.
-- Detecção de gêmeo acústico cujo trecho discriminante não sobreviveu → vai
-- pra revisão manual, NÃO conta como confirmada (spec 2026-07-01).
BEGIN;
ALTER TABLE detections DROP CONSTRAINT detections_evidence_status_check;
ALTER TABLE detections ADD CONSTRAINT detections_evidence_status_check
  CHECK (evidence_status IN ('pending','available','missing','failed','audit_rejected','ambiguous'));
COMMIT;
```

- [ ] **Step 2: Escreva a down.sql** (reverte pro CHECK sem 'ambiguous')

```sql
-- 00NN_evidence_status_ambiguous.down.sql
BEGIN;
ALTER TABLE detections DROP CONSTRAINT detections_evidence_status_check;
ALTER TABLE detections ADD CONSTRAINT detections_evidence_status_check
  CHECK (evidence_status IN ('pending','available','missing','failed','audit_rejected'));
COMMIT;
```

- [ ] **Step 3: Aplicar no DB limpo + commit**

Run o mesmo `migrate ... up` da Task 1. Expected: aplica sem erro.
```bash
git add migrations/00NN_evidence_status_ambiguous.up.sql migrations/00NN_evidence_status_ambiguous.down.sql
git commit -m "feat(migrations): evidence_status 'ambiguous' pra gêmeos indistinguíveis"
```

---

## Task 3: `audit.CoverageOnFrames` — cobertura restrita a frame-ranges

**Files:**
- Modify: `workers/internal/audit/auditor.go`
- Test: `workers/internal/audit/auditor_test.go`

Objetivo: dado o resultado de um match (frames do master cobertas pelo clipe no bin vencedor) e um conjunto de frame-ranges discriminantes, devolver `covered ∩ disc / |disc|`. **Primeiro** exponha as frames cobertas do `runMatch` (hoje só o número vira `Coverage`).

- [ ] **Step 1: Leia `runMatch` (auditor.go:181-260)** para ver como as frames cobertas do bin vencedor são acumuladas (a união `distinct master frames in peak bin ± coverageBinRadius`). Vai reusar essa mesma coleção.

- [ ] **Step 2: Escreva o teste que falha** (`auditor_test.go`)

```go
func TestCoverageOnFrames(t *testing.T) {
	// master com frames cobertas {10,11,12, 40,41} (simuladas)
	covered := map[int32]bool{10: true, 11: true, 12: true, 40: true, 41: true}
	// disc range [39,42) => frames discriminantes 39,40,41 ; cobertas ∩ disc = {40,41}
	got := CoverageOnFrames(covered, []FrameRange{{Lo: 39, Hi: 42}})
	want := 2.0 / 3.0
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("CoverageOnFrames = %v, want %v", got, want)
	}
	// disc vazio => 0 (par não separável)
	if got := CoverageOnFrames(covered, nil); got != 0 {
		t.Fatalf("CoverageOnFrames(empty) = %v, want 0", got)
	}
}
```

- [ ] **Step 3: Rode e veja falhar**

Run: `go test ./internal/audit/ -run TestCoverageOnFrames -v`
Expected: FAIL — `undefined: CoverageOnFrames` / `undefined: FrameRange`.

- [ ] **Step 4: Implemente** (adicione ao `auditor.go`)

```go
// FrameRange é um intervalo de frames [Lo, Hi) do master.
type FrameRange struct{ Lo, Hi int32 }

// CoverageOnFrames devolve a fração das frames discriminantes (união dos ranges)
// que estão em `covered` (frames do master batidas pelo clipe no bin vencedor).
// Ranges vazios => 0 (par de gêmeos sem trecho que os separe).
func CoverageOnFrames(covered map[int32]bool, disc []FrameRange) float64 {
	total, hit := 0, 0
	for _, r := range disc {
		for f := r.Lo; f < r.Hi; f++ {
			total++
			if covered[f] {
				hit++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}
```

- [ ] **Step 5: Exponha as frames cobertas do match.** Modifique `runMatch` pra também devolver o `map[int32]bool` de frames cobertas do bin vencedor (a mesma união já computada pra `Coverage`), num novo campo `CoveredFrames map[int32]bool` no `Result`. Adicione uma função pública `AuditWithCoveredFrames(ctx, commercialID, pcm) (*Result, error)` idêntica a `AuditEvidence` mas que garante `Result.CoveredFrames` preenchido (ou apenas preencha em `AuditEvidence` — é barato).

Teste (novo caso em `auditor_test.go`, usando `MatchHashes` com hashes sintéticos onde você conhece as frames):
```go
func TestRunMatch_ExposesCoveredFrames(t *testing.T) {
	// monte masterHashes e queryHashes sintéticos com um alinhamento conhecido
	// (reuse o padrão dos testes existentes de auditor_test.go) e verifique que
	// res.CoveredFrames contém exatamente as frames esperadas do bin vencedor.
}
```
Implemente preenchendo `res.CoveredFrames` na coleta da união de frames (peak bin ± coverageBinRadius) dentro de `runMatch`.

- [ ] **Step 6: Rode tudo do pacote audit**

Run: `go test ./internal/audit/ -v`
Expected: PASS (os testes existentes de coverage/extent continuam verdes; note que `TestBuildDailySummary_WithDowntime` NÃO é deste pacote).

- [ ] **Step 7: Commit**

```bash
git add workers/internal/audit/auditor.go workers/internal/audit/auditor_test.go
git commit -m "feat(audit): CoverageOnFrames + expõe frames cobertas do bin vencedor"
```

---

## Task 4: `sharing.ComputeTwinOverlap` — frame-ranges de overlap entre dois masters

**Files:**
- Modify: `workers/internal/sharing/sharing.go`
- Test: `workers/internal/sharing/sharing_test.go`

Objetivo: dado master X e Y, devolver os frame-ranges de X que se sobrepõem a Y (as janelas onde o `MatchWindow` de X bate em Y). Os discriminantes = complemento em [0, totalFramesX).

- [ ] **Step 1: Leia `MarkSharedHashes` + `classifyAndFilter` (sharing.go:144-382)** — o cálculo de `ownRanges`/`otherRanges` por par já existe (o mesmo `pairScan` do `similarity`). Extraia a parte que produz os frame-ranges de overlap de X vs um único Y, SEM o filtro subset/sting (queremos o overlap cru).

- [ ] **Step 2: Teste que falha** (`sharing_test.go`) — precisa de DB (masters fingerprintados). Use o helper de fixture do pacote (procure `newTestDB`/seed de fingerprint em `sharing_test.go` ou `index/testhelpers_test.go`).

```go
func TestComputeTwinOverlap_DiscriminativeComplement(t *testing.T) {
	ctx, pool := newTestDB(t)
	// seed: master A (30s) e master B (30s) que compartilham as frames [0, 180)
	// e divergem em [180, 234) (o "trecho da chamada"). (helper de fixture.)
	aID, bID := seedTwinMasters(t, ctx, pool /* shared+divergent */)
	repo := New(pool) // ou a assinatura do pacote
	overlap, totalA, err := repo.ComputeTwinOverlap(ctx, aID, bID)
	if err != nil { t.Fatal(err) }
	// overlap de A vs B deve cobrir ~[0,180); o complemento [180, totalA) é discriminante
	// assert: nenhuma range de overlap entra em [180, totalA) (o trecho único de A)
}
```

- [ ] **Step 3: Rode e veja falhar** — `go test ./internal/sharing/ -run TestComputeTwinOverlap -v` → FAIL (`undefined: ComputeTwinOverlap`).

- [ ] **Step 4: Implemente `ComputeTwinOverlap(ctx, xID, yID) (overlapRanges []audit.FrameRange, totalFramesX int, err error)`** — decodifica o master X, roda `MatchWindow` deslizante (Window/Hop já constantes) SÓ contra o índice de Y, acumula os frame-ranges de X batidos, mescla os sobrepostos (reuse a mesma lógica de merge do `MarkSharedHashes`), devolve. `totalFramesX` = frames totais de X.

> Nota: reaproveite o máximo de `MarkSharedHashes` — idealmente extraia um helper interno `overlapRangesXvsY` que `MarkSharedHashes` e `ComputeTwinOverlap` compartilham (DRY).

- [ ] **Step 5: PASS + commit** — `go test ./internal/sharing/ -v` (DB limpo).
```bash
git add workers/internal/sharing/sharing.go workers/internal/sharing/sharing_test.go
git commit -m "feat(sharing): ComputeTwinOverlap (frame-ranges de overlap entre gêmeos)"
```

---

## Task 5: Repo `catalog.twin_discriminative` + populador

**Files:**
- Create: `workers/internal/catalog/twin_discriminative.go`
- Test: `workers/internal/catalog/twin_discriminative_test.go`

Objetivo: (a) upsert/get das regiões discriminantes; (b) `PopulateForMaterial(materialID)` que acha gêmeos (similaridade ≥ 0.50) de **duração ~igual**, chama `ComputeTwinOverlap`, calcula o complemento (discriminante) e grava os dois lados.

- [ ] **Step 1: Teste que falha (upsert/get puro, sem áudio)** — grava disc_ranges pra (A,B), lê de volta.

```go
func TestTwinDiscriminative_UpsertGet(t *testing.T) {
	ctx, pool := newTestDB(t)
	// seed 2 materiais (helper existente NewMaterials/Create)
	repo := NewTwinDiscriminative(pool)
	err := repo.Upsert(ctx, aID, bID, []Int4Range{{180, 234}}, 54)
	if err != nil { t.Fatal(err) }
	disc, frames, err := repo.Get(ctx, aID, bID)
	if err != nil { t.Fatal(err) }
	if frames != 54 || len(disc) != 1 || disc[0] != (Int4Range{180,234}) {
		t.Fatalf("got %v frames=%d", disc, frames)
	}
	// par inexistente => (nil, 0, nil)
	if _, f, _ := repo.Get(ctx, aID, uuid.New()); f != 0 {
		t.Fatalf("frames p/ par inexistente = %d, want 0", f)
	}
}
```

- [ ] **Step 2: Falha → implemente `Upsert`/`Get`** com `INSERT ... ON CONFLICT (material_id, twin_id) DO UPDATE`. Use o tipo `int4range[]` via pgx (escaneia pra um `[]Int4Range{Lo,Hi int32}` — defina o tipo e o `EncodeText`/scan, ou serialize como `INT[][]`/JSONB se o pgx do projeto não tiver suporte nativo a `int4range[]`; confirme lendo como outros arrays UUID[] são tratados no projeto, ex.: distribution_rules.station_ids).

> **Decisão de tipo:** cheque como o pgx do projeto lida com `int4range[]`. Se for atrito, guarde `disc_ranges` como `INT[]` achatado (pares lo,hi) — mais simples e sem custom type. Ajuste a migration da Task 1 pra `disc_ranges INT[]` se optar por isso. Documente a escolha no commit.

- [ ] **Step 3: Teste do populador (DB + áudio)** — seed 2 gêmeos 30s + 1 material 15s do mesmo cliente; `PopulateForMaterial(aID)` grava (A,B) e (B,A) com disc não-vazio, e **NÃO** cria par com o 15s (duração diferente → fora do gate). Piso: se A⊂B (100%), disc_frames=0.

- [ ] **Step 4: Implemente `PopulateForMaterial`** — usa `similarity` pra achar candidatos ≥0.50; filtra `|dur - dur_twin| ≤ durTolerance` (1s); pra cada, `ComputeTwinOverlap` → complemento → `Upsert` dos dois lados. Best-effort/log em falha parcial.

- [ ] **Step 5: PASS + commit**
```bash
git add workers/internal/catalog/twin_discriminative.go workers/internal/catalog/twin_discriminative_test.go
git commit -m "feat(catalog): repo + populador de regiões discriminantes de gêmeos"
```

---

## Task 6: Regra de decisão pura (`chooseTwinByDiscriminative`)

**Files:**
- Create: `workers/internal/evidence/twin_disambig.go`
- Create: `workers/internal/evidence/twin_disambig_test.go`

Esta é a lógica central — pura, sem DB, totalmente testável. Espelha `chooseByCoverage`.

- [ ] **Step 1: Escreva os testes que falham** (tabela de casos da spec §5.4 + §6)

```go
package evidence

import "testing"

func TestChooseTwinByDiscriminative(t *testing.T) {
	const floor, margin = 0.15, 1.5
	cases := []struct {
		name             string
		discSelf, discTwin float64
		want             twinVerdict
	}{
		{"gemeo claramente maior -> reatribui", 0.05, 0.80, verdictReattribute},
		{"self claramente maior -> mantem", 0.80, 0.05, verdictKeep},
		{"ambos abaixo do piso -> ambiguo", 0.05, 0.08, verdictAmbiguous},
		{"quase-empate acima do piso -> ambiguo", 0.60, 0.55, verdictAmbiguous},
		{"disc vazio (frames=0) vira 0/0 -> ambiguo", 0, 0, verdictAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseTwinByDiscriminative(c.discSelf, c.discTwin, floor, margin)
			if got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Rode e veja falhar** — `go test ./internal/evidence/ -run TestChooseTwinByDiscriminative -v` → FAIL (`undefined`).

- [ ] **Step 3: Implemente** (`twin_disambig.go`)

```go
package evidence

type twinVerdict int

const (
	verdictAmbiguous twinVerdict = iota
	verdictKeep                  // atribuição atual (self) está certa
	verdictReattribute           // o gêmeo (twin) é quem tocou
)

// chooseTwinByDiscriminative decide entre o material atribuído (self) e um gêmeo
// (twin) pela cobertura do clipe NO TRECHO DISCRIMINANTE de cada um. Só é chamada
// quando a cobertura-cheia empatou E as durações são ~iguais (a trava da spec).
//   - se a maior cobertura discriminante < floor -> ambíguo (assinatura não
//     sobreviveu, ou par não-separável com disc vazio -> 0/0 -> 0 < floor).
//   - se uma supera a outra por `margin` -> vence.
//   - quase-empate acima do piso -> ambíguo (não chuta).
func chooseTwinByDiscriminative(discSelf, discTwin, floor, margin float64) twinVerdict {
	hi, hiIsTwin := discSelf, false
	if discTwin > discSelf {
		hi, hiIsTwin = discTwin, true
	}
	if hi < floor {
		return verdictAmbiguous
	}
	lo := discSelf
	if hiIsTwin {
		lo = discSelf
	} else {
		lo = discTwin
	}
	if lo > 0 && hi < lo*margin {
		return verdictAmbiguous // quase-empate
	}
	if hiIsTwin {
		return verdictReattribute
	}
	return verdictKeep
}
```

- [ ] **Step 4: Rode e veja passar** — `go test ./internal/evidence/ -run TestChooseTwinByDiscriminative -v` → PASS.

- [ ] **Step 5: Commit**
```bash
git add workers/internal/evidence/twin_disambig.go workers/internal/evidence/twin_disambig_test.go
git commit -m "feat(evidence): regra pura de desambiguação por trecho discriminante"
```

---

## Task 7: Orquestração — `disambiguateTwin` (junta audit + repo + regra)

**Files:**
- Modify: `workers/internal/evidence/twin_disambig.go`
- Test: `workers/internal/evidence/twin_disambig_test.go` (parte de integração, DB)

Objetivo: função `(*Service).disambiguateTwin(ctx, detectionID, detectedAt, stationID, attributedID, attributedCoverage, pcm)` que: acha gêmeos de mesma duração; audita cobertura-cheia de cada; se um vence por `coverageMargin` → deixa o `reattributeByCoverage` existente cuidar (retorna "não é caso de gêmeo"); se empata → carrega `disc_ranges`, audita `CoverageOnFrames` de cada, chama `chooseTwinByDiscriminative`; aplica: `verdictReattribute` → `ReattributeDetection` (já sincroniza projeção); `verdictAmbiguous` → `MarkAmbiguous` (Task 8); `verdictKeep` → nada.

- [ ] **Step 1** Leia `reattributeByCoverage` (service.go:566-631) e `FindCutWithSiblings` (catalog/detections.go:409-443) pra reusar a busca de gêmeos e o `AuditEvidence` por candidato.

- [ ] **Step 2** Teste de integração (DB + áudio sintético): duas seeds — (a) gêmeos onde o clipe contém a assinatura de Y → verifica reatribuição pra Y + projeção sincronizada; (b) clipe sem assinatura de nenhum → verifica `evidence_status='ambiguous'`. Reuse os helpers de seed das Tasks 4/5.

- [ ] **Step 3** Implemente `disambiguateTwin` orquestrando as peças. Best-effort (log, nunca aborta upload). Métrica `MatchDisambiguation` com labels `reattributed_by_discriminative`/`ambiguous_by_discriminative`/`kept_by_discriminative`.

- [ ] **Step 4** PASS + commit.
```bash
git add workers/internal/evidence/twin_disambig.go workers/internal/evidence/twin_disambig_test.go
git commit -m "feat(evidence): orquestra desambiguação de gêmeos (audit+repo+regra)"
```

---

## Task 8: `catalog.MarkAmbiguous` + excluir do conjunto aprovado

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Test: `workers/internal/catalog/detections_test.go`

- [ ] **Step 1** Leia o `ApprovedDetectionsFilter` (procure em `catalog/`) — o predicado que define "aprovado". Vai adicionar `evidence_status <> 'ambiguous'` a ele (ou garantir que 'ambiguous' não é 'available').

- [ ] **Step 2** Teste que falha: cria detecção, `MarkAmbiguous(id, detectedAt)`, verifica `evidence_status='ambiguous'` E que ela **não** aparece numa contagem que usa `ApprovedDetectionsFilter`.

```go
func TestDetections_MarkAmbiguous_ExcludedFromApproved(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "Ambiguous")
	dets := NewDetections(pool)
	det, _ := dets.Create(ctx, CreateDetectionInput{StationID: statID, CommercialID: matID, CampaignID: campID, DetectedAt: time.Now(), Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8})
	if err := dets.MarkAmbiguous(ctx, det.ID, det.DetectedAt); err != nil { t.Fatal(err) }
	var ev string
	pool.QueryRow(ctx, `SELECT evidence_status FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&ev)
	if ev != "ambiguous" { t.Fatalf("evidence_status=%s want ambiguous", ev) }
	// + assert que uma query com ApprovedDetectionsFilter NÃO conta essa detecção
}
```

- [ ] **Step 3** Implemente `MarkAmbiguous(ctx, id, detectedAt)` (`UPDATE detections SET evidence_status='ambiguous' WHERE id=$1 AND detected_at=$2`) e ajuste `ApprovedDetectionsFilter` pra excluir `'ambiguous'`.

- [ ] **Step 4** PASS (+ garanta que os testes de consistência de contagem existentes seguem verdes) + commit.
```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(catalog): MarkAmbiguous + exclui 'ambiguous' do conjunto aprovado"
```

---

## Task 9: Wire no `reattributeByCoverage` + flag `DISAMBIG_TWIN_DISCRIMINATIVE`

**Files:**
- Modify: `workers/internal/evidence/service.go`

- [ ] **Step 1** Adicione o campo `disambigTwin bool` na `Service` + no construtor (espelhe `disambigByCoverage`), lido de `DISAMBIG_TWIN_DISCRIMINATIVE` (default OFF) onde as outras flags são montadas (procure onde `disambigByCoverage` é setado no wiring do serviço, provavelmente em `cmd/api` ou no `NewService`).

- [ ] **Step 2** No `reattributeByCoverage` (ou logo após ele no pass-path, service.go:491-493): se `s.disambigTwin` E o material tem gêmeos de mesma duração, chame `s.disambiguateTwin(...)`. Ordem: o passo de cobertura-cheia existente roda primeiro (pega subset/loop); o discriminante só entra no empate (a função `disambiguateTwin` já faz esse gate internamente — Task 7).

- [ ] **Step 3** Cross-compile linux (regra 6.1) — o gold standard do deploy:
Run:
```bash
cd workers && WPATH=$(pwd -W) && MSYS_NO_PATHCONV=1 docker run --rm -v "${WPATH}:/app" -w /app -v rc_gomod:/go/pkg/mod -v rc_gocache:/root/.cache/go-build -e CGO_ENABLED=0 -e GOOS=linux -e GOFLAGS=-mod=mod golang:1.26-alpine sh -c 'go build ./... && echo OK'
```
Expected: `OK`.

- [ ] **Step 4** Rode a suíte do evidence + catalog em DB limpo (padrão do projeto). Expected: novos testes verdes; os existentes de disambiguation/reattribution verdes.

- [ ] **Step 5** Commit.
```bash
git add workers/internal/evidence/service.go
git commit -m "feat(evidence): liga desambiguação de gêmeos (flag DISAMBIG_TWIN_DISCRIMINATIVE, default off)"
```

---

## Task 10: Popular na ingestão (hook após fingerprint ready)

**Files:**
- Modify: onde `sharing.MarkSharedHashes` é chamado após `fingerprint.Persist` (procure: `grep -rn "MarkSharedHashes" workers/ --include=*.go | grep -v _test`)

- [ ] **Step 1** Ache o ponto onde, após persistir o fingerprint de um material, o `MarkSharedHashes` roda (CLI de fingerprint + fluxo de upload).

- [ ] **Step 2** Adicione, logo após, a chamada `twinDisc.PopulateForMaterial(materialID)` (best-effort/log). Isso mantém as regiões discriminantes atualizadas quando material novo entra.

- [ ] **Step 3** Cross-compile linux (comando da Task 9 Step 3) → `OK`. Commit.
```bash
git add <arquivo(s) do hook>
git commit -m "feat: popula regiões discriminantes de gêmeos na ingestão do fingerprint"
```

---

## Self-Review (feito)

- **Cobertura da spec:** §5.1 (Task 5 gêmeos por similaridade+duração) · §5.2 (Task 4 overlap + Task 5 complemento) · §5.3 (Task 3 CoverageOnFrames) · §5.4 (Tasks 6-7 regra+orquestração) · §5.5 (Task 8 ambiguous) · §7 backfill → **plano separado** · §9 testes (por tarefa) · §10 flag (Task 9). Frontend da fila de revisão → **plano separado**.
- **Placeholders:** os "00NN" de migration e "procure o nome do constraint/hook" são passos de descoberta explícitos (o executor roda o `grep`/`ls` indicado), não buracos de código. Toda lógica pura tem código completo.
- **Consistência de tipos:** `FrameRange{Lo,Hi int32}` (audit) usado em `ComputeTwinOverlap`/`CoverageOnFrames`; `twinVerdict`/`chooseTwinByDiscriminative` consistentes entre Tasks 6-7; `Int4Range` do repo (Task 5) — **decisão pendente de tipo** (int4range[] nativo vs INT[] achatado) marcada explicitamente na Task 5 Step 2 pra o executor resolver lendo o projeto.

## Riscos de execução
- Task 3 Step 5 (expor frames cobertas do `runMatch`) é a mudança mais delicada — mexe no núcleo do audit. Rode TODA a suíte de `internal/audit` depois (coverage/extent/bypass não podem regredir).
- Task 5 tipo `int4range[]`: se der atrito com pgx, cair pra `INT[]` achatado é aceitável e mais simples (ajustar a migration da Task 1 junto).
- Calibração de `floor`/`margin`/`durTolerance`: começar conservador (favorece manter/ambíguo). Ligar a flag só após validar métricas em sombra com dados reais.
