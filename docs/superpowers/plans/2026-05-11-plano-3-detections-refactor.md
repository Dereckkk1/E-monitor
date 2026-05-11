# Plano 3 — Detections Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refatorar `/detections` pra usar o `<DistributionGrid mode="view">` (mesmo componente do wizard) com badges nas 6 cores (cinza/verde/vermelho/azul/amarelo/roxo), alimentado pela view `daily_play_summary`. Substitui o `DetectionsCalendar` atual que só mostrava detecções flat sem comparação com o plano.

**Architecture:** A página `/detections` muda de "lista de detections plotada num calendário" pra "agregado por (station, material, dia) com 6 cores". Reusa `DistributionGrid` em modo `view` (cliques abrem `DayDetailModal` atualizado em vez do override popover). Adiciona uma **CoverageSummary** no topo mostrando o resumo do mês (verde/vermelho/esperado totalizados).

**Tech Stack:** Mesmo do Plano 2 — React 19 · Vite · React Query · plain CSS. Toda infraestrutura já existe.

**Spec de referência:** [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../specs/2026-05-11-campaign-wizard-design.md) §7
**Planos anteriores:** [Plano 1 — Foundations](2026-05-11-plano-1-foundations.md) e [Plano 2 — Wizard Frontend](2026-05-11-plano-2-wizard-frontend.md) devem estar mergeados ou na mesma branch.

---

## Pré-requisitos

- [ ] Plano 2 mergeado ou branch atual contém `DistributionGrid`, `BadgePill`, `DayCell`, `useDailySummary`, `useCampaignMaterials`, `useDistributionRules`, `useMaterials`, `useMaterialTypes`. Confirme: `ls frontend/src/components/DistributionGrid.jsx`.
- [ ] Backend rodando com migrations 0016-0018 aplicadas. Confirme `curl http://localhost:8080/v1/internal/campaigns/<id>/daily-summary?from=2026-06-01&to=2026-06-30 -H "Authorization: Bearer $TOKEN"` retorna JSON (array vazio se sem dados).

---

## Estrutura de arquivos

### Modificações

| Arquivo | Mudança |
|---------|---------|
| `frontend/src/pages/DetectionsPage.jsx` | Reescrita parcial: substitui `DetectionsCalendar` por `DistributionGrid`, adiciona hidratação de materials, calcula rows/cellData, integra CoverageSummary |
| `frontend/src/components/DayDetailModal.jsx` | Atualizada pra receber `(station, material, date)` em vez de `(station, day)` e mostrar breakdown por categoria |

### Novos

| Arquivo | Responsabilidade |
|---------|------------------|
| `frontend/src/components/CoverageSummary.jsx` | Header de stats do mês: total esperado, in_slot, deficit, bonus, out_slot, out_date |

### Removidos / deprecados

| Arquivo | Razão |
|---------|-------|
| `frontend/src/components/DetectionsCalendar.jsx` | Substituído por DistributionGrid. Remover após confirmar que nenhum outro lugar usa. |

### Docs

| Arquivo | Conteúdo |
|---------|----------|
| `docs/detections-view.md` | Guia operacional da `/detections` refatorada |

---

# Phase A — Page refactor (4 tasks)

---

### Task 1: Adicionar hidratação de materials e rules no DetectionsPage

**Files:**
- Modify: `frontend/src/pages/DetectionsPage.jsx`

A página atual só carrega campaigns + stations + detections. Precisamos adicionar: `useCampaignMaterials`, `useMaterials` (do client da campanha), `useDistributionRules`, `useMaterialTypes`, `useDailySummary`. Tudo só ativa quando uma campanha está selecionada.

- [ ] **Step 1: Modificar os imports no topo**

Abra `frontend/src/pages/DetectionsPage.jsx`. Localize o bloco de imports `import { useCampaigns, useDetections, useStations, useClients, useStreamHealth } from '../api/hooks'` e SUBSTITUA por:

```js
import {
  useCampaigns, useStations, useClients, useStreamHealth,
  useCampaignMaterials, useMaterials, useDistributionRules,
  useMaterialTypes, useDailySummary,
} from '../api/hooks'
```

`useDetections` é removida desta lista — não precisamos mais (vamos usar `useDailySummary` que agrega tudo).

- [ ] **Step 2: Adicionar as queries no componente DetectionsPage**

Localize a função `DetectionsPage` (em torno da linha 178). Encontre o bloco que usa `useDetections(detectionFilters)` e SUBSTITUA tudo daquele bloco (até `confirmedDetections`) por:

```js
  // Find the selected campaign object (we'll use start/end dates from it)
  const selectedCampaign = useMemo(
    () => campaigns.find(c => c.id === selectedCampaignId) ?? null,
    [campaigns, selectedCampaignId]
  )

  // Date range strings for daily-summary query (YYYY-MM-DD)
  const fromISO = useMemo(() => period.start.toISOString().slice(0, 10), [period])
  const toISO   = useMemo(() => period.end.toISOString().slice(0, 10), [period])

  // Hydrate the campaign's material library
  const { data: clientLibrary = [] } = useMaterials(selectedCampaign?.client_id ?? null)
  const materialsById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary]
  )

  // Campaign-scoped data
  const { data: campaignMaterials = [] } = useCampaignMaterials(selectedCampaignId || null)
  const { data: distributionRules = [] } = useDistributionRules(selectedCampaignId || null)
  const { data: materialTypes = [] }     = useMaterialTypes()
  const {
    data: summary = [],
    isLoading: loadingSummary,
    isFetching,
    refetch,
  } = useDailySummary(selectedCampaignId || null, fromISO, toISO)

  const showDetections = !!selectedCampaignId
  const isLoadingData  = showDetections && (loadingSummary || isFetching)
```

(`useDetections`, `loadingDetections`, `detections`, `confirmedDetections` são todos substituídos por `summary` da view.)

- [ ] **Step 3: Verificar build**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/frontend"
npm run build
```

Pode ter erros de "X is not defined" porque o resto do arquivo ainda referencia `confirmedDetections`, `detections`, etc. ISSO É ESPERADO — vamos consertar na próxima task. Por enquanto só verifique que os imports e as novas queries estão sintaticamente corretas (sem erro de syntax).

Se houver erro de syntax, conserte antes de continuar. Erros de "undefined variable" são OK por agora.

- [ ] **Step 4: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add frontend/src/pages/DetectionsPage.jsx
git commit -m "wip(detections): add material library + daily-summary queries"
```

---

### Task 2: Construir rows e cellData para o DistributionGrid

**Files:**
- Modify: `frontend/src/pages/DetectionsPage.jsx`

DistributionGrid (do Plano 2) precisa de:
- `month: Date`
- `campaignStart` / `campaignEnd: ISO strings`
- `stations: Array<station>`
- `rows: Array<{stationId, materialId, materialTitle, typeColor, ruleSummary, extraRules}>`
- `cellData: Map<key, {expected, in_slot, deficit, bonus, out_slot, out_date, hasOverride}>`

Vamos calcular tudo a partir do summary + campaignMaterials + distributionRules.

- [ ] **Step 1: Adicionar o computo de rows e cellData**

No `DetectionsPage`, depois das queries (Task 1 deixou prontas) e ANTES do `return`, adicione:

```js
  // Color lookup for material types
  const typeColorById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t.color])),
    [materialTypes]
  )

  // Build "rows" — one per (station, material) combination that exists in this campaign
  const rows = useMemo(() => {
    const r = []
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat) continue
      for (const sid of cm.target_stations) {
        const matching = distributionRules.filter(rule =>
          rule.material_id === cm.material_id && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          materialId: cm.material_id,
          materialTitle: mat.title,
          typeColor: typeColorById[mat.type_id] ?? '#94a3b8',
          ruleSummary: first
            ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
            : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [campaignMaterials, distributionRules, materialsById, typeColorById])

  // Build cellData map from daily summary
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of summary) {
      const key = `${s.station_id}|${s.material_id}|${s.for_date.slice(0, 10)}`
      m.set(key, { ...s, hasOverride: false })
    }
    return m
  }, [summary])

  // The month being displayed (first of selectedMonth)
  const monthDate = useMemo(() => {
    const [y, m] = selectedMonth.split('-').map(Number)
    return new Date(y, m - 1, 1)
  }, [selectedMonth])

  // Filter rows by station search (existing search behavior, now filters BOTH stations and material rows)
  const filteredRows = useMemo(() => {
    const tokens = tokenize(search)
    if (tokens.length === 0) return rows
    return rows.filter(r => {
      const station = stationCatalog.find(s => s.id === r.stationId)
      if (!station) return false
      const fields = [
        station.name ?? '',
        station.city ?? '',
        station.state ?? '',
        station.band ?? '',
        station.frequency_mhz != null ? String(station.frequency_mhz) : '',
        r.materialTitle ?? '',
      ]
      return tokens.every(tok =>
        fields.some(f => f.toLowerCase().includes(tok.toLowerCase())))
    })
  }, [rows, search, stationCatalog])
```

