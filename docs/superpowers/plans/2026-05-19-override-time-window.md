# Override Time Window — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `time_start`/`time_end` columns to `distribution_overrides` so manual edits on the campaign distribution grid carry a per-cell time range, and update the categorizer to use them so detections on override cells classify as `in_slot`/`out_slot` correctly.

**Architecture:** Schema migration with two-phase backfill (nullable → fill → NOT NULL). Categorizer gains an optional `*Override` param that takes precedence over rules for the cell+day. Frontend extends the existing `OverridePopover` with two `<input type="time">` fields, a multi-rule alert with chips, and a "apply to other cells" checkbox.

**Tech Stack:** Go (pgx, chi), React (hooks), PostgreSQL.

**Spec:** [docs/superpowers/specs/2026-05-19-override-time-window-design.md](../specs/2026-05-19-override-time-window-design.md)

---

## Task 1: Migration `0031_override_time_window`

**Files:**
- Create: `migrations/0031_override_time_window.up.sql`
- Create: `migrations/0031_override_time_window.down.sql`

- [ ] **Step 1.1: Create the up migration**

Create `migrations/0031_override_time_window.up.sql`:

```sql
-- 0031_override_time_window.up.sql
-- Adiciona time_start/time_end em distribution_overrides pra que ajustes
-- manuais no grid carreguem faixa horária. Backfill em duas fases: cria
-- colunas nullable, preenche com a faixa da rule mais antiga da célula+dia
-- (fallback 06:00-22:00 quando não há rule), depois aplica NOT NULL.
-- Spec: docs/superpowers/specs/2026-05-19-override-time-window-design.md

BEGIN;

ALTER TABLE distribution_overrides
    ADD COLUMN time_start TIME,
    ADD COLUMN time_end   TIME;

-- Backfill: faixa da rule mais antiga aplicável à célula+dia.
-- DISTINCT ON garante uma linha por (campaign, type, station, for_date).
-- LEFT JOIN + COALESCE cobre o caso de override sem nenhuma rule.
UPDATE distribution_overrides o
SET time_start = COALESCE(r.time_start, TIME '06:00'),
    time_end   = COALESCE(r.time_end,   TIME '22:00')
FROM (
    SELECT DISTINCT ON (o.campaign_id, o.type_id, o.station_id, o.for_date)
        o.campaign_id, o.type_id, o.station_id, o.for_date,
        r.time_start, r.time_end
    FROM distribution_overrides o
    LEFT JOIN distribution_rules r
        ON r.campaign_id = o.campaign_id
       AND r.type_id     = o.type_id
       AND o.station_id  = ANY(r.station_ids)
       AND o.for_date BETWEEN r.start_date AND r.end_date
       AND (1 << EXTRACT(DOW FROM o.for_date)::INT) & r.weekday_mask != 0
    ORDER BY o.campaign_id, o.type_id, o.station_id, o.for_date,
             r.created_at NULLS LAST, r.id NULLS LAST
) r
WHERE o.campaign_id = r.campaign_id
  AND o.type_id     = r.type_id
  AND o.station_id  = r.station_id
  AND o.for_date    = r.for_date;

-- Salvaguarda: se algum override ainda estiver com faixa NULL (caso não
-- previsto pelo backfill), o ALTER abaixo falha e o transaction inteiro
-- faz rollback. Ou aplica tudo, ou nada.
ALTER TABLE distribution_overrides
    ALTER COLUMN time_start SET NOT NULL,
    ALTER COLUMN time_end   SET NOT NULL,
    ADD CONSTRAINT override_times_valid CHECK (time_end > time_start);

COMMIT;
```

- [ ] **Step 1.2: Create the down migration**

Create `migrations/0031_override_time_window.down.sql`:

```sql
-- 0031_override_time_window.down.sql
BEGIN;

ALTER TABLE distribution_overrides
    DROP CONSTRAINT IF EXISTS override_times_valid,
    DROP COLUMN IF EXISTS time_end,
    DROP COLUMN IF EXISTS time_start;

COMMIT;
```

- [ ] **Step 1.3: Apply migration locally and verify**

Run:
```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U radiocheck -d radiocheck -c "
    SELECT column_name, is_nullable, data_type
    FROM information_schema.columns
    WHERE table_name='distribution_overrides' AND column_name LIKE 'time_%'
    ORDER BY column_name;
  "
```

Expected: two rows, `time_end` and `time_start`, both `NO` for `is_nullable`, both `time without time zone`.

If no migration runner is wired locally, apply via:
```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U radiocheck -d radiocheck -f /migrations/0031_override_time_window.up.sql
```

- [ ] **Step 1.4: Commit**

```bash
git add migrations/0031_override_time_window.up.sql migrations/0031_override_time_window.down.sql
git commit -m "feat(migrations): add time_start/time_end to distribution_overrides

Two-phase backfill: nullable columns → fill from oldest applicable rule
(fallback 06:00-22:00) → NOT NULL with CHECK constraint. Wrapped in
transaction so failure rolls back atomically.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Categorizer — accept optional `*Override`

**Files:**
- Modify: `workers/internal/categorizer/categorizer.go`
- Modify: `workers/internal/categorizer/categorizer_test.go`

- [ ] **Step 2.1: Write failing test for `in_slot` with override**

Add to `workers/internal/categorizer/categorizer_test.go` (before the final `var _ = uuid.UUID{}` line):

```go
func mkOverride(plays int16, ts, te string) *Override {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return &Override{
		PlaysExpected: plays,
		TimeStart:     parseTime(ts),
		TimeEnd:       parseTime(te),
	}
}

func TestCategorize_Override_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Override 14:00–16:00, 2× — detection 14:30 BRT.
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 14, 30, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "in_slot" {
		t.Errorf("got %q, want in_slot (detection in override window)", got)
	}
}
```

- [ ] **Step 2.2: Run test — should fail to compile (Override + 4th param don't exist)**

Run:
```bash
go test ./workers/internal/categorizer/ -run TestCategorize_Override_InSlot
```
Expected: compile error `undefined: Override` and `too many arguments in call to Categorize`.

- [ ] **Step 2.3: Modify `categorizer.go` — add `Override` type and 4th param**

Replace the contents of [workers/internal/categorizer/categorizer.go](../../../workers/internal/categorizer/categorizer.go) with:

```go
package categorizer

import "time"

// Campaign é o subset que o categorizer precisa da campanha.
// StartDate/EndDate são date-only (hora 00:00), idealmente em America/Sao_Paulo.
type Campaign struct {
	StartDate time.Time // date-only, hora 00:00 (inclusivo)
	EndDate   time.Time // date-only (inclusivo)
}

// Rule é o subset que o categorizer precisa de uma distribution_rule.
type Rule struct {
	StartDate   time.Time // date-only
	EndDate     time.Time // date-only
	WeekdayMask int16     // bit 0=Dom, ..., 6=Sáb (matching PostgreSQL EXTRACT(DOW))
	TimeStart   time.Time // só componente HH:MM importa
	TimeEnd     time.Time // só componente HH:MM importa
	PlaysPerDay int16
}

// Override é a entrada do distribution_overrides relevante pra célula
// (campaign, type, station, date) sendo categorizada. Quando passado a
// Categorize, substitui as rules pra essa célula+dia: a faixa do override
// vira a única considerada pra in_slot/out_slot.
type Override struct {
	PlaysExpected int16
	TimeStart     time.Time // só componente HH:MM importa
	TimeEnd       time.Time
}

