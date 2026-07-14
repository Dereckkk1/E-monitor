# Invariante de Categoria por Projeção — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Garantir que `detection_campaigns.category` nunca mais fique dessincronizada das regras vivas (classe do caso COPA 10/07): recat passa a escopar por PROJEÇÃO, falhas de recat viram métrica+log, e um reconciler contínuo cura e denuncia qualquer drift futuro.

**Architecture:** O `recatClassifyTailSQL` (fonte única Go×SQL) é dividido em CTE de classificação + aplicação; o escopo dos recats muda de `detections` (base) para `detection_campaigns` (projeções), com guarda para a tocada-base. Um scheduler novo (`projrecon`, padrão `calibration.Scheduler`) roda o mesmo SQL numa janela móvel e expõe drift em Prometheus. O CLI `backfill-recategorize` ganha `--all` para convergir o histórico uma vez.

**Tech Stack:** Go 1.26, pgx/v5, Prometheus client_golang, zap, testify; testes de integração no harness `newTestDB` (PG descartável `rc-test-pg`, porta 15432 — memória `test-db-native-pg-shadows-docker`).

**Spec:** [docs/superpowers/specs/2026-07-14-projection-category-invariant-design.md](../specs/2026-07-14-projection-category-invariant-design.md)

---

## Task 0: Branch + spec commitada

**Files:** nenhum código.

- [ ] **Step 0.1: Criar branch e commitar spec + plano**

```bash
cd /c/Users/marke/Desktop/Programas/E-Series/E-monitor
git checkout -b feat/projection-recat-invariant
git add docs/superpowers/specs/2026-07-14-projection-category-invariant-design.md \
        docs/superpowers/plans/2026-07-14-projection-category-invariant.md
git commit -m "docs(projrecon): spec + plano do invariante de categoria por projeção"
```

Nota: a working tree tem mudanças não relacionadas (frontend/docs de outra frente) — adicionar SÓ os dois arquivos acima.

---

## Task 1: Motor de recat escopado por projeção + guarda da base

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go` (const `recatClassifyTailSQL` → split; `recategorizeScope`; comentários)
- Test: `workers/internal/catalog/distribution_rules_projection_test.go` (novo)

- [ ] **Step 1.1: Escrever o teste de regressão COPA (falha hoje)**

Criar `workers/internal/catalog/distribution_rules_projection_test.go`:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Regressão do caso COPA 10/07 (spec 2026-07-14): tocada física atribuída à
// campanha A (base) carrega projeção fan-out F-119 na campanha B. A regra da B
// é criada DEPOIS da tocada → projeção nasceu orphan. O recat disparado pela
// criação da regra TEM que alcançar a projeção fan-out (escopo por
// dc.campaign_id, não d.campaign_id) — e NÃO pode escrever na tocada-base nem
// na projeção canônica da A (guarda d.campaign_id = cl.campaign_id).
func TestRecategorizeForRule_ReachesFanoutProjections(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// Sexta-feira mais recente <= agora, 12:00 SP (partição do mês existe).
	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "projrecat-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "projrecat-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "projrecat-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "projrecat-spot")
	sha := "projrecat-" + uuid.NewString()
	materials := NewMaterials(pool)
	// Mesmo áudio subido 2× (o 43≡143 do caso real): matA na base, matB na secundária.
	matA, err := materials.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "projrecat-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/pa", MasterSHA256: sha,
	})
	require.NoError(t, err)
	matB, err := materials.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "projrecat-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/pb", MasterSHA256: sha,
	})
	require.NoError(t, err)

	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Projrecat FM", Band: "FM", StreamURL: "http://example.com/projrecat",
	})
	require.NoError(t, err)

	// Tocada física na base (campA/matA). Sem regra em A → orphan (base e projeção canônica).
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	// Projeção fan-out F-119 na campanha B (material da B), nascida orphan —
	// não havia regra na B no instante do insert. Espelha service.go/InsertProjections.
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Regra carve-out criada DEPOIS na B, nomeando matB, cobrindo o slot da tocada.
	rules := NewDistributionRules(pool)
	rule, err := rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{matB.ID},
		StartDate:   rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 7,
	})
	require.NoError(t, err)
	require.NoError(t, rules.RecategorizeForRule(ctx, rule.ID))

	// A projeção fan-out na B tem que virar in_slot.
	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB, "recat da regra deve alcançar a projeção fan-out")

	// Guarda: base (campA) e projeção canônica da A ficam orphan — o recat da B
	// não pode escrever categoria da B na tocada-base da A.
	var base, projA string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
		det.ID, det.DetectedAt).Scan(&base))
	require.Equal(t, "orphan", base, "tocada-base da campanha A intocada")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campA.ID).Scan(&projA))
	require.Equal(t, "orphan", projA, "projeção canônica da A intocada")
}
```

