> ⚠️ **REGISTRO HISTÓRICO — não descreve o comportamento atual.**
> Este documento é um snapshot datado da sessão de design/implementação que o gerou.
> Em **2026-08-17** a categorização de veiculação foi substituída pelo
> [**fechamento por cota da célula-dia**](../../features/quota-aware-categorization.md):
> `orphan` foi renomeada pra `bonus`; `out_slot` deixou de faturar e de abater o déficit;
> `deficit = expected − in_slot`; `bonus = COUNT(category = 'bonus')` (acabou o termo
> sintético `GREATEST(0, in_slot − expected)`); e **`Impactos = pmm × (in_slot + bonus)`**
> em toda tela e exportável. A decisão daqui continua valendo, mas o veredito é `bonus` explícito (não `orphan`) e sai do passo de cota; `recatClassifyTailSQL` não existe mais.
> **Não copie fórmula daqui pra código novo** — a autoridade é
> [`docs/features/quota-aware-categorization.md`](../../features/quota-aware-categorization.md).

# Carve-out: dia extra dentro do período vira bônus — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** No ramo carve-out do categorizador, uma tocada dentro do range de datas de alguma regra específica do material (por emissora) mas em dia/faixa sem meta passa a ser `orphan` (que credita bônus) em vez de `out_date`.

**Architecture:** Muda a lógica de categorização nos **dois** lugares que a implementam e precisam ficar em sincronia — `categorizer.Categorize` (Go, insert ao vivo) e `recatClassifyTailSQL` (SQL, recategorização/backfill). Reusa a categoria `orphan` (a view `daily_play_summary` já a soma no bônus), então **sem migration, sem mudança na view, sem frontend**. Um CLI de backfill recategoriza o histórico.

**Tech Stack:** Go 1.26, pgx v5, PostgreSQL, testify. Spec: [docs/superpowers/specs/2026-07-13-carveout-in-period-bonus-design.md](../specs/2026-07-13-carveout-in-period-bonus-design.md).

---

## File Structure

- **Modify** `workers/internal/categorizer/categorizer.go` — ramo `if carved` de `Categorize`: computa `inRulePeriod` e retorna `CatOrphan` antes de `CatOutDate`.
- **Modify** `workers/internal/categorizer/categorizer_test.go` — 1 teste novo (sábado dentro do range → orphan).
- **Modify** `workers/internal/catalog/distribution_rules.go` — `recatClassifyTailSQL`: o `ELSE 'out_date'` do ramo carve-out vira `CASE` que checa o range.
- **Create** `workers/internal/catalog/distribution_rules_carveout_test.go` — teste DB-gated de paridade Go(insert) × SQL(recat).
- **Create** `workers/cmd/backfill-recategorize/main.go` — CLI de backfill global.
- **Modify** `infra/docker/Dockerfiles/workers.Dockerfile` — 2 linhas (RUN build + COPY) pro CLI novo (regra 6.7).
- **Modify** `docs/features/material-specific-distribution-rules.md` — tabela de categorias.

---

## Task 1: Categorizador Go — dia extra dentro do período vira orphan

**Files:**
- Modify: `workers/internal/categorizer/categorizer.go:128-146`
- Test: `workers/internal/categorizer/categorizer_test.go` (append)

- [ ] **Step 1: Escrever o teste que falha**

Append ao final de `workers/internal/categorizer/categorizer_test.go` (usa os helpers `mkMatRule`, `saoPaulo`, `Campaign` já existentes no arquivo):

```go
// Caso novo (spec 2026-07-13): material carved toca DENTRO do range da regra
// dele, mas num dia-da-semana sem meta (sábado, regra seg-sex). Está dentro do
// período contratado → dia extra → orphan (credita bônus), NÃO out_date.
func TestCategorize_CarveOut_InPeriodWrongWeekday_Orphan(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m: mês todo (1-30), seg-sex (mask 62), 08-22h.
	rules := []Rule{mkMatRule(1, 30, 62, "08:00", "22:00", 1, m)}
	// 06/06/2026 é SÁBADO (DOW=6). Dentro do range 1-30, mas fora do mask 62.
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, m, rules, nil); got != "orphan" {
		t.Errorf("got %q, want orphan (sábado dentro do período do material)", got)
	}
}

// Guarda-corpo: fora do range da regra do material continua out_date (não vira
// orphan). Distingue "dia extra dentro do período" de "fora do período".
func TestCategorize_CarveOut_OutsidePeriod_StaysOutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra só na 1ª semana (1-7). Detecção no sábado 20/06 (semana 3) → fora
	// do range da regra → out_date.
	rules := []Rule{mkMatRule(1, 7, 62, "08:00", "22:00", 1, m)}
	det := time.Date(2026, 6, 20, 12, 0, 0, 0, saoPaulo) // sáb, fora do range 1-7
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date (fora do período do material)", got)
	}
}
```