// Category labels (idênticos aos valores do CHECK constraint em detections.category).
const (
	CatInSlot  = "in_slot"
	CatOutSlot = "out_slot"
	CatOutDate = "out_date"
	CatOrphan  = "orphan"
)

// SlotToleranceSeconds é a folga (15 min) aplicada a cada extremo da faixa de
// horário ao classificar uma detection como in_slot.
const SlotToleranceSeconds = 15 * 60

var spLocation, _ = time.LoadLocation("America/Sao_Paulo")

// Categorize classifica uma detection.
//
// Regra:
//  1. detectedAt fora de [campaign.StartDate, campaign.EndDate] → out_date
//  2. override != nil:
//       - override.PlaysExpected == 0 → out_slot (faixa inerte; ver D5 do spec)
//       - detection ∈ [ts-15min, te+15min] do override → in_slot
//       - caso contrário → out_slot
//     Rules são IGNORADAS quando há override (override REPLACE total — D1).
//  3. override == nil, nenhuma rule aplicável (date+weekday) → orphan
//  4. override == nil, rule aplicável, detection na faixa tolerada → in_slot
//  5. override == nil, rule aplicável, detection fora da faixa → out_slot
func Categorize(detectedAt time.Time, cmp Campaign, rules []Rule, override *Override) string {
	local := detectedAt.In(spLocation)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

	if date.Before(cmp.StartDate) || date.After(cmp.EndDate) {
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
	hasApplicable := false
	for _, r := range rules {
		if date.Before(r.StartDate) || date.After(r.EndDate) {
			continue
		}
		if (1<<dow)&int(r.WeekdayMask) == 0 {
			continue
		}
		hasApplicable = true
		rs := r.TimeStart.Hour()*3600 + r.TimeStart.Minute()*60 + r.TimeStart.Second()
		re := r.TimeEnd.Hour()*3600 + r.TimeEnd.Minute()*60 + r.TimeEnd.Second()
		if timeOfDay >= rs-SlotToleranceSeconds && timeOfDay <= re+SlotToleranceSeconds {
			return CatInSlot
		}
	}
	if hasApplicable {
		return CatOutSlot
	}
	return CatOrphan
}
```

- [ ] **Step 2.4: Update existing tests to pass `nil` as 4th arg**

In `workers/internal/categorizer/categorizer_test.go`, all existing `Categorize(...)` calls need a 4th argument `nil`. Apply this find/replace mentally — each call signature changes from `Categorize(det, cmp, rules)` to `Categorize(det, cmp, rules, nil)`.

Specifically these lines (verify with grep before editing):

- Line ~34: `Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, nil)` → `Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, nil, nil)`
- Line ~45: `Categorize(time.Date(2026, 6, 15, 10, 0, 0, 0, saoPaulo), cmp, nil)` → `..., nil, nil)`
- Line ~61: `Categorize(det, cmp, rules)` → `Categorize(det, cmp, rules, nil)`
- Line ~75: same — `..., rules, nil)`
- Lines ~89, ~93, ~107, ~111, ~125, ~139, ~145: all `Categorize(det, cmp, rules)` calls → add `, nil` as 4th arg

Run grep to find each occurrence:
```bash
grep -n "Categorize(" workers/internal/categorizer/categorizer_test.go
```

For each match, append `, nil` before the closing `)`.

- [ ] **Step 2.5: Run all categorizer tests — should pass**

```bash
go test ./workers/internal/categorizer/ -v
```
Expected: PASS for all tests, including new `TestCategorize_Override_InSlot`.

- [ ] **Step 2.6: Write additional override tests**

Add to `workers/internal/categorizer/categorizer_test.go` (after `TestCategorize_Override_InSlot`):

```go
func TestCategorize_Override_OutSlot_OutsideWindow(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(2, "14:00", "16:00")
	// 09:00 — fora da faixa do override (>>15min folga)
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_Override_IgnoresRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Rule 08:00-10:00 cobriria a detection às 09:00, mas o override
	// (14:00-16:00) deve mandar — rule é IGNORADA.
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot (override should override rule)", got)
	}
}