- [ ] **Step 1.2: Rodar o teste — tem que FALHAR**

```bash
cd workers && go test ./internal/catalog/ -run TestRecategorizeForRule_ReachesFanoutProjections -v
```

Esperado: FAIL em `projB` (fica `orphan` — o escopo atual por `d.campaign_id` não contém a tocada, base=campA). Se falhar por infra de DB, subir o PG de teste (memória `test-db-native-pg-shadows-docker`).

- [ ] **Step 1.3: Implementar — split do tail + guarda + escopo por projeção**

Em `workers/internal/catalog/distribution_rules.go`:

**(a)** Substituir a declaração única `const recatClassifyTailSQL = ...` por três consts. O corpo do `classified AS (...)` fica byte-idêntico ao atual (linhas 241–353) — só muda a moldura:

```go
// recatClassifiedCTE é a CTE `classified` — replica categorizer.Categorize em
// SQL para cada linha da CTE `scope` (ver recategorizeScope). Separada de
// recatApplySQL para o reconciler (projection_reconcile.go) poder CONTAR
// divergências (SELECT) sem aplicá-las.
const recatClassifiedCTE = `,
classified AS (
    ... corpo atual, inalterado, de "SELECT s.id, s.detected_at, s.campaign_id,"
    ... até "FROM scope s JOIN campaigns c ON c.id = s.campaign_id" inclusive
)`

// recatApplySQL aplica o veredito: atualiza a projeção (detection_campaigns) e,
// SÓ quando a projeção é a canônica (d.campaign_id = cl.campaign_id), espelha na
// tocada-base (detections.category). Sem essa guarda, o recat de uma campanha
// SECUNDÁRIA (fan-out F-119) sobrescreveria a categoria da base com o veredito
// de outra campanha — bug. Ver spec 2026-07-14 §3-T1.
const recatApplySQL = `
, upd_det AS (
    UPDATE detections d
    SET category = cl.new_category
    FROM classified cl
    WHERE d.id = cl.id AND d.detected_at = cl.detected_at
      AND d.campaign_id = cl.campaign_id
      AND d.category IS DISTINCT FROM cl.new_category
    RETURNING 1
)
UPDATE detection_campaigns dc
SET category = cl.new_category
FROM classified cl
WHERE dc.detection_id = cl.id AND dc.detected_at = cl.detected_at
  AND dc.campaign_id = cl.campaign_id
  AND dc.category IS DISTINCT FROM cl.new_category`

const recatClassifyTailSQL = recatClassifiedCTE + recatApplySQL
```

**(b)** `recategorizeScope`: escopo nasce das PROJEÇÕES:

```go
// recategorizeScope é o motor SQL pra escopos rule/campaign. Escopa por
// PROJEÇÃO (detection_campaigns), não pela tocada-base: uma projeção fan-out
// F-119 pertence à campanha $1 mesmo quando a base (d.campaign_id) é outra —
// era o ponto cego do caso COPA 10/07 (spec 2026-07-14 §1.1).
func (dr *DistributionRules) recategorizeScope(ctx context.Context,
	campaignID uuid.UUID, typeID *uuid.UUID, stationIDs []uuid.UUID,
	from, to time.Time) error {

	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.campaign_id = $1
      AND ($2::uuid IS NULL OR m.type_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
)`+recatClassifyTailSQL,
		campaignID, typeID, stationIDs, from, to)
	return err
}
```

- [ ] **Step 1.4: Rodar o teste novo + o pacote inteiro**

```bash
cd workers && go test ./internal/catalog/ -run TestRecategorizeForRule_ReachesFanoutProjections -v
cd workers && go test ./internal/catalog/ 2>&1 | tail -20
```

Esperado: teste novo PASS; testes de paridade carve-out/override existentes continuam PASS (o `classified` não mudou). Falhas pré-existentes conhecidas do harness catalog (memória `test-db-native-pg-shadows-docker`) não contam como regressão — comparar com `git stash && go test` se houver dúvida.

- [ ] **Step 1.5: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_projection_test.go
git commit -m "fix(recat): escopo por projeção + guarda da base — alcança fan-out F-119 (caso COPA)"
```

---

