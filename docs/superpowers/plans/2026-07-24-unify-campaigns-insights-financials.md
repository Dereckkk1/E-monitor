# Padronização financeira /campaigns × /insights — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer `/campaigns` e `/insights` mostrarem os mesmos Impactos / Impactos no target / Investido / CPM no target para o mesmo período, via uma base de cálculo única (`in_slot+bonus`, "base A"), com janela unificada (default mês atual) e rótulo visual do período.

**Architecture:** Um builder SQL compartilhado (`financialBaseCTE`, no estilo de `monthsElapsedSQL`/`ApprovedDetectionsFilter` — **sem migration**) produz, por `(campanha, emissora)`, `plays = Σ(in_slot+bonus)` e `invested` (base A) a partir de `daily_play_summary_for`. Os dois consumidores (`FinancialsByCampaign` e `Insights.aggregateCore`) **embutem o mesmo CTE**, então não podem divergir. Um teste de paridade Go roda os dois caminhos e falha se os números não baterem.

**Tech Stack:** Go (pgx/v5), PostgreSQL (função `daily_play_summary_for` da migration 0052), React (TanStack Query), testes Go com Postgres real (`TEST_DATABASE_URL`).

**Spec:** [docs/superpowers/specs/2026-07-24-unify-campaigns-insights-financials-design.md](../specs/2026-07-24-unify-campaigns-insights-financials-design.md)

---

## Pré-requisitos (uma vez, antes da Task 1)

- [ ] **Subir o Postgres de teste descartável** (memória `test-db-native-pg-shadows-docker`): usar `rc-test-pg` na porta **15432**, rede `docker_default` — **não** o 5432 nativo do Windows nem o `rc-prodcopy` (5544 = PROD).
- [ ] Exportar `TEST_DATABASE_URL` apontando pra ele e rodar as migrations (`migrate up`) nesse banco.
- [ ] Confirmar baseline verde do pacote antes de mexer: `cd workers && go test ./internal/catalog/ -run TestInsights -v` (as falhas conhecidas do harness — `material_ids NOT NULL`, partição, FK user, isolamento stations — não contam; ver memória).

---

## Task 1: Builder SQL compartilhado `financialBaseCTE` + reader `FinancialBase`

**Files:**
- Create: `workers/internal/catalog/financial_base.go`
- Test: `workers/internal/catalog/financial_base_test.go`

O builder devolve uma cadeia de CTEs terminando em `fin_base(campaign_id, station_id, client_id, plays, invested)`. Params são nomes de placeholder (o consumidor controla a numeração), igual a `monthsElapsedSQL`.

- [ ] **Step 1: Escrever o teste que falha (per_insertion)**

```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
)

// Base A per_insertion: plays = in_slot + bonus; invested = unit × (in_slot+bonus).
// Semeia 1 regra de 1 play/dia (expected) + detecções in_slot ACIMA do expected pra
// exercitar o bonus = max(0, in_slot-expected) + orphan.
func TestFinancialBase_PerInsertion(t *testing.T) {
	ctx, pool := newTestDB(t)

	clientID := insSeedClient(t, ctx, pool, "FB PerIns")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, clientID, "Spot FB")
	st := insSeedStation(t, ctx, pool, "FB St", 1000, 60, 40, 20, 50, 30, 30, 50, 20)

	// pricing por-inserção: R$2,00 por inserção do tipo.
	insSeedStationPricing(t, ctx, pool, camp, st, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, st, typeID, 2.0)
	// plano: 1 play/dia (só pra ter expected > 0 e exercitar o bonus).
	insSeedDistributionRule(t, ctx, pool, camp, typeID, []uuid.UUID{st}, "2026-06-01", "2026-06-30", 1)

	// 3 detecções in_slot no dia 10 (expected do dia = 1 → bonus += 2), 1 orphan.
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, st, "orphan", "2026-06-10")

	rows, err := NewCatalogFinancials(pool).FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-06-30"))
	if err != nil {
		t.Fatalf("FinancialBase: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// in_slot=3, expected(dia)=1 → bonus = max(0,3-1)+orphan(1) = 3. plays = 3+3 = 6.
	if rows[0].Plays != 6 {
		t.Errorf("plays = %d, want 6", rows[0].Plays)
	}
	// invested = 2.0 × plays(6) = 12.0
	if !approxEq(rows[0].Invested, 12.0, 0.001) {
		t.Errorf("invested = %v, want 12.0", rows[0].Invested)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestFinancialBase_PerInsertion -v`
Expected: FAIL — `undefined: NewCatalogFinancials`.

- [ ] **Step 3: Implementar `financial_base.go`**

