# Regras de Distribuição por Material Específico — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir que uma `distribution_rule` seja escopada a materiais específicos dentro de um tipo, com categorização "carve-out" (material com regra própria é julgado só por ela), sem mudar nenhuma tela.

**Architecture:** Coluna `material_ids UUID[]` nas regras (vazio = todos do tipo, como hoje). O categorizador Go e o SQL de recategorização ganham a mesma lógica de precedência por material. View de resumo e grid ficam intactos — só `detections.category` fica mais fina.

**Tech Stack:** Go 1.26 (workers), PostgreSQL (golang-migrate), React 18 (Vite). Testes Go: unit puro no `categorizer`, DB-backed no `catalog` via `TEST_DATABASE_URL`.

**Spec:** [docs/superpowers/specs/2026-06-29-material-specific-distribution-rules-design.md](../specs/2026-06-29-material-specific-distribution-rules-design.md)

---

## Convenções deste plano

- **Build de prod = cross-compile** (regra 6.1 do CLAUDE.md): valide com `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`.
- Testes DB-backed **pulam** sem `TEST_DATABASE_URL`. Para rodá-los, exporte a URL de um Postgres de teste com as migrations aplicadas. Sem ele, rode ao menos os testes do pacote `categorizer` (puro, sem DB).
- `frontend/` **não tem** testes automatizados (padrão do repo) — as tasks de frontend usam smoke manual + sanity de build. **Não** rode `npm install` no Windows (regra 5 do CLAUDE.md).

---

## Task 1: Migration — coluna `material_ids`

**Files:**
- Create: `migrations/0043_rule_material_scope.up.sql`
- Create: `migrations/0043_rule_material_scope.down.sql`

> Confirme que `0042` ainda é a última migration (`ls migrations/ | sort | tail -3`). Se outra `0043` apareceu, renumere para a próxima livre.

- [ ] **Step 1: Escreva a migration up**

`migrations/0043_rule_material_scope.up.sql`:
```sql
-- 0043_rule_material_scope.up.sql
-- Escopo opcional de regra a materiais específicos dentro de um tipo.
-- material_ids vazio ('{}') = regra vale pra TODOS os materiais do tipo
-- (comportamento da migration 0019, inalterado). Preenchido = vale só pra
-- esses materiais, com precedência (carve-out) no categorizador.
-- Spec: docs/superpowers/specs/2026-06-29-material-specific-distribution-rules-design.md

BEGIN;

ALTER TABLE distribution_rules
  ADD COLUMN material_ids UUID[] NOT NULL DEFAULT '{}';

COMMIT;
```

- [ ] **Step 2: Escreva a migration down**

`migrations/0043_rule_material_scope.down.sql`:
```sql
-- 0043_rule_material_scope.down.sql
BEGIN;

ALTER TABLE distribution_rules DROP COLUMN material_ids;

COMMIT;
```

- [ ] **Step 3: Aplique a migration no DB de teste local e confirme a coluna**

Run (ajuste a URL ao seu Postgres de dev/teste):
```bash
cd workers && go run ./cmd/migrate up   # ou o caminho de migrate usado no repo
```
Se não houver `cmd/migrate`, aplique via psql:
```bash
psql "$TEST_DATABASE_URL" -c "\d distribution_rules" | grep material_ids
```
Expected: linha mostrando `material_ids | uuid[] | not null | '{}'::uuid[]`.

- [ ] **Step 4: Commit**

```bash
git add migrations/0043_rule_material_scope.up.sql migrations/0043_rule_material_scope.down.sql
git commit -m "feat(distribution): coluna material_ids em distribution_rules (escopo por material)"
```

---

## Task 2: Categorizador Go — `Rule.MaterialIDs` + carve-out

Lógica pura, sem DB. Esta é a fonte de verdade que o SQL (Task 5) vai espelhar.

**Files:**
- Modify: `workers/internal/categorizer/categorizer.go`
- Modify: `workers/internal/categorizer/categorizer_test.go`

- [ ] **Step 1: Escreva os testes que falham (carve-out)**

Adicione ao fim de `categorizer_test.go` (antes do `var _ = uuid.UUID{}`). Helper + casos:

```go
// mkMatRule é como mkRule mas com material_ids preenchido (regra específica).
func mkMatRule(startDay, endDay int, mask int16, ts, te string, plays int16, mats ...uuid.UUID) Rule {
	r := mkRule(startDay, endDay, mask, ts, te, plays)
	r.MaterialIDs = mats
	return r
}

func TestCategorize_CarveOut_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m: 1ª semana (1-7), seg-sex, 18-19h.
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 02/06 (ter) 18:30 BRT (21:30 UTC) → dentro → in_slot
	det := time.Date(2026, 6, 2, 21, 30, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "in_slot" {
		t.Errorf("got %q, want in_slot", got)
	}
}

func TestCategorize_CarveOut_OutSlot_WrongTime(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 02/06 (ter) 10:00 BRT — dentro da data/dia, fora da faixa → out_slot
	det := time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_CarveOut_OutDate_OutsideRulePeriod(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	rules := []Rule{mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)}
	// m toca 16/06 (semana 3) 18:30 BRT — fora do período da regra dele,
	// mas dentro da campanha → out_date (não orphan, não in_slot).
	det := time.Date(2026, 6, 16, 21, 30, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_CarveOut_IgnoresGeneralTypeRule(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra GERAL do tipo (07-19h o mês todo) + regra específica de m (1ª sem 18-19h).
	general := mkRule(1, 30, 62, "07:00", "19:00", 3) // material_ids vazio
	specific := mkMatRule(1, 7, 62, "18:00", "19:00", 1, m)
	rules := []Rule{general, specific}
	// m toca 16/06 (semana 3) 10:00 BRT. A regra geral cobriria (07-19h), MAS
	// m está carved-out → só a regra dele vale → fora do período → out_date.
	det := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, m, rules, nil); got != "out_date" {
		t.Errorf("got %q, want out_date (carve-out ignora regra geral)", got)
	}
}

func TestCategorize_NonCarvedMaterial_UsesGeneralRule(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	other := uuid.New() // material SEM regra específica
	specificForSomeoneElse := mkMatRule(1, 7, 62, "18:00", "19:00", 1, uuid.New())
	general := mkRule(1, 30, 62, "07:00", "19:00", 3)
	rules := []Rule{general, specificForSomeoneElse}
	// other toca 16/06 10:00 BRT — não está carved-out → regra geral vale → in_slot.
	det := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	if got := Categorize(det, cmp, other, rules, nil); got != "in_slot" {
		t.Errorf("got %q, want in_slot (material comum usa regra geral)", got)
	}
}
```

- [ ] **Step 2: Rode os testes — devem falhar de COMPILAÇÃO**

Run: `cd workers && go test ./internal/categorizer/ -run CarveOut`
Expected: FAIL — `too many arguments in call to Categorize` e `r.MaterialIDs undefined`. (A assinatura ainda é a antiga.)

- [ ] **Step 3: Implemente — `Rule.MaterialIDs`, nova assinatura, carve-out**

Em `categorizer.go`, adicione o import de uuid:
```go
import (
	"time"

	"github.com/google/uuid"
)
```

Adicione o campo ao `Rule` (logo após `PlaysPerDay int16`):
```go
	PlaysPerDay int16
	// MaterialIDs vazio = regra vale pra todos os materiais do tipo (migration
	// 0019). Não-vazio = regra "carve-out": vale só pra esses materiais, e eles
	// passam a ser julgados SÓ por regras que os nomeiam (migration 0043).
	MaterialIDs []uuid.UUID
```

Substitua TODA a função `Categorize` por:
```go
// Categorize classifica uma detection do material materialID.
//
// Carve-out (migration 0043): se materialID é nomeado em alguma regra com
// MaterialIDs não-vazio, ele é julgado SÓ por essas regras (regras gerais do
// tipo — MaterialIDs vazio — deixam de valer pra ele). Tocar fora do período/
// dia da regra dele vira out_date; fora da faixa, out_slot; nunca orphan.
// Material sem regra específica usa as regras gerais, exatamente como antes.
//
// Comparações de data no fuso America/Sao_Paulo (ver Categorize original).
func Categorize(detectedAt time.Time, cmp Campaign, materialID uuid.UUID, rules []Rule, override *Override) string {
	local := detectedAt.In(spLocation)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

	if date.Before(dateOnlySP(cmp.StartDate)) || date.After(dateOnlySP(cmp.EndDate)) {
		return CatOutDate
	}

	hh := local.Hour()
	mm := local.Minute()
	ss := local.Second()
	timeOfDay := hh*3600 + mm*60 + ss

	if override != nil {
		if override.PlaysExpected == 0 {
			return CatOutSlot
		}
		os := override.TimeStart.Hour()*3600 + override.TimeStart.Minute()*60 + override.TimeStart.Second()
		oe := override.TimeEnd.Hour()*3600 + override.TimeEnd.Minute()*60 + override.TimeEnd.Second()
		if timeOfDay >= os-SlotToleranceSeconds && timeOfDay <= oe+SlotToleranceSeconds {
			return CatInSlot
		}
		return CatOutSlot
	}

	dow := int(local.Weekday())

	// inWindow casa data+dia da rule e (opcionalmente) a faixa horária tolerada.
	matchesDateWeekday := func(r Rule) bool {
		if date.Before(dateOnlySP(r.StartDate)) || date.After(dateOnlySP(r.EndDate)) {
			return false
		}
		return (1<<dow)&int(r.WeekdayMask) != 0
	}
	matchesTime := func(r Rule) bool {
		rs := r.TimeStart.Hour()*3600 + r.TimeStart.Minute()*60 + r.TimeStart.Second()
		re := r.TimeEnd.Hour()*3600 + r.TimeEnd.Minute()*60 + r.TimeEnd.Second()
		return timeOfDay >= rs-SlotToleranceSeconds && timeOfDay <= re+SlotToleranceSeconds
	}

	// Carve-out: materialID é nomeado em ALGUMA regra específica? (independente de data)
	carved := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 && containsUUID(r.MaterialIDs, materialID) {
			carved = true
			break
		}
	}

	if carved {
		hasDateWeekday := false
		for _, r := range rules {
			if len(r.MaterialIDs) == 0 || !containsUUID(r.MaterialIDs, materialID) {
				continue
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
		return CatOutDate
	}

	// Material comum — só regras gerais (MaterialIDs vazio), lógica original.
	hasApplicable := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 {
			continue
		}
		if !matchesDateWeekday(r) {
			continue
		}
		hasApplicable = true
		if matchesTime(r) {
			return CatInSlot
		}
	}
	if hasApplicable {
		return CatOutSlot
	}
	return CatOrphan
}

func containsUUID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Atualize os call-sites existentes de `Categorize` no teste**

Os ~14 testes antigos chamam `Categorize(det, cmp, rules, nil)` / `(det, cmp, nil, ov)`. Insira `uuid.Nil` como 3º argumento em TODOS. Exemplos:
- `Categorize(time.Date(...), cmp, nil, nil)` → `Categorize(time.Date(...), cmp, uuid.Nil, nil, nil)`
- `Categorize(det, cmp, rules, nil)` → `Categorize(det, cmp, uuid.Nil, rules, nil)`
- `Categorize(det, cmp, nil, ov)` → `Categorize(det, cmp, uuid.Nil, nil, ov)`

Remova a linha `var _ = uuid.UUID{}` do fim do arquivo (uuid agora é usado de verdade).

> Por que `uuid.Nil` mantém o comportamento: as regras desses testes têm `MaterialIDs` vazio (gerais). `uuid.Nil` não está em nenhuma → `carved=false` → caminho geral → mesmo resultado de antes.

- [ ] **Step 5: Rode todo o pacote categorizer**

Run: `cd workers && go test ./internal/categorizer/ -v`
Expected: PASS — incluindo os 5 novos `CarveOut`/`NonCarved` e todos os antigos.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/categorizer/categorizer.go workers/internal/categorizer/categorizer_test.go
git commit -m "feat(categorizer): carve-out por material (MaterialIDs + precedência)"
```

