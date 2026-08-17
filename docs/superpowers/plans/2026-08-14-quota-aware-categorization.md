# Categorização por cota (quota-aware) — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** trocar a categorização por-detecção-sem-estado por um fechamento por célula-dia
com cota, de modo que `in_slot`/`out_slot`/`bonus`/`out_date` passem a significar a mesma
coisa no sistema inteiro.

**Architecture:** o categorizador deixa de classificar uma tocada e passa a *fechar* uma
célula-dia (campanha × tipo × emissora × dia): as tocadas dentro da faixa preenchem a meta
N do dia, o excedente vira `bonus`, as de fora da faixa viram `out_slot` enquanto a meta não
tiver fechado dentro da faixa, e `deficit = max(0, N − in_slot)`. O mesmo fechamento existe
em dois motores que precisam concordar bit a bit — Go (insert-path) e SQL (recat/reconciler)
— com teste de paridade entre eles, e é espelhado no frontend pelo `buildDayPlan`.

**Tech Stack:** Go 1.26, PostgreSQL 16 particionado, pgx/v5, golang-migrate, React 19 + Vite.

**Spec:** [docs/superpowers/specs/2026-08-14-quota-aware-categorization-design.md](../specs/2026-08-14-quota-aware-categorization-design.md)

---

## Pré-requisitos

- DB de teste de integração: PG descartável `rc-test-pg` em `localhost:15432`, database
  **`radiocheck_test`** (vazio — o guard bloqueia DB com dado). Rode um pacote por vez
  (`go test -p 1 ./internal/catalog/...`); concorrência no mesmo DB gera deadlock que
  parece regressão.
- `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` tem que passar antes de qualquer
  push (regra 6.1 do CLAUDE.md).
- Nenhuma migration deste plano pode ir pra prod sem o `shadow_migration_test` do
  `deploy.sh` passar (regra 4.8).

---

## File Structure

| Arquivo | Responsabilidade |
|---|---|
| `workers/internal/categorizer/categorizer.go` | **Modificar.** `Categorize` (1 tocada) → `Settle` (célula-dia). Fonte da verdade da regra. |
| `workers/internal/categorizer/settle_test.go` | **Criar.** Tabela-verdade da spec, sem DB. |
| `migrations/0063_category_bonus.{up,down}.sql` | **Criar.** Categoria `bonus` no CHECK + `UPDATE orphan → bonus`. |
| `migrations/0064_quota_aware_summary.{up,down}.sql` | **Criar.** `deficit = expected − in_slot`; `bonus` = contagem da categoria. |
| `workers/internal/catalog/detections.go` | **Modificar.** Insert-path fecha a célula-dia na mesma tx. |
| `workers/internal/catalog/distribution_rules.go` | **Modificar.** `recatClassifiedCTE` reescrita + expansão de escopo. |
| `workers/internal/catalog/settle_parity_test.go` | **Criar.** Paridade Go × SQL sobre a mesma tabela-verdade. |
| `workers/internal/catalog/insights.go` | **Modificar.** `executado = in_slot`; breakdown lê `bonus`. |
| `workers/internal/catalog/campaign_failures.go` | **Modificar.** Déficit novo + split `absent`/`off_slot`. |
| `frontend/src/components/DayDetailModal.jsx` | **Modificar.** `buildDayPlan` espelha o `Settle`. |
| `frontend/src/pages/AdminStationFailures.jsx` | **Modificar.** Colunas separadas de falha. |
| `workers/cmd/backfill-recategorize/main.go` | **Modificar.** Relatório de delta por categoria (dry-run). |
| `docs/architecture/distribution-rules.md` | **Modificar.** Regra canônica nova. |

---

## Task 1: `Settle` — o fechamento da célula-dia em Go

> **Correções aplicadas durante a execução (2026-08-14) — Tasks 4 e 5 DEVEM seguir estas:**
> 1. O helper chamado aqui de `matchesDateWeekday` colidia em nome com uma closure interna
>    do `Categorize`. Nome final no código: **`ruleCoversDay`**. Semântica idêntica
>    (`for_date BETWEEN start_date AND end_date` + máscara de dia-da-semana).
> 2. `TestSettle_Override_WindowSupersedesRules` estava **errado como escrito abaixo**:
>    com override `N=1` e a tocada das 14:30 preenchendo a meta, a das 09:00 vira `bonus`
>    pelo passo 4, não `out_slot` — o próprio modelo. Corrigido subindo a meta do override
>    pra `2`, o que mantém as duas asserções e ainda discrimina (se o motor usasse a faixa
>    da regra, os dois papéis invertem e as duas asserções falham). **O SQL da Task 4 tem
>    que devolver `bonus` nesse cenário também.**
> 3. Adicionado `TestSettle_TwoWindows_QuotaIsDaily_NotPerWindow`, que cobre a linha
>    "3 (2 faixas)" da tabela-verdade — a única que prova as duas metades de D1 (N é a
>    soma das regras do dia; não há cota por faixa).

**Files:**
- Modify: `workers/internal/categorizer/categorizer.go`
- Test: `workers/internal/categorizer/settle_test.go` (criar)

- [ ] **Step 1: Escrever o teste da tabela-verdade (falhando)**

Criar `workers/internal/categorizer/settle_test.go`:

```go
package categorizer

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// day6 é 10/06/2026 (quarta-feira) à meia-noite SP — o dia de todas as células
// destes testes.
func day6() time.Time { return time.Date(2026, 6, 10, 0, 0, 0, 0, saoPaulo) }

func at(h, m int) time.Time { return time.Date(2026, 6, 10, h, m, 0, 0, saoPaulo) }

func plays(ts ...time.Time) []Play {
	out := make([]Play, len(ts))
	for i, t := range ts {
		out[i] = Play{DetectedAt: t, MaterialID: uuid.Nil}
	}
	return out
}

// Tabela-verdade da spec 2026-08-14 §2. Faixa 10:00–12:00 em todos os casos.
func TestSettle_TruthTable(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 2)} // N=2, todo dia

	cases := []struct {
		name  string
		plays []Play
		want  []string
	}{
		{
			name:  "1 dentro 1 fora: a de fora segura o saldo",
			plays: plays(at(10, 30), at(3, 0)),
			want:  []string{CatInSlot, CatOutSlot},
		},
		{
			name:  "2 dentro 1 fora: meta fechada dentro da faixa, a de fora e bonus",
			plays: plays(at(10, 30), at(11, 0), at(3, 0)),
			want:  []string{CatInSlot, CatInSlot, CatBonus},
		},
		{
			name:  "0 dentro 3 fora: nenhuma bonificacao, tudo out_slot",
			plays: plays(at(3, 0), at(4, 0), at(5, 0)),
			want:  []string{CatOutSlot, CatOutSlot, CatOutSlot},
		},
		{
			name:  "4 dentro: as 2 excedentes viram bonus",
			plays: plays(at(10, 10), at(10, 20), at(10, 30), at(10, 40)),
			want:  []string{CatInSlot, CatInSlot, CatBonus, CatBonus},
		},
		{
			name:  "ordem cronologica manda: a de fora chega primeiro mas a meta fecha depois",
			plays: plays(at(3, 0), at(10, 30), at(11, 0)),
			want:  []string{CatBonus, CatInSlot, CatInSlot},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Settle(day6(), tc.plays, cmp, rules, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("play %d (%s): got %q, want %q",
						i, tc.plays[i].DetectedAt.Format("15:04"), got[i], tc.want[i])
				}
			}
		})
	}
}

// Célula zerada — o caso da campanha 270. N=0 → toda tocada é bônus,
// independente da faixa (que o override deixa gravada mas inerte).
func TestSettle_ZeroedOverride_AllBonus(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "05:00", "23:59", 2)}
	ov := mkOverride(0, "05:00", "23:59")

	got := Settle(day6(), plays(at(10, 36), at(14, 1), at(15, 17)), cmp, rules, ov)
	for i, c := range got {
		if c != CatBonus {
			t.Errorf("play %d: got %q, want bonus (meta zerada = sem plano no dia)", i, c)
		}
	}
}

// Override com meta > 0: a faixa do override é a única que vale (supersede rules).
func TestSettle_Override_WindowSupersedesRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 127, "08:00", "10:00", 5)} // ignorada
	ov := mkOverride(1, "14:00", "16:00")

	got := Settle(day6(), plays(at(14, 30), at(9, 0)), cmp, rules, ov)
	if got[0] != CatInSlot {
		t.Errorf("14:30 na faixa do override: got %q, want in_slot", got[0])
	}
	if got[1] != CatOutSlot {
		t.Errorf("09:00 (faixa da regra, ignorada): got %q, want out_slot", got[1])
	}
}

// D6: out_date precede tudo e não consome cota. Tocada fora do período da
// campanha continua out_date mesmo com meta sobrando.
func TestSettle_OutDate_DoesNotConsumeQuota(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 9, 0, 0, 0, 0, saoPaulo), // termina 09/06
	}
	rules := []Rule{mkRule(1, 30, 127, "10:00", "12:00", 2)}

	got := Settle(day6(), plays(at(10, 30), at(11, 0)), cmp, rules, nil)
	for i, c := range got {
		if c != CatOutDate {
			t.Errorf("play %d fora do período da campanha: got %q, want out_date", i, c)
		}
	}
}

// D6 + carve-out: material nomeado numa regra específica, tocando fora do
// período DELA, continua out_date mesmo com a célula zerada por override —
// era o caminho que o override mascarava (bug original).
func TestSettle_CarveOutOfPeriod_StaysOutDate_EvenWithZeroedOverride(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	m := uuid.New()
	// Regra específica de m cobre só 01–07/06. O dia é 10/06 → fora do período dele.
	rules := []Rule{mkMatRule(1, 7, 127, "10:00", "12:00", 2, m)}
	ov := mkOverride(0, "00:00", "23:59")

	got := Settle(day6(), []Play{{DetectedAt: at(10, 30), MaterialID: m}}, cmp, rules, ov)
	if got[0] != CatOutDate {
		t.Errorf("carve-out fora do período: got %q, want out_date", got[0])
	}
}
```

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test ./internal/categorizer/ -run TestSettle -v`
Expected: FAIL — `undefined: Settle`, `undefined: Play`, `undefined: CatBonus`.

- [ ] **Step 3: Implementar `Settle`**

Em `workers/internal/categorizer/categorizer.go`, adicionar a constante nova junto às
outras (linha 43-48):

```go
	// CatBonus é a veiculação que excede a meta do dia — bonificação. Substitui
	// o antigo CatOrphan como veredito (spec 2026-08-14 D4). CatOrphan segue
	// declarado só pra leitura de linhas antigas até o backfill global rodar.
	CatBonus = "bonus"
```

Adicionar os tipos e a função (no fim do arquivo, antes de `containsUUID`):

```go
// Play é uma tocada da célula-dia a ser fechada.
type Play struct {
	DetectedAt time.Time
	MaterialID uuid.UUID
}

// Settle fecha uma célula-dia (campanha, tipo, emissora, dia) inteira e devolve
// a categoria de cada tocada NA MESMA ORDEM do slice de entrada.
//
// Regra canônica (spec 2026-08-14 §2):
//
//	0. out_date  — fora do período da campanha, ou (carve-out) fora do período
//	               das regras que nomeiam o material. Não consome cota.
//	1. N         — override.PlaysExpected, senão Σ plays_per_day das regras do dia.
//	2. "dentro da faixa" — casa ALGUMA faixa válida hoje (±SlotToleranceSeconds).
//	                       Sem cota por faixa: a meta é do dia.
//	3. dentro da faixa, em ordem cronológica: as N primeiras → in_slot, resto → bonus.
//	4. fora da faixa: in_slot < N → out_slot, senão → bonus.
//
// `day` é a data local SP da célula à meia-noite — passada explicitamente pra que
// N seja bem definido mesmo quando todas as tocadas são out_date.
//
// PARIDADE: esta função e a CTE `settled` de distribution_rules.go
// (recatClassifiedCTE) DEVEM concordar. settle_parity_test.go prova isso.
func Settle(day time.Time, plays []Play, cmp Campaign, rules []Rule, override *Override) []string {
	out := make([]string, len(plays))
	dayLocal := dateOnlySP(day)

	// Meta do dia. Override supersede as regras (D1/D7 do spec 2026-05-19).
	n := 0
	if override != nil {
		n = int(override.PlaysExpected)
	} else {
		for _, r := range rules {
			if matchesDateWeekday(r, dayLocal) {
				n += int(r.PlaysPerDay)
			}
		}
	}

	// Índices que entram na cota, em ordem cronológica (desempate estável pela
	// posição original) — o mesmo ORDER BY do ROW_NUMBER no SQL.
	type entry struct {
		i        int
		inWindow bool
	}
	entries := make([]entry, 0, len(plays))
	for i, p := range plays {
		local := p.DetectedAt.In(spLocation)
		date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

		if date.Before(dateOnlySP(cmp.StartDate)) || date.After(dateOnlySP(cmp.EndDate)) {
			out[i] = CatOutDate
			continue
		}
		// out_date do carve-out vale INDEPENDENTE de override — sem isso, uma
		// célula zerada mascarava o material fora do período dele (bug 2026-08).
		if carvedOutsidePeriod(date, p.MaterialID, rules) {
			out[i] = CatOutDate
			continue
		}
		entries = append(entries, entry{i: i, inWindow: inAnyWindow(local, date, p.MaterialID, rules, override)})
	}
	sort.SliceStable(entries, func(a, b int) bool {
		return plays[entries[a].i].DetectedAt.Before(plays[entries[b].i].DetectedAt)
	})

	// Passo 3 — dentro da faixa preenche a meta.
	inSlot := 0
	for _, e := range entries {
		if !e.inWindow {
			continue
		}
		if inSlot < n {
			out[e.i] = CatInSlot
			inSlot++
		} else {
			out[e.i] = CatBonus
		}
	}
	// Passo 4 — fora da faixa: segura o saldo enquanto a meta não fechou DENTRO
	// da faixa; depois disso é excedente (D2).
	for _, e := range entries {
		if e.inWindow {
			continue
		}
		if inSlot < n {
			out[e.i] = CatOutSlot
		} else {
			out[e.i] = CatBonus
		}
	}
	return out
}

// matchesDateWeekday casa data+dia-da-semana de uma regra contra o dia da célula.
func matchesDateWeekday(r Rule, day time.Time) bool {
	if day.Before(dateOnlySP(r.StartDate)) || day.After(dateOnlySP(r.EndDate)) {
		return false
	}
	return (1<<int(day.Weekday()))&int(r.WeekdayMask) != 0
}

// carvedOutsidePeriod: o material é nomeado em alguma regra específica (carve-out)
// e o dia está fora do range de datas de TODAS elas.
func carvedOutsidePeriod(date time.Time, materialID uuid.UUID, rules []Rule) bool {
	carved, inPeriod := false, false
	for _, r := range rules {
		if len(r.MaterialIDs) == 0 || !containsUUID(r.MaterialIDs, materialID) {
			continue
		}
		carved = true
		if !date.Before(dateOnlySP(r.StartDate)) && !date.After(dateOnlySP(r.EndDate)) {
			inPeriod = true
		}
	}
	return carved && !inPeriod
}