- [ ] **Step 2: Rodar o teste e ver falhar**

Run: `cd workers && go test ./internal/categorizer/ -run TestCategorize_CarveOut_InPeriodWrongWeekday_Orphan -v`
Expected: **FAIL** — `got "out_date", want orphan` (hoje o ramo carved retorna `CatOutDate`).

- [ ] **Step 3: Implementar a mudança no categorizer**

Em `workers/internal/categorizer/categorizer.go`, substituir o bloco `if carved { ... }` (linhas 128-146) por:

```go
	if carved {
		hasDateWeekday := false
		inRulePeriod := false
		for _, r := range rules {
			if len(r.MaterialIDs) == 0 || !containsUUID(r.MaterialIDs, materialID) {
				continue
			}
			// Dentro do range de datas da regra dele (ignorando dia/faixa)?
			if !date.Before(dateOnlySP(r.StartDate)) && !date.After(dateOnlySP(r.EndDate)) {
				inRulePeriod = true
			}
			if !matchesDateWeekday(r) {
				continue
			}
			hasDateWeekday = true
			if matchesTime(r) {
				return CatInSlot
			}
		}
		if hasDateWeekday {
			return CatOutSlot
		}
		// Dentro do período do material mas em dia/faixa sem meta → dia extra
		// dentro do período contratado → orphan (a view credita bônus). out_date
		// fica reservado a tocadas FORA do período das regras do material.
		if inRulePeriod {
			return CatOrphan
		}
		return CatOutDate
	}
```

- [ ] **Step 4: Rodar os testes do pacote e ver passar**

Run: `cd workers && go test ./internal/categorizer/ -v`
Expected: **PASS** em todos — o teste novo passa, e os existentes (`TestCategorize_CarveOut_OutDate_OutsideRulePeriod`, `TestCategorize_CarveOut_IgnoresGeneralTypeRule`, etc.) continuam verdes porque neles a data está fora do range da regra do material.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/categorizer/categorizer.go workers/internal/categorizer/categorizer_test.go
git commit -m "feat(categorizer): dia extra dentro do período do carve-out vira orphan (bônus)"
```

---

## Task 2: Recategorização SQL — mesma lógica no recat/backfill

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go:296-308` (ramo carve-out do `recatClassifyTailSQL`)
- Create: `workers/internal/catalog/distribution_rules_carveout_test.go`

- [ ] **Step 1: Escrever o teste DB-gated de paridade (falha)**

Create `workers/internal/catalog/distribution_rules_carveout_test.go`. Usa os helpers já existentes no pacote de teste (`newTestDB`, `seedType`, `NewClients/NewCampaigns/NewMaterials/NewStations/NewDistributionRules/NewDetections`). Pega o **sábado mais recente ≤ hoje** pra garantir que a partição de `detections` do mês existe:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Paridade Go(insert) × SQL(recat) do caso novo (spec 2026-07-13): material
// carved toca DENTRO do range da regra dele mas em dia sem meta (sábado, regra
// seg-sex) → orphan nas DUAS bordas. O insert-path (categorizer.Categorize) e o
// recat-path (recatClassifyTailSQL) têm que concordar — divergir é bug silencioso.
func TestCarveOut_InPeriodWrongWeekday_Orphan_InsertAndRecat(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// Sábado mais recente <= agora (garante partição do mês existente).
	sat := time.Now().In(saoPaulo)
	for sat.Weekday() != time.Saturday {
		sat = sat.AddDate(0, 0, -1)
	}
	sat = time.Date(sat.Year(), sat.Month(), sat.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := sat.AddDate(0, 0, -6) // domingo anterior — cobre o sábado
	rangeEnd := sat.AddDate(0, 0, 6)    // sexta seguinte

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "carve-cli"})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "carve-camp", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "carve-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "carve-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/cv", MasterSHA256: "carve-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Carve FM", Band: "FM", StreamURL: "http://example.com/carve",
	})
	require.NoError(t, err)

	// Regra carve-out (material_ids = [mat]) seg-sex (mask 62), faixa ampla.
	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{mat.ID},
		StartDate:   rangeStart, EndDate: rangeEnd,
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "22:00", PlaysPerDay: 5,
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: sat, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	catOf := func() (string, string) {
		var base, proj string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&base))
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`, det.ID, cmp.ID).Scan(&proj))
		return base, proj
	}

	// Insert-path (Go): sábado dentro do range da regra → orphan.
	base, proj := catOf()
	require.Equal(t, "orphan", base, "insert-path (Go) deve dar orphan")
	require.Equal(t, "orphan", proj, "projeção nasce orphan")

	// Recat-path (SQL): recategoriza a campanha → tem que CONTINUAR orphan.
	require.NoError(t, rules.RecategorizeForCampaign(ctx, cmp.ID))
	base, proj = catOf()
	require.Equal(t, "orphan", base, "recat-path (SQL) deve concordar com o insert (orphan)")
	require.Equal(t, "orphan", proj, "projeção recategorizada deve ser orphan")
}
```