---

## Task 3: Catalog repo — `material_ids` no struct/input/SQL

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go`
- Modify: `workers/internal/catalog/distribution_rules_test.go`

- [ ] **Step 1: Teste que falha — Create/Get com material_ids**

Adicione a `distribution_rules_test.go`:
```go
func TestDistributionRules_MaterialIDs_Roundtrip(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeID := seedType(t, ctx, pool, "Spot")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})
	m1, m2 := uuid.New(), uuid.New()
	repo := NewDistributionRules(pool)
	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{uuid.New()},
		MaterialIDs: []uuid.UUID{m1, m2},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rule.MaterialIDs) != 2 {
		t.Fatalf("MaterialIDs len = %d, want 2", len(rule.MaterialIDs))
	}
	got, _ := repo.Get(ctx, rule.ID)
	if len(got.MaterialIDs) != 2 {
		t.Errorf("after Get: MaterialIDs len = %d, want 2", len(got.MaterialIDs))
	}
	// Default vazio quando não informado.
	rule2, _ := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{uuid.New()},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00", PlaysPerDay: 1,
	})
	if len(rule2.MaterialIDs) != 0 {
		t.Errorf("default MaterialIDs len = %d, want 0", len(rule2.MaterialIDs))
	}
}
```

- [ ] **Step 2: Rode — falha de compilação**

Run: `cd workers && go test ./internal/catalog/ -run MaterialIDs_Roundtrip`
Expected: FAIL — `unknown field MaterialIDs` em `CreateDistributionRuleInput`/`DistributionRule`.

- [ ] **Step 3: Adicione `MaterialIDs` ao struct e ao input**

Em `distribution_rules.go`, no `DistributionRule` (após `StationIDs`):
```go
	StationIDs  []uuid.UUID `json:"station_ids"`
	MaterialIDs []uuid.UUID `json:"material_ids"`
```
No `CreateDistributionRuleInput` (após `StationIDs`):
```go
	StationIDs  []uuid.UUID
	MaterialIDs []uuid.UUID
```

- [ ] **Step 4: Inclua `material_ids` no `ruleColumns` e em todos os Scan**

Troque a constante `ruleColumns`:
```go
const ruleColumns = `id, campaign_id, type_id, station_ids, material_ids,
       start_date, end_date, weekday_mask,
       to_char(time_start, 'HH24:MI') AS time_start,
       to_char(time_end,   'HH24:MI') AS time_end,
       plays_per_day, created_at, updated_at`
```
Em CADA um dos 4 sites de `.Scan(...)` (Create RETURNING, Get, ListByCampaign loop, ListApplicable loop), insira `&r.MaterialIDs` logo após `&r.StationIDs`:
```go
.Scan(&r.ID, &r.CampaignID, &r.TypeID, &r.StationIDs, &r.MaterialIDs,
	&r.StartDate, &r.EndDate, &r.WeekdayMask,
	&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
	&r.CreatedAt, &r.UpdatedAt)