func TestCategorize_Override_CountZero_AlwaysOutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// count=0 + faixa "14:00-16:00" — faixa é inerte. Qualquer detection
	// no dia → out_slot, mesmo dentro da faixa.
	ov := mkOverride(0, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 15, 0, 0, 0, saoPaulo) // dentro da faixa
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("count=0 in faixa: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_ToleranceBoundary(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// 09:45 — exatamente 15min antes → in_slot
	det := time.Date(2026, 6, 10, 9, 45, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "in_slot" {
		t.Errorf("09:45 override 10:00-12:00: got %q, want in_slot", got)
	}
	// 09:44 — 16min antes → out_slot
	det = time.Date(2026, 6, 10, 9, 44, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("09:44 override 10:00-12:00: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_OutOfDate_StillOutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// Detection em 1º de julho — fora da campanha. out_date manda sobre override.
	det := time.Date(2026, 7, 1, 11, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_date" {
		t.Errorf("got %q, want out_date (campaign window has priority)", got)
	}
}
```

- [ ] **Step 2.7: Run all categorizer tests**

```bash
go test ./workers/internal/categorizer/ -v
```
Expected: PASS for all tests including the 5 new override tests.

- [ ] **Step 2.8: Commit**

```bash
git add workers/internal/categorizer/categorizer.go workers/internal/categorizer/categorizer_test.go
git commit -m "feat(categorizer): accept optional Override that supersedes rules

When an override is present for the (campaign, type, station, date), its
time window is the sole source of truth for in_slot/out_slot. Rules are
ignored for that cell+day. Override count=0 forces out_slot regardless
of detection time (window stored but inert).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Catalog `distribution_overrides` — extend struct + SQL

**Files:**
- Modify: `workers/internal/catalog/distribution_overrides.go`
- Modify: `workers/internal/catalog/distribution_overrides_test.go`

- [ ] **Step 3.1: Write failing test for time window persistence**

Append to `workers/internal/catalog/distribution_overrides_test.go`:

```go
func TestDistributionOverrides_Upsert_WithTimeWindow(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "z2",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM 2", Band: "FM", StreamURL: "http://example2.com",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_overrides WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	repo := NewDistributionOverrides(pool)
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 2,
		TimeStart: "08:00", TimeEnd: "10:00",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	list, _ := repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].TimeStart != "08:00" {
		t.Errorf("TimeStart = %q, want 08:00", list[0].TimeStart)
	}
	if list[0].TimeEnd != "10:00" {
		t.Errorf("TimeEnd = %q, want 10:00", list[0].TimeEnd)
	}

	// Upsert with different window — should overwrite.
	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 3,
		TimeStart: "14:00", TimeEnd: "16:00",
	}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	list, _ = repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if list[0].TimeStart != "14:00" || list[0].TimeEnd != "16:00" {
		t.Errorf("after upsert: TimeStart=%q TimeEnd=%q, want 14:00/16:00",
			list[0].TimeStart, list[0].TimeEnd)
	}
}

func TestDistributionOverrides_CheckConstraint_RejectsInvertedWindow(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "z3",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM 3", Band: "FM", StreamURL: "http://example3.com",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_overrides WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	repo := NewDistributionOverrides(pool)
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 2,
		TimeStart: "16:00", TimeEnd: "14:00", // inverted
	})
	if err == nil {
		t.Fatal("expected CHECK constraint violation, got nil")
	}
}
```

- [ ] **Step 3.2: Run failing tests**

```bash
go test ./workers/internal/catalog/ -run TestDistributionOverrides_Upsert_WithTimeWindow -v
```
Expected: compile error — `UpsertOverrideInput` has no `TimeStart`/`TimeEnd`, `DistributionOverride` has no `TimeStart`/`TimeEnd`.

- [ ] **Step 3.3: Update `distribution_overrides.go`**

Replace the contents of [workers/internal/catalog/distribution_overrides.go](../../../workers/internal/catalog/distribution_overrides.go) with:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DistributionOverride é uma exceção pontual ao "expected" calculado pelas
// rules — uma célula (campaign, type, station, date) com um valor próprio
// E uma faixa horária própria. Migration 0031 adicionou time_start/time_end.
//
// Quando um override existe pra uma célula+dia, o categorizador o trata como
// a única fonte de verdade pra (count, janela de tempo). Rules são ignoradas
// para essa célula+dia.
type DistributionOverride struct {
	CampaignID    uuid.UUID  `json:"campaign_id"`
	TypeID        uuid.UUID  `json:"type_id"`
	StationID     uuid.UUID  `json:"station_id"`
	ForDate       time.Time  `json:"for_date"`
	PlaysExpected int16      `json:"plays_expected"`
	TimeStart     string     `json:"time_start"` // "HH:MM"
	TimeEnd       string     `json:"time_end"`   // "HH:MM"
	Reason        *string    `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
}

type DistributionOverrides struct {
	pool *pgxpool.Pool
}

func NewDistributionOverrides(pool *pgxpool.Pool) *DistributionOverrides {
	return &DistributionOverrides{pool: pool}
}

type UpsertOverrideInput struct {
	CampaignID    uuid.UUID
	TypeID        uuid.UUID
	StationID     uuid.UUID
	ForDate       time.Time
	PlaysExpected int16
	TimeStart     string // "HH:MM" — obrigatório (NOT NULL no banco mesmo quando count=0)
	TimeEnd       string // "HH:MM" — obrigatório
	Reason        *string
	CreatedBy     *uuid.UUID
}

func (do *DistributionOverrides) Upsert(ctx context.Context, in UpsertOverrideInput) error {
	_, err := do.pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date,
		   plays_expected, time_start, time_end, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::time, $7::time, $8, $9)
		ON CONFLICT (campaign_id, type_id, station_id, for_date)
		DO UPDATE SET
		  plays_expected = EXCLUDED.plays_expected,
		  time_start     = EXCLUDED.time_start,
		  time_end       = EXCLUDED.time_end,
		  reason         = EXCLUDED.reason,
		  created_by     = EXCLUDED.created_by`,
		in.CampaignID, in.TypeID, in.StationID, in.ForDate,
		in.PlaysExpected, in.TimeStart, in.TimeEnd, in.Reason, in.CreatedBy)
	return err
}

func (do *DistributionOverrides) Delete(ctx context.Context,
	campaignID, typeID, stationID uuid.UUID, forDate time.Time) error {
	_, err := do.pool.Exec(ctx,
		`DELETE FROM distribution_overrides
		 WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date=$4`,
		campaignID, typeID, stationID, forDate)
	return err
}

func (do *DistributionOverrides) ListByCampaignAndDateRange(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DistributionOverride, error) {
	rows, err := do.pool.Query(ctx, `
		SELECT campaign_id, type_id, station_id, for_date,
		       plays_expected,
		       to_char(time_start, 'HH24:MI') AS time_start,
		       to_char(time_end,   'HH24:MI') AS time_end,
		       reason, created_at, created_by
		FROM distribution_overrides
		WHERE campaign_id = $1 AND for_date BETWEEN $2 AND $3
		ORDER BY for_date ASC`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionOverride
	for rows.Next() {
		var o DistributionOverride
		if err := rows.Scan(&o.CampaignID, &o.TypeID, &o.StationID, &o.ForDate,
			&o.PlaysExpected, &o.TimeStart, &o.TimeEnd,
			&o.Reason, &o.CreatedAt, &o.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3.4: Update existing `TestDistributionOverrides_Upsert` to pass `TimeStart`/`TimeEnd`**

In `workers/internal/catalog/distribution_overrides_test.go`, the original `TestDistributionOverrides_Upsert` test calls `Upsert` without time fields. Update both calls (insert + update) to add `TimeStart: "08:00", TimeEnd: "10:00"`.

Find these two blocks (around lines 39-43 and 47-51) and add the time fields:

Before:
```go
if err := repo.Upsert(ctx, UpsertOverrideInput{
    CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
    ForDate: date, PlaysExpected: 2,
}); err != nil {
```

After:
```go
if err := repo.Upsert(ctx, UpsertOverrideInput{
    CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
    ForDate: date, PlaysExpected: 2,
    TimeStart: "08:00", TimeEnd: "10:00",
}); err != nil {
```

Apply the same change to the second `Upsert` call (PlaysExpected: 5).

- [ ] **Step 3.5: Run catalog tests**

```bash
go test ./workers/internal/catalog/ -run TestDistributionOverrides -v
```
Expected: PASS for all three tests (`Upsert`, `Upsert_WithTimeWindow`, `CheckConstraint_RejectsInvertedWindow`).

- [ ] **Step 3.6: Commit**

```bash
git add workers/internal/catalog/distribution_overrides.go workers/internal/catalog/distribution_overrides_test.go
git commit -m "feat(catalog): persist time_start/time_end in distribution_overrides

Extends DistributionOverride struct, UpsertOverrideInput, and the
Upsert/ListByCampaignAndDateRange SQL. Times exchanged as 'HH:MM' strings
with explicit ::time casts on the Postgres side.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: API handler — accept `time_start`/`time_end` + validation

**Files:**
- Modify: `workers/internal/api/handlers/distribution_overrides.go`

- [ ] **Step 4.1: Update handler payload + validation**

Replace the contents of [workers/internal/api/handlers/distribution_overrides.go](../../../workers/internal/api/handlers/distribution_overrides.go) with:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type DistributionOverridesHandler struct {
	Repo *catalog.DistributionOverrides
}

type overridePayload struct {
	TypeID        uuid.UUID `json:"type_id"`
	StationID     uuid.UUID `json:"station_id"`
	ForDate       string    `json:"for_date"` // YYYY-MM-DD
	PlaysExpected int16     `json:"plays_expected"`
	TimeStart     string    `json:"time_start"` // "HH:MM"
	TimeEnd       string    `json:"time_end"`   // "HH:MM"
	Reason        *string   `json:"reason,omitempty"`
}

// hhmmPattern aceita 00:00 até 23:59. Validação no handler (não no DB)
// pra dar erro 400 amigável antes de bater no Postgres.
var hhmmPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (h *DistributionOverridesHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if !hhmmPattern.MatchString(p.TimeStart) {
		http.Error(w, "time_start must be HH:MM (00:00-23:59)", http.StatusBadRequest)
		return
	}
	if !hhmmPattern.MatchString(p.TimeEnd) {
		http.Error(w, "time_end must be HH:MM (00:00-23:59)", http.StatusBadRequest)
		return
	}
	// String compare é seguro porque o regex força HH:MM com zero-padding.
	if p.TimeEnd <= p.TimeStart {
		http.Error(w, "time_end must be greater than time_start", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Upsert(r.Context(), catalog.UpsertOverrideInput{
		CampaignID: campaignID, TypeID: p.TypeID, StationID: p.StationID,
		ForDate: date, PlaysExpected: p.PlaysExpected,
		TimeStart: p.TimeStart, TimeEnd: p.TimeEnd,
		Reason: p.Reason,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		http.Error(w, "for_date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Delete(r.Context(), campaignID, p.TypeID, p.StationID, date); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) ListByDateRange(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		http.Error(w, "invalid campaignID", http.StatusBadRequest)
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	overrides, err := h.Repo.ListByCampaignAndDateRange(r.Context(), campaignID, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if overrides == nil {
		overrides = []catalog.DistributionOverride{}
	}
	writeJSON(w, http.StatusOK, overrides)
}
```

- [ ] **Step 4.2: Verify build**

```bash
go build ./workers/...
```
Expected: success, no errors.

- [ ] **Step 4.3: Commit**

```bash
git add workers/internal/api/handlers/distribution_overrides.go
git commit -m "feat(api): validate and persist time window on override upsert

PUT /campaigns/:id/distribution-overrides now requires time_start and
time_end as HH:MM strings. Validates format, ensures end > start before
hitting the DB so callers get a useful 400.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: `detections.go` — look up override before categorizing

**Files:**
- Modify: `workers/internal/catalog/detections.go`

- [ ] **Step 5.1: Replace the `categorize` function**

Replace the existing `categorize` function in [workers/internal/catalog/detections.go](../../../workers/internal/catalog/detections.go) (lines ~118-170) with:

```go
// categorize resolves the detection's category by loading the campaign,
// applicable rules, AND any override on (campaign, type, station, date),
// then invoking the pure categorizer.
//
// Migration 0019: rules and overrides are keyed by material TYPE. We look
// up the type of the detected material via JOIN materials.
// Migration 0031: overrides now carry their own time_start/time_end and
// supersede rules for the cell+day when present.
func (d *Detections) categorize(ctx context.Context, in CreateDetectionInput) (string, error) {
	var cmpStart, cmpEnd time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, in.CampaignID,
	).Scan(&cmpStart, &cmpEnd)
	if err != nil {
		return categorizer.CatOrphan, err
	}

	rows, err := d.pool.Query(ctx, `
		SELECT r.start_date, r.end_date, r.weekday_mask,
		       r.time_start::text, r.time_end::text, r.plays_per_day
		FROM distribution_rules r
		WHERE r.campaign_id = $1
		  AND r.type_id = (SELECT type_id FROM materials WHERE id = $2)
		  AND $3 = ANY(r.station_ids)`,
		in.CampaignID, in.CommercialID, in.StationID)
	if err != nil {
		return categorizer.CatOrphan, err
	}
	defer rows.Close()

	var rules []categorizer.Rule
	for rows.Next() {
		var r categorizer.Rule
		var tsStr, teStr string
		var plays int16
		if err := rows.Scan(&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&tsStr, &teStr, &plays); err != nil {
			return categorizer.CatOrphan, err
		}
		r.TimeStart, _ = time.Parse("15:04:05", tsStr)
		r.TimeEnd, _ = time.Parse("15:04:05", teStr)
		r.PlaysPerDay = plays
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return categorizer.CatOrphan, err
	}

	// Override lookup. (campaign, type, station, for_date) é PK em
	// distribution_overrides. for_date é a data local em São Paulo
	// derivada da timestamp da detection.
	var (
		ov     *categorizer.Override
		pe     int16
		tsStr  string
		teStr  string
	)
	err = d.pool.QueryRow(ctx, `
		SELECT plays_expected, time_start::text, time_end::text
		FROM distribution_overrides
		WHERE campaign_id = $1
		  AND type_id = (SELECT type_id FROM materials WHERE id = $2)
		  AND station_id = $3
		  AND for_date = ($4::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`,
		in.CampaignID, in.CommercialID, in.StationID, in.DetectedAt,
	).Scan(&pe, &tsStr, &teStr)
	switch {
	case err == nil:
		ts, _ := time.Parse("15:04:05", tsStr)
		te, _ := time.Parse("15:04:05", teStr)
		ov = &categorizer.Override{PlaysExpected: pe, TimeStart: ts, TimeEnd: te}
	case errors.Is(err, pgx.ErrNoRows):
		// Sem override — ov fica nil, comportamento antigo.
	default:
		return categorizer.CatOrphan, err
	}

	return categorizer.Categorize(
		in.DetectedAt,
		categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd},
		rules,
		ov,
	), nil
}
```

- [ ] **Step 5.2: Add `pgx` import**

At the top of `workers/internal/catalog/detections.go`, the existing imports include `errors` and `github.com/jackc/pgx/v5/pgxpool`. Add `"github.com/jackc/pgx/v5"` to the import block so `pgx.ErrNoRows` resolves.

The final import block should look like:

```go
import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/categorizer"
)
```

- [ ] **Step 5.3: Verify build**

```bash
go build ./workers/...
```
Expected: success.

- [ ] **Step 5.4: Run existing detections tests**

```bash
go test ./workers/internal/catalog/ -run TestDetections -v
```
Expected: PASS — the change is additive (override lookup is a no-op when no override exists, so existing tests behave the same).

- [ ] **Step 5.5: Add an end-to-end categorization test with override**

Append to `workers/internal/catalog/detections_test.go`:

```go
func TestDetections_Create_RespectsOverride(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Cli"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Cmp", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	typeID := seedType(t, ctx, pool, "SpotOv")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Mat", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "ovz",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Ov FM", Band: "FM", StreamURL: "http://example-ov.com",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM distribution_overrides WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	// Rule diz 08:00-10:00 — detection 14:30 normalmente seria out_slot.
	_, err := pool.Exec(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, type_id, station_ids, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, ARRAY[$3]::uuid[], '2026-06-01', '2026-06-30',
		        127, '08:00', '10:00', 1)`,
		cmp.ID, typeID, stat.ID)
	if err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	// Override naquela célula muda a faixa pra 14:00-16:00, 1×.
	_, err = pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date,
		   plays_expected, time_start, time_end)
		VALUES ($1, $2, $3, '2026-06-10', 1, '14:00', '16:00')`,
		cmp.ID, typeID, stat.ID)
	if err != nil {
		t.Fatalf("seed override: %v", err)
	}

	// Detection às 14:30 BRT em 10/06/2026 — dentro da faixa do override → in_slot
	sp, _ := time.LoadLocation("America/Sao_Paulo")
	detAt := time.Date(2026, 6, 10, 14, 30, 0, 0, sp)
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detAt, Confidence: 0.9, HashCount: 10,
	})
	if err != nil {
		t.Fatalf("create detection: %v", err)
	}
	if det.Category != "in_slot" {
		t.Errorf("Category = %q, want in_slot (override window applies)", det.Category)
	}
}
```

- [ ] **Step 5.6: Run the new test**

```bash
go test ./workers/internal/catalog/ -run TestDetections_Create_RespectsOverride -v
```
Expected: PASS.

- [ ] **Step 5.7: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(detections): consult override time window during categorization

Detections.categorize now loads any matching override row alongside the
rules, and hands it to Categorize. When present, the override wins for
the cell+day. Pre-existing tests stay green because the lookup is a
no-op when no override exists.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Frontend hook — propagate `time_start`/`time_end`

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 6.1: Verify hook already forwards extra fields**

The existing `useUpsertOverride` (around line 680) spreads the body:

```js
mutationFn: ({ campaignId, ...body }) =>
  api.put(`/campaigns/${campaignId}/distribution-overrides`, body),