## Task 2: `RecategorizeForMaterial` alcança projeções em outras campanhas

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go:399-410` (`RecategorizeForMaterial`)
- Test: `workers/internal/catalog/distribution_rules_projection_test.go` (append)

- [ ] **Step 2.1: Teste (falha hoje)** — append no arquivo de teste da Task 1:

```go
// Mudança de tipo do material deve reclassificar as projeções que o carregam
// em QUALQUER campanha — não só onde ele é a atribuição-base.
func TestRecategorizeForMaterial_ReachesFanoutProjections(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "matrecat-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "matrecat-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "matrecat-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeRight := seedType(t, ctx, pool, "matrecat-certo")
	typeWrong := seedType(t, ctx, pool, "matrecat-errado")
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "matrecat-B", TypeID: &typeWrong, DurationSeconds: 30,
		MasterStoragePath: "/tmp/mb", MasterSHA256: "matrecat-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "matrecat-A", TypeID: &typeWrong, DurationSeconds: 30,
		MasterStoragePath: "/tmp/ma", MasterSHA256: "matrecat-" + uuid.NewString(),
	})
	require.NoError(t, err)

	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Matrecat FM", Band: "FM", StreamURL: "http://example.com/matrecat",
	})
	require.NoError(t, err)

	// Regra GERAL na B para o tipo certo (sem carve-out).
	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeRight,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 5,
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	// Projeção fan-out na B com matB (tipo errado → orphan no insert).
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Corrige o tipo do matB e recategoriza por material (o que o handler
	// PATCH /materials/:id faz).
	_, err = pool.Exec(ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, matB.ID, typeRight)
	require.NoError(t, err)
	require.NoError(t, rules.RecategorizeForMaterial(ctx, matB.ID))

	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB, "projeção fan-out do material deve ser reclassificada")
}
```

- [ ] **Step 2.2: Rodar — FAIL** (`RecategorizeForMaterial` escopa `d.commercial_id`; a base carrega matA, não matB)

```bash
cd workers && go test ./internal/catalog/ -run TestRecategorizeForMaterial_ReachesFanoutProjections -v
```

- [ ] **Step 2.3: Implementar** — trocar o scope de `RecategorizeForMaterial`:

```go
func (dr *DistributionRules) RecategorizeForMaterial(ctx context.Context, materialID uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.commercial_id = $1
)`+recatClassifyTailSQL,
		materialID)
	return err
}
```

(Atualizar o comentário do método: escopo agora é "todas as projeções que carregam o material", e a base é espelhada só pela projeção canônica via guarda do `recatApplySQL`.)

- [ ] **Step 2.4: Rodar teste novo + pacote — PASS**

```bash
cd workers && go test ./internal/catalog/ -run 'TestRecategorizeForMaterial' -v
cd workers && go test ./internal/catalog/ 2>&1 | tail -10
```

- [ ] **Step 2.5: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_projection_test.go
git commit -m "fix(recat): RecategorizeForMaterial escopa por projeção (fan-out em outras campanhas)"
```

---

## Task 3: Métodos de drift — contar e curar (base do reconciler)

**Files:**
- Create: `workers/internal/catalog/projection_reconcile.go`
- Test: `workers/internal/catalog/projection_reconcile_test.go`

- [ ] **Step 3.1: Teste (falha: métodos não existem)**

Criar `workers/internal/catalog/projection_reconcile_test.go`:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Count acha a divergência; Heal cura; Count volta a zero. Cenário: projeção
// fan-out orphan + regra criada via repo (repo.Create NÃO dispara recat — quem
// dispara é o handler), ou seja, drift real como o do caso COPA.
func TestProjectionDrift_CountAndHeal(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "drift-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "drift-spot")
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/da", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/db", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Drift FM", Band: "FM", StreamURL: "http://example.com/drift",
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{matB.ID},
		StartDate: rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 7,
	})
	require.NoError(t, err) // repo.Create não recategoriza → drift instalado

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	since := day.Add(-2 * time.Hour)

	// Count: 1 divergência orphan→in_slot na campanha B.
	drifts, err := rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	found := false
	for _, dr := range drifts {
		if dr.CampaignID == campB.ID {
			require.Equal(t, "orphan", dr.From)
			require.Equal(t, "in_slot", dr.To)
			require.GreaterOrEqual(t, dr.N, int64(1))
			found = true
		}
	}
	require.True(t, found, "drift da campanha B tem que aparecer no Count")

	// Heal: cura >= 1 linha; Count da B volta a zero.
	healed, err := rules.HealProjectionDrift(ctx, since)
	require.NoError(t, err)
	require.GreaterOrEqual(t, healed, int64(1))

	drifts, err = rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	for _, dr := range drifts {
		require.NotEqual(t, campB.ID, dr.CampaignID, "depois do Heal a B não pode ter drift")
	}

	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB)
}
```

- [ ] **Step 3.2: Rodar — FAIL (compile error: métodos não existem)**

```bash
cd workers && go test ./internal/catalog/ -run TestProjectionDrift_CountAndHeal -v
```