```

- [ ] **Step 5: Inclua `material_ids` no INSERT (Create) e no UPDATE (Update)**

Create — adicione a coluna e o placeholder (deslocando os seguintes em +1):
```go
	err := dr.pool.QueryRow(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, type_id, station_ids, material_ids, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::time, $9::time, $10)
		RETURNING `+ruleColumns,
		in.CampaignID, in.TypeID, in.StationIDs, in.MaterialIDs,
		in.StartDate, in.EndDate, in.WeekdayMask,
		in.TimeStart, in.TimeEnd, in.PlaysPerDay,
	).Scan(...)  // scan já atualizado no Step 4
```
Update — adicione `material_ids = $N` (renumere os placeholders):
```go
	_, err := dr.pool.Exec(ctx, `
		UPDATE distribution_rules
		SET type_id = $2, station_ids = $3, material_ids = $4, start_date = $5,
		    end_date = $6, weekday_mask = $7, time_start = $8::time,
		    time_end = $9::time, plays_per_day = $10, updated_at = now()
		WHERE id = $1`,
		id, in.TypeID, in.StationIDs, in.MaterialIDs, in.StartDate, in.EndDate,
		in.WeekdayMask, in.TimeStart, in.TimeEnd, in.PlaysPerDay)
```

- [ ] **Step 6: Rode o teste**

Run: `cd workers && go test ./internal/catalog/ -run MaterialIDs_Roundtrip -v`
Expected: PASS. (Pula se `TEST_DATABASE_URL` não setado — nesse caso garanta ao menos `go build`.)

- [ ] **Step 7: Garanta o build cross-compile**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem erros.

- [ ] **Step 8: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_test.go
git commit -m "feat(catalog): material_ids no repo de distribution_rules"
```

---

## Task 4: Insert path — carregar `material_ids` e passar `materialID` ao categorizador

**Files:**
- Modify: `workers/internal/catalog/detections.go` (função `categorize`, ~linha 186-252)
- Modify: `workers/internal/catalog/detections_test.go`

- [ ] **Step 1: Teste de insert que falha (carve-out no insert real)**

Adicione a `detections_test.go` (segue o padrão de seed dos testes existentes):
```go
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
```

- [ ] **Step 2: Rode — deve falhar (hoje vira in_slot/orphan, não out_date)**

Run: `cd workers && go test ./internal/catalog/ -run Insert_CarveOut -v`
Expected: FAIL — `category = "orphan"` (ou similar), want `out_date`. (Pula sem DB; nesse caso confie na Task 2 + paridade da Task 5.)

- [ ] **Step 3: Carregue `material_ids` e passe `materialID` no `categorize`**

Em `detections.go`, no SELECT de rules (~linha 186), adicione `r.material_ids`:
```go
	rows, err := d.pool.Query(ctx, `
		SELECT r.start_date, r.end_date, r.weekday_mask,
		       r.time_start::text, r.time_end::text, r.plays_per_day, r.material_ids
		FROM distribution_rules r
		WHERE r.campaign_id = $1
		  AND r.type_id = (SELECT type_id FROM materials WHERE id = $2)
		  AND $3 = ANY(r.station_ids)`,
		in.CampaignID, in.CommercialID, in.StationID)
```
No loop de scan (~linha 200-211), adicione `&r.MaterialIDs`:
```go
		var r categorizer.Rule
		var tsStr, teStr string
		var plays int16
		if err := rows.Scan(&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&tsStr, &teStr, &plays, &r.MaterialIDs); err != nil {
			return categorizer.CatOrphan, err
		}
```
Na chamada final (~linha 247), passe `in.CommercialID`:
```go
	return categorizer.Categorize(
		in.DetectedAt,
		categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd},
		in.CommercialID,
		rules,
		ov,
	), nil
```

- [ ] **Step 4: Rode o teste**

Run: `cd workers && go test ./internal/catalog/ -run Insert_CarveOut -v`
Expected: PASS.

- [ ] **Step 5: Build cross-compile + commit**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(catalog): insert categoriza com carve-out por material"
```

---

## Task 5: Recategorização SQL — paridade do carve-out + escopo da campanha