```go
package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogFinancials agrupa a base financeira COMPARTILHADA entre /campaigns
// (FinancialsByCampaign) e /insights (aggregateCore). Antes desta base as duas
// telas tinham cálculos independentes que derivaram (o Modelo B foi aplicado só
// no /insights) — ver docs/features/client-target-pmm.md e a spec
// 2026-07-24-unify-campaigns-insights-financials-design.md.
type CatalogFinancials struct {
	pool *pgxpool.Pool
}

func NewCatalogFinancials(pool *pgxpool.Pool) *CatalogFinancials {
	return &CatalogFinancials{pool: pool}
}

// FinancialBaseRow é uma linha por (campanha, emissora) da base A.
type FinancialBaseRow struct {
	CampaignID uuid.UUID
	StationID  uuid.UUID
	ClientID   uuid.UUID
	Plays      int64   // Σ(in_slot + bonus) na janela
	Invested   float64 // base A: per_insertion Σ unit×(in_slot+bonus); consolidado value×meses
}

// financialBaseCTE devolve uma CADEIA de CTEs terminando em `fin_base`, com
// colunas (campaign_id, station_id, client_id, plays, invested). Os dois
// consumidores EMBUTEM este mesmo texto — é o que garante paridade por
// construção. Params são placeholders (ex.: "$1"), o consumidor controla a
// numeração (mesmo contrato de monthsElapsedSQL).
//
//   campaignsP: uuid[] — NULL = todas as campanhas (daily_play_summary_for).
//   clientP:    uuid   — NULL = todos os clientes (filtro extra em fin_base).
//   stationsP:  uuid[] — '{}' = todas as emissoras.
//   fromP/toP:  date   — janela.
//   todayP:     date   — acúmulo mensal do consolidado.
//
// Base é PRICING-DRIVEN (parte de campaign_station_pricing): emissora sem
// pricing não entra — igual ao /campaigns histórico. Consolidada com zero plays
// na janela ainda soma invested (value×meses).
func financialBaseCTE(campaignsP, clientP, stationsP, fromP, toP, todayP string) string {
	return `
	fb_dps AS (
	    SELECT campaign_id, station_id, type_id, in_slot, bonus
	    FROM daily_play_summary_for(` + fromP + `::date, ` + toP + `::date, ` + campaignsP + `::uuid[])
	    WHERE (` + stationsP + `::uuid[] = '{}' OR station_id = ANY(` + stationsP + `::uuid[]))
	),
	fb_plays AS (
	    SELECT campaign_id, station_id, SUM(in_slot + bonus)::bigint AS plays
	    FROM fb_dps
	    GROUP BY campaign_id, station_id
	),
	fb_perins AS (
	    SELECT d.campaign_id, d.station_id,
	           COALESCE(SUM(tp.unit_value * (d.in_slot + d.bonus)), 0)::numeric AS invested
	    FROM fb_dps d
	    JOIN campaign_station_type_pricing tp
	      ON tp.campaign_id = d.campaign_id
	     AND tp.station_id  = d.station_id
	     AND tp.type_id     = d.type_id
	    GROUP BY d.campaign_id, d.station_id
	),
	fin_base AS (
	    SELECT
	        p.campaign_id,
	        p.station_id,
	        c.client_id,
	        COALESCE(fp.plays, 0)::bigint AS plays,
	        (CASE
	            WHEN p.mode = 'consolidated'
	                THEN COALESCE(p.consolidated_value, 0)::numeric
	                     * ` + monthsElapsedSQL("c.start_date", "c.end_date", todayP, fromP, toP) + `
	            WHEN p.mode = 'per_insertion'
	                THEN COALESCE(fpi.invested, 0)
	            ELSE 0
	        END)::float8 AS invested
	    FROM campaign_station_pricing p
	    JOIN campaigns c ON c.id = p.campaign_id
	    LEFT JOIN fb_plays  fp  ON fp.campaign_id  = p.campaign_id AND fp.station_id  = p.station_id
	    LEFT JOIN fb_perins fpi ON fpi.campaign_id = p.campaign_id AND fpi.station_id = p.station_id
	    WHERE (` + campaignsP + `::uuid[] IS NULL OR p.campaign_id = ANY(` + campaignsP + `::uuid[]))
	      AND (` + clientP + `::uuid IS NULL OR c.client_id = ` + clientP + `::uuid)
	      AND (` + stationsP + `::uuid[] = '{}' OR p.station_id = ANY(` + stationsP + `::uuid[]))
	)`
}

// FinancialBase roda o CTE compartilhado isoladamente (para teste e usos
// diretos). Os consumidores de produção EMBUTEM financialBaseCTE nas próprias
// queries — não chamam este reader — pra manter demografia/agregação em um só
// SELECT. Ordem dos params: $1 campaigns, $2 client, $3 stations, $4 from,
// $5 to, $6 today.
func (r *CatalogFinancials) FinancialBase(ctx context.Context, campaignIDs []uuid.UUID, clientID *uuid.UUID, stationIDs []uuid.UUID, from, to, today interface{ }) ([]FinancialBaseRow, error) {
	panic("replaced in Step 3b")
}
```

> Nota: a assinatura acima usa `interface{}` como stand-in; o Step 3b fixa os tipos reais (`time.Time`) — quebrei em dois passos só pra o diff ficar legível. Se preferir, escreva direto o 3b.

- [ ] **Step 3b: Fixar a assinatura e o corpo do reader**

```go
// (imports: adicionar "time")
func (r *CatalogFinancials) FinancialBase(ctx context.Context, campaignIDs []uuid.UUID, clientID *uuid.UUID, stationIDs []uuid.UUID, from, to, today time.Time) ([]FinancialBaseRow, error) {
	if stationIDs == nil {
		stationIDs = []uuid.UUID{}
	}
	var camps interface{} = campaignIDs
	if campaignIDs == nil {
		camps = nil // NULL = todas
	}
	q := `WITH ` + financialBaseCTE("$1", "$2", "$3", "$4", "$5", "$6") + `
		SELECT campaign_id, station_id, client_id, plays, invested FROM fin_base`
	rows, err := r.pool.Query(ctx, q, camps, clientID, stationIDs, from, to, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinancialBaseRow, 0)
	for rows.Next() {
		var b FinancialBaseRow
		if err := rows.Scan(&b.CampaignID, &b.StationID, &b.ClientID, &b.Plays, &b.Invested); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestFinancialBase_PerInsertion -v`