Note: `tokenize` is already imported from `../utils/search` at the top of the file (used by the existing search logic).

- [ ] **Step 2: Build (still expecting some unresolved errors)**

```bash
cd frontend && npm run build
```

Will likely still have errors because `confirmedDetections`, `targetStations`, `filteredTargetStations` etc. are still referenced. Move to Task 3.

- [ ] **Step 3: Commit progress**

```bash
cd ..
git add frontend/src/pages/DetectionsPage.jsx
git commit -m "wip(detections): compute rows and cellData for grid"
```

---

### Task 3: Substituir `<DetectionsCalendar>` por `<DistributionGrid mode="view">`

**Files:**
- Modify: `frontend/src/pages/DetectionsPage.jsx`

- [ ] **Step 1: Atualizar o import**

No topo de `DetectionsPage.jsx`, REMOVA:
```js
import DetectionsCalendar from '../components/DetectionsCalendar'
```

ADICIONE:
```js
import DistributionGrid from '../components/DistributionGrid'
```

- [ ] **Step 2: Remover lógica antiga e atualizar o render**

No `DetectionsPage`, **REMOVA** os blocos antigos:
- `const targetStations = useMemo(...)` — não precisa mais (rows já tem station info via stationCatalog)
- `const filteredTargetStations = useMemo(...)` — substituído por `filteredRows`

Localize o bloco do render que tem:
```jsx
) : confirmedDetections.length === 0 ? (
  <EmptyNoDetections periodLabel={monthLabel(selectedMonth)} />
) : filteredTargetStations.length === 0 ? (
  <EmptyNoDetections periodLabel={monthLabel(selectedMonth)} />
) : (
  <DetectionsCalendar
    stations={filteredTargetStations}
    detections={confirmedDetections}
    period={period}
    onCellClick={(station, dayKey) => setModalCell({ station, dayKey })}
    onStationClick={(station) => setHealthStationId(station.id)}
  />
)}
```

SUBSTITUA por:
```jsx
) : rows.length === 0 ? (
  <EmptyNoRules onCreateCampaign={() => {}} />
) : filteredRows.length === 0 ? (
  <EmptyNoDetections periodLabel={monthLabel(selectedMonth)} />
) : (
  <DistributionGrid
    mode="view"
    month={monthDate}
    campaignStart={selectedCampaign?.start_date}
    campaignEnd={selectedCampaign?.end_date}
    stations={stationCatalog}
    rows={filteredRows}
    cellData={cellData}
    onCellClick={(stationId, materialId, dateISO) =>
      setModalCell({ stationId, materialId, dateISO })}
  />
)}
```

Note: `modalCell` agora carrega `{ stationId, materialId, dateISO }` em vez de `{ station, dayKey }`. Vamos atualizar o `DayDetailModal` na Task 4.

Adicione também um novo empty state `EmptyNoRules` (caso a campanha não tenha regras nem materiais ainda — situação comum em campanhas recém-criadas). Adicione no arquivo (junto com `EmptyNoCampaign` e `EmptyNoDetections`):

```jsx
function EmptyNoRules() {
  return (
    <div className="detection-empty">
      <div className="detection-empty-icon">
        <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
          <rect x="6" y="12" width="36" height="28" rx="4" stroke="currentColor" strokeWidth="2" />
          <path d="M14 22h20M14 28h12M14 34h8" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </div>
      <h3>Campanha sem materiais vinculados</h3>
      <p>Vá em <strong>Campanhas → Editar</strong> pra adicionar materiais e regras de distribuição.</p>
    </div>
  )
}
```

- [ ] **Step 3: Atualizar o modal call**

Localize o bloco `{modalCell && <DayDetailModal ... />}`. Substitua por:

```jsx
{modalCell && (
  <DayDetailModal
    stationId={modalCell.stationId}
    materialId={modalCell.materialId}
    dateISO={modalCell.dateISO}
    campaignId={selectedCampaignId}
    station={stationCatalog.find(s => s.id === modalCell.stationId) ?? null}
    material={materialsById[modalCell.materialId] ?? null}
    cellSummary={cellData.get(`${modalCell.stationId}|${modalCell.materialId}|${modalCell.dateISO}`) ?? null}
    onClose={() => setModalCell(null)}
  />
)}
```