O ponto de maior risco: o SQL precisa dar o MESMO resultado do Go (Task 2).

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go` (`recatClassifyTailSQL`, `RecategorizeForRule`)
- Modify: `workers/internal/catalog/distribution_rules_test.go`

- [ ] **Step 1: Teste de recategorização que falha (carve-out retroativo)**

Adicione a `distribution_rules_test.go`:
```go
func TestDistributionRules_Recategorize_CarveOut(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mats := NewMaterials(pool)
	special, _ := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Special", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-special",
	})
	normal, _ := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Normal", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk-normal",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM Recat", Band: "FM", StreamURL: "http://x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = ANY($1)", []uuid.UUID{special.ID, normal.ID})
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	dets := NewDetections(pool)
	mk := func(mat uuid.UUID, utc time.Time) *Detection {
		t.Helper()
		d, err := dets.Create(ctx, CreateDetectionInput{
			StationID: stat.ID, CommercialID: mat, CampaignID: cmp.ID,
			DetectedAt: utc, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		if err != nil {
			t.Fatalf("create detection: %v", err)
		}
		return d
	}
	// special toca 16/06 (semana 3) 18:30 BRT — vai virar out_date após a regra dele.
	dSpecialLate := mk(special.ID, time.Date(2026, 6, 16, 21, 30, 0, 0, time.UTC))
	// normal toca 16/06 10:00 BRT — coberto pela regra geral → in_slot.
	dNormal := mk(normal.ID, time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC))

	repo := NewDistributionRules(pool)
	// Regra GERAL (todos do tipo, mês todo, 07-19h).
	if _, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "07:00", TimeEnd: "19:00", PlaysPerDay: 3,
	}); err != nil {
		t.Fatalf("create general rule: %v", err)
	}
	// Regra ESPECÍFICA de special: 1ª semana, 18-19h. Dispara o carve-out.
	specRule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID, StationIDs: []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{special.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "18:00", TimeEnd: "19:00", PlaysPerDay: 1,
	})
	if err != nil {
		t.Fatalf("create specific rule: %v", err)
	}
	if err := repo.RecategorizeForRule(ctx, specRule.ID); err != nil {
		t.Fatalf("recategorize: %v", err)
	}

	readCat := func(d *Detection) string {
		t.Helper()
		var c string
		pool.QueryRow(ctx, `SELECT category FROM detections WHERE id=$1 AND detected_at=$2`,
			d.ID, d.DetectedAt).Scan(&c)
		return c
	}
	// special fora do período da regra dele (semana 3) → out_date, mesmo com a
	// regra geral cobrindo 07-19h (carve-out ignora a geral).
	if got := readCat(dSpecialLate); got != "out_date" {
		t.Errorf("special late: category = %q, want out_date", got)
	}
	// normal não é carved-out → regra geral → in_slot.
	if got := readCat(dNormal); got != "in_slot" {
		t.Errorf("normal: category = %q, want in_slot", got)
	}
}
```

- [ ] **Step 2: Rode — deve falhar**

Run: `cd workers && go test ./internal/catalog/ -run Recategorize_CarveOut -v`
Expected: FAIL — `special late` vem `in_slot` (regra geral ainda casa) em vez de `out_date`. (Pula sem DB.)

- [ ] **Step 3: Reescreva `recatClassifyTailSQL` com carve-out**

Substitua a constante inteira `recatClassifyTailSQL` por (mesma estrutura de UPDATE no fim, só o bloco `classified` muda):
```go
const recatClassifyTailSQL = `,
classified AS (
    SELECT
        s.id, s.detected_at, s.campaign_id,
        CASE
            WHEN date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                 NOT BETWEEN c.start_date AND c.end_date
                THEN 'out_date'
            WHEN s.type_id IS NULL THEN 'orphan'
            -- ── Carve-out: material nomeado em alguma regra específica ──
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.type_id = s.type_id
                  AND s.station_id = ANY(r.station_ids)
                  AND cardinality(r.material_ids) > 0
                  AND s.material_id = ANY(r.material_ids)
            ) THEN (
                CASE
                    WHEN EXISTS (
                        SELECT 1 FROM distribution_rules r
                        WHERE r.campaign_id = s.campaign_id
                          AND r.type_id = s.type_id
                          AND s.station_id = ANY(r.station_ids)
                          AND s.material_id = ANY(r.material_ids)
                          AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                              BETWEEN r.start_date AND r.end_date
                          AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                          AND EXTRACT(EPOCH FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo')::time)
                              BETWEEN EXTRACT(EPOCH FROM r.time_start) - 900
                                  AND EXTRACT(EPOCH FROM r.time_end)   + 900
                    ) THEN 'in_slot'
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
                    ELSE 'out_date'
                END
            )
            -- ── Material comum: só regras gerais (material_ids vazio) ──
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.type_id = s.type_id
                  AND s.station_id = ANY(r.station_ids)
                  AND cardinality(r.material_ids) = 0
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                  AND EXTRACT(EPOCH FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo')::time)
                      BETWEEN EXTRACT(EPOCH FROM r.time_start) - 900
                          AND EXTRACT(EPOCH FROM r.time_end)   + 900
            )
                THEN 'in_slot'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.type_id = s.type_id
                  AND s.station_id = ANY(r.station_ids)
                  AND cardinality(r.material_ids) = 0
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
            )
                THEN 'out_slot'
            ELSE 'orphan'
        END AS new_category
    FROM scope s
    JOIN campaigns c ON c.id = s.campaign_id
)
, upd_det AS (
    UPDATE detections d
    SET category = cl.new_category
    FROM classified cl
    WHERE d.id = cl.id AND d.detected_at = cl.detected_at
      AND d.category IS DISTINCT FROM cl.new_category
    RETURNING 1
)
UPDATE detection_campaigns dc
SET category = cl.new_category
FROM classified cl
WHERE dc.detection_id = cl.id AND dc.detected_at = cl.detected_at
  AND dc.campaign_id = cl.campaign_id
  AND dc.category IS DISTINCT FROM cl.new_category`