Expected: PASS.

- [ ] **Step 5: Adicionar teste do consolidado (acúmulo por mês, escopado à janela)**

```go
// Consolidado: invested = value × meses_no_período. Campanha 3 meses × R$1000,
// hoje mês 2, janela = campanha inteira → 2000. Janela = só mês 1 → 1000.
func TestFinancialBase_Consolidated(t *testing.T) {
	ctx, pool := newTestDB(t)
	clientID := insSeedClient(t, ctx, pool, "FB Cons")
	camp := insSeedCampaign(t, ctx, pool, clientID, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "FB Cons St", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	fin := NewCatalogFinancials(pool)

	// Janela = campanha inteira, hoje = 2026-07-15 (mês 2) → 2 ciclos → 2000.
	full, err := fin.FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-08-31"), parseDate("2026-07-15"))
	if err != nil {
		t.Fatalf("FinancialBase full: %v", err)
	}
	if len(full) != 1 || !approxEq(full[0].Invested, 2000, 1) {
		t.Fatalf("full invested = %v, want ~2000", full)
	}

	// Janela = só junho → 1 ciclo → 1000.
	jun, err := fin.FinancialBase(ctx, []uuid.UUID{camp}, nil, []uuid.UUID{},
		parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-07-15"))
	if err != nil {
		t.Fatalf("FinancialBase jun: %v", err)
	}
	if len(jun) != 1 || !approxEq(jun[0].Invested, 1000, 1) {
		t.Fatalf("jun invested = %v, want ~1000", jun)
	}
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestFinancialBase -v`
Expected: PASS (os dois).

- [ ] **Step 7: Commit**

```bash
git add workers/internal/catalog/financial_base.go workers/internal/catalog/financial_base_test.go
git commit -m "feat(catalog): base financeira compartilhada financialBaseCTE (base A in_slot+bonus)"
```

---

## Task 2: `/campaigns` consome a base + ganha janela `from/to`

**Files:**
- Modify: `workers/internal/catalog/campaigns.go:493-596` (`FinancialsByCampaign`)
- Modify: `workers/internal/api/handlers/campaigns.go:157-169` (`Financials` handler)
- Test: `workers/internal/catalog/insights_test.go` (novo teste ao lado dos financials existentes)

- [ ] **Step 1: Teste que falha — janela escopa o consolidado**

```go
// FinancialsByCampaign com janela: consolidado 3 meses × R$1000, hoje mês 2.
// Janela mês inteiro da campanha → 2000. Janela só junho → 1000.
func TestCampaigns_Financials_WindowScopesConsolidated(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewCampaigns(pool)
	client := insSeedClient(t, ctx, pool, "Win")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-08-31")
	st := insSeedStation(t, ctx, pool, "Win St", 1000, 50, 50, 30, 40, 30, 30, 40, 30)
	insSeedStationPricing(t, ctx, pool, camp, st, "consolidated", 1000)

	get := func(from, to string) float64 {
		fins, err := repo.FinancialsByCampaign(ctx, &client, parseDate(from), parseDate(to), parseDate("2026-07-15"))
		if err != nil {
			t.Fatalf("FinancialsByCampaign: %v", err)
		}
		for _, f := range fins {
			if f.CampaignID == camp {
				return f.TotalInvested
			}
		}
		t.Fatalf("campanha não veio")
		return 0
	}
	if v := get("2026-06-01", "2026-08-31"); !approxEq(v, 2000, 1) {
		t.Errorf("full = %v, want ~2000", v)
	}
	if v := get("2026-06-01", "2026-06-30"); !approxEq(v, 1000, 1) {
		t.Errorf("junho = %v, want ~1000", v)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestCampaigns_Financials_WindowScopesConsolidated -v`
Expected: FAIL — `too many arguments in call to repo.FinancialsByCampaign` (assinatura antiga tem só `today`).

- [ ] **Step 3: Reescrever `FinancialsByCampaign`**

Trocar a assinatura e o corpo. Nova assinatura:

```go
func (c *Campaigns) FinancialsByCampaign(ctx context.Context, clientID *uuid.UUID, from, to, today time.Time) ([]CampaignFinancials, error) {
```

Novo corpo da query (substitui as CTEs `per_ins`/`consolidated_inv`/`consolidated_ins`/`target_cov` por consumo do `fin_base`). `$1`=campaigns(NULL), `$2`=client, `$3`=stations('{}'), `$4`=from, `$5`=to, `$6`=today:

```go
	q := `
		WITH ` + financialBaseCTE("$1", "$2", "$3", "$4", "$5", "$6") + `
		SELECT
			fb.campaign_id,
			COALESCE(SUM(fb.invested), 0)::float8                                              AS total_invested,
			COALESCE(SUM(fb.plays), 0)::int                                                    AS total_insertions,
			COALESCE(SUM(fb.plays * COALESCE(st.pmm, 0)), 0)::float8                            AS total_audience,
			COALESCE(SUM(fb.plays * COALESCE(cst.pmm_target, 0)), 0)::float8                    AS total_audience_target,
			COUNT(DISTINCT fb.station_id) FILTER (WHERE cst.pmm_target IS NOT NULL AND fb.plays > 0)::int AS stations_with_target,
			c.fixed_cpm
		FROM fin_base fb
		JOIN campaigns c   ON c.id = fb.campaign_id
		LEFT JOIN stations st ON st.id = fb.station_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = fb.client_id AND cst.station_id = fb.station_id
		GROUP BY fb.campaign_id, c.fixed_cpm
	`
	rows, err := c.pool.Query(ctx, q, nil, clientID, []uuid.UUID{}, from, to, today)
```