// inAnyWindow: a tocada cai em alguma faixa que vale hoje, com tolerância.
// Com override, a faixa do override é a ÚNICA considerada. Sem override,
// material carve-out é julgado só pelas regras que o nomeiam; material comum,
// só pelas regras gerais.
func inAnyWindow(local, date time.Time, materialID uuid.UUID, rules []Rule, override *Override) bool {
	tod := local.Hour()*3600 + local.Minute()*60 + local.Second()
	within := func(ts, te time.Time) bool {
		s := ts.Hour()*3600 + ts.Minute()*60 + ts.Second()
		e := te.Hour()*3600 + te.Minute()*60 + te.Second()
		return tod >= s-SlotToleranceSeconds && tod <= e+SlotToleranceSeconds
	}
	if override != nil {
		return within(override.TimeStart, override.TimeEnd)
	}
	carved := false
	for _, r := range rules {
		if len(r.MaterialIDs) > 0 && containsUUID(r.MaterialIDs, materialID) {
			carved = true
			break
		}
	}
	for _, r := range rules {
		specific := len(r.MaterialIDs) > 0
		if carved != specific {
			continue // carved usa só específicas; comum usa só gerais
		}
		if carved && !containsUUID(r.MaterialIDs, materialID) {
			continue
		}
		if !matchesDateWeekday(r, date) {
			continue
		}
		if within(r.TimeStart, r.TimeEnd) {
			return true
		}
	}
	return false
}
```

Adicionar `"sort"` ao import block.

- [ ] **Step 4: Rodar até passar**

Run: `cd workers && go test ./internal/categorizer/ -run TestSettle -v`
Expected: PASS — 5 subtests da tabela-verdade + os 4 testes nomeados.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/categorizer/categorizer.go workers/internal/categorizer/settle_test.go
git commit -m "feat(categorizer): fechamento por celula-dia com cota (Settle)"
```

---

## Task 2: Migration 0063 — categoria `bonus`

> **Correção aplicada durante a execução (2026-08-14):** a migration foi **dividida em
> duas** porque juntar o `ALTER` e o `UPDATE` na mesma transação segura o ACCESS EXCLUSIVE
> lock (parent + todas as partições) durante o rewrite inteiro — medido: SELECT concorrente
> bloqueado ~4,9s num UPDATE de 375k linhas, e o `NOT VALID` não evita nada nesse arranjo.
> Um arquivo com duas transações não tem precedente no repo (nenhuma das 122 migrations usa
> dois `BEGIN;`), então dois arquivos:
>
> - **`0063_category_bonus_constraint`** — só os 4 `ALTER TABLE` (lock de milissegundos).
> - **`0064_category_bonus_rename`** — só os dois `UPDATE orphan → bonus` (row locks).
>
> **Consequência de numeração: a migration da view (Task 6) passa de 0064 para `0065`.**

**Files:**
- Create: `migrations/0063_category_bonus_constraint.{up,down}.sql`
- Create: `migrations/0064_category_bonus_rename.{up,down}.sql`

- [ ] **Step 1: Escrever a migration**

`migrations/0063_category_bonus.up.sql`:

```sql
-- 0063: categoria 'bonus' explícita (spec 2026-08-14 D4).
--
-- 'orphan' CONTINUA aceito de propósito: durante a janela de deploy o binário
-- antigo da API ainda pode gravar 'orphan', e um CHECK sem ele derrubaria o
-- insert. Uma migration futura remove 'orphan' do CHECK depois que o backfill
-- global tiver rodado e nenhum produtor escrever mais o valor.
--
-- Os CHECKs entram NOT VALID pra não travar as tabelas particionadas com um
-- scan em ACCESS EXCLUSIVE. Como o conjunto novo é um SUPERSET do antigo, toda
-- linha existente já satisfaz — validar é formalidade e pode rodar depois.

BEGIN;

ALTER TABLE detections DROP CONSTRAINT IF EXISTS detections_category_check;
ALTER TABLE detections ADD CONSTRAINT detections_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan','bonus')) NOT VALID;

ALTER TABLE detection_campaigns DROP CONSTRAINT IF EXISTS detection_campaigns_category_check;
ALTER TABLE detection_campaigns ADD CONSTRAINT detection_campaigns_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan','bonus')) NOT VALID;

-- Renomeia o histórico: 'orphan' já significava bônus (a view somava orphan em
-- bonus desde 0018), então isto é renomeação semântica, não reclassificação.
UPDATE detections          SET category = 'bonus' WHERE category = 'orphan';
UPDATE detection_campaigns SET category = 'bonus' WHERE category = 'orphan';

COMMIT;
```

`migrations/0063_category_bonus.down.sql`:

```sql
BEGIN;

UPDATE detections          SET category = 'orphan' WHERE category = 'bonus';
UPDATE detection_campaigns SET category = 'orphan' WHERE category = 'bonus';

ALTER TABLE detections DROP CONSTRAINT IF EXISTS detections_category_check;
ALTER TABLE detections ADD CONSTRAINT detections_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan')) NOT VALID;

ALTER TABLE detection_campaigns DROP CONSTRAINT IF EXISTS detection_campaigns_category_check;
ALTER TABLE detection_campaigns ADD CONSTRAINT detection_campaigns_category_check
    CHECK (category IN ('in_slot','out_slot','out_date','orphan')) NOT VALID;

COMMIT;
```

- [ ] **Step 2: Descobrir o nome real das constraints antes de aplicar**

Os `DROP CONSTRAINT IF EXISTS` acima assumem o nome default do Postgres. Confirme
contra o banco de teste:

Run:
```bash
docker exec rc-test-pg psql -U radiocheck -d radiocheck_test -Atc \
  "SELECT conrelid::regclass, conname FROM pg_constraint
   WHERE contype='c' AND conrelid::regclass::text IN ('detections','detection_campaigns');"
```
Expected: `detections|detections_category_check` e
`detection_campaigns|detection_campaigns_category_check`. Se vier outro nome, corrija a
migration antes de seguir.

- [ ] **Step 3: Aplicar no DB de teste**

Run: `docker exec rc-test-pg psql -U radiocheck -d radiocheck_test -f /dev/stdin < migrations/0063_category_bonus.up.sql`
Expected: `COMMIT`, sem erro.

- [ ] **Step 4: Provar que a categoria nova é aceita e a antiga sumiu**

Run:
```bash
docker exec rc-test-pg psql -U radiocheck -d radiocheck_test -Atc \
  "SELECT count(*) FROM detections WHERE category='orphan';"
```
Expected: `0`

- [ ] **Step 5: Commit**

```bash
git add migrations/0063_category_bonus.up.sql migrations/0063_category_bonus.down.sql
git commit -m "feat(migrations): categoria bonus explicita (0063)"
```

---

## Task 3: Insert-path fecha a célula-dia

**Files:**
- Modify: `workers/internal/catalog/detections.go:178-267` (`categorize`)
- Test: `workers/internal/catalog/detections_settle_test.go` (criar)

- [ ] **Step 1: Escrever o teste de integração (falhando)**