```

Any new fields (`time_start`, `time_end`) passed to `mutateAsync` flow through automatically — no code change needed. **Skip if true**: re-read [frontend/src/api/hooks.js:680-690](../../../frontend/src/api/hooks.js#L680-L690) to confirm.

- [ ] **Step 6.2: Commit (only if anything changed — usually nothing)**

If no changes, skip this commit.

---

## Task 7: `OverridePopover` — time inputs + multi-rule alert + replication checkbox

**Files:**
- Modify: `frontend/src/components/OverridePopover.jsx`

- [ ] **Step 7.1: Replace component**

Replace the entire contents of [frontend/src/components/OverridePopover.jsx](../../../frontend/src/components/OverridePopover.jsx) with:

```jsx
import { useState, useEffect, useRef, useMemo } from 'react'
import { createPortal } from 'react-dom'

/**
 * Inline popover for setting per-cell override (count + time window).
 *
 * Props:
 *  - open: bool
 *  - anchorRect: DOMRect | null
 *  - onClose: () => void
 *  - onApply: (newValue, timeStart, timeEnd, applyToOthers) => void | Promise<void>
 *  - onRevert: () => void
 *  - currentRuleValue: number       — what the rule(s) would produce
 *  - currentOverrideValue?: number | null
 *  - currentRuleWindows: Array<{time_start, time_end}>  — every applicable rule's window
 *  - currentOverrideWindow?: {time_start, time_end} | null
 *  - lastUsedWindow?: {time_start, time_end} | null
 *  - materialTitle: string
 *  - stationName: string
 *  - date: ISO string
 */
