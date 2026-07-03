# Override: recategorização automática + visibilidade + auto-conserto — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer com que criar/editar/apagar um `distribution_override` reclassifique automaticamente as veiculações, mostrar na tela qual faixa (regra ou override) rege cada dia, e permitir consertar de onde o problema é visto — eliminando a recorrência de "editei e não mudou".

**Architecture:** (A) o handler de override passa a disparar `RecategorizeForOverride`, um wrapper fino sobre o `recategorizeScope` já existente, que atualiza `detections.category` **e** a projeção `detection_campaigns`. (B) o bloco "Plano do dia" do `DayDetailModal` recebe o override real e mostra a faixa que rege como "ajuste do dia", explicando o `out_slot`. (C) os hooks de override passam a invalidar `detections` e um botão abre o `OverridePopover` de dentro do modal.

**Tech Stack:** Go (pgx, chi, testify) no backend; React + Vite + @tanstack/react-query no frontend. Backend com TDD (harness `newTestDB`); frontend sem test runner → gates são `npm run lint` + `npm run build` + verificação manual.

**Spec:** [`docs/superpowers/specs/2026-07-03-override-recat-and-visibility-design.md`](../specs/2026-07-03-override-recat-and-visibility-design.md)

**Pré-requisito de teste (backend):** os testes de `catalog` usam `newTestDB`, que exige um Postgres de teste. Ver memória `test-db-native-pg-shadows-docker`: usar o `rc-test-pg` em `localhost:15432` na rede `docker_default` (NÃO o `rc-prodcopy`/5544). Falhas pré-existentes do harness (isolamento de stations, partição, FK user) não são regressão desta mudança — confirme que o pacote que você tocou passa.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|--------|-----------------|------|
| `workers/internal/catalog/distribution_rules.go` | motor de recat | **Modify**: novo método `RecategorizeForOverride` |
| `workers/internal/catalog/distribution_rules_override_test.go` | teste de recat×override | **Modify**: novo teste do método |
| `workers/internal/api/handlers/distribution_overrides.go` | handler HTTP do override | **Modify**: interface `OverrideRecategorizer` + campo `Recat` + disparo em Upsert/Delete; `Repo` vira interface `overrideStore` p/ testabilidade |
| `workers/internal/api/handlers/distribution_overrides_test.go` | teste do handler | **Modify**: teste unitário de disparo com mocks |
| `workers/cmd/api/main.go` | wiring | **Modify**: injetar `distRulesRepo` no handler |
| `frontend/src/api/hooks.js` | hooks react-query | **Modify**: `useUpsertOverride`/`useDeleteOverride` invalidam `detections` |
| `frontend/src/components/DayDetailModal.jsx` | modal do dia | **Modify**: busca override, `buildDayPlan`/`DayPlan` cientes de override, botão "Ajustar faixa" |
| `frontend/src/components/OverridePopover.jsx` | popover de override | **Modify** (Task 8, opcional): aviso de divergência |
| `docs/architecture/distribution-rules.md` | doc | **Modify**: seção recat-em-override + leitura "por que fora" |

---

## Task 1: `RecategorizeForOverride` (backend, motor)

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go` (após `RecategorizeForMaterial`, ~linha 396)
- Test: `workers/internal/catalog/distribution_rules_override_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Adicione ao fim de `distribution_rules_override_test.go` (dentro do package `catalog`, imports já presentes):