Criar `workers/internal/catalog/detections_settle_test.go`. O teste insere 3 tocadas
numa célula com N=2 (faixa 10:00–12:00) e confere que a 3ª fecha o dia mudando a
categoria da que já estava gravada:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A 3ª tocada (fora da faixa) chega DEPOIS da meta ter fechado dentro da faixa
// → ela nasce bonus. E a 1ª, gravada out_slot quando chegou sozinha às 03:00,
// tem que ter sido reclassificada pra bonus pelo fechamento do dia.
func TestInsertPath_SettlesWholeCellDay(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "settle-cli"})
	require.NoError(t, err)
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "settle-cmp", ClientID: cli.ID,
		StartDate: day.AddDate(0, 0, -5), EndDate: day.AddDate(0, 0, 5),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "settle-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "settle-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/s", MasterSHA256: "settle-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Settle FM", Band: "FM", StreamURL: "http://example.com/settle",
	})
	require.NoError(t, err)

	_, err = NewDistributionRules(pool).Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{},
		StartDate: day.AddDate(0, 0, -5), EndDate: day.AddDate(0, 0, 5),
		WeekdayMask: 127, TimeStart: "10:00", TimeEnd: "12:00", PlaysPerDay: 2,
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

	sp, _ := time.LoadLocation("America/Sao_Paulo")
	mk := func(h, m int) uuid.UUID {
		d, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
			StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
			DetectedAt: time.Date(2026, 6, 10, h, m, 0, 0, sp),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
			Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
		})
		require.NoError(t, err)
		return d.ID
	}
	catOf := func(id uuid.UUID) string {
		var c string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id=$1`, id).Scan(&c))
		return c
	}

	early := mk(3, 0) // fora da faixa, meta ainda aberta
	require.Equal(t, "out_slot", catOf(early), "sozinha às 03:00, a meta está aberta")

	first := mk(10, 30)
	second := mk(11, 0) // fecha a meta dentro da faixa

	require.Equal(t, "in_slot", catOf(first))
	require.Equal(t, "in_slot", catOf(second))
	require.Equal(t, "bonus", catOf(early),
		"meta fechou dentro da faixa → a das 03:00 vira excedente")
}
```

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestInsertPath_SettlesWholeCellDay -v`
Expected: FAIL na última asserção — `early` continua `out_slot` (o insert-path ainda
classifica tocada a tocada).

- [ ] **Step 3: Trocar `categorize` por `settleCellDay`**

Em `workers/internal/catalog/detections.go`, substituir o corpo de `categorize`
(linhas 190-267) por uma função que (a) carrega campanha, regras e override como hoje,
(b) carrega TODAS as tocadas aprovadas da célula-dia incluindo a que está sendo inserida,
(c) chama `categorizer.Settle`, (d) grava as categorias das outras tocadas e devolve a da
tocada nova.

```go
// settleCellDay fecha a célula-dia (campanha, tipo, emissora, dia local SP) da
// detection sendo inserida: recategoriza TODAS as tocadas aprovadas do dia e
// devolve a categoria da tocada nova. Roda no MESMO querier (tx) do caller.
//
// Custo: a célula-dia tem tipicamente < 20 linhas; o SELECT poda partição por
// detected_at e o UPDATE só toca as linhas cuja categoria muda.
func (d *Detections) settleCellDay(ctx context.Context, q pgxQuerier, in CreateDetectionInput) (string, error) {
	var cmpStart, cmpEnd time.Time
	var typeID *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT c.start_date, c.end_date, m.type_id
		FROM campaigns c, materials m
		WHERE c.id = $1 AND m.id = $2`, in.CampaignID, in.CommercialID,
	).Scan(&cmpStart, &cmpEnd, &typeID)
	if err != nil {
		return categorizer.CatBonus, err
	}
	if typeID == nil {
		// Material sem tipo não casa regra nenhuma → N=0 → bônus.
		return categorizer.CatBonus, nil
	}

	day := in.DetectedAt.In(spLoc)
	dayLocal := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, spLoc)

	rules, err := d.loadRulesForCell(ctx, q, in.CampaignID, *typeID, in.StationID, dayLocal)
	if err != nil {
		return categorizer.CatBonus, err
	}
	ov, err := d.loadOverrideForCell(ctx, q, in.CampaignID, *typeID, in.StationID, dayLocal)
	if err != nil {
		return categorizer.CatBonus, err
	}

	// Tocadas já gravadas da célula-dia (aprovadas). A tocada nova ainda não
	// está no banco — entra no fim do slice e é identificada pelo índice.
	type row struct {
		id  uuid.UUID
		at  time.Time
		mat uuid.UUID
	}
	rows, err := q.Query(ctx, `
		SELECT d.id, d.detected_at, dc.commercial_id
		FROM detection_campaigns dc
		JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
		JOIN materials m ON m.id = dc.commercial_id
		WHERE dc.campaign_id = $1
		  AND m.type_id = $2
		  AND d.station_id = $3
		  AND dc.detected_at >= ($4::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
		  AND dc.detected_at <  (($4::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
		  AND d.retracted_at IS NULL AND d.ignored_at IS NULL
		  AND d.evidence_status <> 'audit_rejected'
		ORDER BY d.detected_at, d.id`,
		in.CampaignID, *typeID, in.StationID, dayLocal)
	if err != nil {
		return categorizer.CatBonus, err
	}
	var existing []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.at, &r.mat); err != nil {
			rows.Close()
			return categorizer.CatBonus, err
		}
		existing = append(existing, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return categorizer.CatBonus, err
	}

	plays := make([]categorizer.Play, 0, len(existing)+1)
	for _, r := range existing {
		plays = append(plays, categorizer.Play{DetectedAt: r.at, MaterialID: r.mat})
	}
	plays = append(plays, categorizer.Play{DetectedAt: in.DetectedAt, MaterialID: in.CommercialID})

	cats := categorizer.Settle(dayLocal, plays,
		categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd}, rules, ov)

	// Regrava as já existentes que mudaram de categoria (base + projeção canônica).
	for i, r := range existing {
		if _, err := q.Query(ctx, `
			WITH u AS (
			    UPDATE detections SET category = $3
			    WHERE id = $1 AND detected_at = $2 AND category IS DISTINCT FROM $3
			    RETURNING 1
			)
			UPDATE detection_campaigns SET category = $3
			WHERE detection_id = $1 AND detected_at = $2 AND campaign_id = $4
			  AND category IS DISTINCT FROM $3`,
			r.id, r.at, cats[i], in.CampaignID); err != nil {
			return categorizer.CatBonus, err
		}
	}
	return cats[len(cats)-1], nil
}
```

`spLoc` é `var spLoc, _ = time.LoadLocation("America/Sao_Paulo")` no topo do arquivo (se
já existir um equivalente, reusar). `loadRulesForCell` e `loadOverrideForCell` são
extrações diretas dos blocos que já estão em `categorize` (linhas 196-258) — mover o SQL
como está, trocando só a assinatura pra receber `typeID`/`dayLocal` prontos.

Trocar a chamada: onde hoje se chama `d.categorize(ctx, q, in)`, chamar
`d.settleCellDay(ctx, q, in)`.

- [ ] **Step 4: Rodar até passar**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestInsertPath_SettlesWholeCellDay -v`
Expected: PASS

- [ ] **Step 5: Rodar o pacote inteiro pra achar o que quebrou**

Run: `cd workers && go test -p 1 ./internal/catalog/`
Expected: falham os testes que afirmam o comportamento antigo —
`TestRecategorizeForCampaign_RespectsOverride` (espera `out_slot` em célula zerada) e os
casos de `distribution_rules_override_test.go:168`. Eles são corrigidos na Task 4; **não
apague nenhum**, só ajuste a expectativa quando chegar lá.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_settle_test.go
git commit -m "feat(detections): insert-path fecha a celula-dia inteira"
```

---

## Task 3b: Re-fechar a célula-dia quando uma tocada SAI do conjunto aprovado

> **Task nova, descoberta na review da Task 3 (2026-08-14).** Não estava no plano original.

**O buraco:** no modelo antigo, remover uma tocada não podia mudar a categoria de nenhuma
outra — a classificação era isolada. Com cota, pode: liberar uma vaga deveria promover uma
`out_slot` retida a `in_slot`. Hoje nada re-fecha a célula quando uma tocada sai do conjunto
aprovado, e se nenhuma tocada nova cair naquela célula-dia, as categorias ficam erradas
**pra sempre** — `in_slot` subnotificado e déficit superestimado.

Caso concreto: o dedup de co-fire (`evidence/service.go:673,684`) retrata uma duplicata na
mesma célula-dia. A vaga libera e ninguém reaproveita.

**Files:**
- Modify: `workers/internal/catalog/detections.go` — `RetractByID` (:1528), `Ignore` (:1279),
  `MarkAmbiguous` (:1547), `ClearRetraction` (:1514), `Restore` (:1288)
- Modify: `workers/internal/catalog/detections.go` — `ReattributeDetection` (:674): re-fechar
  também a célula-dia de **ORIGEM**, não só a de destino
- Test: `workers/internal/catalog/detections_settle_test.go`

- [ ] **Step 1: Teste falhando** — célula com N=2: uma `in_slot`, uma `out_slot` retida.
  Retratar a `in_slot` e afirmar que a `out_slot` foi promovida a `in_slot`.
- [ ] **Step 2: Provar que falha** (hoje a `out_slot` fica congelada).
- [ ] **Step 3:** chamar o re-fechamento da célula-dia em cada um dos 6 pontos acima,
  dentro da transação de cada um. Cuidado: a tocada que está saindo não pode entrar na cota.
- [ ] **Step 4:** rodar o pacote com `-p 1` e `TEST_DATABASE_URL` setada.
- [ ] **Step 5: Commit.**

---

## Task 4: Recat SQL — fechamento set-wise + expansão de escopo

> **Herança da review da Task 3 — dois itens obrigatórios nesta task:**
>
> 1. **Filtro de aprovadas na cota.** O motor Go usa `ApprovedDetectionsFilter`
>    (exclui retratada / ignorada / `audit_rejected` / `ambiguous`), mas a CTE `scope` do
>    `recategorizeScope` (`distribution_rules.go:389-400`) **não filtra nada disso**. Se o
>    SQL contar uma tocada retratada na cota, os dois motores discordam em toda célula-dia
>    que tenha uma — que é exatamente o que o teste de paridade da Task 5 existe pra proibir.
> 2. **Ordem de lock.** `recatApplySQL` (`distribution_rules.go:361-376`) trava
>    `detection_campaigns` antes de `detections`. A Task 3 padronizou **detections-first**
>    pra fechar um deadlock reproduzido contra o caminho de reatribuição. Inverter aqui
>    também, senão o deadlock só muda de lugar.

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go:242-403`
- Modify: `workers/internal/catalog/distribution_rules_override_test.go:17,76,91,168`

> **Herança da Task 3:** `TestCarveOut_InPeriodWrongWeekday_Orphan_InsertAndRecat` ficou
> **vermelho de propósito**. Ele existe pra provar que o insert-path Go e o recat SQL
> concordam; a Task 3 flipou a metade Go pra `bonus` e a metade SQL só flipa aqui.
> **As duas metades têm que virar `bonus` nesta task** — flipar só uma faria o teste
> afirmar justamente a divergência que ele existe pra proibir.

- [ ] **Step 1: Ajustar os testes existentes pra regra nova**

Em `distribution_rules_override_test.go`, o teste
`TestRecategorizeForCampaign_RespectsOverride` continua provando que o recat respeita o
override em vez da regra — só muda o veredito. Trocar as linhas 90-91:

```go
	require.Equal(t, "bonus", cat,
		"celula zerada por override: meta 0 → toda tocada é excedente (bonus), não out_slot")
```

E o comentário do topo (linhas 16-17):

```go
// respeita o override (plays_expected=0 → meta 0 → bonus), não a regra (in_slot).
```

Fazer o mesmo ajuste no caso da linha 168.

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestRecategorize -v`
Expected: FAIL — o SQL ainda devolve `out_slot`.

- [ ] **Step 3: Reescrever `recatClassifiedCTE`**

Substituir o bloco inteiro (linhas 242-354) por:

```go
// recatClassifiedCTE é a CTE `classified` — replica categorizer.Settle em SQL.
//
// DIFERENÇA CRÍTICA pro modelo antigo: a decisão depende de TODAS as tocadas da
// célula-dia, não só das linhas do `scope`. Por isso a primeira coisa que a CTE
// faz é EXPANDIR o escopo recebido pras células-dia completas (`cell`) e recarregar
// todas as tocadas aprovadas delas (`plays`). Um escopo parcial classificado linha
// a linha produziria cota errada.
//
// Espera uma CTE `scope(id, detected_at, campaign_id, material_id, type_id,
// station_id)` definida antes dela. Devolve `classified(id, detected_at,
// campaign_id, new_category)`.
//
// PARIDADE: settle_parity_test.go prova que esta CTE e categorizer.Settle
// concordam sobre a tabela-verdade da spec.
const recatClassifiedCTE = `,
cell AS (
    SELECT DISTINCT s.campaign_id, s.type_id, s.station_id,
           date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date
    FROM scope s
    WHERE s.type_id IS NOT NULL
),
meta AS (
    SELECT c.campaign_id, c.type_id, c.station_id, c.for_date,
           COALESCE(o.plays_expected, r.rule_expected, 0)::int AS n
    FROM cell c
    LEFT JOIN distribution_overrides o
           ON o.campaign_id = c.campaign_id AND o.type_id = c.type_id
          AND o.station_id  = c.station_id  AND o.for_date = c.for_date
    LEFT JOIN LATERAL (
        SELECT SUM(r.plays_per_day)::int AS rule_expected
        FROM distribution_rules r
        WHERE r.campaign_id = c.campaign_id
          AND r.type_id     = c.type_id
          AND c.station_id  = ANY(r.station_ids)
          AND c.for_date BETWEEN r.start_date AND r.end_date
          AND ((1 << EXTRACT(DOW FROM c.for_date)::int) & r.weekday_mask) != 0
    ) r ON TRUE
),
plays AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, c.type_id, c.station_id, c.for_date,
           (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::time AS tod,
           cmp.start_date AS cmp_start, cmp.end_date AS cmp_end
    FROM cell c
    JOIN campaigns cmp ON cmp.id = c.campaign_id
    JOIN detection_campaigns dc
          ON dc.campaign_id = c.campaign_id
         AND dc.detected_at >= (c.for_date::timestamp AT TIME ZONE 'America/Sao_Paulo')
         AND dc.detected_at <  ((c.for_date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
         AND d.station_id = c.station_id
         AND d.retracted_at IS NULL
         AND d.ignored_at IS NULL
         AND d.evidence_status <> 'audit_rejected'
    JOIN materials m ON m.id = dc.commercial_id AND m.type_id = c.type_id
),
flagged AS (
    SELECT p.*,
        -- out_date: fora do período da campanha OU carve-out fora do período das
        -- regras que nomeiam o material (vale mesmo com override — D6).
        (p.for_date NOT BETWEEN p.cmp_start AND p.cmp_end
         OR (EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND cardinality(r.material_ids) > 0
                  AND p.material_id = ANY(r.material_ids))
             AND NOT EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND p.material_id = ANY(r.material_ids)
                  AND p.for_date BETWEEN r.start_date AND r.end_date))
        ) AS is_out_date,
        -- dentro da faixa: override manda; senão, carve-out usa só as regras que
        -- nomeiam o material e material comum só as gerais. Tolerância 900s = 15
        -- min = categorizer.SlotToleranceSeconds.
        CASE
            WHEN EXISTS (
                SELECT 1 FROM distribution_overrides o
                WHERE o.campaign_id = p.campaign_id AND o.type_id = p.type_id
                  AND o.station_id = p.station_id AND o.for_date = p.for_date)
            THEN EXISTS (
                SELECT 1 FROM distribution_overrides o
                WHERE o.campaign_id = p.campaign_id AND o.type_id = p.type_id
                  AND o.station_id = p.station_id AND o.for_date = p.for_date
                  AND EXTRACT(EPOCH FROM p.tod)
                      BETWEEN EXTRACT(EPOCH FROM o.time_start) - 900
                          AND EXTRACT(EPOCH FROM o.time_end)   + 900)
            ELSE EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = p.campaign_id AND r.type_id = p.type_id
                  AND p.station_id = ANY(r.station_ids)
                  AND (CASE
                         WHEN EXISTS (
                            SELECT 1 FROM distribution_rules r2
                            WHERE r2.campaign_id = p.campaign_id AND r2.type_id = p.type_id
                              AND p.station_id = ANY(r2.station_ids)
                              AND cardinality(r2.material_ids) > 0
                              AND p.material_id = ANY(r2.material_ids))
                         THEN cardinality(r.material_ids) > 0
                              AND p.material_id = ANY(r.material_ids)
                         ELSE cardinality(r.material_ids) = 0
                       END)
                  AND p.for_date BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM p.for_date)::int) & r.weekday_mask) != 0
                  AND EXTRACT(EPOCH FROM p.tod)
                      BETWEEN EXTRACT(EPOCH FROM r.time_start) - 900
                          AND EXTRACT(EPOCH FROM r.time_end)   + 900)
        END AS in_window
    FROM plays p
),
ranked AS (
    -- rn_in numera dentro de cada (célula-dia, in_window). ROW_NUMBER() é window
    -- function pura e NÃO aceita FILTER — por isso in_window entra na PARTITION e
    -- o valor só é lido no ramo in_window do CASE. Já COUNT(*) aceita FILTER com
    -- OVER (é agregado usado como window function).
    SELECT f.*, m.n,
        ROW_NUMBER() OVER (
            PARTITION BY f.campaign_id, f.type_id, f.station_id, f.for_date, f.in_window
            ORDER BY f.detected_at, f.id
        ) AS rn_in,
        LEAST(m.n, COUNT(*) FILTER (WHERE f.in_window) OVER (
            PARTITION BY f.campaign_id, f.type_id, f.station_id, f.for_date
        )) AS in_slot_total
    FROM flagged f
    JOIN meta m ON m.campaign_id = f.campaign_id AND m.type_id = f.type_id
               AND m.station_id  = f.station_id  AND m.for_date = f.for_date
    WHERE NOT f.is_out_date
),
classified AS (
    SELECT id, detected_at, campaign_id, 'out_date' AS new_category
    FROM flagged WHERE is_out_date
    UNION ALL
    SELECT id, detected_at, campaign_id,
        CASE
            WHEN in_window AND rn_in <= n     THEN 'in_slot'
            WHEN in_window                    THEN 'bonus'
            WHEN in_slot_total < n            THEN 'out_slot'
            ELSE 'bonus'
        END AS new_category
    FROM ranked
)`
```

- [ ] **Step 4: Rodar até passar**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestRecategorize -v`
Expected: PASS

- [ ] **Step 5: Rodar o pacote e o build cross-compile**

Run: `cd workers && go test -p 1 ./internal/catalog/ && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: PASS + build limpo

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_override_test.go
git commit -m "feat(recat): fechamento por celula-dia em SQL com expansao de escopo"
```

---

## Task 5: Teste de paridade Go × SQL

> **Ponto de partida pronto (review da Task 4, 2026-08-14).** O revisor deixou um harness
> diferencial funcionando no scratchpad da sessão, em `review-harness/` (5 arquivos):
> roda `recatScopeByCampaignSQL + recatSelectTailSQL` como SELECT puro e compara contra
> `categorizer.Settle` alimentado pelos **loaders de produção** (`loadRulesForCell`,
> `loadOverrideForCell`, `loadCellDayPlays`) — ou seja, exatamente os inputs do insert-path.
> Já rodou 600 cenários / 3.369 vereditos com 0 divergência, e provou sensibilidade por
> mutação (3 mutações no SQL, todas pegas em <20 cenários).
>
> Promover esse harness pra teste versionado é a Task 5. Ele cobre 2 casos que o plano
> original não previa e que devem entrar: **independência entre tipos** na mesma célula-dia
> (a cota particiona por tipo) e **`meta.N` × o `expected` da view** (se divergirem, o
> déficit sai errado mesmo com as categorias certas).
>
> Gerar também os limites exatos de tolerância (900 e 901 segundos), empates no mesmo
> segundo, dias na borda do período da campanha, e linhas não-aprovadas.

**Files:**
- Create: `workers/internal/catalog/settle_parity_test.go`

- [ ] **Step 1: Escrever o teste**

O teste monta cada linha da tabela-verdade da spec no banco, roda
`RecategorizeForCampaign` e compara com o que `categorizer.Settle` devolve pro mesmo
input em memória. Sem esse teste os dois motores divergem em silêncio — foi exatamente
o que aconteceu com a tolerância de 15 min (comentário em `distribution_rules.go:236`).

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/categorizer"
)

func TestSettleParity_GoVsSQL(t *testing.T) {
	ctx, pool := newTestDB(t)
	sp, _ := time.LoadLocation("America/Sao_Paulo")

	cases := []struct {
		name  string
		n     int16
		hours []int // hora local de cada tocada
	}{
		{"1 dentro 1 fora", 2, []int{10, 3}},
		{"2 dentro 1 fora", 2, []int{10, 11, 3}},
		{"0 dentro 3 fora", 2, []int{3, 4, 5}},
		{"4 dentro", 2, []int{10, 10, 11, 11}},
		{"meta zerada", 0, []int{10, 14, 15}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ... seed cliente/campanha/tipo/material/emissora/regra (faixa 10:00-12:00,
			// plays_per_day = tc.n) e as detections em tc.hours, com t.Cleanup como nos
			// outros testes do pacote ...

			require.NoError(t, NewDistributionRules(pool).RecategorizeForCampaign(ctx, cmpID))

			// Veredito do SQL, na mesma ordem cronológica.
			rows, err := pool.Query(ctx, `
				SELECT category FROM detections
				WHERE campaign_id = $1 ORDER BY detected_at, id`, cmpID)
			require.NoError(t, err)
			var fromSQL []string
			for rows.Next() {
				var c string
				require.NoError(t, rows.Scan(&c))
				fromSQL = append(fromSQL, c)
			}
			rows.Close()

			// Veredito do Go pro mesmo input.
			day := time.Date(2026, 6, 10, 0, 0, 0, 0, sp)
			var ps []categorizer.Play
			for _, h := range sortedInts(tc.hours) {
				ps = append(ps, categorizer.Play{
					DetectedAt: time.Date(2026, 6, 10, h, 0, 0, 0, sp),
					MaterialID: matID,
				})
			}
			fromGo := categorizer.Settle(day, ps,
				categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd},
				[]categorizer.Rule{{
					StartDate: cmpStart, EndDate: cmpEnd, WeekdayMask: 127,
					TimeStart: mustTime("10:00"), TimeEnd: mustTime("12:00"),
					PlaysPerDay: tc.n,
				}}, nil)

			require.Equal(t, fromGo, fromSQL,
				"Go e SQL divergiram na célula-dia — os dois motores TÊM que concordar")
		})
	}
}
```

Preencher o seed com o mesmo padrão dos testes vizinhos (`distribution_rules_override_test.go:19-68`),
e implementar os helpers `sortedInts` e `mustTime` no próprio arquivo.

- [ ] **Step 2: Rodar**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestSettleParity -v`
Expected: PASS nos 5 casos. Qualquer divergência aqui é bug de um dos dois motores —
conserte o motor, nunca o teste.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/settle_parity_test.go
git commit -m "test(catalog): paridade Go x SQL do fechamento por celula-dia"
```

> **O que realmente entrou (2026-08-14).** `settle_parity_test.go` com 11 testes: a
> tabela-verdade, limites de tolerância (900 e 901 s nos dois extremos, na faixa da regra
> E na do override), empate no mesmo segundo, bordas do período da campanha, linhas fora
> do conjunto aprovado, carve-out (incl. dia sem regra própria), override zerado,
> independência entre tipos e entre emissoras, `meta.N` × `daily_play_summary_for().expected`
> e o sweep aleatório. O lado SQL roda como SELECT puro (nenhum teste chama `Recategorize*`)
> e o lado Go usa os loaders de produção.
>
> Dois desvios a registrar:
> 1. **A linha "N=0" da tabela-verdade não é montável com `plays_per_day = 0`** — o CHECK
>    `distribution_rules_plays_per_day_check` proíbe. As duas formas REAIS de N=0 viraram
>    dois subcasos: override com `plays_expected = 0` (campanha 270) e regra que não cobre
>    o dia-da-semana.
> 2. **O sweep é parametrizado**: 150 cenários (~10 s) por padrão, 25 em `-short`,
>    `SETTLE_PARITY_SCENARIOS`/`SETTLE_PARITY_SEED` pra reproduzir. Seed constante
>    (`20260814`) e âncora sempre numa segunda-feira, pra ser determinístico em CI sem
>    fixar uma data que sai da janela de partições. 600 cenários / 3.132 vereditos: 0
>    divergências. Sensibilidade re-provada por mutação no SQL (`rn_in <= n+1`, tolerância
>    899, teste extra de dia-da-semana no `is_out_date`): todas as três pegas.

---

## Task 6: Migration 0065 — déficit e bônus na view/função

> **Renumerada de 0064 para 0065** — a Task 2 foi dividida em duas migrations (ver a nota
> lá). Trocar `0064_quota_aware_summary` por `0065_quota_aware_summary` em todos os passos
> abaixo.

**Files:**
- Create: `migrations/0065_quota_aware_summary.{up,down}.sql`

- [ ] **Step 1: Escrever a migration**

`migrations/0064_quota_aware_summary.up.sql` redefine `daily_play_summary` (última
definição em `0041_detection_campaigns.up.sql:100-135`) e
`daily_play_summary_for` (`0052_daily_play_summary_fn.up.sql`) — **copiar as duas
definições inteiras** e trocar só o bloco final:

```sql
-- Antes (0041:132-133 / 0052:107-108):
--   GREATEST(0, expected - in_slot - out_slot) AS deficit,
--   (GREATEST(0, in_slot - expected) + orphan) AS bonus,
--
-- Depois (spec 2026-08-14 D3 + §2): out_slot não abate o contrato, e o bônus
-- vem inteiro da categoria — o categorizador virou a fonte única (in_slot nunca
-- passa de expected por construção, então o GREATEST antigo era sempre 0).
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0))::int AS deficit,
    COALESCE(a.bonus, 0)::int AS bonus,
```

E na CTE `actual` das duas definições, trocar o contador de `orphan`:

```sql
        COUNT(*) FILTER (WHERE dc.category = 'bonus')::int AS bonus,
```

`0064_quota_aware_summary.down.sql` restaura as duas definições exatamente como estão
hoje em 0041/0052 (copiar de lá sem alterar).

- [ ] **Step 2: Aplicar no DB de teste e conferir a aritmética**

Run:
```bash
docker exec rc-test-pg psql -U radiocheck -d radiocheck_test -f /dev/stdin < migrations/0064_quota_aware_summary.up.sql
```
Expected: `CREATE VIEW` / `CREATE FUNCTION` sem erro.

- [ ] **Step 3: Teste de integração do resumo**

Adicionar a `workers/internal/catalog/daily_summary_test.go` um caso que monta o
cenário "N=2, 1 dentro, 1 fora" e afirma `expected=2, in_slot=1, out_slot=1, bonus=0,
deficit=1` — o exemplo 1 do dono, que hoje daria `deficit=0`.

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestBuildDailySummary -v`
Expected: PASS. (Atenção: `TestBuildDailySummary_WithDowntime` é flaky antes das ~13:00
UTC por causa de `time.Now().Add(-13h)` — regra 6.6 do CLAUDE.md.)

- [ ] **Step 4: Commit**

```bash
git add migrations/0064_quota_aware_summary.up.sql migrations/0064_quota_aware_summary.down.sql workers/internal/catalog/daily_summary_test.go
git commit -m "feat(migrations): deficit sem out_slot e bonus pela categoria (0064)"
```

---

## Task 7: `/insights` — `executado` deixa de somar `out_slot`

**Files:**
- Modify: `workers/internal/catalog/insights.go:403-405,447-449,505,519,594,617,756,775`

- [ ] **Step 1: Trocar as fórmulas**

Quatro trocas mecânicas, todas de `in_slot + out_slot` → `in_slot` (D3 — out_slot não
fatura):

- `cs_window` (linha 594): `SUM(s.in_slot)::bigint AS executed`
- `cs_per_ins` (linha 617): `COALESCE(SUM(tp.unit_value * s.in_slot), 0)::numeric AS pi_executado`
- CTE do numerador consolidado (linha 756): `SUM(s.in_slot)::bigint AS executed`
- `pi_executado` do bloco 775: `COALESCE(SUM(tp.unit_value * s.in_slot), 0)::numeric`

Mais duas de categoria:
- linha 405: `COUNT(*) FILTER (WHERE f.category='bonus')::bigint AS bonus_n` (renomeando
  `orphan_n` → `bonus_n`, e `sum_orphan` → `sum_bonus` na linha 449)
- linha 519: `AND d.category = 'bonus'`

E o déficit do gráfico (linha 505): `GREATEST(0, SUM(expected) - SUM(in_slot))::int AS deficit`.

Atualizar o comentário do bloco 568-576 pra refletir a base nova.

- [ ] **Step 2: Rodar os testes de insights**

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestInsights -v`
Expected: PASS, ou falha em asserção que fixava a base antiga — nesse caso ajuste a
asserção pro número novo e registre no commit.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/insights.go
git commit -m "feat(insights): executado = in_slot; breakdown le categoria bonus"
```

---

## Task 8: `/admin/station-failures` — déficit novo com os dois tipos separados

**Files:**
- Modify: `workers/internal/catalog/campaign_failures.go:22,295-303,397-405,523-531`

- [ ] **Step 1: Adicionar o split ao agregado**

O déficit novo (Task 6) já faz o dia "tocou tudo fora do horário" virar falha. D7 exige
distinguir os dois tipos. Em cada uma das três queries (Q3/Get/ListHistorical), somar
duas colunas ao lado de `deficit`:

```sql
       SUM(dps.deficit)::int                          AS deficit,
       SUM(LEAST(dps.deficit, dps.out_slot))::int     AS deficit_off_slot,
       SUM(GREATEST(0, dps.deficit - dps.out_slot))::int AS deficit_absent,
```

`deficit_off_slot` = a parte do déficit que tem uma veiculação fora do horário por trás
(a emissora tocou, no horário errado). `deficit_absent` = a parte em que simplesmente não
tocou. As duas somam `deficit`.

Adicionar os dois campos às structs de retorno correspondentes e ao JSON.

- [ ] **Step 2: Teste**

Adicionar em `campaign_failures_test.go` um caso "expected 3, in_slot 1, out_slot 1" →
`deficit=2, deficit_off_slot=1, deficit_absent=1`.

Run: `cd workers && go test -p 1 ./internal/catalog/ -run TestCampaignFailures -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/campaign_failures.go workers/internal/catalog/campaign_failures_test.go
git commit -m "feat(failures): separa deficit por ausencia de deficit por horario"
```

---

## Task 9: Frontend — `buildDayPlan` espelha o fechamento

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx:100-154,1409-1415`

- [ ] **Step 1: Reescrever `buildDayPlan`**

A função hoje atribui cada `in_slot` à faixa mais estreita que casa, **sem teto**. Trocar
por: contar `expected` (já vem em `cellSummary.expected`), contar as tocadas dentro de
qualquer faixa válida, e derivar in_slot/out_slot/bônus com a mesma regra do `Settle`.
O `played` por faixa continua existindo pra barra de progresso, mas passa a ser
informativo — a categoria vem do backend (`det.category`), que agora é a fonte única.

- [ ] **Step 2: Trocar a mensagem da célula zerada**

Linhas 1409-1415: a mensagem "o ajuste do dia zerou a meta — toda tocada conta como fora
do prazo" fica falsa. Trocar o bloco por um que, quando `gov.plays_expected === 0` e
houver tocadas, diga que elas contam como bonificação:

```jsx
      {gov && gov.plays_expected === 0 && detections.length > 0 && (
        <p style={{ margin: 0, padding: '6px 12px 0', fontSize: 11, color: '#1d4ed8', lineHeight: 1.45 }}>
          {detections.length} tocou sem meta no dia — conta como bonificação.
        </p>
      )}
```

- [ ] **Step 3: Conferir no navegador**

Run: `cd frontend && npm run dev`
Abrir uma campanha com célula zerada e tocada, e confirmar o selo azul de bonificação
no lugar do amarelo de "fora da faixa".

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(frontend): plano do dia espelha o fechamento por cota"
```

---

## Task 10: Frontend — painel de falhas com os dois tipos

**Files:**
- Modify: `frontend/src/pages/AdminStationFailures.jsx` (e o PDF de cobrança)

- [ ] **Step 1: Renderizar as duas colunas**

Consumir `deficit_absent` e `deficit_off_slot` da API (Task 8) e mostrar dois badges
distintos por linha — "não tocou N" (vermelho) e "fora do horário N" (âmbar) — em vez do
`deficit` único. Mesma separação no PDF de cobrança.

- [ ] **Step 2: Commit**

```bash
git add frontend/src/pages/AdminStationFailures.jsx
git commit -m "feat(frontend): painel de falhas separa ausencia de horario errado"
```

---

## Task 11: Rótulos de categoria nos relatórios

**Files:**
- Modify: `workers/internal/reportcsv/reportcsv.go`, `workers/internal/postsale/repo.go`,
  `workers/internal/api/router.go`

- [ ] **Step 1: Trocar `orphan` por `bonus`**

Run: `grep -rn "'orphan'\|\"orphan\"" workers/internal/ --include=*.go | grep -v _test`
Trocar cada ocorrência pela categoria nova, e o rótulo visível pra "Bonificação".

- [ ] **Step 2: Build + testes**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go test -p 1 ./...`
Expected: build limpo, testes verdes (exceto o flaky conhecido de `TestBuildDailySummary_WithDowntime`).

- [ ] **Step 3: Commit**

```bash
git add workers/internal/
git commit -m "refactor: categoria bonus substitui orphan nos consumidores"
```

---

## Task 12: Medir o delta contra um clone de prod (antes de qualquer deploy)

**Files:**
- Modify: `workers/cmd/backfill-recategorize/main.go`

- [ ] **Step 1: Fazer o dry-run reportar o delta por categoria**

Hoje o dry-run só imprime a distribuição atual. Adicionar a contagem de transições
usando `CountProjectionDrift` sem bound de data, agrupada por `(from, to)`, e um resumo
por campanha com o total de linhas que mudam.

- [ ] **Step 2: Restaurar o dump de prod num PG descartável e medir**

Run (§4.8 — nunca contra o DB de prod):
```bash
docker run -d --name rc-delta -e POSTGRES_PASSWORD=x -p 15433:5432 postgres:16
psql -h localhost -p 15433 -U postgres -f c:/tmp/backup-prod-AAAA-MM-DD.sql
cd workers && go run ./cmd/backfill-recategorize --dsn "postgres://postgres:x@localhost:15433/radiocheck" --all
```
Expected: relatório de quantas tocadas mudam de categoria, por transição e por campanha.
**Confira a data do dump antes de confiar** — os snapshots em `c:\tmp` costumam estar velhos.

- [ ] **Step 3: Entregar o número pro Dereck e esperar a decisão de alcance**

O alcance retroativo é decisão dele (D9). Nada de `--apply` em prod antes disso.

- [ ] **Step 4: Commit**

```bash
git add workers/cmd/backfill-recategorize/main.go
git commit -m "feat(backfill): dry-run reporta delta de categoria por campanha"
```

---

## Task 13: Documentação

**Files:**
- Modify: `docs/architecture/distribution-rules.md`, `docs/features/override-time-window.md`,
  `docs/architecture/detection-count-consistency.md`, `docs/features/detections-day-plan.md`
- Create: `docs/features/quota-aware-categorization.md`

- [ ] **Step 1: Escrever o doc canônico da feature**

`docs/features/quota-aware-categorization.md` com o header YAML obrigatório
(`status: implementado`, `ultima-verificacao: AAAA-MM-DD`, `codigo-relacionado` listando
categorizer.go, distribution_rules.go, 0063, 0064), a regra em 5 passos e a tabela-verdade.

- [ ] **Step 2: Corrigir os docs que ficaram falsos**

- `override-time-window.md`: a seção "count=0" e a tabela de decisão inteira descrevem o
  comportamento antigo. Reescrever apontando pro doc novo.
- `distribution-rules.md`: a regra de categorização canônica mudou.
- `detection-count-consistency.md`: `out_slot` saiu da base financeira.
- `detections-day-plan.md`: o "Plano do dia" mudou de semântica.

- [ ] **Step 3: Atualizar o índice**

Adicionar a linha nova em `docs/README.md` e no mapa de consulta do `CLAUDE.md`.

- [ ] **Step 4: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs: categorizacao por cota (spec 2026-08-14)"
```

---

## ⛔ Trava de deploy — esta branch NÃO pode chegar em prod antes da Task 6

Descoberto na review da Task 4 (2026-08-14). A migration **0064 já renomeia todo o dado
`orphan` → `bonus`**, mas enquanto a **0065 (Task 6)** não existir, a `daily_play_summary` e
a `daily_play_summary_for` continuam calculando `bonus` como
`COUNT(*) FILTER (WHERE category = 'orphan')` — que passa a ser **sempre 0**.

No mesmo estado, seguem lendo `'orphan'`: `insights.go:405` e `:519`, `daily_summary.go`,
`detections.go:1744` e o `DayDetailModal` do frontend.

Efeito se subir assim: **bonificação lê zero em todas as telas e relatórios.** As Tasks 6, 7,
9 e 11 têm que estar na mesma leva. Não existe deploy parcial seguro desta branch.

---

## Ordem de deploy

1. **Backend primeiro** (regra: frontend sobe sozinho no push, backend só com `deploy.sh`).
   Migrations 0063 + 0064 passam pelo `shadow_migration_test` automaticamente.
2. Confirmar no boot que o `projrecon` está curando: `docker compose logs api | grep projrecon`.
   As últimas 48h convergem sozinhas em até 15 min.
3. **Só então** o frontend (Tasks 9/10), senão a UI nova lê campos que a API ainda não devolve.
4. Backfill retroativo (Task 12) apenas depois da decisão do Dereck sobre o alcance.