- [ ] **Step 3.3: Implementar `projection_reconcile.go`**

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProjectionDrift é uma divergência agregada entre a categoria gravada numa
// projeção (detection_campaigns.category) e o veredito atual do categorizador
// — invariante I da spec 2026-07-14.
type ProjectionDrift struct {
	CampaignID uuid.UUID
	From       string // categoria gravada
	To         string // categoria correta pelas regras vivas
	N          int64
}

// reconScopeSQL escopa TODAS as projeções com detected_at >= $1 (janela móvel
// do reconciler). Partições de detection_campaigns/detections podam por
// detected_at; materials via PK.
const reconScopeSQL = `
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.detected_at >= $1
)`

// CountProjectionDrift recomputa a categoria de toda projeção na janela e
// devolve as divergências agrupadas por (campanha, from, to). SELECT-only —
// não muta nada. Usa a MESMA CTE de classificação do recat (fonte única).
func (dr *DistributionRules) CountProjectionDrift(ctx context.Context, since time.Time) ([]ProjectionDrift, error) {
	rows, err := dr.pool.Query(ctx, reconScopeSQL+recatClassifiedCTE+`
SELECT cl.campaign_id, dc.category, cl.new_category, count(*)
FROM classified cl
JOIN detection_campaigns dc
  ON dc.detection_id = cl.id AND dc.detected_at = cl.detected_at
 AND dc.campaign_id = cl.campaign_id
WHERE dc.category IS DISTINCT FROM cl.new_category
GROUP BY cl.campaign_id, dc.category, cl.new_category
ORDER BY count(*) DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectionDrift
	for rows.Next() {
		var p ProjectionDrift
		if err := rows.Scan(&p.CampaignID, &p.From, &p.To, &p.N); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HealProjectionDrift aplica o recat à janela inteira (todas as campanhas) e
// devolve o nº de projeções corrigidas. Idempotente — segunda chamada é no-op.
func (dr *DistributionRules) HealProjectionDrift(ctx context.Context, since time.Time) (int64, error) {
	tag, err := dr.pool.Exec(ctx, reconScopeSQL+recatClassifyTailSQL, since)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 3.4: Rodar — PASS**

```bash
cd workers && go test ./internal/catalog/ -run TestProjectionDrift_CountAndHeal -v
```

- [ ] **Step 3.5: Commit**

```bash
git add workers/internal/catalog/projection_reconcile.go workers/internal/catalog/projection_reconcile_test.go
git commit -m "feat(projrecon): CountProjectionDrift + HealProjectionDrift no motor de recat"
```

---

## Task 4: Métricas + scheduler `projrecon` + wiring na API

**Files:**
- Modify: `workers/internal/metrics/metrics.go` (3 métricas novas + registro)
- Create: `workers/internal/projrecon/scheduler.go`
- Test: `workers/internal/projrecon/scheduler_test.go`
- Modify: `workers/cmd/api/main.go` (wiring, ao lado do calibrationScheduler ~linha 324)

- [ ] **Step 4.1: Métricas** — em `metrics.go`, no bloco `var` (depois de `NotificationsRecipients`):

```go
	// ── Invariante de categoria por projeção (spec 2026-07-14) ──
	// RecategorizeFailures conta falhas dos disparos best-effort de
	// recategorização (handlers de rule/override/material). Antes eram
	// engolidas (`_ =`) — categoria ficava velha em silêncio.
	RecategorizeFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_recategorize_failures_total",
		Help: "Falhas de recategorização best-effort, por origem.",
	}, []string{"origin"}) // rule_create | rule_update | rule_delete | override_upsert | override_delete | material_type_change

	// ProjectionDriftHealed conta projeções cuja categoria o reconciler
	// corrigiu. Cura sem métrica esconderia bug upstream — drift sustentado
	// > 0 = produtor novo furando o invariante (runbook ProjectionDriftPersistent).
	ProjectionDriftHealed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_projection_drift_healed_total",
		Help: "Projeções com categoria corrigida pelo reconciler, por transição.",
	}, []string{"from", "to"})

	ProjectionDriftLastRun = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_projection_drift_last_run",
		Help: "Divergências de categoria encontradas no último ciclo do reconciler.",
	})
```

E no `init()` MustRegister, adicionar à lista: `RecategorizeFailures, ProjectionDriftHealed, ProjectionDriftLastRun,`

Conferir unicidade (regra 6.5 — colisão = panic no boot):

```bash
cd workers && grep -rn "radiocheck_projection_drift\|radiocheck_recategorize_failures" internal/ | grep -v _test
```

Esperado: só as declarações em metrics.go.

- [ ] **Step 4.2: Teste do scheduler (falha: pacote não existe)**

Criar `workers/internal/projrecon/scheduler_test.go`:

```go
package projrecon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