- [ ] **Step 2: Rodar o teste e ver falhar no recat**

Run (DB descartável — ver memória `test-db-native-pg-shadows-docker`; PG nativo do Windows ocupa 5432, use o `rc-test-pg` em 15432):
```bash
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck?sslmode=disable" \
  go test ./internal/catalog/ -run TestCarveOut_InPeriodWrongWeekday_Orphan_InsertAndRecat -v
```
Expected: **FAIL** no assert pós-`RecategorizeForCampaign` — o insert (Go, Task 1) já dá `orphan`, mas o SQL do recat ainda retorna `out_date`, revertendo a categoria. (Se `TEST_DATABASE_URL` não estiver setado, o teste faz `t.Skip` — garanta o DB.)

- [ ] **Step 3: Implementar a mudança no SQL**

Em `workers/internal/catalog/distribution_rules.go`, no `recatClassifyTailSQL`, localizar o fim do ramo carve-out (o `WHEN EXISTS (... data+dia ...) THEN 'out_slot'` seguido de `ELSE 'out_date'`, ~linhas 296-307). Substituir o `ELSE 'out_date'` por um `WHEN EXISTS (range) THEN 'orphan'` + `ELSE 'out_date'`:

```sql
                    WHEN EXISTS (
                        SELECT 1 FROM distribution_rules r
                        WHERE r.campaign_id = s.campaign_id
                          AND r.type_id = s.type_id
                          AND s.station_id = ANY(r.station_ids)
                          AND s.material_id = ANY(r.material_ids)
                          AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                              BETWEEN r.start_date AND r.end_date
                          AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                    ) THEN 'out_slot'
                    -- [NOVO] Dentro do range de alguma regra específica do material
                    -- (ignorando dia/faixa) → dia extra dentro do período → orphan
                    -- (credita bônus). Espelha categorizer.Categorize inRulePeriod.
                    WHEN EXISTS (
                        SELECT 1 FROM distribution_rules r
                        WHERE r.campaign_id = s.campaign_id
                          AND r.type_id = s.type_id
                          AND s.station_id = ANY(r.station_ids)
                          AND s.material_id = ANY(r.material_ids)
                          AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                              BETWEEN r.start_date AND r.end_date
                    ) THEN 'orphan'
                    ELSE 'out_date'
                END
```

> **Atenção:** o primeiro `WHEN EXISTS` acima (o do `out_slot`, com o filtro de `weekday_mask`) já existe no código — mantenha-o. A inserção é apenas o segundo `WHEN EXISTS (... BETWEEN ...) THEN 'orphan'` logo antes do `ELSE 'out_date'`.

- [ ] **Step 4: Rodar o teste e ver passar**

Run:
```bash
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck?sslmode=disable" \
  go test ./internal/catalog/ -run TestCarveOut_InPeriodWrongWeekday_Orphan_InsertAndRecat -v
```
Expected: **PASS** — insert e recat concordam em `orphan`.

- [ ] **Step 5: Rodar o teste de recat existente (não-regressão)**