> `stations_with_target` agora é **period-dependent** (`fb.plays > 0`), conforme decisão da spec §2.1. `total_audience` usa `plays × pmm` (base A) — idêntico ao que o /insights vai usar.

- [ ] **Step 4: Ajustar o handler pra parsear `from/to` (default mês atual)**

Em `workers/internal/api/handlers/campaigns.go`, no `Financials` (linhas 157-169). Reusar o `parseDateOr` que já existe em `handlers/insights.go` (mesmo pacote `handlers`):

```go
func (h *CampaignsHandler) Financials(w http.ResponseWriter, r *http.Request) {
	scope := auth.ClientScopeFromContext(r.Context())
	q := r.URL.Query()
	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	defaultTo := defaultFrom.AddDate(0, 1, 0).Add(-time.Second)
	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), defaultTo)
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}
	out, err := h.Repo.FinancialsByCampaign(r.Context(), scope, from, to, todaySaoPaulo())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
```

(Adicionar `"time"` aos imports do arquivo se ainda não estiver.)

- [ ] **Step 5: Rodar o teste novo + os financials existentes**

Run: `cd workers && go test ./internal/catalog/ -run 'TestCampaigns_Financials' -v`
Expected: PASS. **Atenção:** `TestCampaigns_Financials_ConsolidatedAccruesByMonth` (insights_test.go:980) chama a assinatura ANTIGA (`FinancialsByCampaign(ctx, &client, parseDate(...))`) — atualizar essa chamada pra `FinancialsByCampaign(ctx, &client, parseDate("2026-06-01"), parseDate("2026-08-31"), parseDate("2026-07-15"))` (janela = campanha inteira, mantém o esperado 2000). Fazer o mesmo em QUALQUER outro teste que chame a função (grep abaixo).

- [ ] **Step 6: Grep por outros callers da assinatura antiga e corrigir**

Run: `cd workers && grep -rn "FinancialsByCampaign(" --include=*.go`
Expected: só o handler (já ajustado) e os testes. Corrigir cada caller de teste pra passar `from, to, today`.

- [ ] **Step 7: Build + commit**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && cd ..
git add workers/internal/catalog/campaigns.go workers/internal/api/handlers/campaigns.go workers/internal/catalog/insights_test.go
git commit -m "feat(campaigns): FinancialsByCampaign na base compartilhada + janela from/to"
```

---

## Task 3: `/campaigns` frontend — seletor de período + hook com `from/to`

**Files:**
- Modify: `frontend/src/api/hooks.js:234-243` (`useCampaignsFinancials`)
- Modify: `frontend/src/pages/CampaignsPage.jsx` (estado do período + render do seletor + passar pro hook)

- [ ] **Step 1: `useCampaignsFinancials` aceita `{ from, to }`**

```js
// Agregado financeiro por campanha — base A (in_slot+bonus), escopado por [from,to].
// audience = Σ(plays × stations.pmm). CPM = (invested / audience) × 1000 no frontend.
export function useCampaignsFinancials({ from, to } = {}) {
  return useQuery({
    queryKey: ['campaigns-financials', from, to],
    queryFn: () => api.get('/campaigns/financials', {
      params: { from: from || undefined, to: to || undefined },
    }).then(r => r.data ?? []),
  })
}
```

- [ ] **Step 2: Estado de período no `CampaignsPage` (default mês atual)**

Perto do estado `competence` (CampaignsPage.jsx:1298-1304), adicionar um período `{from,to}` para os financials. Reusar os helpers de mês do padrão do InsightsPage (declarar no topo do arquivo se não existirem):

```jsx
function firstOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), 1)).toISOString().slice(0, 10)
}
function lastOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth() + 1, 0)).toISOString().slice(0, 10)
}
```

```jsx
  // Período dos NÚMEROS financeiros (impactos/investido/CPM). Default mês atual;
  // "Acumulado" manda from bem antigo → total da campanha até hoje.
  const [finPeriod, setFinPeriod] = useState({ from: firstOfMonthISO(), to: lastOfMonthISO() })
```

- [ ] **Step 3: Passar o período pro hook**

Trocar a linha 1282:

```jsx
  const { data: financialsList = [], isPending: financialsLoading } =
    useCampaignsFinancials({ from: finPeriod.from, to: finPeriod.to })
```

- [ ] **Step 4: Render do seletor (presets Mês atual / Acumulado / Personalizado)**

Adicionar perto do cabeçalho da lista (onde ficam os outros filtros). Presets:

```jsx
  const ACUMULADO_FROM = '2000-01-01'
  function setFinPreset(kind) {
    if (kind === 'mes') setFinPeriod({ from: firstOfMonthISO(), to: lastOfMonthISO() })
    else if (kind === 'acumulado') setFinPeriod({ from: ACUMULADO_FROM, to: lastOfMonthISO() })
  }
  const finIsMes = finPeriod.from === firstOfMonthISO() && finPeriod.to === lastOfMonthISO()
  const finIsAcum = finPeriod.from === ACUMULADO_FROM