type fakeRec struct {
	drifts    []catalog.ProjectionDrift
	healed    int64
	countErr  error
	healCalls int
	lastSince time.Time
}

func (f *fakeRec) CountProjectionDrift(_ context.Context, since time.Time) ([]catalog.ProjectionDrift, error) {
	f.lastSince = since
	return f.drifts, f.countErr
}
func (f *fakeRec) HealProjectionDrift(context.Context, time.Time) (int64, error) {
	f.healCalls++
	return f.healed, nil
}

func TestRunOnce_HealsOnlyWhenDriftFound(t *testing.T) {
	rec := &fakeRec{}
	s := New(rec, nil)

	// Sem drift → não chama Heal.
	found, healed, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, found)
	require.Zero(t, healed)
	require.Zero(t, rec.healCalls)

	// Com drift → cura e reporta.
	rec.drifts = []catalog.ProjectionDrift{{CampaignID: uuid.New(), From: "orphan", To: "in_slot", N: 10}}
	rec.healed = 10
	found, healed, err = s.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(10), found)
	require.Equal(t, int64(10), healed)
	require.Equal(t, 1, rec.healCalls)

	// Lookback aplicado: since ≈ now-Lookback.
	require.WithinDuration(t, time.Now().Add(-s.Lookback), rec.lastSince, time.Minute)
}

func TestRunOnce_CountErrorPropagates(t *testing.T) {
	rec := &fakeRec{countErr: errors.New("boom")}
	s := New(rec, nil)
	_, _, err := s.RunOnce(context.Background())
	require.Error(t, err)
	require.Zero(t, rec.healCalls)
}
```

- [ ] **Step 4.3: Rodar — FAIL (compile)**

```bash
cd workers && go test ./internal/projrecon/ -v
```

- [ ] **Step 4.4: Implementar `workers/internal/projrecon/scheduler.go`**

```go
// Package projrecon fecha a camada contínua do invariante de categoria por
// projeção (spec 2026-07-14): toda projeção em detection_campaigns deve ter
// category igual ao veredito do categorizador contra as regras/overrides
// VIVOS da campanha da projeção. Os produtores calculam certo na escrita e o
// recat cobre edições de regra — este reconciler cura (e DENUNCIA via métrica)
// qualquer caminho futuro que fure o invariante. Cura sem alerta esconderia o
// bug upstream: drift sustentado > 0 entre ciclos = investigar o produtor
// (runbook ProjectionDriftPersistent).
//
// Sem advisory lock (cf. calibration.Scheduler): HealProjectionDrift é
// idempotente — duas réplicas curando a mesma janela fazem o mesmo UPDATE; o
// único efeito colateral é dupla contagem aproximada nas métricas, aceitável.
package projrecon

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

const (
	DefaultInterval = 15 * time.Minute
	DefaultLookback = 48 * time.Hour
)

// Reconciler é a fatia de catalog.DistributionRules que o scheduler usa.
type Reconciler interface {
	CountProjectionDrift(ctx context.Context, since time.Time) ([]catalog.ProjectionDrift, error)
	HealProjectionDrift(ctx context.Context, since time.Time) (int64, error)
}

type Scheduler struct {
	rec Reconciler
	log *zap.Logger

	Interval time.Duration
	Lookback time.Duration
	NowFn    func() time.Time
}

func New(rec Reconciler, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		rec:      rec,
		log:      log,
		Interval: DefaultInterval,
		Lookback: DefaultLookback,
		NowFn:    time.Now,
	}
}

// Run bloqueia até ctx cancelar. Tick imediato no boot (padrão
// calibration.Scheduler) — um deploy não espera 15 min pra primeira cura.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.rec == nil {
		return errors.New("projrecon: reconciler is nil")
	}
	s.log.Info("projection reconciler started",
		zap.Duration("interval", s.Interval), zap.Duration("lookback", s.Lookback))

	s.tick(ctx)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("projection reconciler stopped")
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	if _, _, err := s.RunOnce(ctx); err != nil {
		s.log.Warn("projrecon: tick falhou", zap.Error(err))
	}
}