```

- [ ] **Step 4: Amplie o escopo de `RecategorizeForRule` pro período da campanha**

Carve-out exige reavaliar tocadas do material FORA do período da regra. Troque `RecategorizeForRule` por (escopo = tipo, TODAS as estações, período da campanha):
```go
func (dr *DistributionRules) RecategorizeForRule(ctx context.Context, ruleID uuid.UUID) error {
	r, err := dr.Get(ctx, ruleID)
	if err != nil {
		return err
	}
	var cs, ce time.Time
	if err := dr.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, r.CampaignID,
	).Scan(&cs, &ce); err != nil {
		return err
	}
	// Escopo amplo (tipo inteiro, todas as estações, período da campanha) pra
	// pegar detections do material fora do período/estação da regra — que o
	// carve-out pode reclassificar (ex.: out_date fora da 1ª semana).
	return dr.recategorizeScope(ctx, r.CampaignID, &r.TypeID, nil, cs, ce)
}
```

- [ ] **Step 5: Rode o teste de carve-out + os de regressão da recategorização**

Run: `cd workers && go test ./internal/catalog/ -run "Recategorize" -v`
Expected: PASS — `Recategorize_CarveOut`, `RecategorizeAfterCreate`, `RecategorizeForMaterial`, `RecategorizeRespectsSlotTolerance` (os antigos seguem verdes: regras sem material_ids caem no ramo "material comum").

- [ ] **Step 6: Build cross-compile + commit**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_test.go
git commit -m "feat(catalog): recategorização SQL com carve-out por material (paridade Go)"
```

---

## Task 6: API handler — `material_ids` no payload

**Files:**
- Modify: `workers/internal/api/handlers/distribution_rules.go`

- [ ] **Step 1: Adicione `material_ids` ao `rulePayload` e ao `toInput`**

No struct `rulePayload` (após `StationIDs`):
```go
	StationIDs  []uuid.UUID `json:"station_ids"`
	MaterialIDs []uuid.UUID `json:"material_ids"`
```
Em `toInput`, no retorno do `CreateDistributionRuleInput` (após `StationIDs`):
```go
		CampaignID: campaignID, TypeID: p.TypeID,
		StationIDs:  p.StationIDs,
		MaterialIDs: p.MaterialIDs,
		StartDate:  start, EndDate: end,
```
> `material_ids` ausente no JSON → `nil` → INSERT grava `'{}'` (regra geral). Comportamento de hoje preservado pra clientes que não mandam o campo.

- [ ] **Step 2: Build cross-compile**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem erros.

- [ ] **Step 3: (Se houver suite de API up) smoke via curl — opcional**

Com a API local de pé:
```bash
curl -s -X POST localhost:8080/v1/internal/campaigns/<CID>/distribution-rules \
  -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \
  -d '{"type_id":"<TID>","station_ids":["<SID>"],"material_ids":["<MID>"],"start_date":"2026-06-01","end_date":"2026-06-07","weekday_mask":62,"time_start":"18:00","time_end":"19:00","plays_per_day":1}'
```
Expected: `201` com a regra retornando `material_ids` preenchido.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/api/handlers/distribution_rules.go
git commit -m "feat(api): material_ids no payload de distribution-rules"
```

---

## Task 7: Frontend — seletor de materiais no `RuleSidePanel`

> Sem testes automatizados no frontend (padrão do repo). Implemente, valide por build e smoke manual.

**Files:**
- Modify: `frontend/src/components/RuleSidePanel.jsx`
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`

- [ ] **Step 1: `DistributionStep` — monte `materialsByType` e passe ao painel**

Em `DistributionStep.jsx`, adicione um `useMemo` perto de `ruleEditorTypes` (que mapeia tipo → materiais vinculados na campanha):
```jsx
  // type_id → [{ id, title }] dos materiais daquele tipo vinculados à campanha.
  // Alimenta o seletor opcional de materiais do RuleSidePanel (regra por material).
  const materialsByType = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      if (!m.has(mat.type_id)) m.set(mat.type_id, [])
      m.get(mat.type_id).push({ id: cm.material_id, title: mat.title ?? 'Material' })
    }
    return m
  }, [campaignMaterials, materialsById])
```
E passe ao `<RuleSidePanel ...>` (junto dos outros props):
```jsx
        materialsByType={materialsByType}
```

- [ ] **Step 2: `RuleSidePanel` — estado + UI do seletor de materiais**