```go
// TestRecategorizeForOverride: criar override que exclui a tocada reclassifica
// in_slot→out_slot (detections E projeção detection_campaigns); apagar o override
// reverte pra regra (out_slot→in_slot). Prova o disparo dos DOIS caminhos com o
// escopo preciso de célula (campaign, type, station, dia).
func TestRecategorizeForOverride(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "recat-ov1-cli"})
	require.NoError(t, err)
	now := time.Now()
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "recat-ov1", ClientID: cli.ID,
		StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 1),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "recat-ov1-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "recat-ov1-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/ro1", MasterSHA256: "recat-ov1-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Recat OV1 FM", Band: "FM", StreamURL: "http://example.com/recatov1",
	})
	require.NoError(t, err)

	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{},
		StartDate:   now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 1),
		WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "23:59", PlaysPerDay: 10,
	})
	require.NoError(t, err)

	detectedAt := now
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detectedAt, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM distribution_overrides WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// for_date = dia-calendário SP da tocada (mesma expressão do insert-path).
	var forDate time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT ($1::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date`, detectedAt).Scan(&forDate))

	catOf := func(t *testing.T) (string, string) {
		var base, proj string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&base))
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`, det.ID, cmp.ID).Scan(&proj))
		return base, proj
	}

	// Precondição: regra cobre o dia → in_slot nas duas tabelas.
	base, proj := catOf(t)
	require.Equal(t, "in_slot", base)
	require.Equal(t, "in_slot", proj, "projeção deve nascer in_slot")

	// Override plays_expected=0 (faixa inerte) → out_slot.
	_, err = pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, type_id, station_id, for_date, plays_expected, time_start, time_end)
		VALUES ($1, $2, $3, $4, 0, '00:00', '00:01')`,
		cmp.ID, typeID, stat.ID, forDate)
	require.NoError(t, err)

	require.NoError(t, rules.RecategorizeForOverride(ctx, cmp.ID, typeID, stat.ID, forDate))
	base, proj = catOf(t)
	require.Equal(t, "out_slot", base, "override deve reclassificar base p/ out_slot")
	require.Equal(t, "out_slot", proj, "override deve reclassificar a projeção também")

	// Apagar o override → volta pra regra (in_slot).
	_, err = pool.Exec(ctx,
		`DELETE FROM distribution_overrides WHERE campaign_id=$1 AND type_id=$2 AND station_id=$3 AND for_date=$4`,
		cmp.ID, typeID, stat.ID, forDate)
	require.NoError(t, err)

	require.NoError(t, rules.RecategorizeForOverride(ctx, cmp.ID, typeID, stat.ID, forDate))
	base, proj = catOf(t)
	require.Equal(t, "in_slot", base, "após delete, recat deve reverter p/ regra (in_slot)")
	require.Equal(t, "in_slot", proj, "projeção deve reverter também")
}
```

- [ ] **Step 2: Rodar e confirmar que falha (método não existe)**

Run: `cd workers && go test ./internal/catalog/ -run TestRecategorizeForOverride -v`
Expected: FAIL de compilação — `rules.RecategorizeForOverride undefined`.

- [ ] **Step 3: Implementar o método**

Em `distribution_rules.go`, logo após o fim de `RecategorizeForMaterial` (~linha 396):

```go
// RecategorizeForOverride re-classifica as detections de UMA célula (campaign,
// type, station, dia) após criar/editar/apagar um override. Escopo preciso: só
// aquele dia/tipo/estação. recategorizeScope lê rules+overrides ao vivo, então
// serve tanto p/ Upsert (aplica o override) quanto p/ Delete (célula reverte pra
// regra). O tail atualiza detections.category E detection_campaigns.category.
func (dr *DistributionRules) RecategorizeForOverride(ctx context.Context,
	campaignID, typeID, stationID uuid.UUID, forDate time.Time) error {
	return dr.recategorizeScope(ctx, campaignID, &typeID, []uuid.UUID{stationID}, forDate, forDate)
}
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd workers && go test ./internal/catalog/ -run TestRecategorizeForOverride -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_override_test.go
git commit -m "feat(catalog): RecategorizeForOverride reclassifica célula após mudança de override"
```

---

## Task 2: Handler dispara recat em Upsert/Delete (backend)

**Files:**
- Modify: `workers/internal/api/handlers/distribution_overrides.go`
- Test: `workers/internal/api/handlers/distribution_overrides_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Adicione a `distribution_overrides_test.go` (package `handlers`). Usa mocks — sem DB:

```go
type mockOverrideStore struct{ upserted, deleted bool }

func (m *mockOverrideStore) Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error {
	m.upserted = true
	return nil
}
func (m *mockOverrideStore) Delete(ctx context.Context, c, t, s uuid.UUID, d time.Time) error {
	m.deleted = true
	return nil
}

type recatCall struct {
	campaign, typ, station uuid.UUID
	date                   time.Time
}
type mockOverrideRecat struct{ ch chan recatCall }

func (m *mockOverrideRecat) RecategorizeForOverride(ctx context.Context, c, t, s uuid.UUID, d time.Time) error {
	m.ch <- recatCall{c, t, s, d}
	return nil
}

func TestDistributionOverridesHandler_Upsert_FiresRecat(t *testing.T) {
	campID := uuid.New()
	typeID := uuid.New()
	statID := uuid.New()
	recat := &mockOverrideRecat{ch: make(chan recatCall, 1)}
	h := &DistributionOverridesHandler{Repo: &mockOverrideStore{}, Recat: recat}

	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	body := `{"type_id":"` + typeID.String() + `","station_id":"` + statID.String() +
		`","for_date":"2026-07-01","plays_expected":13,"time_start":"00:00","time_end":"23:59"}`
	req := httptest.NewRequest("PUT", "/campaigns/"+campID.String()+"/distribution-overrides",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	select {
	case c := <-recat.ch:
		if c.campaign != campID || c.typ != typeID || c.station != statID {
			t.Fatalf("recat chamado com escopo errado: %+v", c)
		}
		if c.date.Format("2006-01-02") != "2026-07-01" {
			t.Fatalf("recat chamado com data errada: %s", c.date.Format("2006-01-02"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upsert não disparou RecategorizeForOverride")
	}
}
```

Garanta os imports no topo do arquivo de teste: `context`, `net/http`, `net/http/httptest`, `strings`, `time`, `github.com/go-chi/chi/v5`, `github.com/google/uuid`, `radiocheck/internal/catalog`.

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `cd workers && go test ./internal/api/handlers/ -run TestDistributionOverridesHandler_Upsert_FiresRecat -v`
Expected: FAIL de compilação — `Recat` não existe no struct e `Repo` não aceita `*mockOverrideStore`.

- [ ] **Step 3: Implementar — interface do store, campo Recat, disparo**

Em `distribution_overrides.go`: (a) adicione `context` aos imports; (b) troque o tipo de `Repo` por interface e adicione `Recat`:

```go
// overrideStore é a fatia de catalog.DistributionOverrides que o handler usa —
// interface p/ permitir mock no teste sem DB.
type overrideStore interface {
	Upsert(ctx context.Context, in catalog.UpsertOverrideInput) error
	Delete(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

// OverrideRecategorizer redispara a recat de uma célula após mudança de override.
// Satisfeita por *catalog.DistributionRules.
type OverrideRecategorizer interface {
	RecategorizeForOverride(ctx context.Context, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error
}

type DistributionOverridesHandler struct {
	Repo  overrideStore
	Recat OverrideRecategorizer
}
```

Em `Upsert`, entre o `if err := h.Repo.Upsert(...)` (após o bloco de erro) e o `w.WriteHeader(http.StatusNoContent)`:

```go
	if h.Recat != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = h.Recat.RecategorizeForOverride(ctx, campaignID, p.TypeID, p.StationID, date)
		}()
	}
```

Em `Delete`, o mesmo bloco antes do `w.WriteHeader(http.StatusNoContent)` (usa `p.TypeID`, `p.StationID`, `date` já parseados no handler).

- [ ] **Step 4: Rodar e confirmar que passa (Upsert + Delete + os testes antigos)**

Run: `cd workers && go test ./internal/api/handlers/ -run TestDistributionOverridesHandler -v`
Expected: PASS em todos (inclusive os de bad-input existentes).

- [ ] **Step 5: (opcional) espelhar o teste pra Delete**

Copie `TestDistributionOverridesHandler_Upsert_FiresRecat` como `..._Delete_FiresRecat`, trocando o método pra `DELETE`, `r.Delete(...)`, e o mesmo corpo JSON (o Delete lê `type_id/station_id/for_date` do body). Rode e confirme PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/distribution_overrides.go workers/internal/api/handlers/distribution_overrides_test.go
git commit -m "feat(api): handler de override dispara RecategorizeForOverride em upsert/delete"
```

---

## Task 3: Wiring no main.go (backend)

**Files:**
- Modify: `workers/cmd/api/main.go:475`

- [ ] **Step 1: Injetar o recategorizador**

Em `main.go`, na construção do handler (~linha 475), troque:

```go
		DistributionOverrides: &handlers.DistributionOverridesHandler{Repo: distOverRepo},
```
por:
```go
		DistributionOverrides: &handlers.DistributionOverridesHandler{Repo: distOverRepo, Recat: distRulesRepo},
```

(`distRulesRepo` já existe em `main.go:110`; `*catalog.DistributionRules` satisfaz `OverrideRecategorizer`.)

- [ ] **Step 2: Verificar cross-compile (o que o deploy faz — regra 6.1)**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`
Expected: sem erros.

- [ ] **Step 3: Commit**

```bash
git add workers/cmd/api/main.go
git commit -m "feat(api): wire distRulesRepo como recategorizador do handler de override"
```

---

## Task 4: Hooks de override invalidam `detections` (frontend)

Sem isso, após A reclassificar no backend, a grade/modal continuariam mostrando a categoria velha em cache.

**Files:**
- Modify: `frontend/src/api/hooks.js` (`useUpsertOverride` ~880, `useDeleteOverride` ~895)

- [ ] **Step 1: Adicionar invalidação de detections**

Em `useUpsertOverride`, no `onSuccess`, após as invalidações existentes de `distribution-overrides` e `daily-summary`, adicione:

```js
      qc.invalidateQueries({ queryKey: ['detections'] })
```

Faça o mesmo no `onSuccess` de `useDeleteOverride`.

- [ ] **Step 2: Lint + build**

Run: `cd frontend && npm run lint && npm run build`
Expected: sem erros. (NÃO rode `npm install` — regra 5.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "fix(frontend): hooks de override invalidam detections (recat backend reflete na grade)"
```

---

## Task 5: `DayPlan` mostra a faixa que rege como "ajuste do dia" (frontend)

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx` (`buildDayPlan` ~96, `DayPlan` ~1219, invocação de `<DayPlan>` ~332, import ~3)

- [ ] **Step 1: Buscar o override do dia no modal**

No topo do arquivo, adicione `useDistributionOverrides` ao import de hooks (linha 3):

```js
import { useDetections, useCreateManualBatchDetection, useDistributionOverrides } from '../api/hooks'
```

Dentro de `DayDetailModal`, junto aos outros hooks de dados (perto de `useDetections`, ~linha 261):

```js
  // Override do dia (fonte de verdade da célula quando existe — precede a regra).
  const { data: dayOverrides = [] } = useDistributionOverrides(campaignId, dateISO, dateISO)
  const override = dayOverrides.find(o => o.type_id === typeId && o.station_id === stationId) ?? null
```

E passe pro `<DayPlan>` (~linha 332):

```jsx
          <DayPlan
            rules={rules}
            override={override}
            dateISO={dateISO}
            detections={filtered}
            cellSummary={cellSummary}
            materialTitleById={materialTitleById}
          />
```

- [ ] **Step 2: `buildDayPlan` trata override**

Troque a assinatura e adicione o caminho de override no início de `buildDayPlan` (~linha 96):

```js
function buildDayPlan({ rules, override = null, dateISO, detections, expected }) {
  // Override supersede as regras nesta célula+dia (categorizer.go / recatClassifyTailSQL).
  // A janela do override é a única que rege; atribuímos in_slot a ela (±15min).
  if (override) {
    const s = hhmmToSec(override.time_start), e = hhmmToSec(override.time_end)
    let played = 0, changedWindow = 0
    for (const det of detections) {
      if (det.category !== 'in_slot') continue
      const t = spSecOfDay(det.detected_at)
      if (t >= s - SLOT_TOLERANCE_SEC && t <= e + SLOT_TOLERANCE_SEC) played++
      else changedWindow++
    }
    return {
      override: { ...override, played },
      applicable: [], notApplicable: [], played: {}, changedWindow,
      sumTargets: override.plays_expected ?? 0, overrideLikely: true,
    }
  }

  const applicable = rules.filter(r => ruleAppliesOn(r, dateISO))
  // ... resto do corpo existente inalterado ...
```

Mantenha todo o corpo atual após esse bloco. Só duas mudanças no caminho de regra existente: (1) a assinatura ganhou `override = null`; (2) o `return` do fim (linha ~131, hoje `return { applicable, notApplicable, played, changedWindow, sumTargets, overrideLikely }`) passa a incluir `override: null` p/ o shape casar com o early-return:

```js
  return { override: null, applicable, notApplicable, played, changedWindow, sumTargets, overrideLikely }
```

- [ ] **Step 3: `DayPlan` recebe `override`, repassa e renderiza a banda "ajuste do dia"**

Em `DayPlan` (~1219), atualize a assinatura, a chamada de `buildDayPlan`, e o destructure (removendo `overrideLikely`, que deixa de ser usado — senão o eslint quebra o build por unused-var):

```js
function DayPlan({ rules, override = null, dateISO, detections = [], cellSummary, materialTitleById }) {
  const [showOff, setShowOff] = useState(false)
  const plan = buildDayPlan({ rules, override, dateISO, detections, expected: cellSummary?.expected ?? null })
  const { applicable, notApplicable, played, changedWindow, sumTargets } = plan
  const gov = plan.override
```

Substitua o bloco `{applicable.length > 0 ? (...) : (...)}` (linhas ~1281-1297) por:

```jsx
      {gov ? (
        <>
          <div style={{ padding: '6px 12px 0', fontSize: 10.5, color: '#92400e', lineHeight: 1.4 }}>
            <span style={{
              display: 'inline-block', padding: '1px 6px', borderRadius: 999,
              background: '#fef3c7', border: '1px solid #fcd34d',
              fontSize: 9.5, fontWeight: 700, letterSpacing: '0.04em',
              textTransform: 'uppercase', fontFamily: 'var(--font-heading)',
            }}>ajuste do dia</span>
            {' '}substitui a regra nesta emissora.
          </div>
          <PlanRow
            rule={{
              id: 'override', time_start: gov.time_start, time_end: gov.time_end,
              plays_per_day: gov.plays_expected ?? 0, material_ids: [],
            }}
            played={gov.played}
            materialTitleById={materialTitleById}
            first
          />
        </>
      ) : applicable.length > 0 ? (
        applicable.map((r, i) => (
          <PlanRow key={r.id} rule={r} played={played[r.id] ?? 0}
            materialTitleById={materialTitleById} first={i === 0} />
        ))
      ) : (
        <p style={{ margin: 0, padding: '8px 12px', fontSize: 12, color: '#64748b', lineHeight: 1.45 }}>
          {expected > 0
            ? 'Sem faixa de regra neste dia (ajuste manual define o esperado).'
            : 'Sem faixa programada; tocadas entram como bônus.'}
        </p>
      )}
```

Como a banda de override já explica o "ajuste manual", remova a nota inferida antiga (linhas ~1300-1304, `{overrideLikely && applicable.length > 0 && (...)}`) — ela vira redundante e menos precisa. `PlanRow` aceita `time_start`/`time_end` no formato "HH:MM" (faz `.slice(0,5)`), que é o que o override retorna.

- [ ] **Step 4: Lint + build**

Run: `cd frontend && npm run lint && npm run build`
Expected: sem erros.

- [ ] **Step 5: Verificação manual**

Suba o frontend (`npm run dev`), abra `/detections` de uma campanha com override de faixa num dia (ex.: reproduzir o cenário 99,5), clique numa célula com override. Confirme: o "Plano do dia" mostra **uma** linha de faixa com o chip "ajuste do dia" e a janela do override (não as da regra); a contagem tocou/alvo bate com o override.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(frontend): Plano do dia mostra a faixa do override como 'ajuste do dia'"
```

---

## Task 6: Explicação do `out_slot` no Plano do dia (frontend)

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx` (`DayPlan`, junto às notas curtas ~1305)

- [ ] **Step 1: Nota de "fora da faixa"**

Em `DayPlan`, calcule o rótulo da janela que rege e o count de out_slot. Após `const gov = plan.override` (Task 5), adicione:

```jsx
  const govWindow = gov
    ? `${gov.time_start}–${gov.time_end}`
    : (applicable.length
        ? applicable.map(r => `${r.time_start.slice(0, 5)}–${r.time_end.slice(0, 5)}`).join(', ')
        : null)
  const govSource = gov ? 'ajuste do dia' : 'da regra'
```

Depois do bloco `{changedWindow > 0 && ...}` (nota de janela editada, ~linha 1305-1309), adicione:

```jsx
      {eff.out_slot > 0 && govWindow && (
        <p style={{ margin: 0, padding: '6px 12px 0', fontSize: 11, color: '#92400e', lineHeight: 1.45 }}>
          {eff.out_slot} tocou fora da faixa {govWindow} ({govSource}) — conta como fora do prazo.
          Tolerância de 15 min já considerada.
        </p>
      )}
```

(`eff` é o summary efetivo já calculado em `DayPlan`; `out_slot` é o count amarelo.)

- [ ] **Step 2: Lint + build**

Run: `cd frontend && npm run lint && npm run build`
Expected: sem erros.

- [ ] **Step 3: Verificação manual**

No mesmo cenário da Task 5, num dia com veiculação fora da faixa (ex.: 20:02 com override 07–19h), confirme a linha: *"1 tocou fora da faixa 07:00–19:00 (ajuste do dia) — conta como fora do prazo. Tolerância de 15 min já considerada."*

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(frontend): Plano do dia explica out_slot nomeando a faixa que rege"
```

---

## Task 7: Botão "Ajustar faixa deste dia" abre o OverridePopover (frontend)

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx` (import de `OverridePopover` + hooks `useUpsertOverride`/`useDeleteOverride`; estado do popover; botão admin; render do popover)

- [ ] **Step 1: Imports e hooks**

No import de hooks (linha 3), acrescente `useUpsertOverride, useDeleteOverride`. Importe o popover:

```js
import OverridePopover from './OverridePopover'
```

Dentro de `DayDetailModal`, junto aos outros hooks:

```js
  const upsertOverride = useUpsertOverride()
  const deleteOverride = useDeleteOverride()
  const [ovAnchor, setOvAnchor] = useState(null) // DOMRect | null
```

- [ ] **Step 2: Botão (admin) no cabeçalho do modal**

No `modal-header`, ao lado do botão de fechar (~linha 326), adicione (admin-only):

```jsx
          {isAdmin && (
            <button
              type="button"
              className="btn btn-ghost btn-sm"
              onClick={e => setOvAnchor(e.currentTarget.getBoundingClientRect())}
              style={{ marginRight: 8 }}
            >
              Ajustar faixa deste dia
            </button>
          )}
```

- [ ] **Step 3: Render do popover + handlers**

Antes do fechamento do componente (após o `modal-body`, ~linha 377), adicione:

```jsx
        <OverridePopover
          open={ovAnchor != null}
          anchorRect={ovAnchor}
          onClose={() => setOvAnchor(null)}
          currentRuleValue={rules.reduce((n, r) =>
            ruleAppliesOn(r, dateISO) ? n + (r.plays_per_day || 0) : n, 0)}
          currentOverrideValue={override ? override.plays_expected : null}
          currentRuleWindows={rules.filter(r => ruleAppliesOn(r, dateISO))
            .map(r => ({ time_start: r.time_start.slice(0, 5), time_end: r.time_end.slice(0, 5) }))}
          currentOverrideWindow={override
            ? { time_start: override.time_start, time_end: override.time_end } : null}
          materialTitle={materialType?.name ?? ''}
          stationName={station?.name ?? ''}
          date={dateISO}
          onApply={async (value, timeStart, timeEnd) => {
            await upsertOverride.mutateAsync({
              campaignId,
              body: {
                type_id: typeId, station_id: stationId, for_date: dateISO,
                plays_expected: value, time_start: timeStart, time_end: timeEnd,
              },
            })
            setOvAnchor(null)
          }}
          onRevert={async () => {
            await deleteOverride.mutateAsync({
              campaignId,
              body: { type_id: typeId, station_id: stationId, for_date: dateISO },
            })
            setOvAnchor(null)
          }}
        />
```

> Confira em `hooks.js` a forma exata dos args de `useUpsertOverride`/`useDeleteOverride` (mutationFn recebe `{ campaignId, body }`). Ajuste as chaves do `body` se o handler esperar nomes diferentes (o `overridePayload` do backend usa `type_id`, `station_id`, `for_date`, `plays_expected`, `time_start`, `time_end`).

- [ ] **Step 4: Lint + build**

Run: `cd frontend && npm run lint && npm run build`
Expected: sem erros.

- [ ] **Step 5: Verificação manual (o fluxo completo do incidente)**

Com o cenário 99,5 (ou equivalente): abra a célula, clique "Ajustar faixa deste dia", alargue a faixa pro dia todo, Aplique. Confirme que **sem re-salvar a regra** a veiculação das 20:02 vira `in_slot` (Plano do dia atualiza; a linha de out_slot some). Isso valida A+B+C ponta a ponta.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(frontend): botão 'Ajustar faixa deste dia' abre OverridePopover no modal"
```

---

## Task 8 (opcional): Aviso de divergência no OverridePopover (frontend)

**Files:**
- Modify: `frontend/src/components/OverridePopover.jsx`

- [ ] **Step 1: Detectar faixa mais estreita que a regra**

Dentro do componente, após os `useState`, calcule (usa `currentRuleWindows` já recebido):

```js
  const ruleSpan = currentRuleWindows.length === 1 ? currentRuleWindows[0] : null
  const isNarrower = ruleSpan && /^[0-2]\d:[0-5]\d$/.test(timeStart) && /^[0-2]\d:[0-5]\d$/.test(timeEnd)
    && (timeStart > ruleSpan.time_start || timeEnd < ruleSpan.time_end)
```

- [ ] **Step 2: Renderizar o aviso soft (não bloqueia)**

Antes do bloco de botões (`<div style={{ display: 'flex', gap: 6 }}>`), adicione:

```jsx
      {isNarrower && (
        <div style={{
          padding: 8, marginBottom: 10, background: '#fffbeb',
          border: '1px solid #fde68a', borderRadius: 6, color: '#92400e', fontSize: 11, lineHeight: 1.4,
        }}>
          ⚠ Faixa mais estreita que a regra ({ruleSpan.time_start}–{ruleSpan.time_end}) —
          veiculações fora dela contam como fora do prazo.
        </div>
      )}
```

- [ ] **Step 3: Lint + build + verificação manual**

Run: `cd frontend && npm run lint && npm run build`. Abra o popover numa célula cuja regra é dia-todo e digite faixa 07:00–19:00; confirme o aviso. Não bloqueia o Aplicar.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/OverridePopover.jsx
git commit -m "feat(frontend): aviso soft quando faixa do override diverge da regra"
```

---

## Task 9: Documentação

**Files:**
- Modify: `docs/architecture/distribution-rules.md`

- [ ] **Step 1: Documentar recat-em-override + leitura "por que fora"**

Na seção "Re-categorizacao", adicione que criar/editar/apagar override dispara `RecategorizeForOverride` (escopo célula), atualizando `detections.category` e `detection_campaigns`. Numa seção nova curta, documente que o `DayDetailModal` mostra a faixa que rege como "ajuste do dia" e explica o `out_slot`. Atualize `ultima-verificacao` pra `2026-07-03` e acrescente os arquivos tocados em `codigo-relacionado`.

- [ ] **Step 2: Commit**

```bash
git add docs/architecture/distribution-rules.md
git commit -m "docs(distribution-rules): recat-em-override + leitura 'por que fora' no modal"
```

---

## Verificação final (antes de abrir PR — regra 6)

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — cross-compile do deploy passa.
- [ ] `cd workers && go test ./internal/catalog/ ./internal/api/handlers/` — pacotes tocados passam (ignore flaky pré-existentes de OUTROS pacotes; ver regra 6.6).
- [ ] `cd frontend && npm run lint && npm run build` — sem `npm install` (regra 5).
- [ ] Fluxo manual A+B+C: alterar override pela UI reclassifica a veiculação **sem** re-salvar a regra.
```