// RunOnce executa um ciclo: conta divergências na janela, cura se houver, e
// reporta métrica + log por transição. Devolve (encontradas, curadas).
func (s *Scheduler) RunOnce(ctx context.Context) (found, healed int64, err error) {
	since := s.NowFn().Add(-s.Lookback)
	drifts, err := s.rec.CountProjectionDrift(ctx, since)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range drifts {
		found += d.N
	}
	metrics.ProjectionDriftLastRun.Set(float64(found))
	if found == 0 {
		return 0, 0, nil
	}

	healed, err = s.rec.HealProjectionDrift(ctx, since)
	if err != nil {
		return found, 0, err
	}
	for _, d := range drifts {
		metrics.ProjectionDriftHealed.WithLabelValues(d.From, d.To).Add(float64(d.N))
		s.log.Info("projrecon: divergência de categoria curada",
			zap.String("campaign_id", d.CampaignID.String()),
			zap.String("from", d.From), zap.String("to", d.To),
			zap.Int64("n", d.N))
	}
	return found, healed, nil
}
```

- [ ] **Step 4.5: Rodar testes do pacote — PASS**

```bash
cd workers && go test ./internal/projrecon/ -v
```

- [ ] **Step 4.6: Wiring em `cmd/api/main.go`** — ao lado do calibrationScheduler (~linha 324), seguindo o mesmo padrão de env:

```go
	// Reconciler do invariante de categoria por projeção (spec 2026-07-14).
	// PROJECTION_RECONCILE=off desliga; intervalo/janela via env.
	if os.Getenv("PROJECTION_RECONCILE") != "off" {
		projScheduler := projrecon.New(distRulesRepo, logger)
		if v := os.Getenv("PROJECTION_RECONCILE_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				projScheduler.Interval = d
			}
		}
		if v := os.Getenv("PROJECTION_RECONCILE_LOOKBACK"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				projScheduler.Lookback = d
			}
		}
		go func() {
			if err := projScheduler.Run(ctx); err != nil {
				logger.Error("projection reconciler saiu com erro", zap.Error(err))
			}
		}()
	}
```

Notas de wiring: (a) usar a instância `*catalog.DistributionRules` que a main já constrói pros handlers (procurar `catalog.NewDistributionRules(pool)` na main; se o nome da variável for outro, ajustar); (b) import `"radiocheck/internal/projrecon"`; (c) confirmar que a main tem um `ctx` cancelável de shutdown — usar o mesmo dos outros schedulers.

- [ ] **Step 4.7: Build + testes**

```bash
cd workers && go build ./... && go test ./internal/projrecon/ ./internal/metrics/ 2>&1 | tail -5
```

- [ ] **Step 4.8: Commit**

```bash
git add workers/internal/metrics/metrics.go workers/internal/projrecon/ workers/cmd/api/main.go
git commit -m "feat(projrecon): reconciler contínuo de categoria por projeção + métricas de drift"
```

---

## Task 5: Falha de recat visível (handlers + fan-out)

**Files:**
- Create: `workers/internal/api/handlers/recat_failures.go`
- Test: `workers/internal/api/handlers/recat_failures_test.go`
- Modify: `workers/internal/api/handlers/distribution_rules.go:86,152,176`
- Modify: `workers/internal/api/handlers/distribution_overrides.go:96,130`
- Modify: `workers/internal/api/handlers/materials.go:246`
- Modify: `workers/internal/evidence/service.go:262-265`
- Verify: `workers/cmd/api/main.go` (zap.ReplaceGlobals)

- [ ] **Step 5.1: Teste do helper (falha: não existe)**

Criar `workers/internal/api/handlers/recat_failures_test.go`:

```go
package handlers

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/metrics"
)

func TestRecordRecatFailure(t *testing.T) {
	before := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	recordRecatFailure("unit_test", errors.New("boom"))
	after := testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test"))
	require.Equal(t, before+1, after)

	// err == nil não conta.
	recordRecatFailure("unit_test", nil)
	require.Equal(t, after, testutil.ToFloat64(metrics.RecategorizeFailures.WithLabelValues("unit_test")))
}
```

- [ ] **Step 5.2: Rodar — FAIL (compile)**

```bash
cd workers && go test ./internal/api/handlers/ -run TestRecordRecatFailure -v
```

- [ ] **Step 5.3: Implementar helper** — criar `workers/internal/api/handlers/recat_failures.go`:

```go
package handlers

import (
	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// recordRecatFailure torna visível a falha de uma recategorização best-effort
// (goroutine disparada pelos handlers de rule/override/material). Antes o erro
// era engolido (`_ =`) e a categoria ficava velha em silêncio — spec 2026-07-14
// §3-T2. O reconciler (projrecon) cura o dado; isto aqui denuncia a causa.
// Usa zap.L() (global) porque os handlers não carregam logger próprio.
func recordRecatFailure(origin string, err error) {
	if err == nil {
		return
	}
	metrics.RecategorizeFailures.WithLabelValues(origin).Inc()
	zap.L().Error("recategorização best-effort falhou",
		zap.String("origin", origin), zap.Error(err))
}
```

- [ ] **Step 5.4: Trocar os call sites (6 pontos)**

`distribution_rules.go` — Create (linha ~86), Update (~152), Delete (~176):

```go
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recordRecatFailure("rule_create", h.Repo.RecategorizeForRule(ctx, rule.ID))
	}()