export default function OverridePopover({
  open, anchorRect, onClose, onApply, onRevert,
  currentRuleValue, currentOverrideValue = null,
  currentRuleWindows = [],
  currentOverrideWindow = null,
  lastUsedWindow = null,
  materialTitle, stationName, date,
}) {
  // Default window resolution (priority order):
  //   1. existing override → use its window
  //   2. exactly 1 applicable rule → use its window
  //   3. multiple rules → no default, force the user to pick
  //   4. no rules + lastUsedWindow → use last
  //   5. fallback 06:00–22:00
  const defaultWindow = useMemo(() => {
    if (currentOverrideWindow) return currentOverrideWindow
    if (currentRuleWindows.length === 1) return currentRuleWindows[0]
    if (currentRuleWindows.length > 1)   return null // force choice via chip
    if (lastUsedWindow)                  return lastUsedWindow
    return { time_start: '06:00', time_end: '22:00' }
  }, [currentOverrideWindow, currentRuleWindows, lastUsedWindow])

  const [value, setValue] = useState(currentOverrideValue ?? currentRuleValue ?? 0)
  const [timeStart, setTimeStart] = useState(defaultWindow?.time_start ?? '')
  const [timeEnd,   setTimeEnd]   = useState(defaultWindow?.time_end   ?? '')
  const [applyToOthers, setApplyToOthers] = useState(false)
  const ref = useRef(null)

  // Reset state when popover re-opens or context changes.
  useEffect(() => {
    setValue(currentOverrideValue ?? currentRuleValue ?? 0)
    setTimeStart(defaultWindow?.time_start ?? '')
    setTimeEnd(defaultWindow?.time_end ?? '')
    setApplyToOthers(false)
  }, [currentOverrideValue, currentRuleValue, defaultWindow, open])

  // Close on outside click / Esc.
  useEffect(() => {
    if (!open) return
    function onMouseDown(e) {
      if (ref.current && !ref.current.contains(e.target)) onClose()
    }
    function onKey(e) { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, onClose])

  if (!open || !anchorRect) return null

  const top  = anchorRect.top  + window.scrollY - 280
  const left = Math.max(8, anchorRect.left + window.scrollX + anchorRect.width / 2 - 150)
  const dateStr = date ? date.slice(0, 10).split('-').reverse().join('/') : '—'
  const multiRule = currentRuleWindows.length > 1 && !currentOverrideWindow

  // Validation: HH:MM, time_end > time_start.
  const validRange = /^[0-2]\d:[0-5]\d$/.test(timeStart)
                  && /^[0-2]\d:[0-5]\d$/.test(timeEnd)
                  && timeEnd > timeStart
  const canApply = validRange && (!multiRule || (timeStart && timeEnd))

  return createPortal(
    <div ref={ref} style={{
      position: 'absolute', top, left, width: 300, zIndex: 60,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
      padding: '14px 16px',
      boxShadow: '0 10px 25px -5px rgba(15,23,42,0.18), 0 4px 10px -4px rgba(15,23,42,0.08)',
      fontSize: 12,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8, color: '#64748b' }}>
        <span>{stationName} · {materialTitle}</span>
        <strong style={{ color: '#0f172a' }}>{dateStr}</strong>
      </div>

      {/* Contexto: o que as rules dizem */}
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: 8, background: '#fafbfc', borderRadius: 6, marginBottom: 10,
      }}>
        <span style={{ color: '#64748b' }}>Regra: {currentRuleValue}×/dia</span>
      </div>

      {/* Alerta multi-rule + chips */}
      {multiRule && (
        <div style={{
          padding: 8, marginBottom: 10, background: '#fef3c7',
          border: '1px solid #fcd34d', borderRadius: 6, color: '#78350f',
        }}>
          <div style={{ marginBottom: 6, fontWeight: 600 }}>
            ⚠ Esta célula tem {currentRuleWindows.length} regras:
          </div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 6 }}>
            {currentRuleWindows.map((w, i) => (
              <button
                key={i}
                type="button"
                onClick={() => { setTimeStart(w.time_start); setTimeEnd(w.time_end) }}
                style={{
                  padding: '3px 8px', borderRadius: 999,
                  border: '1px solid #fcd34d', background: '#fff',
                  cursor: 'pointer', fontSize: 11, color: '#78350f',
                }}
              >
                {w.time_start}–{w.time_end}
              </button>
            ))}
          </div>
          <div style={{ fontSize: 11 }}>
            Definir override substituirá ambas neste dia.
          </div>
        </div>
      )}

      {/* Count stepper */}
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: '#475569' }}>Veiculações:</span>
        <button onClick={() => setValue(v => Math.max(0, v - 1))} style={stepperBtn}>−</button>
        <input type="number" min="0" max="100" value={value}
          onChange={e => setValue(Number(e.target.value))}
          style={{
            width: 56, padding: '5px 8px', border: '1px solid #e2e8f0',
            borderRadius: 6, textAlign: 'center', fontWeight: 700, color: '#b45309',
            background: '#fef9c3', fontFamily: 'inherit',
          }} />
        <button onClick={() => setValue(v => Math.min(100, v + 1))} style={stepperBtn}>+</button>
      </div>

      {/* Time window inputs (disabled when count=0) */}
      <div style={{ marginBottom: 10, opacity: value === 0 ? 0.5 : 1 }}>
        <div style={{ fontSize: 11, color: '#475569', marginBottom: 4 }}>
          {value === 0
            ? 'Faixa horária (inativa quando 0 veiculações):'
            : 'Faixa horária:'}
        </div>
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <input type="time" value={timeStart} onChange={e => setTimeStart(e.target.value)}
                 disabled={value === 0} style={timeInputStyle} />
          <span style={{ color: '#64748b' }}>–</span>
          <input type="time" value={timeEnd} onChange={e => setTimeEnd(e.target.value)}
                 disabled={value === 0} style={timeInputStyle} />
        </div>
        {!validRange && timeStart && timeEnd && (
          <div style={{ fontSize: 11, color: '#b91c1c', marginTop: 4 }}>
            Faixa inválida: fim deve ser maior que início.
          </div>
        )}
      </div>

      {/* Replication checkbox */}
      <label style={{
        display: 'flex', gap: 6, alignItems: 'flex-start',
        marginBottom: 12, cursor: 'pointer', fontSize: 11, color: '#475569',
      }}>
        <input type="checkbox" checked={applyToOthers}
               onChange={e => setApplyToOthers(e.target.checked)}
               style={{ marginTop: 2 }} />
        <span>
          Aplicar essa mesma faixa nas demais células deste tipo neste mês
          <div style={{ fontSize: 10, color: '#94a3b8', marginTop: 2 }}>
            (afeta apenas células que já têm override)
          </div>
        </span>
      </label>

      <div style={{ display: 'flex', gap: 6 }}>
        {currentOverrideValue != null && (
          <button onClick={onRevert} style={{
            flex: 1, padding: 6, borderRadius: 6, fontSize: 11, fontWeight: 600,
            background: '#fafbfc', color: '#475569', border: '1px solid #e2e8f0', cursor: 'pointer',
          }}>↺ Voltar à regra</button>
        )}
        <button
          onClick={() => onApply(value, timeStart, timeEnd, applyToOthers)}
          disabled={!canApply}
          className="btn btn-primary btn-sm"
          style={{ flex: 1, opacity: canApply ? 1 : 0.5, cursor: canApply ? 'pointer' : 'not-allowed' }}
        >
          Aplicar
        </button>
      </div>
    </div>,
    document.body
  )
}