```

```jsx
      <div className="campaigns-fin-period">
        <span className="campaigns-fin-period-label">Impactos/CPM no período:</span>
        <button type="button" className={`in-chip${finIsMes ? ' is-active' : ''}`}
                onClick={() => setFinPreset('mes')}>Mês atual</button>
        <button type="button" className={`in-chip${finIsAcum ? ' is-active' : ''}`}
                onClick={() => setFinPreset('acumulado')}>Acumulado</button>
        <input type="date" className="in-date" value={finPeriod.from === ACUMULADO_FROM ? '' : finPeriod.from}
               onChange={e => setFinPeriod(p => ({ ...p, from: e.target.value || firstOfMonthISO() }))} />
        <input type="date" className="in-date" value={finPeriod.to}
               onChange={e => setFinPeriod(p => ({ ...p, to: e.target.value }))} />
        <PeriodLabel from={finPeriod.from} to={finPeriod.to} acumuladoFrom={ACUMULADO_FROM} />
      </div>
```

(`PeriodLabel` vem da Task 6 — se implementar esta task antes, deixar um `<span>` provisório e trocar depois; ou fazer a Task 6 primeiro.)

- [ ] **Step 5: Verificar no browser**

Run: `cd frontend && npm run dev` e abrir `/campaigns`. Trocar entre "Mês atual" e "Acumulado" e confirmar que os números do bloco financeiro (Impactos, Impactos no target, CPM no target, Investimento) recalculam. Comparar com `/insights` da mesma campanha no MESMO período → devem bater.

- [ ] **Step 6: Commit** (sem `npm install` — regra 5 do CLAUDE.md; só editamos `.jsx`/`.js`)

```bash
git add frontend/src/api/hooks.js frontend/src/pages/CampaignsPage.jsx
git commit -m "feat(campaigns-ui): seletor de periodo (default mes atual) no bloco financeiro"
```

---

## Task 4: `/insights` migra a base inteira para a base compartilhada

**Files:**
- Modify: `workers/internal/catalog/insights.go:381-465` (`aggregateCore`) — trocar a base de contagem por `fin_base`.
- Modify: `workers/internal/catalog/insights.go:161-240` (`Compute`) — `Executado` vem da base.
- Test: `workers/internal/catalog/insights_test.go` (atualizar `TargetPMM`, `TargetPMM_ZeroIsNotAbsent`).

- [ ] **Step 1: Reescrever `aggregateCore` para usar `fin_base`**

Trocar a CTE `filt`/`per_station`/`joined` por consumo de `fin_base`. `plays` substitui `det_count`. Adicionar `SUM(fb.invested)` como novo campo do `coreAggregates` (o executado base A). `$1`=campaigns, `$2`=client(NULL), `$3`=stations, `$4`=from, `$5`=to, `$6`=today.

```go
	row := r.pool.QueryRow(ctx, `
		WITH `+financialBaseCTE("$1", "$2", "$3", "$4", "$5", "$6")+`,
		joined AS (
		    SELECT fb.station_id, fb.plays, fb.invested,
		           s.pmm,
		           cst.pmm_target,
		           (s.metadata->'audience_profile'->'gender'      ->>'male')::float    AS male_p,
		           (s.metadata->'audience_profile'->'gender'      ->>'female')::float  AS female_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeAB')::float AS ab_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeC')::float  AS c_p,
		           (s.metadata->'audience_profile'->'socialClass' ->>'classeDE')::float AS de_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range18to24')::float AS r18_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range25to49')::float AS r25_p,
		           (s.metadata->'audience_profile'->'ageRanges'   ->>'range50plus')::float AS r50_p
		    FROM fin_base fb
		    JOIN stations s ON s.id = fb.station_id
		    LEFT JOIN client_station_pmm cst
		           ON cst.client_id = fb.client_id AND cst.station_id = fb.station_id
		)
		SELECT
		    COALESCE(SUM(plays), 0)::bigint                                        AS veic_total,
		    COUNT(DISTINCT station_id) FILTER (WHERE plays > 0)::int               AS stations_count,
		    COUNT(DISTINCT station_id) FILTER (WHERE plays > 0 AND pmm IS NOT NULL)::int        AS stations_with_pmm,
		    COUNT(DISTINCT station_id) FILTER (WHERE plays > 0 AND pmm_target IS NOT NULL)::int AS stations_with_target,
		    COALESCE(SUM(invested), 0)::float8                                     AS executado,
		    COALESCE(SUM(plays * pmm)        FILTER (WHERE pmm IS NOT NULL), 0)::bigint        AS impactos,
		    COALESCE(SUM(plays * pmm_target) FILTER (WHERE pmm_target IS NOT NULL), 0)::bigint AS impactos_target,
		    COALESCE(SUM(plays * pmm * male_p   / 100.0) FILTER (WHERE pmm IS NOT NULL AND male_p   IS NOT NULL), 0)::bigint AS gender_m,
		    COALESCE(SUM(plays * pmm * female_p / 100.0) FILTER (WHERE pmm IS NOT NULL AND female_p IS NOT NULL), 0)::bigint AS gender_f,
		    COALESCE(SUM(plays * pmm * ab_p     / 100.0) FILTER (WHERE pmm IS NOT NULL AND ab_p     IS NOT NULL), 0)::bigint AS cls_ab,
		    COALESCE(SUM(plays * pmm * c_p      / 100.0) FILTER (WHERE pmm IS NOT NULL AND c_p      IS NOT NULL), 0)::bigint AS cls_c,
		    COALESCE(SUM(plays * pmm * de_p     / 100.0) FILTER (WHERE pmm IS NOT NULL AND de_p     IS NOT NULL), 0)::bigint AS cls_de,
		    COALESCE(SUM(plays * pmm * r18_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r18_p    IS NOT NULL), 0)::bigint AS age_18,
		    COALESCE(SUM(plays * pmm * r25_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r25_p    IS NOT NULL), 0)::bigint AS age_25,
		    COALESCE(SUM(plays * pmm * r50_p    / 100.0) FILTER (WHERE pmm IS NOT NULL AND r50_p    IS NOT NULL), 0)::bigint AS age_50
		FROM joined
	`, p.CampaignIDs, nil, p.StationIDs, p.From, p.To, p.Today)