Run:
```bash
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck?sslmode=disable" \
  go test ./internal/catalog/ -run "TestRecategorizeForOverride|TestRecategorizeForCampaign_RespectsOverride" -v
```
Expected: **PASS** — a mudança não afeta override nem regra geral.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_carveout_test.go
git commit -m "feat(recat): SQL espelha orphan p/ dia extra dentro do período (paridade Go×SQL)"
```

---

## Task 3: CLI de backfill global + Dockerfile

**Files:**
- Create: `workers/cmd/backfill-recategorize/main.go`
- Modify: `infra/docker/Dockerfiles/workers.Dockerfile:16` e `:34`

- [ ] **Step 1: Escrever o CLI**

Create `workers/cmd/backfill-recategorize/main.go`:

```go
// backfill-recategorize re-classifica detections/detection_campaigns de campanhas
// com carve-out (distribution_rules.material_ids não-vazio) usando a lógica atual
// do categorizador — necessário após a mudança do spec 2026-07-13 (dia extra
// dentro do período do material vira orphan/bônus em vez de out_date). Idempotente:
// RecategorizeForCampaign só altera linhas cuja categoria muda.
//
//	# DEFAULT DRY-RUN (só reporta a distribuição atual, não altera nada):
//	backfill-recategorize --dsn "$DATABASE_URL"
//
//	# Aplicar (muta linhas) — SÓ após rodar --apply contra um CLONE do dump de
//	# prod e conferir o delta (§4.8):
//	backfill-recategorize --dsn "$DATABASE_URL" --apply
//
// Escopa a uma campanha com --campaign <uuid>.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	campaign := flag.String("campaign", "", "recategorizar só esta campanha (uuid); vazio = todas com carve-out")
	apply := flag.Bool("apply", false, "aplicar a recategorização (default: dry-run, só reporta)")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn (ou DATABASE_URL) é obrigatório")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Campanhas-alvo: as que têm ao menos uma regra carve-out (material_ids não-vazio).
	// São as únicas cuja categoria pode mudar com o spec 2026-07-13.
	const targetQuery = `
		SELECT DISTINCT c.id, c.name
		FROM campaigns c
		JOIN distribution_rules r ON r.campaign_id = c.id
		WHERE cardinality(r.material_ids) > 0
		  AND ($1::uuid IS NULL OR c.id = $1)
		ORDER BY c.name`
	var campaignFilter *uuid.UUID
	if *campaign != "" {
		id, err := uuid.Parse(*campaign)
		if err != nil {
			log.Fatalf("--campaign inválido: %v", err)
		}
		campaignFilter = &id
	}

	rows, err := pool.Query(ctx, targetQuery, campaignFilter)
	if err != nil {
		log.Fatalf("target query: %v", err)
	}
	type camp struct {
		id   uuid.UUID
		name string
	}
	var camps []camp
	for rows.Next() {
		var c camp
		if err := rows.Scan(&c.id, &c.name); err != nil {
			log.Fatalf("scan: %v", err)
		}
		camps = append(camps, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}

	// Distribuição global de out_date/orphan (o "antes") nas campanhas-alvo.
	countCats := func() (outDate, orphan int64) {
		_ = pool.QueryRow(ctx, `
			SELECT
			  COUNT(*) FILTER (WHERE d.category = 'out_date'),
			  COUNT(*) FILTER (WHERE d.category = 'orphan')
			FROM detections d
			WHERE d.campaign_id IN (
			  SELECT DISTINCT r.campaign_id FROM distribution_rules r
			  WHERE cardinality(r.material_ids) > 0
			    AND ($1::uuid IS NULL OR r.campaign_id = $1))`, campaignFilter).Scan(&outDate, &orphan)
		return
	}

	beforeOut, beforeOrphan := countCats()
	fmt.Printf("\n=== backfill-recategorize (%d campanhas com carve-out) ===\n", len(camps))
	fmt.Printf("ANTES:  out_date=%d  orphan=%d\n", beforeOut, beforeOrphan)

	if !*apply {
		fmt.Printf("\nDRY-RUN: nada foi alterado. Rode com --apply (após --apply num CLONE, §4.8) para recategorizar.\n")
		return
	}

	dr := catalog.NewDistributionRules(pool)
	for _, c := range camps {
		if err := dr.RecategorizeForCampaign(ctx, c.id); err != nil {
			log.Fatalf("recategorize %s (%s): %v", c.name, c.id, err)
		}
		fmt.Printf("  recategorizada: %s\n", c.name)
	}

	afterOut, afterOrphan := countCats()
	fmt.Printf("\nDEPOIS: out_date=%d  orphan=%d\n", afterOut, afterOrphan)
	fmt.Printf("DELTA:  out_date %+d  orphan %+d\n", afterOut-beforeOut, afterOrphan-beforeOrphan)
	fmt.Printf("APLICADO em %d campanhas.\n", len(camps))
}
```

- [ ] **Step 2: Compilar o CLI (nativo)**

Run: `cd workers && go build ./cmd/backfill-recategorize`
Expected: sem erros (binário criado). `rm -f backfill-recategorize backfill-recategorize.exe` depois, se gerado.

- [ ] **Step 3: Cross-compile linux de tudo (o que o deploy faz — regra 6.1)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: **exit 0** — passa pra todos os `cmd/*` e pacotes.

- [ ] **Step 4: Adicionar as 2 linhas ao Dockerfile (regra 6.7)**

Em `infra/docker/Dockerfiles/workers.Dockerfile`, após a linha 16 (`RUN ... redisambiguate-twins`) adicionar:

```dockerfile
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-recategorize ./cmd/backfill-recategorize
```

E após a linha 34 (`COPY ... redisambiguate-twins`) adicionar:

```dockerfile
COPY --from=builder /out/backfill-recategorize  /usr/local/bin/backfill-recategorize
```

- [ ] **Step 5: Commit**

```bash
git add workers/cmd/backfill-recategorize/main.go infra/docker/Dockerfiles/workers.Dockerfile
git commit -m "feat(backfill): CLI backfill-recategorize p/ o histórico + entrada no Dockerfile"
```

---

## Task 4: Documentação

**Files:**
- Modify: `docs/features/material-specific-distribution-rules.md`

- [ ] **Step 1: Atualizar a tabela de categorias e o header**

Em `docs/features/material-specific-distribution-rules.md`, na seção "Carve-out (precedência)", substituir a linha da tabela:

```markdown
| Fora do período/dia programado dele (dentro da campanha) | `out_date` 🔴 |
```

por estas duas linhas:

```markdown
| Dentro do período dele, mas em dia/faixa sem meta (ex.: sábado, regra seg-sex) | `orphan` 🔵 → conta como **bônus** |
| Fora do período das regras dele (antes/depois da vigência, dentro da campanha) | `out_date` 🔴 |
```

E logo abaixo da tabela, adicionar o parágrafo:

```markdown
> **Dia extra dentro do período = bônus (spec 2026-07-13).** `out_date` fica
> reservado a tocadas fora do range de datas das regras do material. Uma tocada
> dentro desse range mas num dia-da-semana/faixa sem meta é uma entrega extra
> dentro do período contratado → `orphan`, que a view `daily_play_summary` soma
> no bônus. A regra vale nos dois caminhos (insert `categorizer.Categorize` e
> recat `recatClassifyTailSQL`). Backfill histórico: `cmd/backfill-recategorize`.
```

Atualizar o header YAML: trocar `ultima-verificacao` para `2026-07-13` e adicionar `workers/cmd/backfill-recategorize/main.go` em `codigo-relacionado`.

- [ ] **Step 2: Commit**

```bash
git add docs/features/material-specific-distribution-rules.md
git commit -m "docs(carveout): dia extra dentro do período conta como bônus"
```

---

## Task 5: Verificação final (regra 6)

- [ ] **Step 1: Cross-compile linux (gold standard do build de imagem)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: **exit 0**.

- [ ] **Step 2: go vet**

Run: `cd workers && go vet ./internal/categorizer/ ./internal/catalog/ ./cmd/backfill-recategorize/`
Expected: sem output (limpo).

- [ ] **Step 3: Rodar a suíte relevante**

Run: `cd workers && go test ./internal/categorizer/...`
Expected: **PASS**.

Run (DB-gated):
```bash
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck?sslmode=disable" \
  go test ./internal/catalog/ -run "Carve|Recategorize" -v
```
Expected: os testes de carve-out e recat **PASS**. (Falhas pré-existentes do harness catalog — `material_ids NOT NULL`, partição, FK user, isolamento stations — não são regressão desta mudança; ver memória `test-db-native-pg-shadows-docker`.)

- [ ] **Step 4: Confirmar o plano de rollout do backfill (não executa aqui)**

O backfill roda em prod DEPOIS do deploy do binário novo (regra 4.2: `build` → `up -d --force-recreate --no-deps api` → `exec`). Antes de `--apply` em prod, rodar `--apply` contra um CLONE do dump de prod e conferir o `DELTA` (§4.8). Documentar o delta observado no PR.

---

## Self-Review (preenchido)

- **Spec coverage:** regra nova (Task 1+2), reuso de orphan sem migration/view/frontend (Tasks 1-2, verificado — nenhuma toca migration/view/frontend), alcance global + backfill (Task 3), paridade Go×SQL (Task 2 Step 1), docs (Task 4). ✅
- **Placeholder scan:** sem TBD/TODO; todo código presente. ✅
- **Type consistency:** `inRulePeriod`/`hasDateWeekday` locais; `CatOrphan`/`CatOutDate` constantes existentes; `RecategorizeForCampaign(ctx, uuid.UUID) error` assinatura real; import `radiocheck/internal/catalog` confirmado. ✅