const stepperBtn = {
  width: 24, height: 24, borderRadius: 6, border: '1px solid #e2e8f0',
  background: '#fff', fontWeight: 700, cursor: 'pointer', color: '#475569',
}

const timeInputStyle = {
  flex: 1, padding: '5px 8px', border: '1px solid #e2e8f0',
  borderRadius: 6, fontFamily: 'inherit', fontSize: 12, color: '#0f172a',
}
```

- [ ] **Step 7.2: Manual smoke test in browser**

Start the dev server and load `/campaigns/<id>/edit` for any campaign with materials linked to stations. Click any cell in the distribution grid. Verify:

- Popover opens with time inputs visible.
- If the cell has exactly 1 rule, the time inputs are pre-filled with that rule's window.
- If the cell has 2+ rules, a yellow warning appears with clickable chips for each rule's window.
- Clicking a chip fills the inputs.
- Typing an invalid range (end ≤ start) disables Aplicar with an inline error.
- Setting Veiculações to 0 fades and disables the time inputs.

- [ ] **Step 7.3: Commit**

```bash
git add frontend/src/components/OverridePopover.jsx
git commit -m "feat(frontend): time window controls in override popover

OverridePopover now collects time_start/time_end alongside the play count,
with smart defaults (existing override → single rule → multi-rule chips →
last used → 06:00-22:00). Adds a replication checkbox that pushes the
chosen window to every other override of the same type in the visible
month. Time inputs gracefully disable when count=0.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: `DistributionStep` — wire windows, multi-rule context, replication, inline +/-

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`

- [ ] **Step 8.1: Add `lastUsedWindow` state and compute `currentRuleWindows`**

Open [frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx](../../../frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx).

After the existing `useState` declarations (around line 54-55 — after `pendingDrafts`/`committing`), add:

```jsx
const [lastUsedWindow, setLastUsedWindow] = useState(null)
```

After the `cellDataWithDrafts` `useMemo` block (around line 132), add a helper that computes applicable rule windows for an arbitrary (stationId, typeId, dateISO) cell:

```jsx
// Computa todas as faixas horárias de rules aplicáveis a uma célula
// específica. Usado pra alimentar o popover com herança inteligente
// (decisão D2 + D4 do spec).
function ruleWindowsForCell(stationId, typeId, dateISO) {
  const d = new Date(dateISO + 'T12:00:00') // meio-dia local pra evitar quirks
  const dowBit = 1 << d.getDay()
  return rules
    .filter(r =>
      r.type_id === typeId &&
      r.station_ids.includes(stationId) &&
      dateISO >= r.start_date.slice(0,10) &&
      dateISO <= r.end_date.slice(0,10) &&
      (r.weekday_mask & dowBit) !== 0
    )
    .map(r => ({
      time_start: String(r.time_start).slice(0,5),
      time_end:   String(r.time_end).slice(0,5),
    }))
}
```

- [ ] **Step 8.2: Update inline +/- handlers to open popover when cell is "clean"**

Replace the `stageCellChange` function (around line 134-144) with a more aware version. Add after it a helper that decides whether to stage or open popover:

```jsx
function stageCellChange(stationId, typeId, dateISO, delta) {
  const key = `${stationId}|${typeId}|${dateISO}`
  setPendingDrafts(prev => {
    const next = new Map(prev)
    const current = next.has(key)
      ? next.get(key)
      : (cellData.get(key)?.expected ?? 0)
    next.set(key, Math.max(0, current + delta))
    return next
  })
}

// Decide se um clique de +/- na célula deve stagear direto OU forçar o
// popover. Regra (decisão D3): só staga se a célula já tem rule única OU
// já tem override. Multi-rule sem override → popover. Sem rule e sem
// override → popover.
function handleInlineStep(stationId, typeId, dateISO, delta, anchorRect) {
  const windows = ruleWindowsForCell(stationId, typeId, dateISO)
  const hasOverride = overrides.some(o =>
    o.station_id === stationId && o.type_id === typeId &&
    o.for_date.slice(0,10) === dateISO)

  // Caminho rápido: 1 rule OR override existente — staga sem popover.
  if (hasOverride || windows.length === 1) {
    stageCellChange(stationId, typeId, dateISO, delta)
    return
  }
  // Caso ambíguo: abre popover. Pré-incrementa o valor inicial pela delta.
  setPopoverAnchor(anchorRect)
  setPopoverContext({ stationId, typeId, date: dateISO, initialDelta: delta })
}
```

- [ ] **Step 8.3: Wire the new handler into the grid + adapt `handleCellClick`**

Find the `<DistributionGrid ... />` block (around line 331). Replace the `onCellIncrement`/`onCellDecrement` lines so they pass the anchor rect too:

```jsx
<DistributionGrid
  mode="edit"
  month={month}
  campaignStart={campaignStart}
  campaignEnd={campaignEnd}
  stations={allStations}
  rows={rows}
  cellData={cellDataWithDrafts}
  onCellClick={handleCellClick}
  onCellIncrement={(stationId, typeId, dateISO, rect) =>
    handleInlineStep(stationId, typeId, dateISO, +1, rect)}
  onCellDecrement={(stationId, typeId, dateISO, rect) =>
    handleInlineStep(stationId, typeId, dateISO, -1, rect)}
  capAtToday={false}
