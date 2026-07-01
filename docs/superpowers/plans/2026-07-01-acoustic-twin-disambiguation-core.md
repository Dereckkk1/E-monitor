# Desambiguação de Gêmeos Acústicos — Plano 1 (backend core)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Atribuir gêmeos acústicos de mesma duração ao criativo que de fato tocou, medindo a cobertura do clipe no trecho que os distingue; marcar `ambiguous` quando o áudio não permite decidir.

**Architecture:** Arquitetura B (pós-audit). Estende o gancho `reattributeByCoverage` que já roda após o §9.9. Quando a cobertura-cheia empata E as durações são ~iguais (único caso que cobertura/duração não resolvem), computa cobertura discriminante (frames não-sobrepostos, reusando o overlap do `sharing`) e decide; senão marca `ambiguous`. Não toca no matcher em tempo real.

**Tech Stack:** Go (workers/), pgx/pgxpool, PostgreSQL (particionado), testes com DB limpo migrado.

**Spec:** [`docs/superpowers/specs/2026-07-01-acoustic-twin-disambiguation-design.md`](../specs/2026-07-01-acoustic-twin-disambiguation-design.md)

**Escopo deste plano:** só o backend core. A **fila de revisão (frontend)** e os **comandos de backfill** são planos separados que dependem deste.

---

## Correções pós-auditoria de regressão (2026-07-01)

Antes de escrever a Task 7 rodou uma **auditoria de regressão** cruzando esta feature contra TODOS os postmortems (`docs/incidents/*`) e a arquitetura de atribuição. Achados e desvios já aplicados durante a execução subagent-driven:

**Status de execução (branch `feat/acoustic-twin-disambiguation`):**

| Task | Estado | Commit |
|---|---|---|
| 1+2 migrations (0046 tabela, 0047 `ambiguous` no CHECK) | ✅ feito | `6c1c70c`, `17ff3d5` |
| 3 `audit.CoverageOnFrames` + `Result.CoveredFrames` | ✅ feito | `a0625e4` |
| 4 `sharing.ComputeTwinOverlap` (core puro + shell) | ✅ feito | `73b059c` |
| 5a migration `INT[]` + repo + `complement` puro | ✅ feito | `f460236`, `0f3fe9b` |
| 5b `similarity.FindSimilarMaterials` + `PopulateForMaterial` | ✅ feito | `44ea7cd` |
| 6 regra pura `chooseTwinByDiscriminative` | ✅ feito | `90e0771` |
| 8 `MarkAmbiguous` + filtro | ⏳ em execução | — |
| 7 orquestração `disambiguateTwin` | ⏳ pendente (revisada abaixo) | — |
| 9 wire + flag | ⏳ pendente (revisada abaixo) | — |
| 10 hook na ingestão | ⏳ pendente | — |

**Desvios do plano original (justificados, já aplicados):**

1. **`disc_ranges` é `INT[]` achatado** (`[lo0,hi0,...]`), não `int4range[]` — evita custom pgx codec (Task 1/5a).
2. **Tipo único de range: `audit.FrameRange`** atravessa todo o pipeline (sharing→catalog→evidence); o `Int4Range` do plano foi descartado.
3. **CHECK de `evidence_status` (0047) preserva TODOS os valores atuais** (`pending,generating,available,missing,failed,audit_rejected`) + `ambiguous`. O plano original dropava `generating`/`audit_rejected` por engano (teria quebrado linhas existentes — incidente 2026-05-17).
4. **Estratégia de teste = core puro in-memory + shell de DB/áudio sem teste dedicado** (espelha `similarity`/`sharing`, cujos shells `CheckMaterialSimilarity`/`MarkSharedHashes` não têm teste). Os fixtures `seedTwinMasters`/`newTestDB`-com-áudio do plano original **não existem** no repo; foram substituídos por ruído denso determinístico.
5. **Task 5 dividida em 5a (repo+complement, testados) e 5b (FindSimilarMaterials+Populate, shells).** `similarity.FindSimilarMaterials` (read-only, todos candidatos ≥0.50) foi adicionado porque `CheckMaterialSimilarity` só persiste o TOP match — insuficiente pro caso Milium (vários gêmeos).
6. **Ordem reordenada 6 → 8 → 7 → 9 → 10** (a Task 7 consome `MarkAmbiguous` da Task 8).

**Guards de regressão OBRIGATÓRIOS (incorporados nas Tasks 7/8/9 abaixo):**