```

(Update usa `"rule_update"` com `RecategorizeForRule(ctx, ruleID)`; Delete usa `"rule_delete"` com `RecategorizeForCampaign(ctx, campaignID)`.)

`distribution_overrides.go` — Upsert (~96) e Delete (~130):

```go
	recordRecatFailure("override_upsert", h.Recat.RecategorizeForOverride(ctx, campaignID, p.TypeID, p.StationID, date))
```

(Delete usa `"override_delete"`. Atenção: os call sites atuais são `_ = h.Recat...` — manter o mesmo contexto/goroutine em que já rodam, só trocando o descarte pelo helper.)

`materials.go` (~246):

```go
	recordRecatFailure("material_type_change", h.DistRules.RecategorizeForMaterial(ctx, id))
```

- [ ] **Step 5.5: Garantir zap.L() funcional** — verificar se a main registra o logger global:

```bash
cd workers && grep -rn "ReplaceGlobals" cmd/ internal/
```

Se NÃO houver hit, adicionar em `cmd/api/main.go` logo após a construção do `logger`:

```go
	zap.ReplaceGlobals(logger) // zap.L() nos helpers de handler (recordRecatFailure)
```

- [ ] **Step 5.6: Warn no fallback do fan-out** — em `workers/internal/evidence/service.go` (~262):

```go
			cat, cerr := s.detections.CategorizeFor(ctx, p.CampaignID, p.CommercialID, stationID, detectedAt)
			if cerr != nil {
				cat = "orphan"
				s.log.Warn("evidence: fan-out CategorizeFor falhou; projeção nasce orphan (projrecon cura)",
					zap.String("detection_id", det.ID.String()),
					zap.String("campaign_id", p.CampaignID.String()),
					zap.Error(cerr))
			}
```

- [ ] **Step 5.7: Testes + build**

```bash
cd workers && go test ./internal/api/handlers/ -run 'TestRecordRecatFailure|TestOverride' -v && go build ./...
```

Esperado: PASS (incl. os testes de mock existentes de overrides, que continuam válidos — a interface não mudou).

- [ ] **Step 5.8: Commit**

```bash
git add workers/internal/api/handlers/recat_failures.go workers/internal/api/handlers/recat_failures_test.go \
        workers/internal/api/handlers/distribution_rules.go workers/internal/api/handlers/distribution_overrides.go \
        workers/internal/api/handlers/materials.go workers/internal/evidence/service.go workers/cmd/api/main.go
git commit -m "feat(recat): falha de recategorização vira métrica + log (era engolida)"
```

---

## Task 6: `backfill-recategorize --all`

**Files:**
- Modify: `workers/cmd/backfill-recategorize/main.go`

- [ ] **Step 6.1: Adicionar a flag e o target alternativo**

Depois de `apply := flag.Bool(...)` (linha ~33):

```go
	all := flag.Bool("all", false, "todas as campanhas com projeções (default: só campanhas com regra carve-out)")
```

E o target query vira condicional (substituir o bloco `const targetQuery` ~linha 48):

```go
	// Default: campanhas com regra carve-out (escopo original do spec 2026-07-13).
	// --all: toda campanha com ao menos uma projeção — necessário 1× após o motor
	// de recat passar a escopar por projeção (spec 2026-07-14), pra convergir o
	// histórico de projeções fan-out que os recats antigos nunca alcançaram.
	targetQuery := `
		SELECT DISTINCT c.id, c.name
		FROM campaigns c
		JOIN distribution_rules r ON r.campaign_id = c.id
		WHERE cardinality(r.material_ids) > 0
		  AND ($1::uuid IS NULL OR c.id = $1)
		ORDER BY c.name`
	if *all {
		targetQuery = `
		SELECT c.id, c.name
		FROM campaigns c
		WHERE EXISTS (SELECT 1 FROM detection_campaigns dc WHERE dc.campaign_id = c.id)
		  AND ($1::uuid IS NULL OR c.id = $1)
		ORDER BY c.name`
	}