/>
```

`handleCellClick` stays as-is for now — just receives the anchor & context.

- [ ] **Step 8.4: Compute popover context and pass new props**

Find the block defining `cellInfo`, `matchingOverride`, etc. (around line 213-218). Add:

```jsx
const ctxWindows = ctx ? ruleWindowsForCell(ctx.stationId, ctx.typeId, ctx.date) : []
const ctxOverrideWindow = matchingOverride
  ? { time_start: String(matchingOverride.time_start).slice(0,5),
      time_end:   String(matchingOverride.time_end).slice(0,5) }
  : null
```

Then update the `<OverridePopover ... />` block (around line 362) to pass the new props and the new `onApply` signature:

```jsx
<OverridePopover
  open={!!popoverAnchor && !!ctx}
  anchorRect={popoverAnchor}
  onClose={() => { setPopoverAnchor(null); setPopoverContext(null) }}
  onApply={async (newValue, newTimeStart, newTimeEnd, applyToOthers) => {
    await upsertOverride.mutateAsync({
      campaignId,
      type_id:    ctx.typeId,
      station_id: ctx.stationId,
      for_date:   ctx.date,
      plays_expected: newValue,
      time_start: newTimeStart,
      time_end:   newTimeEnd,
    })
    setLastUsedWindow({ time_start: newTimeStart, time_end: newTimeEnd })

    if (applyToOthers) {
      const others = overrides.filter(o =>
        o.type_id === ctx.typeId &&
        !(o.station_id === ctx.stationId && o.for_date.slice(0,10) === ctx.date)
      )
      await Promise.all(others.map(o => upsertOverride.mutateAsync({
        campaignId,
        type_id:    o.type_id,
        station_id: o.station_id,
        for_date:   o.for_date.slice(0,10),
        plays_expected: o.plays_expected,
        time_start: newTimeStart,
        time_end:   newTimeEnd,
      })))
    }
    setPopoverAnchor(null); setPopoverContext(null)
  }}
  onRevert={async () => {
    await deleteOverride.mutateAsync({
      campaignId,
      type_id:    ctx.typeId,
      station_id: ctx.stationId,
      for_date:   ctx.date,
    })
    setPopoverAnchor(null); setPopoverContext(null)
  }}
  currentRuleValue={cellInfo?.expected ?? 0}
  currentOverrideValue={matchingOverride?.plays_expected ?? null}
  currentRuleWindows={ctxWindows}
  currentOverrideWindow={ctxOverrideWindow}
  lastUsedWindow={lastUsedWindow}
  materialTitle={rows.find(r => r.materialId === ctx?.typeId)?.materialTitle ?? '—'}
  stationName={allStations.find(s => s.id === ctx?.stationId)?.name ?? '—'}
  date={ctx?.date}
/>
```

- [ ] **Step 8.5: Update `commitDrafts` to carry time window with each upsert**

Find `commitDrafts` (around line 146-176). The staged drafts now must derive a window from the cell context. Replace the function:

```jsx
async function commitDrafts() {
  if (pendingDrafts.size === 0 || committing) return
  setCommitting(true)
  const snapshot = [...pendingDrafts.entries()]
  for (const [key, value] of snapshot) {
    const [stationId, typeId, dateISO] = key.split('|')
    // Deriva faixa: override existente → rule única → bloqueia draft.
    // (handleInlineStep impede a entrada da draft no caso ambíguo, então
    // se chegou aqui há uma fonte clara — mas mantemos defensivo.)
    const existingOv = overrides.find(o =>
      o.station_id === stationId && o.type_id === typeId &&
      o.for_date.slice(0,10) === dateISO)
    const windows = ruleWindowsForCell(stationId, typeId, dateISO)
    // Importante: NÃO usar `window` como nome de variável aqui — sombrearia
    // o global `window` do browser e quebraria o `window.alert` abaixo.
    const draftWindow = existingOv
      ? { time_start: String(existingOv.time_start).slice(0,5),
          time_end:   String(existingOv.time_end).slice(0,5) }
      : (windows.length === 1 ? windows[0] : null)
    if (!draftWindow) {
      // Defensivo: pula células ambíguas. Não deveria acontecer dado o
      // gating em handleInlineStep, mas mantém safety net.
      continue
    }
    try {
      await upsertOverride.mutateAsync({
        campaignId,
        type_id: typeId,
        station_id: stationId,
        for_date: dateISO,
        plays_expected: value,
        time_start: draftWindow.time_start,
        time_end:   draftWindow.time_end,
      })
      setPendingDrafts(prev => {
        if (prev.get(key) !== value) return prev
        const next = new Map(prev)
        next.delete(key)
        return next
      })
    } catch {
      setCommitting(false)
      window.alert(`Erro ao salvar alterações em ${dateISO}. Tente novamente.`)
      return
    }
  }
  setCommitting(false)
}
```

- [ ] **Step 8.6: Smoke test in browser**

Reload `/campaigns/<id>/edit`. Verify:

- Click `+` on a cell that has a single rule → count increments, pending draft appears.
- Click `Confirmar` → upsert sent with the rule's window; cell shows expected updated.
- Click `+` on a cell with no rule AND no override → popover opens with `06:00–22:00` pre-filled.
- Click `+` on a cell with multiple rules → popover opens, warning + chips visible.
- Apply with "Aplicar essa mesma faixa nas demais células deste tipo neste mês" checked → other overrides of same type in month update to the new window (verify via DB or re-open another cell's popover).

- [ ] **Step 8.7: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(frontend): wire override time windows into distribution step

DistributionStep computes per-cell rule windows on the fly, gates inline
+/- (single rule or existing override → stage draft; otherwise → popover),
threads the window through to upsert calls (draft commit + popover apply),
and orchestrates the 'apply to other cells' replication that updates every
override of the same type in the visible month.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Verify `DistributionGrid` forwards anchor rect to +/- handlers

**Files:**
- Modify (only if needed): `frontend/src/components/DistributionGrid.jsx`
- Modify (only if needed): `frontend/src/components/DayCell.jsx`

- [ ] **Step 9.1: Inspect current signatures**

Run:
```bash
grep -n "onCellIncrement\|onCellDecrement" frontend/src/components/DistributionGrid.jsx frontend/src/components/DayCell.jsx
```

If the current handlers do NOT pass the anchor rect (3rd/4th arg), wrap the click handler on the +/- buttons in `DayCell` to capture the event's `currentTarget.getBoundingClientRect()` and forward it.

Concretely, the typical adjustment in `DayCell.jsx` is:

```jsx
<button
  onClick={e => onIncrement(stationId, typeId, dateISO,
    e.currentTarget.closest('.day-cell').getBoundingClientRect())}
  ...
>+</button>
```

If `DayCell` already passes a rect (some implementations do), no change needed.

- [ ] **Step 9.2: Manual smoke test**

If you changed `DayCell` or grid props, reload the page and verify the popover opens correctly anchored when triggered by inline +/- on a "clean" cell (no rule, no override).

- [ ] **Step 9.3: Commit (only if changed)**

```bash
git add frontend/src/components/DayCell.jsx frontend/src/components/DistributionGrid.jsx
git commit -m "fix(frontend): forward cell bounding rect to inline +/- handlers