- **[HIGH] Co-fire guard (2026-06-30 / memória `disambig-v2-reattribution-duplicates-cofiring-sting`):** reatribuir pra um gêmeo co-firing SEM checar se ele já tem row na janela **duplica a tocada**. A Task 7 DEVE reusar `FindSiblingDetectionInWindow` + `decideCofireAction` antes de qualquer `ReattributeDetection`, idêntico ao `reattributeByCoverage` (service.go:609-655). Gêmeos co-programados de mesma duração são o PIOR caso. **O plano original omitiu isto.**
- **[HIGH] Projeção-fantasma (2026-06-30):** reatribuir **só** via `catalog.ReattributeDetection` (sincroniza `detection_campaigns` in-tx). Nunca `UPDATE detections` bare.
- **[HIGH] `ambiguous` (2026-05-17 + count-consistency):** `MarkAmbiguous` seta `evidence_status='ambiguous'` **E** `retracted_at` (invariante `ambiguous ⟺ retracted`). Todas as views filtram `retracted_at IS NULL` ao vivo → exclui de tudo sem migration de view. Task 8 também adiciona `<> 'ambiguous'` ao const.
- **[HIGH] Ordenação (§18.2.2):** Task 9 gateia `disambiguateTwin` em `reattributeByCoverage` ter retornado `false`; `disambiguateTwin` no-op se a row já está retraída (não desfaz decisão do v2).
- **[MEDIUM] Campanha cancelada:** reusar `resolveAttribution` (filtra `('programada','ativa')`) → sem campanha viva = deixa intacto. Verificado.
- **[MEDIUM] Supressão silenciosa (2026-06-12):** `ambiguous` é nova classe tipo `audit_rejected`. Métrica `ambiguous_by_discriminative` dá o contador; flag OFF default; `floor`/`margin` conservadores calibrados em sombra antes de ligar.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `migrations/0046_material_twin_discriminative.up/.down.sql` | Tabela das regiões discriminantes (`disc_ranges INT[]` achatado) | ✅ Criado |
| `migrations/0047_evidence_status_ambiguous.up/.down.sql` | Adiciona `'ambiguous'` ao CHECK (preservando todos os valores) | ✅ Criado |
| `workers/internal/audit/auditor.go` | `CoverageOnFrames` + `Result.CoveredFrames` | ✅ Modificado |
| `workers/internal/sharing/sharing.go` | `ComputeTwinOverlap` (core puro + shell) | ✅ Modificado |
| `workers/internal/similarity/similarity.go` | `FindSimilarMaterials` (read-only, todos ≥0.50) + `pairScore` | ✅ Modificado |
| `workers/internal/catalog/twin_discriminative.go` | Repo (`Upsert`/`Get`/**`ListForMaterial`**) + `complement`/`PopulateForMaterial` | Modificar (repo ✅; `ListForMaterial` na Task 7) |
| `workers/internal/evidence/twin_disambig.go` | Regra pura `chooseTwinByDiscriminative` (✅) + `pickTwinAction` + `disambiguateTwin` | Modificar |
| `workers/internal/evidence/service.go` | Extrair `applyReattributionWithCofireGuard` + wire + flag `disambigTwin` | Modificar (Task 7/9) |
| `workers/internal/catalog/detections.go` | `MarkAmbiguous` (retrata) | Modificar (Task 8) |
| `workers/internal/catalog/detection_filter.go` | `<> 'ambiguous'` no `ApprovedDetectionsFilter` | Modificar (Task 8) |
| `cmd/api/main.go` | Thread flag `DISAMBIG_TWIN_DISCRIMINATIVE` pro `NewService` | Modificar (Task 9) |
| `workers/internal/metrics/metrics.go` | Labels novos em `MatchDisambiguation` (strings, sem código) | — |

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

## Task 7: Orquestração — `disambiguateTwin` (junta audit + repo + regra) — **REVISADA pós-auditoria**

**Files:**
- Modify: `workers/internal/catalog/twin_discriminative.go` (novo `ListForMaterial`)
- Modify: `workers/internal/evidence/twin_disambig.go` (agregador puro + `disambiguateTwin`)
- Test: `workers/internal/evidence/twin_disambig_test.go` (agregador puro)

**Fonte dos gêmeos = a própria tabela `material_twin_discriminative`** (não `FindCutWithSiblings`): as linhas já são de mesma duração (filtro aplicado no `PopulateForMaterial`) e já carregam a região discriminante. Novo repo method:
```go
type TwinRow struct { TwinID uuid.UUID; TwinShortID int32; Disc []audit.FrameRange; DiscFrames int }
// ListForMaterial retorna os gêmeos POPULADOS de materialID (só pares com row).
func (r *TwinDiscriminative) ListForMaterial(ctx, materialID uuid.UUID) ([]TwinRow, error)
//   SELECT t.twin_id, m.short_id, t.disc_ranges, t.disc_frames
//   FROM material_twin_discriminative t JOIN materials m ON m.id = t.twin_id
//   WHERE t.material_id = $1
```
> Só materiais têm row aqui (FK → materials(id)), então `short_id` sai de `materials` direto (sem polimorfismo). `FindSiblingDetectionInWindow`/`resolveAttribution` a jusante já resolvem `commercials ∪ materials`.

**Algoritmo do `(*Service).disambiguateTwin(ctx, detectionID, detectedAt, stationID, attributedID, pcm)`** (shell best-effort, nunca aborta upload):
1. `twins := ListForMaterial(attributedID)`; se vazio → return (não é caso de gêmeo).
2. Defensivo: se a row já está retraída/reatribuída (v2 agiu antes), no-op. (Gate primário fica na Task 9, mas revalide barato.)
3. `auditSelf := s.auditor.AuditEvidence(ctx, attributedID, pcm)` → `auditSelf.CoveredFrames` (Task 3).
4. Pra cada twin: `discTwin,_ := twinRepo.Get(twin.TwinID, attributedID)`; `auditTwin := AuditEvidence(twin.TwinID, pcm)`; `covSelf := CoverageOnFrames(auditSelf.CoveredFrames, twin.Disc)`; `covTwin := CoverageOnFrames(auditTwin.CoveredFrames, discTwin)`; `verdict := chooseTwinByDiscriminative(covSelf, covTwin, twinDiscFloor, twinDiscMargin)`. Acumula `twinEval{TwinID, TwinShortID, covTwin, verdict}`.
5. `action, winner := pickTwinAction(evals)` (agregador PURO — Step abaixo).
6. Aplica:
   - `verdictReattribute` → **reatribui pro `winner` COM co-fire guard** (Step co-fire); métrica `reattributed_by_discriminative`.
   - `verdictAmbiguous` → `s.detections.MarkAmbiguous(ctx, detectionID, detectedAt)` (Task 8, retrata); métrica `ambiguous_by_discriminative`.
   - `verdictKeep` → nada; métrica opcional `kept_by_discriminative`.

- [ ] **Step 1 — agregador PURO (`twin_disambig.go`) + teste.** `pickTwinAction(evals []twinEval) (twinVerdict, *twinEval)`: se ALGUM eval é `verdictReattribute` → retorna `(verdictReattribute, &eval com maior covTwin)`; senão se algum é `verdictAmbiguous` → `(verdictAmbiguous, nil)`; senão `(verdictKeep, nil)`. Teste puro cobrindo: um reattribute vence; dois reattribute → maior covTwin; nenhum reattribute + um ambiguous → ambiguous; todos keep → keep; lista vazia → keep.

- [ ] **Step 2 — CO-FIRE GUARD (obrigatório — regressão 2026-06-30).** Antes de qualquer `ReattributeDetection` pro `winner`, replicar EXATAMENTE o guard do `reattributeByCoverage` (service.go:609-655): `existing := s.detections.FindSiblingDetectionInWindow(ctx, winner.TwinShortID, stationID, detectedAt, recoverRejWindowSeconds)`; `switch decideCofireAction(existing)`:
  - `cofireRetractSelf` → `RetractByID(self)`, métrica `duplicate_cofire_retracted`, **NÃO reatribui**.
  - `cofireRestoreThenRetractSelf` → `ClearRetraction(existing)` + `RetractByID(self)`, métrica `duplicate_cofire_retracted`.
  - `cofireReattribute` → segue pro fluxo de reatribuição normal (Step 3).
  > **DRY:** extraia a cauda de `reattributeByCoverage` (service.go:609-685 — o guard + `resolveAttribution` + `ReattributeDetection` + `SetAuditCoverage`) num helper compartilhado `(*Service) applyReattributionWithCofireGuard(ctx, detectionID, detectedAt, stationID, winnerShortID, winnerCoverage, reattributeMetricLabel) bool` e chame-o de AMBOS. Mantenha o comportamento do `reattributeByCoverage` **idêntico** — os testes existentes (`cofire_guard_test.go`, `disambig_coverage_test.go`) são a rede de segurança; rode-os e confirme verdes. Se o risco de mexer no caminho provado incomodar, duplique o switch inline, mas então garanta paridade com o original.

- [ ] **Step 3 — reatribuição normal (dentro do helper).** `newCommercialID, newCampaignID, err := resolveAttribution(ctx, s.db, winner.TwinShortID, stationID, detectedAt)`; se `ErrNoRows`/erro → **deixa intacto** (não inventa tocada — campanha cancelada/concluída cai aqui). Senão `s.detections.ReattributeDetection(...)` (sincroniza projeção in-tx — regressão 2026-06-30) + `SetAuditCoverage`.

- [ ] **Step 4 — cross-compile linux** (regra 6.1) + rodar suíte `evidence` (incl. os testes de cofire/disambig existentes verdes). Commit.
```bash
git add workers/internal/catalog/twin_discriminative.go workers/internal/evidence/twin_disambig.go workers/internal/evidence/twin_disambig_test.go workers/internal/evidence/service.go
git commit -m "feat(evidence): orquestra desambiguação de gêmeos com co-fire guard (audit+repo+regra)"
```

> **Constantes:** `twinDiscFloor = 0.15`, `twinDiscMargin = 1.5` (conservador; calibrar em sombra antes de ligar a flag). `recoverRejWindowSeconds`/`decideCofireAction` já existem no pacote `evidence`.

---

## Task 8: `catalog.MarkAmbiguous` + excluir do conjunto aprovado — **REVISADA (retração)**

**Files:**
- Modify: `workers/internal/catalog/detections.go` (`MarkAmbiguous`)
- Modify: `workers/internal/catalog/detection_filter.go` (const + doc)
- Test: `workers/internal/catalog/detections_test.go`

> **Decisão de design (regressão 2026-05-17 + count-consistency):** `MarkAmbiguous` retrata a linha (`retracted_at`) ALÉM de setar `evidence_status='ambiguous'`. Motivo: o filtro aprovado é duplicado em views SQL (`daily_play_summary` 0029, grade 0041) que já filtram `d.retracted_at IS NULL` **ao vivo** (JOIN na detection base) → retratar exclui de TUDO sem migration de view (redefinir a view complexa da 0041 seria arriscado). Invariante: **`ambiguous ⟺ retracted`**. Também adiciona `<> 'ambiguous'` ao const (defesa/documentação; consistente pois ambiguous⟹retracted).

- [ ] **Step 1** `ApprovedDetectionsFilter` está em `catalog/detection_filter.go:31`. Adicione `AND d.evidence_status <> 'ambiguous'` ao const e explique a invariante no doc-comment.

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

- [ ] **Step 3** Implemente `MarkAmbiguous(ctx, id, detectedAt)`: `UPDATE detections SET evidence_status='ambiguous', retracted_at = COALESCE(retracted_at, now()) WHERE id=$1 AND detected_at=$2` (idempotente via COALESCE; `detected_at` no WHERE pra partition pruning). Ajuste o const. O teste (Step 2) deve provar: antes = aprovada; depois = `evidence_status='ambiguous'` E `retracted_at IS NOT NULL` E fora do conjunto aprovado E idempotente.

- [ ] **Step 4** PASS (+ garanta que os testes de consistência de contagem existentes seguem verdes) + commit.
```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detection_filter.go workers/internal/catalog/detections_test.go
git commit -m "feat(catalog): MarkAmbiguous retrata + exclui 'ambiguous' do conjunto aprovado"
```

---

## Task 9: Wire no `reattributeByCoverage` + flag `DISAMBIG_TWIN_DISCRIMINATIVE`

**Files:**
- Modify: `workers/internal/evidence/service.go`

- [ ] **Step 1** Adicione o campo `disambigTwin bool` na `Service` + param no `NewService` (espelhe `disambigByCoverage`), thread desde `cmd/api/main.go` lido de `DISAMBIG_TWIN_DISCRIMINATIVE` (default OFF — mesmo padrão de `DISAMBIG_BY_COVERAGE`). Atualize TODOS os callers de `NewService` (incl. testes) pro novo param.

- [ ] **Step 2 — ORDERING GATE (regressão §18.2.2).** No pass-path (service.go:491-493), **capture o retorno** do `reattributeByCoverage` e só rode o discriminante se ele NÃO agiu:
```go
reattributed := false
if s.disambigByCoverage {
    reattributed = s.reattributeByCoverage(auditCtx, detectionID, detectedAt, stationID, commercialID, result.Coverage, pcm)
}
if s.disambigTwin && !reattributed {
    s.disambiguateTwin(auditCtx, detectionID, detectedAt, stationID, commercialID, pcm)
}
```
> Assim o discriminante NUNCA desfaz nem duplica a decisão do v2: se o v2 reatribuiu/retratou a row, o twin-step é pulado. O `disambiguateTwin` (Task 7) ainda revalida barato que a row não está retraída (defesa em profundidade). Nota: hoje `reattributeByCoverage` já é chamado ignorando o retorno; passar a usá-lo é a mudança.

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