No `RuleSidePanel.jsx`, adicione `materialsByType = new Map()` aos props (na desestruturação). Adicione estado:
```jsx
  const [materialIds, setMaterialIds] = useState(initial?.material_ids ?? [])
```
No `useEffect(open, ...)` que reseta os campos, adicione:
```jsx
      setMaterialIds(initial?.material_ids ?? [])
```
Adicione o toggle:
```jsx
  function toggleMaterial(id) {
    setMaterialIds(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }
```
Quando o usuário troca pra multi-tipo, materiais não fazem sentido — limpe ao passar de 1 tipo. Ajuste `toggleType`:
```jsx
  function toggleType(id) {
    if (isEdit) return
    setTypeIds(prev => {
      const next = prev.includes(id) ? prev.filter(t => t !== id) : [...prev, id]
      if (next.length !== 1) setMaterialIds([]) // materiais só com tipo único
      return next
    })
  }
```
Renderize o bloco do seletor logo APÓS o bloco de "Tipos de material" (e antes de "Emissoras"). Só aparece com exatamente 1 tipo selecionado:
```jsx
          {typeIds.length === 1 && (materialsByType.get(typeIds[0])?.length ?? 0) > 0 && (
            <div style={{ marginBottom: 20 }}>
              <Label>
                Materiais específicos
                <span style={{ marginLeft: 8, fontSize: 10, fontWeight: 600,
                  color: 'var(--c-text-3)', textTransform: 'none', letterSpacing: 0 }}>
                  (opcional — vazio = vale pra qualquer material do tipo)
                </span>
              </Label>
              <div style={chipRow}>
                {(materialsByType.get(typeIds[0]) ?? []).map(mat => {
                  const on = materialIds.includes(mat.id)
                  return (
                    <button key={mat.id} type="button"
                      onClick={() => toggleMaterial(mat.id)}
                      style={{ ...chip, ...(on ? chipOn : {}) }}>
                      {mat.title}
                    </button>
                  )
                })}
              </div>
              {materialIds.length > 0 && (
                <div style={{ marginTop: 10, padding: '8px 12px', background: 'var(--c-bg)',
                  border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)',
                  fontSize: 11, color: 'var(--c-text-2)', lineHeight: 1.5 }}>
                  Essa regra vale <strong style={{ color: 'var(--c-text)' }}>só pra {materialIds.length} material{materialIds.length !== 1 ? 'is' : ''}</strong> selecionado{materialIds.length !== 1 ? 's' : ''}. Eles passam a ser cobrados só por esta regra (tocar fora vira desvio).
                </div>
              )}
            </div>
          )}
```

- [ ] **Step 3: `RuleSidePanel` — inclua `material_ids` no submit**

No `submit()`, adicione `material_ids` ao `common`:
```jsx
    const common = {
      station_ids: stationIds,
      material_ids: typeIds.length === 1 ? materialIds : [],
      start_date: startDate,
      end_date: endDate,
      weekday_mask: weekdayMask,
      time_start: timeStart,
      time_end: timeEnd,
      plays_per_day: Number(playsPerDay),
    }
```
> Em multi-tipo (`type_ids` fan-out), `material_ids` vai vazio — materiais pertencem a um tipo só.

- [ ] **Step 4: Sanity de build**

Run: `cd frontend && npm run build`
Expected: build conclui sem erro. (NÃO rode `npm install`.)

- [ ] **Step 5: Smoke manual**

1. `/campaigns/<id>/edit` → Step 5 → "Nova regra".
2. Selecione 1 tipo com ≥1 material → aparece "Materiais específicos".
3. Selecione 1 material, faixa 18:00–19:00, período 1ª semana → salve.
4. Selecione um 2º tipo → o seletor de materiais some e a seleção zera.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/RuleSidePanel.jsx frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): seletor opcional de materiais na regra de distribuição"
```

---

## Task 8: Frontend — chip de regra mostra o escopo + edição

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`

- [ ] **Step 1: `RuleChipList` — rótulo de escopo por material**

No componente `RuleChipList`, dentro do `.map(rule => ...)`, calcule um rótulo de escopo e exiba-o. Adicione, logo após `const name = type?.name ?? 'Tipo'`:
```jsx
        const matCount = Array.isArray(rule.material_ids) ? rule.material_ids.length : 0
        const scopeLabel = matCount === 0 ? 'todos do tipo' : `${matCount} material${matCount !== 1 ? 'is' : ''}`
```
E acrescente um span ao conteúdo do chip (depois do span de horário/plays):
```jsx
            <span style={{ color: 'var(--c-text-3)', fontWeight: 500 }}>· {scopeLabel}</span>
```

- [ ] **Step 2: Garanta que a edição pré-preenche `material_ids`**

`openRuleEditor(rule)` passa a `rule` inteira como `initial`. O `RuleSidePanel` (Task 7, Step 2) já lê `initial?.material_ids`. Confirme visualmente que editar uma regra específica mostra os materiais marcados. Nenhuma mudança de código adicional se o `initial` carrega `material_ids` (carrega — vem do `useDistributionRules`, que reflete o JSON do backend com o campo novo).

- [ ] **Step 3: Sanity de build**

Run: `cd frontend && npm run build`
Expected: sem erro.

- [ ] **Step 4: Smoke manual**

Crie uma regra "todos do tipo" e outra "1 material" → os chips mostram "· todos do tipo" e "· 1 material". Clique no chip da específica → painel reabre com o material marcado.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): chip de regra exibe escopo (todos do tipo vs N materiais)"
```

---

## Task 9: Documentação

**Files:**
- Create: `docs/features/material-specific-distribution-rules.md`
- Modify: `docs/architecture/distribution-rules.md`
- Modify: `docs/features/campaign-wizard.md`
- Modify: `docs/README.md`
- Modify: `CLAUDE.md` (mapa "quando mexer em X, ver Y")

- [ ] **Step 1: Crie a doc da feature**

`docs/features/material-specific-distribution-rules.md` com header YAML obrigatório:
```markdown
---
status: implementado
ultima-verificacao: 2026-06-29
codigo-relacionado:
  - migrations/0043_rule_material_scope.up.sql
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/distribution_rules.go
  - frontend/src/components/RuleSidePanel.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
---

# Regras de distribuição por material específico

Uma `distribution_rule` pode ser escopada a materiais específicos dentro de um
tipo via `material_ids UUID[]` (vazio = todos do tipo, como na migration 0019).