```

Ajustar o `coreAggregates` struct (adicionar `Executado float64`) e o `row.Scan` (adicionar `&out.Executado` na posição correta; **remover** os 4 campos de breakdown `sum_in/out/outdate/orphan` que saíam da CTE antiga — ver Step 2).

> `p.CampaignIDs` nunca é NULL aqui (o /insights sempre tem lista), mas o CTE aceita — `$1` recebe a lista. `$2` (client) = `nil`. `$3` = `p.StationIDs`.

- [ ] **Step 2: Resolver o breakdown de categorias (`VeiculacoesBreakdown`)**

`aggregateCore` deixou de produzir `sum_in/out/outdate/orphan`. O `VeiculacoesBreakdown` (in_slot/out_slot/out_date/extras_orphan) agora sai de `daily_play_summary_for` (tem as colunas). Adicionar uma query curta no `aggregateCore` (ou um helper) que soma da view:

```go
	// breakdown informativo — vem da mesma view; out_slot/out_date NÃO entram
	// em impactos (base A), mas seguem exibidos como categorias.
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(in_slot),0)::bigint, COALESCE(SUM(out_slot),0)::bigint,
		       COALESCE(SUM(out_date),0)::bigint, COALESCE(SUM(bonus),0)::bigint
		FROM daily_play_summary_for($1::date, $2::date, $3::uuid[])
		WHERE ($4::uuid[] = '{}' OR station_id = ANY($4::uuid[]))
	`, p.From, p.To, p.CampaignIDs, p.StationIDs).Scan(
		&out.Breakdown.InSlot, &out.Breakdown.OutSlot, &out.Breakdown.OutDate, &out.Breakdown.ExtrasOrphan)
	if err != nil {
		return nil, err
	}
```

- [ ] **Step 3: `Compute` usa o executado da base + simplifica o consolidado**

Em `Compute` (insights.go:185-210):
- `inv.Executado = core.Executado` (base A) — substitui o override do `consolidatedSummary` para o número de investido.
- Manter `consolidatedSummary` **só** para o `hasConsolidated` bool (flag do frontend + zerar bonificação). `inv.Contratado` e `bon` seguem vindos de `aggregateInvestment` (Modelo B) para per_insertion; quando `hasConsolidated`, `bon = BonificacaoK{}` (como hoje).

```go
	_, hasConsolidated, err := r.consolidatedSummary(ctx, p.CampaignIDs, p.StationIDs, p.From, p.To, p.Today)
	if err != nil {
		return nil, fmt.Errorf("consolidatedSummary: %w", err)
	}
	inv.Executado = core.Executado // base A (fin_base) — igual ao /campaigns
	if hasConsolidated {
		bon = BonificacaoK{}
	}
```

(O `cpmTarget` e o `computeCPM` seguem usando `inv.Executado` — agora base A — sem outra mudança. `core.Impactos`/`ImpactosTarget` já são base A.)

- [ ] **Step 4: Atualizar os testes `TargetPMM`**

Os valores esperados mudam porque a base agora é `plays (in_slot+bonus)` e a emissora precisa de **pricing** pra entrar. Adicionar pricing per_insertion + regra às emissoras dos testes e recalcular. Ex., no `TargetPMM` (insights_test.go:1095), cada `in_slot` seed com plano 1/dia vira `plays = in_slot + bonus`. Para manter o teste simples, semear **1 regra de 1 play/dia** por emissora e detecções `in_slot` iguais ao expected (bonus 0) → `plays = in_slot`, e adicionar pricing. Recalcular impactos/target com os novos `plays`. (O worker deve ajustar os literais esperados rodando o teste e lendo o valor real, confirmando que é o que a base A prevê.)

> Este passo é TDD-invertido (o comportamento mudou de propósito): rodar, ver o número novo, **conferir na mão** que bate com `Σ plays×pmm` e fixar o literal.

- [ ] **Step 5: Rodar os testes de insights**

Run: `cd workers && go test ./internal/catalog/ -run TestInsights -v`
Expected: PASS após ajuste dos literais.

- [ ] **Step 6: Build + commit**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && cd ..
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): dashboard inteiro na base compartilhada (base A); executado = fin_base"
```

---

## Task 5: Teste de paridade `/campaigns` × `/insights` (guard anti-regressão)

**Files:**
- Test: `workers/internal/catalog/financial_parity_test.go`

- [ ] **Step 1: Escrever o teste de paridade**

```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
)