Needed so DistributionStep can anchor the override popover when an inline
+/- click falls into the ambiguous branch (multi-rule or empty cell).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Documentation in `docs/features/`

**Files:**
- Create: `docs/features/override-time-window.md`
- Modify: `docs/README.md`
- Modify: `CLAUDE.md` (add row to the "Mapa de consulta" table)

- [ ] **Step 10.1: Create the feature doc**

Create `docs/features/override-time-window.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - migrations/0031_override_time_window.up.sql
  - workers/internal/catalog/distribution_overrides.go
  - workers/internal/api/handlers/distribution_overrides.go
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/detections.go
  - frontend/src/components/OverridePopover.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
---

# Faixa horária em overrides de distribuição

## O que faz

No step "Distribuição" do wizard de campanha (`/campaigns/:id/edit`),
o operador pode ajustar manualmente o número de veiculações esperadas
em qualquer célula `(emissora, tipo, dia)` clicando na célula ou usando
os botões `+`/`−`. A partir desta entrega, cada ajuste **também carrega
uma faixa horária**, gravada em `distribution_overrides.time_start` e
`time_end`.

## Por que existe

Sem a faixa horária no override, o categorizador classificava como
`orphan` toda detection numa célula que tinha override mas nenhuma
rule — não havia janela de tempo conhecida. E mesmo em células com
rule + override, o "+1 manual" usava a faixa da rule por arrasto, sem
flexibilidade pro operador dizer "a inserção extra vai às 19h".

## Comportamento

### Default da faixa no popover

Ao abrir o `OverridePopover` numa célula, o campo de faixa vem
pré-preenchido (primeira condição que casar):

1. Override existente → faixa do override.
2. Apenas 1 rule aplicável → faixa dessa rule.
3. Mais de 1 rule aplicável → **sem pré-preenchimento**, alerta amarelo
   com chips clicáveis pra cada faixa existente.
4. Nenhuma rule, mas há `lastUsedWindow` (última faixa usada na sessão)
   → faixa anterior.
5. Fallback: `06:00–22:00`.

### Inline +/-

Os botões `+`/`−` na célula só fazem staging direto quando a célula
tem ou (a) uma única rule aplicável, ou (b) um override existente. Em
células ambíguas (multi-rule sem override) ou "limpas" (sem rule e sem
override), o clique abre o popover pra forçar escolha explícita de
faixa.

### count=0

Override `plays_expected=0` mantém faixa NOT NULL no banco (decisão de
schema), mas o categorizador trata o caso como "qualquer detection
naquele dia → `out_slot`", ignorando a faixa. O frontend desabilita os
inputs de faixa quando count=0 pra deixar claro que ela é inerte.

### Replicação

Checkbox no popover: "Aplicar essa mesma faixa nas demais células deste
tipo neste mês". Quando marcado, ao aplicar, a faixa é replicada em
**todos os overrides existentes** do mesmo `type_id` no mês visível,
preservando o `plays_expected` de cada um. **Não cria override novo**
em células limpas.

### Categorizador

Quando há override numa célula+dia, o categorizador **ignora as rules
daquela célula naquele dia** — a faixa do override é a única
considerada pra decidir `in_slot`/`out_slot`. Mantém a tolerância
existente de 15min em cada extremo.

## Fora de escopo

- Múltiplas faixas por célula (override com lista de slots) — escolha
  consciente, modelo "uma faixa por célula" (override REPLACE).
- Recategorização retroativa de detections já gravadas — mudança de
  override afeta apenas detections **futuras**. Mesmo princípio das rules.
- Modo "pintar" / seleção múltipla de células.

## Como testar manualmente

1. Abre uma campanha existente em `/campaigns/<id>/edit`, vai pro
   step "Distribuição".
2. Cria uma rule "Spot 30s, 08–10h, 2×/dia, seg-sex, todas as estações".
3. Clica numa célula da grid pra abrir o popover. A faixa vem
   pré-preenchida 08:00–10:00.
4. Muda pra 14:00–16:00, mantém count=3, aplica.
5. Cria outra rule "Spot 30s, 12–14h, 1×/dia" na mesma estação.
6. Clica numa célula ainda sem override — popover mostra alerta com 2
   chips (08–10h e 12–14h).
7. Marca o checkbox de replicação, aplica → todos os overrides
   existentes do tipo no mês mudam pra mesma faixa.

## Migration

`migrations/0031_override_time_window` — adiciona colunas em duas
fases (nullable → backfill → NOT NULL). Backfill usa a faixa da rule
mais antiga aplicável à célula+dia; fallback `06:00–22:00` quando
não há rule. Tudo em transaction — risco de perda de dado: zero.
```

- [ ] **Step 10.2: Add entry to `docs/README.md`**

Find the section listing feature docs in `docs/README.md` and add (in alphabetical-ish order, near `material-similarity-warning.md`):

```markdown
- [override-time-window](features/override-time-window.md) — faixa horária em overrides de distribuição manual no grid da campanha
```

If the README is organized differently, place it where similar feature docs live.

- [ ] **Step 10.3: Add row to `CLAUDE.md` "Mapa de consulta" table**

In `CLAUDE.md`, find the "Mapa de consulta" table (under the section "Documentação em `/docs`"). Add this row right after the row referencing `material-similarity-warning.md`:

```markdown
| Faixa horária em overrides (popover do grid, categorizador) | [docs/features/override-time-window.md](docs/features/override-time-window.md) |
```

- [ ] **Step 10.4: Commit**

```bash
git add docs/features/override-time-window.md docs/README.md CLAUDE.md
git commit -m "docs: feature page for override time window

Documents the per-cell time range now carried by distribution_overrides,
the popover behavior with rule inheritance, the replication checkbox,
and the categorizer's override-supersedes-rules semantic.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Full verification

- [ ] **Step 11.1: Run all backend tests**

```bash
go test ./workers/... -v
```
Expected: PASS for all packages.

- [ ] **Step 11.2: Build the frontend**

```bash
cd frontend && npm run build && cd ..
```
Expected: success, no type errors.

- [ ] **Step 11.3: Manual smoke against full stack**

With the stack up locally:

1. Create a campaign with at least one material, type, and one station targeted.
2. Add a distribution rule covering the campaign window.
3. Distribution step:
   - Open a cell's popover → verify pre-filled window.
   - Change the window to a different range → apply.
   - Confirm via the popover that the override persisted (re-open the cell).
   - Use the replication checkbox on a second cell — verify other overrides got the same window via the popover of a third cell or via SQL: `SELECT for_date, time_start, time_end FROM distribution_overrides WHERE campaign_id='...' ORDER BY for_date;`
4. Simulate a detection at a time inside the override window (via worker test mode or direct DB insert with a fake `detections` row through the API) and verify `category = 'in_slot'`.

- [ ] **Step 11.4: Final commit (if any tweaks needed during smoke)**

If smoke surfaced small fixes, commit them with a focused message. Otherwise, skip.

---

## Done criteria

- [ ] Migration applied locally and on a copy of prod data, idempotent on rerun (via down then up).
- [ ] All Go tests pass (`go test ./workers/...`).
- [ ] Frontend builds clean.
- [ ] Manual smoke covered: single-rule cell, multi-rule cell, empty cell, count=0, replication checkbox.
- [ ] Feature doc created and linked from `CLAUDE.md` + `docs/README.md`.
- [ ] No `plano_implementacao.md` changes (operational docs go to `/docs`).