## Carve-out (precedência)

Material nomeado em ≥1 regra específica é julgado **só** por essas regras
(as gerais do tipo deixam de valer pra ele), por emissora:

| Tocada | Categoria |
|---|---|
| No período + dia + faixa da regra dele | `in_slot` 🟢 |
| No período + dia, fora da faixa | `out_slot` 🟡 |
| Fora do período/dia programado dele (dentro da campanha) | `out_date` 🟣 |

Material sem regra específica usa as regras gerais, inalterado (`orphan` para
tocadas sem regra). Override por tipo continua vencendo a célula no dia
([override-time-window](override-time-window.md)).

## Exibição

Nada muda no grid/resumo (emissora × tipo × dia). Só `detections.category` fica
mais fina — desvios do material específico aparecem roxos/amarelos na própria
célula do tipo.

## Onde fica

Step 5 do wizard ([campaign-wizard](campaign-wizard.md)): no painel "Nova regra",
com 1 tipo selecionado aparece o seletor opcional "Materiais específicos".
```

- [ ] **Step 2: Atualize `distribution-rules.md`**

Em `docs/architecture/distribution-rules.md`: na seção "O que é uma regra", adicione `material_ids[]` à estrutura; ajuste `ultima-verificacao: 2026-06-29` e some `migrations/0043_rule_material_scope.up.sql` ao `codigo-relacionado`. Acrescente uma subseção "Carve-out por material" apontando pra [material-specific-distribution-rules.md](../features/material-specific-distribution-rules.md), resumindo a tabela de categorias.

- [ ] **Step 3: Atualize `campaign-wizard.md`**

Na seção "5. Distribuição", acrescente um bullet: "Painel 'Nova regra' com 1 tipo selecionado mostra o seletor opcional **Materiais específicos** — escopa a regra a materiais individuais (carve-out). Ver [material-specific-distribution-rules](material-specific-distribution-rules.md)."

- [ ] **Step 4: Atualize índices**

Em `docs/README.md`: adicione a nova doc ao índice de `features/`. Em `CLAUDE.md`, na tabela "Mapa de consulta", adicione a linha:
```markdown
| Regra de distribuição escopada a materiais específicos (carve-out, `material_ids[]`) | [docs/features/material-specific-distribution-rules.md](docs/features/material-specific-distribution-rules.md) |
```

- [ ] **Step 5: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs(distribution): regras por material específico (feature + índices)"
```

---

## Task 10: Verificação final (regra 6 do CLAUDE.md)

**Files:** nenhum (verificação).

- [ ] **Step 1: Cross-compile linux de TODOS os cmd**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem erros (é o que o `workers.Dockerfile` faz).

- [ ] **Step 2: Suite Go completa**

Run: `cd workers && go test ./...`
Expected: PASS. Pacotes DB-backed pulam sem `TEST_DATABASE_URL` — com ele, devem passar. Falha conhecida não-relacionada: `internal/catalog TestBuildDailySummary_WithDowntime` pode falhar antes de ~13:00 UTC (flaky pré-existente, regra 6.6) — confirme que é esse e não algo que você tocou.

- [ ] **Step 3: Lockfile do frontend intacto (regra 5.4)**

Esta entrega NÃO mexe em `frontend/package.json`. Confirme:
```bash
git diff --name-only master -- frontend/package-lock.json
```
Expected: vazio (lockfile não mudou). Se mudou, reverta — não era pra mudar.

- [ ] **Step 4: Build do frontend**

Run: `cd frontend && npm run build`
Expected: sucesso.

- [ ] **Step 5: Revisão de diff + finalização da branch**

Use a skill `superpowers:finishing-a-development-branch` pra decidir merge/PR. Antes, releia o diff de `recatClassifyTailSQL` confrontando com `categorizer.go` (Task 2) — paridade Go×SQL é o ponto de regressão histórico deste módulo.

---

## Self-Review (preenchido por quem escreveu o plano)

**Cobertura da spec:**
- §3 schema → Task 1. §4 categorizador Go → Task 2. §4 insert → Task 4. §5 recat SQL + escopo → Task 5. §6 view sem mudança → confirmado (nenhuma task altera a view; expected soma regras por tipo automaticamente). §7 override por tipo → preservado (Task 2 mantém o ramo de override antes do carve-out). §8 frontend → Tasks 7-8. §9 API → Tasks 3,6. §10 testes → Tasks 2,4,5. §11 docs → Task 9.
- Item da spec §9 "ListApplicable atualizar" → coberto via `ruleColumns` na Task 3 (ListApplicable usa a constante).

**Consistência de tipos:** `MaterialIDs []uuid.UUID` em `categorizer.Rule` (Task 2) e `catalog.DistributionRule`/`CreateDistributionRuleInput` (Task 3). Assinatura `Categorize(detectedAt, cmp, materialID, rules, override)` usada igual em Task 2 (def), Task 4 (call) e nos testes. JSON `material_ids` consistente entre handler (Task 6), hooks (spread, sem mudança) e front (Tasks 7-8).

**Placeholders:** nenhum TBD/TODO; todo passo que muda código mostra o código.