// As duas telas partem do MESMO fin_base — este teste falha se alguém quebrar o
// compartilhamento (ex.: reintroduzir out_slot no investido de um lado só).
// Cenário misto: 1 emissora per_insertion + 1 consolidada, mesma campanha,
// mesmo cliente, mesma janela.
func TestFinancialParity_CampaignsVsInsights(t *testing.T) {
	ctx, pool := newTestDB(t)
	client := insSeedClient(t, ctx, pool, "Paridade")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	typeID, mat := insSeedTypeAndMaterial(t, ctx, pool, client, "Spot Par")

	stPI := insSeedStation(t, ctx, pool, "PI", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stCons := insSeedStation(t, ctx, pool, "Cons", 2000, 60, 40, 20, 50, 30, 30, 50, 20)
	insSeedStationPricing(t, ctx, pool, camp, stPI, "per_insertion", 0)
	insSeedTypePricing(t, ctx, pool, camp, stPI, typeID, 3.0)
	insSeedStationPricing(t, ctx, pool, camp, stCons, "consolidated", 5000)
	insSeedDistributionRule(t, ctx, pool, camp, typeID, []uuid.UUID{stPI, stCons}, "2026-06-01", "2026-06-30", 1)

	// target pmm nas duas
	v300, v900 := 300, 900
	if _, _, err := NewClientStationPMM(pool).BulkUpsert(ctx, client, []TargetPMMEntry{
		{StationID: stPI, PMMTarget: &v300}, {StationID: stCons, PMMTarget: &v900},
	}); err != nil {
		t.Fatalf("target: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id=$1", client) })

	insSeedDetection(t, ctx, pool, camp, mat, stPI, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stPI, "in_slot", "2026-06-11")
	insSeedDetection(t, ctx, pool, camp, mat, stCons, "in_slot", "2026-06-10")

	from, to, today := parseDate("2026-06-01"), parseDate("2026-06-30"), parseDate("2026-06-30")

	// /campaigns
	fins, err := NewCampaigns(pool).FinancialsByCampaign(ctx, &client, from, to, today)
	if err != nil {
		t.Fatalf("campaigns: %v", err)
	}
	var camF CampaignFinancials
	for _, f := range fins {
		if f.CampaignID == camp {
			camF = f
		}
	}

	// /insights (mesma campanha, mesma janela)
	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{camp}, From: from, To: to, Today: today, StationIDs: []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("insights: %v", err)
	}

	if int64(camF.TotalAudience) != core.Impactos {
		t.Errorf("impactos divergem: campaigns=%v insights=%v", camF.TotalAudience, core.Impactos)
	}
	if int64(camF.TotalAudienceTarget) != core.ImpactosTarget {
		t.Errorf("impactos_target divergem: campaigns=%v insights=%v", camF.TotalAudienceTarget, core.ImpactosTarget)
	}
	if !approxEq(camF.TotalInvested, core.Executado, 0.01) {
		t.Errorf("investido diverge: campaigns=%v insights=%v", camF.TotalInvested, core.Executado)
	}
	if camF.StationsWithTarget != core.StationsWithTarget {
		t.Errorf("stations_with_target divergem: campaigns=%d insights=%d", camF.StationsWithTarget, core.StationsWithTarget)
	}
}
```

- [ ] **Step 2: Rodar e ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestFinancialParity -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/financial_parity_test.go
git commit -m "test(catalog): paridade /campaigns x /insights na base compartilhada"
```

---

## Task 6: Rótulo visual do período (`PeriodLabel`) nas duas telas

**Files:**
- Create: `frontend/src/components/PeriodLabel.jsx`
- Create: `frontend/src/components/PeriodLabel.test.mjs` (node --test, puro, sem dep de frontend — regra 5)
- Modify: `frontend/src/pages/CampaignsPage.jsx` (usar no seletor da Task 3)
- Modify: `frontend/src/pages/InsightsPage.jsx` (mostrar junto aos KPIs)

- [ ] **Step 1: Teste do formatador (puro)**

```js
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { formatPeriod } from './PeriodLabel.js'

test('mês de calendário cheio → "jul/2026"', () => {
  assert.equal(formatPeriod('2026-07-01', '2026-07-31'), 'jul/2026')
})
test('range parcial → "01–24 jul 2026"', () => {
  assert.equal(formatPeriod('2026-07-01', '2026-07-24'), '01–24 jul 2026')
})
test('acumulado (from muito antigo) → "acumulado até 24 jul 2026"', () => {
  assert.equal(formatPeriod('2000-01-01', '2026-07-24', '2000-01-01'), 'acumulado até 24 jul 2026')
})
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `node --test frontend/src/components/PeriodLabel.test.mjs`
Expected: FAIL — módulo não existe.

- [ ] **Step 3: Implementar `formatPeriod` (lógica pura) + o componente**

Criar `frontend/src/components/PeriodLabel.js` (a função pura, importável no teste) e `PeriodLabel.jsx` (o wrapper React que a usa). `formatPeriod`:

```js
const MESES = ['jan','fev','mar','abr','mai','jun','jul','ago','set','out','nov','dez']
function d(iso) { const [y,m,day] = iso.split('-').map(Number); return { y, m, day } }
function lastDay(y, m) { return new Date(Date.UTC(y, m, 0)).getUTCDate() }