`DayDetailModal` ainda não suporta essa nova assinatura — vamos atualizar na Task 4. Por enquanto vai ignorar as props desconhecidas e funcionar provisoriamente. Se o modal não abrir corretamente, é esperado.

- [ ] **Step 4: Update the station click in DistributionGrid context**

O DetectionsPage antigo passava `onStationClick={(station) => setHealthStationId(station.id)}` pra abrir o HealthDrawer ao clicar no nome da emissora. `DistributionGrid` não tem essa prop. Mantenha o `healthStationId` state e o `<HealthDrawer />` no JSX — mas o trigger pra abrir vai ter que ser via outro path (provavelmente um botão dedicado, ou clicar no avatar da station no header da grade). Por enquanto, deixa o estado pendurado sem trigger — não bloqueia.

Adicione um TODO comment perto do `healthStationId` state:

```js
// TODO F-100: re-wire station click to open HealthDrawer (DistributionGrid station headers don't expose onStationClick yet)
const [healthStationId, setHealthStationId] = useState(null)
```

- [ ] **Step 5: Build deve passar agora**

```bash
cd frontend && npm run build
```

Esperado: clean build. Se ainda tiver erros de variável não definida, leia o erro e remove a referência (é provavelmente um resíduo do código antigo que esquecemos de limpar).

- [ ] **Step 6: Commit**

```bash
cd ..
git add frontend/src/pages/DetectionsPage.jsx
git commit -m "feat(detections): replace DetectionsCalendar with DistributionGrid mode=view"
```

---

### Task 4: Atualizar DayDetailModal pra novo schema

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx`

O modal anterior recebia `(station, dayKey, buckets)` e mostrava detections agrupadas por dia. Agora recebe `(stationId, materialId, dateISO, campaignId, station, material, cellSummary)` e deve mostrar:
- Header com nome da emissora + material + data
- Badges com os 6 valores (expected, in_slot, deficit, bonus, out_slot, out_date)
- Lista das detections do dia (filtradas pelos 3 IDs) com horário + categoria

- [ ] **Step 1: Adicionar hook pra buscar detections do dia**

`useDetections` ainda existe (usado pela página antiga). Continue usando — só passe um filtro mais específico.

Substitua TODO o conteúdo de `frontend/src/components/DayDetailModal.jsx`:

```jsx
import { useDetections } from '../api/hooks'
import BadgePill from './BadgePill'

const CATEGORY_LABEL = {
  in_slot:  { label: 'Dentro da faixa', variant: 'green' },
  out_slot: { label: 'Fora da faixa',   variant: 'yellow' },
  out_date: { label: 'Fora da data',    variant: 'purple' },
  orphan:   { label: 'Bônus (sem regra)', variant: 'blue' },
}