```

(Trocar `const targetQuery` por variável; o resto do fluxo — dry-run default, report ANTES/DEPOIS, loop `RecategorizeForCampaign` — fica intacto e passa a alcançar projeções de graça via Task 1.)

Atualizar o comentário do topo do arquivo mencionando `--all` e a spec 2026-07-14.

- [ ] **Step 6.2: Build + smoke local**

```bash
cd workers && go build ./cmd/backfill-recategorize/ && go vet ./cmd/backfill-recategorize/
```

(O binário já está no `workers.Dockerfile` — regra 6.7 satisfeita, sem mudança.)

- [ ] **Step 6.3: Commit**

```bash
git add workers/cmd/backfill-recategorize/main.go
git commit -m "feat(backfill): --all converge projeções de todas as campanhas (spec 2026-07-14)"
```

---

## Task 7: Documentação

**Files:**
- Create: `docs/architecture/projection-category-invariant.md`
- Create: `docs/runbooks/ProjectionDriftPersistent.md`
- Modify: `docs/runbooks/README.md` (índice), `docs/README.md` (índice), `CLAUDE.md` (mapa), `docs/features/multi-attribution.md` + `docs/architecture/detection-count-consistency.md` (referência cruzada)

- [ ] **Step 7.1: Doc de arquitetura** — `docs/architecture/projection-category-invariant.md` com header YAML (`status: implementado`, `ultima-verificacao: <data do dia>`, `codigo-relacionado`: `workers/internal/catalog/distribution_rules.go`, `workers/internal/catalog/projection_reconcile.go`, `workers/internal/projrecon/scheduler.go`, `workers/cmd/backfill-recategorize/main.go`). Conteúdo: o invariante I, as 3 camadas (escrita/edição/contínua), a guarda `d.campaign_id = cl.campaign_id`, o caso COPA 10/07 como exemplo motivador, envs (`PROJECTION_RECONCILE`, `_INTERVAL`, `_LOOKBACK`), métricas e o procedimento do backfill `--all` (§4.8: clone → dry-run → apply).

- [ ] **Step 7.2: Runbook** — `docs/runbooks/ProjectionDriftPersistent.md`: alerta = `radiocheck_projection_drift_last_run > 0` sustentado por 3+ ciclos (45min). Diagnóstico: drift esporádico pós-edição de regra é normal (janela até o próximo tick); drift que REAPARECE a cada ciclo = um produtor está escrevendo categoria errada em loop (ex.: rotina de desambiguação re-suja o que o reconciler cura) → identificar via log `projrecon: divergência de categoria curada` (campanha + transição) e investigar o caminho de escrita, NUNCA silenciar o alerta. Adicionar linha no índice `docs/runbooks/README.md`.

- [ ] **Step 7.3: Cross-refs** — em `multi-attribution.md` e `detection-count-consistency.md`, seção curta "Sincronização de categoria" apontando pro doc novo. No `CLAUDE.md`, linha nova na tabela do mapa:

```markdown
| Categoria de projeção divergente da grade (bônus/órfã fantasma), reconciler de projeções, drift | [docs/architecture/projection-category-invariant.md](docs/architecture/projection-category-invariant.md) |
```

E linha no índice `docs/README.md`.

- [ ] **Step 7.4: Commit**

```bash
git add docs/architecture/projection-category-invariant.md docs/runbooks/ProjectionDriftPersistent.md \
        docs/runbooks/README.md docs/README.md CLAUDE.md \
        docs/features/multi-attribution.md docs/architecture/detection-count-consistency.md
git commit -m "docs(projrecon): invariante de categoria por projeção + runbook de drift"
```

---

## Task 8: Verificação final de deploy (regra 6)

- [ ] **Step 8.1: Suite completa**

```bash
cd workers && go test ./... 2>&1 | tail -30
```

Esperado: verde, EXCETO flakies conhecidas (regra 6.6: `internal/catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC; harness pré-existente da memória `test-db-native-pg-shadows-docker`). Qualquer falha em pacote tocado por este plano = regressão, investigar.

- [ ] **Step 8.2: Cross-compile linux (o que o deploy realmente builda — regra 6.1)**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: exit 0 para todos os `cmd/*`.

- [ ] **Step 8.3: Unicidade de métricas (regra 6.5)**

```bash
cd workers && grep -rn "radiocheck_projection_\|radiocheck_recategorize_" internal/ --include="*.go" | grep -v _test | grep Name:
```

Esperado: exatamente 3 linhas (as declarações de metrics.go).

- [ ] **Step 8.4: Commit final (se sobrou algo) + resumo pro usuário**

Reportar: branch pronta, o que falta é decisão de merge/deploy + o backfill `--all` one-shot (clone §4.8 → prod) e observação do gauge nas primeiras 48h.

---

## Pós-merge (operacional — fora do código, executa o Dereck)

1. Deploy padrão (`./scripts/deploy.sh`).
2. `backfill-recategorize --all` **dry-run** → clone de prod §4.8 com `--apply` → conferir delta → `--apply` em prod.
3. Observar `radiocheck_projection_drift_last_run` por 48h (deve tender a 0 fora de janelas de edição).
4. Alerta Prometheus (drift > 0 por 3 ciclos) — wiring junto dos alertas existentes.