export function formatPeriod(from, to, acumuladoFrom = null) {
  const t = d(to)
  if (acumuladoFrom && from === acumuladoFrom) {
    return `acumulado até ${String(t.day).padStart(2,'0')} ${MESES[t.m-1]} ${t.y}`
  }
  const f = d(from)
  if (f.y === t.y && f.m === t.m && f.day === 1 && t.day === lastDay(t.y, t.m)) {
    return `${MESES[t.m-1]}/${t.y}`
  }
  if (f.y === t.y && f.m === t.m) {
    return `${String(f.day).padStart(2,'0')}–${String(t.day).padStart(2,'0')} ${MESES[t.m-1]} ${t.y}`
  }
  return `${String(f.day).padStart(2,'0')} ${MESES[f.m-1]} – ${String(t.day).padStart(2,'0')} ${MESES[t.m-1]} ${t.y}`
}
```

```jsx
// PeriodLabel.jsx
import { formatPeriod } from './PeriodLabel.js'
export default function PeriodLabel({ from, to, acumuladoFrom = null }) {
  if (!from || !to) return null
  return <span className="period-label">{formatPeriod(from, to, acumuladoFrom)}</span>
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `node --test frontend/src/components/PeriodLabel.test.mjs`
Expected: PASS (3 testes).

- [ ] **Step 5: Usar nas duas páginas**

- `CampaignsPage.jsx`: já referenciado no seletor (Task 3, Step 4) — importar `PeriodLabel` e trocar o `<span>` provisório.
- `InsightsPage.jsx`: importar e renderizar `<PeriodLabel from={filters.from} to={filters.to} />` junto ao cabeçalho dos KPIs.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/PeriodLabel.js frontend/src/components/PeriodLabel.jsx frontend/src/components/PeriodLabel.test.mjs frontend/src/pages/CampaignsPage.jsx frontend/src/pages/InsightsPage.jsx
git commit -m "feat(ui): rotulo visual de periodo (PeriodLabel) em /campaigns e /insights"
```

---

## Task 7: Atualizar docs

**Files:**
- Modify: `docs/features/client-target-pmm.md` (seção "Base de contagem", ~linha 179)
- Modify: `docs/architecture/detection-count-consistency.md`
- Modify: `docs/features/insights-dashboard.md`
- Create: `docs/architecture/shared-financial-base.md`

- [ ] **Step 1: Reescrever a seção "Base de contagem" do client-target-pmm.md**

Substituir o parágrafo que diz "cada tela espelha a própria base / não tente reconciliar" por: as duas telas passam a usar a **base única `in_slot+bonus`** via `financialBaseCTE`, para o mesmo período/filtro batem; apontar pra `shared-financial-base.md`. Atualizar `ultima-verificacao: 2026-07-24`.

- [ ] **Step 2: Idem em detection-count-consistency.md** — registrar que `/campaigns` e `/insights` convergiram (a divergência de base descrita ali é histórica; exportáveis seguem com base própria).

- [ ] **Step 3: Criar `docs/architecture/shared-financial-base.md`** com header YAML (`status: implementado`, `codigo-relacionado: financial_base.go, campaigns.go, insights.go`), descrevendo o `financialBaseCTE`, os consumidores, as consequências (pricing-driven; sem type_id; bonificação é métrica isolada) e o teste de paridade.

- [ ] **Step 4: Nota em insights-dashboard.md** — nova base + seletor de período (mês atual default) + rótulo.

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: base financeira compartilhada /campaigns x /insights"
```

---

## Task 8: Validação contra cópia de prod (regra 4.8) — Dereck roda

**Files:** nenhum (procedimento operacional).

- [ ] **Step 1: Verificar cross-compile linux (regra 6.1)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: passa para todos os `cmd/*`.

- [ ] **Step 2: Suite completa**

Run: `cd workers && go test ./...`
Expected: verde (descontando flaky conhecidos — regra 6.6).

- [ ] **Step 3: Escrever o script de comparação antes×depois** (SELECT read-only) que roda `FinancialsByCampaign` e o `aggregateCore` de algumas campanhas reais (per_insertion, consolidada, mista) contra uma **cópia** do dump de prod num Postgres descartável, e imprime os números novos ao lado dos antigos. **Dereck executa** e confere o diff (a mudança altera cobrança — regra 7).

- [ ] **Step 4: Só com o OK do Dereck no diff**, seguir o fluxo de deploy (`superpowers:finishing-a-development-branch`): merge da branch em `master` e `./scripts/deploy.sh` (Dereck).

---

## Self-Review (preenchido pelo autor do plano)

- **Cobertura da spec:** §3.1 núcleo→Task 1; §3.2 /campaigns→Task 2; §3.4 janela→Tasks 2/3; §3.3 /insights→Task 4; §3.5 rótulo→Task 6; §5 paridade→Task 5; §6 rollout→Task 8; §7 docs→Task 7. ✔
- **Placeholders:** o Step 3 da Task 1 usa um stand-in `interface{}` **explicitamente substituído** no Step 3b (marcado). Task 4 Step 4 é TDD-invertido por decisão consciente (o comportamento muda de propósito) — instrui conferência manual, não "TODO".
- **Consistência de tipos:** `financialBaseCTE(campaignsP, clientP, stationsP, fromP, toP, todayP)` — 6 params, mesma ordem em Task 1/2/4. `FinancialsByCampaign(ctx, clientID, from, to, today)` — mesma assinatura em Task 2 e no teste da Task 5. `coreAggregates.Executado` novo, usado em Task 4 Step 3.
- **Consequências registradas (adicionar ao spec §2.2):** base **pricing-driven** → emissora com tocada mas sem pricing sai do /insights (novo, além do `type_id`).