function fmtTime(iso) {
  const d = new Date(iso)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

function fmtDate(iso) {
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

/**
 * Day-detail modal for the /detections grid.
 *
 * Props:
 *  - campaignId: uuid
 *  - stationId: uuid
 *  - materialId: uuid (same UUID as commercial_id in detections)
 *  - dateISO: 'YYYY-MM-DD'
 *  - station: station object (for displaying name)
 *  - material: material object (for displaying title)
 *  - cellSummary: row from daily_play_summary view ({expected, in_slot, deficit, bonus, out_slot, out_date})
 *  - onClose: () => void
 */
export default function DayDetailModal({
  campaignId, stationId, materialId, dateISO,
  station, material, cellSummary,
  onClose,
}) {
  // Fetch detections for this exact (campaign, station, commercial, day)
  const startISO = `${dateISO}T00:00:00.000Z`
  const endISO   = `${dateISO}T23:59:59.999Z`
  const { data: detections = [], isLoading } = useDetections({
    campaign_id: campaignId,
    station_id: stationId,
    start_date: startISO,
    end_date: endISO,
    limit: 200,
  })

  // Filter to just this material (the existing /detections endpoint doesn't have commercial_id filter param)
  const filtered = detections.filter(d => d.commercial_id === materialId)
  const grouped = {
    in_slot:  filtered.filter(d => d.category === 'in_slot'),
    out_slot: filtered.filter(d => d.category === 'out_slot'),
    out_date: filtered.filter(d => d.category === 'out_date'),
    orphan:   filtered.filter(d => d.category === 'orphan'),
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 600 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <div>
            <h3 style={{ margin: 0 }}>
              {material?.title ?? 'Material'} · {station?.name ?? 'Emissora'}
            </h3>
            <p style={{ margin: '4px 0 0', color: '#64748b', fontSize: 13 }}>
              {fmtDate(dateISO)}
            </p>
          </div>
          <button className="modal-close" onClick={onClose} type="button">×</button>
        </div>

        <div className="modal-body" style={{ padding: 20 }}>

          {/* Cell summary badges */}
          {cellSummary && (
            <div style={{
              display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 20,
              padding: 14, background: '#fafbfc', borderRadius: 8, border: '1px solid #e2e8f0',
            }}>
              <SummaryStat label="Esperado" value={cellSummary.expected} variant="gray" />
              <SummaryStat label="Tocou (faixa)" value={cellSummary.in_slot} variant="green" />
              {cellSummary.deficit > 0 && (
                <SummaryStat label="Faltou" value={cellSummary.deficit} variant="red" />
              )}
              {cellSummary.bonus > 0 && (
                <SummaryStat label="Bônus" value={cellSummary.bonus} variant="blue" prefix="+" />
              )}
              {cellSummary.out_slot > 0 && (
                <SummaryStat label="Fora faixa" value={cellSummary.out_slot} variant="yellow" prefix="+" />
              )}
              {cellSummary.out_date > 0 && (
                <SummaryStat label="Fora data" value={cellSummary.out_date} variant="purple" prefix="+" />
              )}
            </div>
          )}

          {isLoading ? (
            <p style={{ color: '#64748b' }}>Carregando detecções…</p>
          ) : filtered.length === 0 ? (
            <p style={{ color: '#64748b' }}>Nenhuma detection registrada nesse dia.</p>
          ) : (
            <DetectionsList grouped={grouped} />
          )}
        </div>
      </div>
    </div>
  )
}

function SummaryStat({ label, value, variant, prefix }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
      <span style={{ fontSize: 10, color: '#64748b', textTransform: 'uppercase', fontWeight: 600 }}>{label}</span>
      <BadgePill variant={variant} value={value} prefix={prefix ?? ''} />
    </div>
  )
}

function DetectionsList({ grouped }) {
  return (
    <div>
      {Object.entries(grouped).map(([cat, list]) => {
        if (list.length === 0) return null
        const { label, variant } = CATEGORY_LABEL[cat]
        return (
          <div key={cat} style={{ marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <BadgePill variant={variant} value={list.length} />
              <strong style={{ fontSize: 13 }}>{label}</strong>
            </div>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0, fontSize: 12 }}>
              {list.map(d => (
                <li key={d.id} style={{
                  display: 'flex', justifyContent: 'space-between',
                  padding: '6px 10px', background: '#fafbfc',
                  borderRadius: 6, marginBottom: 4,
                }}>
                  <span style={{ fontFamily: 'monospace' }}>{fmtTime(d.detected_at)}</span>
                  <span style={{ color: '#64748b' }}>
                    conf: {(d.confidence * 100).toFixed(0)}% · hash {d.hash_count}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 2: Build**

```bash
cd frontend && npm run build
```

Esperado: clean. Pode dar warning sobre `useDetections` arguments diferentes — verifique o assinatura atual em `hooks.js` e adapte.

- [ ] **Step 3: Commit**

```bash
cd ..
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(detections): rewrite DayDetailModal for (station, material, date) tuple with category breakdown"
```

---

# Phase B — Coverage summary + polish (3 tasks)

---

### Task 5: CoverageSummary component

**Files:**
- Create: `frontend/src/components/CoverageSummary.jsx`

Header com totais do mês: esperado, in_slot, deficit, bonus, out_slot, out_date. Mostra "Cobertura do plano: 87%" como número grande.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/CoverageSummary.jsx`:

```jsx
import BadgePill from './BadgePill'

/**
 * Top-of-page coverage summary for /detections.
 *
 * Props:
 *  - summary: Array<daily_play_summary row>
 *    Sums all rows (across station × material × day) to produce period totals.
 */
export default function CoverageSummary({ summary }) {
  const totals = summary.reduce((acc, s) => ({
    expected: acc.expected + (s.expected ?? 0),
    in_slot:  acc.in_slot  + (s.in_slot  ?? 0),
    deficit:  acc.deficit  + (s.deficit  ?? 0),
    bonus:    acc.bonus    + (s.bonus    ?? 0),
    out_slot: acc.out_slot + (s.out_slot ?? 0),
    out_date: acc.out_date + (s.out_date ?? 0),
  }), { expected: 0, in_slot: 0, deficit: 0, bonus: 0, out_slot: 0, out_date: 0 })

  const coveragePct = totals.expected > 0
    ? Math.round((totals.in_slot / totals.expected) * 100)
    : null

  const coverageColor = coveragePct == null ? '#94a3b8'
    : coveragePct >= 95 ? '#15803d'
    : coveragePct >= 80 ? '#a16207'
    : '#b91c1c'

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 24,
      padding: '14px 18px', marginBottom: 14,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        <span style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', fontWeight: 600, letterSpacing: '0.04em' }}>
          Cobertura do plano
        </span>
        <span style={{ fontSize: 32, fontWeight: 700, color: coverageColor, lineHeight: 1 }}>
          {coveragePct != null ? `${coveragePct}%` : '—'}
        </span>
      </div>

      <div style={{ height: 40, width: 1, background: '#e2e8f0' }} />

      <Stat label="Esperado" value={totals.expected} variant="gray" />
      <Stat label="Tocou (faixa)" value={totals.in_slot} variant="green" />
      {totals.deficit > 0 && <Stat label="Faltou" value={totals.deficit} variant="red" />}
      {totals.bonus > 0 && <Stat label="Bônus" value={totals.bonus} variant="blue" prefix="+" />}
      {totals.out_slot > 0 && <Stat label="Fora faixa" value={totals.out_slot} variant="yellow" prefix="+" />}
      {totals.out_date > 0 && <Stat label="Fora data" value={totals.out_date} variant="purple" prefix="+" />}
    </div>
  )
}

function Stat({ label, value, variant, prefix }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
      <span style={{ fontSize: 10, color: '#64748b', textTransform: 'uppercase', fontWeight: 600 }}>{label}</span>
      <BadgePill variant={variant} value={value} prefix={prefix ?? ''} />
    </div>
  )
}
```

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/components/CoverageSummary.jsx
git commit -m "feat(detections): add CoverageSummary header component"
```

---

### Task 6: Integrar CoverageSummary no DetectionsPage

**Files:**
- Modify: `frontend/src/pages/DetectionsPage.jsx`

- [ ] **Step 1: Import e render**

Adicione no topo de `DetectionsPage.jsx`:
```jsx
import CoverageSummary from '../components/CoverageSummary'
```

Localize o bloco onde `<DistributionGrid ...>` é renderizado. ANTES dele (mas dentro do mesmo branch condicional onde a grid aparece), adicione:

```jsx
<CoverageSummary summary={summary} />
```

Resultado final do branch:
```jsx
) : (
  <>
    <CoverageSummary summary={summary} />
    <DistributionGrid
      mode="view"
      month={monthDate}
      campaignStart={selectedCampaign?.start_date}
      campaignEnd={selectedCampaign?.end_date}
      stations={stationCatalog}
      rows={filteredRows}
      cellData={cellData}
      onCellClick={(stationId, materialId, dateISO) =>
        setModalCell({ stationId, materialId, dateISO })}
    />
  </>
)}
```

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/DetectionsPage.jsx
git commit -m "feat(detections): show CoverageSummary above grid"
```

---

### Task 7: Re-wire station click pra abrir HealthDrawer

**Files:**
- Modify: `frontend/src/components/DistributionGrid.jsx`
- Modify: `frontend/src/pages/DetectionsPage.jsx`

A grade não tem callback de clique no header da emissora. Vamos adicionar uma prop opcional `onStationClick(stationId)`.

- [ ] **Step 1: DistributionGrid — adicionar prop e wire**

Em `frontend/src/components/DistributionGrid.jsx`:

1. Adicione `onStationClick` aos props no JSDoc e na assinatura da função:

Mude a assinatura `export default function DistributionGrid({ month, campaignStart, ..., onCellClick, mode = 'edit' })` pra incluir `onStationClick`:
```js
export default function DistributionGrid({
  month, campaignStart, campaignEnd, stations, rows, cellData,
  onCellClick, onStationClick, mode = 'edit',
}) {
```

2. Localize o `<div>` que renderiza o station header (com `<StationAvatar />` e o nome). Envolva-o num button clicável SE `onStationClick` foi passado. Substitua:

```jsx
<div style={{
  gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
  padding: '11px 14px', display: 'flex', alignItems: 'center', gap: 10,
}}>
  <StationAvatar station={station} size={30} />
  <div>
    <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
    <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
      {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
    </span>
  </div>
</div>
```

por:

```jsx
<div style={{
  gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
  padding: '11px 14px', display: 'flex', alignItems: 'center', gap: 10,
  cursor: onStationClick ? 'pointer' : 'default',
}} onClick={onStationClick ? () => onStationClick(station.id) : undefined}>
  <StationAvatar station={station} size={30} />
  <div>
    <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
    <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
      {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
    </span>
  </div>
</div>
```

- [ ] **Step 2: DetectionsPage — passar a prop**

Em `frontend/src/pages/DetectionsPage.jsx`, no `<DistributionGrid>`, adicione `onStationClick`:

```jsx
<DistributionGrid
  mode="view"
  ...existing props
  onStationClick={(stationId) => setHealthStationId(stationId)}
/>
```

Remova o TODO comment de F-100 que tava perto do `useState(healthStationId)` — agora está resolvido.

- [ ] **Step 3: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/components/DistributionGrid.jsx frontend/src/pages/DetectionsPage.jsx
git commit -m "feat(grid): add onStationClick callback for station headers"
```

---

# Phase C — Cleanup + smoke + docs (5 tasks)

---

### Task 8: Remover DetectionsCalendar (se não usado em outros lugares)

**Files:**
- Delete: `frontend/src/components/DetectionsCalendar.jsx`

- [ ] **Step 1: Verificar que ninguém mais usa**

```bash
grep -rn "DetectionsCalendar" "c:/Users/marke/Desktop/Programas/Radiocheck/frontend/src/" 2>&1
```

Se aparece SÓ em `DetectionsCalendar.jsx` (a definição), pode deletar.
Se aparece em outros lugares, ABORTA esta task — esses lugares precisam ser refatorados primeiro.

- [ ] **Step 2: Verificar utils relacionados**

```bash
ls "c:/Users/marke/Desktop/Programas/Radiocheck/frontend/src/pages/detections/" 2>&1
```

Se existe pasta `detections/utils.js` com função `bucketDetections` que só é usada pelo `DetectionsCalendar` + `DayDetailModal` antigo, considere remover também (ou deixar — não custa nada manter código não-utilizado em arquivo separado).

- [ ] **Step 3: Deletar arquivo + commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
rm frontend/src/components/DetectionsCalendar.jsx
git add -A frontend/src/components/DetectionsCalendar.jsx
cd frontend && npm run build && cd ..
```

Se build passa, commit:

```bash
git commit -m "chore: remove unused DetectionsCalendar component"
```

Se build falha (alguém ainda usa), restaure o arquivo: `git restore frontend/src/components/DetectionsCalendar.jsx` e investigue.

---

### Task 9: Smoke test manual

**Files:** (nenhum — só verificação)

- [ ] **Step 1: Iniciar API + frontend dev server**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose up -d
cd ../../frontend
npm run dev
```

Em outro terminal, abrir `http://localhost:5173`.

- [ ] **Step 2: Smoke test sequencial**

1. Login com bootstrap admin
2. Navegar pra `/campaigns/new` e criar uma campanha de teste com 2 emissoras + 1 material + 1 regra (Mon-Fri, 08:00-10:00, 3x/dia). Pula pra `/detections`.
3. Selecionar a campanha. Esperado: ver grade com badges cinza (no plays yet) no mês corrente.
4. CoverageSummary no topo deve mostrar "Cobertura do plano: 0%" + "Esperado: N" onde N = total de inserções no mês.
5. Clicar em uma célula → DayDetailModal abre com header `Material · Emissora · Data` + badges de stats + lista vazia ("Nenhuma detection nesse dia").
6. Clicar no header da emissora → HealthDrawer abre.
7. Trocar pra "Mês anterior" — grade atualiza.
8. Buscar pelo nome de uma das emissoras na search box — grade filtra.

Anotar qualquer comportamento estranho.

- [ ] **Step 3: Se houver bugs, fix + commit**

Cada bug fix vira um commit separado.

- [ ] **Step 4: Sem código pra commitar nesta task (se smoke passou limpo)**

---

### Task 10: Atualizar docs

**Files:**
- Create: `docs/detections-view.md`
- Modify: `docs/follow-ups-fase2.md`

- [ ] **Step 1: Criar `docs/detections-view.md`**

```markdown
# Detections View — Guia Operacional

Documenta a `/detections` refatorada pelo Plano 3.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md) §7
> Plano: [`docs/superpowers/plans/2026-05-11-plano-3-detections-refactor.md`](superpowers/plans/2026-05-11-plano-3-detections-refactor.md)

## O que mudou

A `/detections` antes era um calendário station × day com hits flat. Não comparava com o plano da campanha. Agora é uma grade station × material × day idêntica à etapa 4 do wizard, alimentada pela view `daily_play_summary`.

## Cores

Veja [`distribution-rules.md`](distribution-rules.md) pra detalhes da semântica. Resumo:

| Cor | Significado |
|-----|-------------|
| Cinza | Esperado (plano) |
| Verde | Tocou dentro da faixa |
| Vermelho | Saldo devedor (esperado − tocou − fora-faixa) |
| Azul (+N) | Bônus (excesso na faixa OU sem regra) |
| Amarelo (+N) | Tocou na data, fora da faixa |
| Roxo (+N) | Tocou fora da data da campanha |

## Como usar

1. Selecione uma campanha no dropdown do topo
2. Use as pills "Mês atual" / "Mês anterior" ou o input de mês pra navegar
3. **CoverageSummary** no topo mostra:
   - Cobertura % (verde ÷ esperado) — verde se ≥95%, amarelo 80-94%, vermelho <80%
   - Totais do mês por categoria
4. **Busca** filtra emissora E material (nome, cidade, dial, banda, título do material)
5. **Clique em célula**: abre `DayDetailModal` com breakdown por categoria + lista de detecções
6. **Clique no header da emissora**: abre `HealthDrawer` com saúde do stream

## Empty states

- **Sem campanha selecionada** → instrução pra escolher uma
- **Campanha sem materiais/regras** → CTA pra editar a campanha
- **Sem detections no período** → mensagem neutra

## Limitações conhecidas

- Pesquisar texto não é fuzzy — só substring case-insensitive em campos pré-definidos
- `daily_play_summary` é VIEW não-materializada — pode lentificar com volume alto (ver F-84)
- Não há export CSV/PDF do relatório (futuro)
```

- [ ] **Step 2: Atualizar follow-ups**

Append em `docs/follow-ups-fase2.md` (após F-99 do Plano 2):

```markdown
## Detections Refactor (Plano 3) — Follow-ups

- **F-100** — Botão dedicado de "Saúde da emissora" no header de cada bloco da grade, em vez de clique no avatar (acessibilidade melhor). Atualmente o clique no header todo abre o HealthDrawer.
- **F-101** — Filtros de categoria na toolbar (ex: "Mostrar só células com déficit"). Útil pra investigar problemas rapidamente.
- **F-102** — Export do relatório (CSV/PDF) com os totais + breakdown por (station, material, day) pra entregar ao cliente.
- **F-103** — Indicador visual de "última atualização" da view (a `daily_play_summary` é live mas usuário não sabe). Mostrar timestamp do último refetch.
```

- [ ] **Step 3: Commit**

```bash
git add docs/detections-view.md docs/follow-ups-fase2.md
git commit -m "docs: add detections view guide + Plan 3 follow-ups"
```

---

### Task 11: Final review + ajuste de build

**Files:** (verificação final)

- [ ] **Step 1: Lint + build + git status**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/frontend"
npm run lint 2>&1 | tail -20
npm run build
```

Esperado: build limpa. Lint pode ter warnings, mas não erros novos introduzidos pelo Plano 3.

- [ ] **Step 2: Total commit count do plano**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git log --oneline master..HEAD | wc -l
git log --oneline -15
```

Esperado: ~10 commits do Plano 3 visíveis no topo.

- [ ] **Step 3: Final commit** (se houver alguma mudança não-commitada)

Se tudo limpo, nada a commitar.

---

## Critério de conclusão do Plano 3

- [ ] `npm run build` passa sem erros
- [ ] DetectionsPage renderiza a DistributionGrid com badges nas 6 cores
- [ ] CoverageSummary mostra % de cobertura + totais
- [ ] DayDetailModal abre com breakdown por categoria
- [ ] HealthDrawer abre ao clicar no header da emissora
- [ ] DetectionsCalendar removido (ou justificado se mantido)
- [ ] Docs `detections-view.md` criado
- [ ] Follow-ups F-100 a F-103 registrados

## Conclusão de Plano 1, 2 e 3

Após Plano 3, todo o blueprint da spec está implementado:

- **Plano 1**: Backend foundations (3 migrations, 6 repos, categorizer, 7 handlers, router wiring)
- **Plano 2**: Wizard frontend (10 components, 4 wizard steps, orchestrator, 2 admin pages)
- **Plano 3**: Detections refactor (reusa o grid, summary stats, modal atualizado)

Próximo passo natural: PR, merge em master, deploy em staging, treinar operador.
