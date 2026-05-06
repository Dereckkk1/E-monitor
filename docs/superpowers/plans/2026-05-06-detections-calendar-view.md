# Detections Calendar View — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Substituir a tabela plana em `/detections` por uma grade calendário (emissoras × dias) com modal de detalhamento ao clicar numa célula.

**Architecture:** Apenas frontend. Reaproveita os endpoints existentes (`/campaigns`, `/stations`, `/detections`). Adiciona dois componentes novos (`DetectionsCalendar`, `DayDetailModal`) e refatora a parte de listagem em `DetectionsPage.jsx`. Bucketização das detecções por `station_id × YYYY-MM-DD` é feita em memória no cliente; o catálogo de emissoras-alvo vem de `campaign.target_stations` cruzado com `useStations({ limit: 2000 })`.

**Tech Stack:** React 18, Vite, React Query (`@tanstack/react-query`), `react-router-dom`, CSS plain (variáveis de tema em `frontend/src/index.css`). **Sem framework de testes no frontend** — verificação é manual via dev server + Playwright MCP.

**Spec:** [docs/superpowers/specs/2026-05-06-detections-calendar-view-design.md](../specs/2026-05-06-detections-calendar-view-design.md)

**Convenções relevantes do projeto:**
- Fuso de exibição: `America/Sao_Paulo` (já usado em `formatDateTime`).
- `useStations()` retorna `{ data: Station[], total, page, limit, pages }` — acessar `.data` no consumer.
- `useCampaigns()` retorna `Campaign[]` direto (objeto com `target_stations: UUID[]`).
- `useDetections({ campaign_id, start_date, end_date, limit })` retorna `Detection[]` direto.
- Cada `Detection` carrega `station_id`, `station_name`, `commercial_id`, `commercial_name`, `detected_at` (ISO UTC), `evidence_status`.

---

## File Structure

**Created:**
- `frontend/src/pages/detections/utils.js` — helpers puros (range de dias, bucketização, formatação de data curta, `dayKeyOf`).
- `frontend/src/components/DetectionsCalendar.jsx` — grade emissoras × dias.
- `frontend/src/components/DayDetailModal.jsx` — modal de detalhamento diário.
- `docs/detections-calendar.md` — documentação operacional da página.

**Modified:**
- `frontend/src/pages/DetectionsPage.jsx` — substitui o bloco de tabela (linhas 292–333) pela grade; adiciona estado de modal; bumpa `limit` de 200 para 5000.
- `frontend/src/index.css` — estilos para `.calendar-*`, `.day-detail-*`.

**Untouched:** backend, migrations, hooks da API, `AudioPlayer`, `StationAvatar`, `ConfirmModal`.

---

## Task 1: Helpers puros (range de dias + bucketização)

Pure functions são o tijolo do componente. Sem React, sem CSS — só lógica.

**Files:**
- Create: `frontend/src/pages/detections/utils.js`

- [ ] **Step 1: Create the helpers file**

```javascript
// frontend/src/pages/detections/utils.js
const TZ = 'America/Sao_Paulo'

// Returns YYYY-MM-DD in São Paulo time for a given Date or ISO string.
export function dayKeyOf(dateOrIso) {
  const d = dateOrIso instanceof Date ? dateOrIso : new Date(dateOrIso)
  // 'en-CA' yields YYYY-MM-DD natively, and timeZone forces SP local date.
  return d.toLocaleDateString('en-CA', { timeZone: TZ })
}

// Returns array of day keys (YYYY-MM-DD) covering [start, end] inclusive,
// stepping by one day in São Paulo local time.
export function daysBetween(start, end) {
  const startKey = dayKeyOf(start)
  const endKey   = dayKeyOf(end)
  const out = []
  // Iterate by adding 24h to a UTC date anchored at SP midnight.
  // Simpler approach: build Date at noon UTC for each day to avoid DST jumps.
  const cursor = new Date(`${startKey}T12:00:00Z`)
  const stop   = new Date(`${endKey}T12:00:00Z`)
  while (cursor <= stop) {
    out.push(dayKeyOf(cursor))
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return out
}

// Buckets detections into Map<stationId, Map<dayKey, Detection[]>>.
export function bucketDetections(detections) {
  const byStation = new Map()
  for (const d of detections) {
    const key = dayKeyOf(d.detected_at)
    let perStation = byStation.get(d.station_id)
    if (!perStation) {
      perStation = new Map()
      byStation.set(d.station_id, perStation)
    }
    const list = perStation.get(key)
    if (list) list.push(d)
    else perStation.set(key, [d])
  }
  return byStation
}

// Returns 0 if station has no detections that day, else the count.
export function countFor(buckets, stationId, dayKey) {
  return buckets.get(stationId)?.get(dayKey)?.length ?? 0
}

// Returns the array of detections for the cell, sorted by detected_at ASC.
export function detectionsFor(buckets, stationId, dayKey) {
  const list = buckets.get(stationId)?.get(dayKey)
  if (!list) return []
  return [...list].sort((a, b) => new Date(a.detected_at) - new Date(b.detected_at))
}

// Formats a YYYY-MM-DD key as "DD/MM".
export function formatShortDay(dayKey) {
  const [, m, d] = dayKey.split('-')
  return `${d}/${m}`
}

// Returns 3-letter pt-BR weekday for a YYYY-MM-DD key in SP timezone.
const WEEKDAY_FMT = new Intl.DateTimeFormat('pt-BR', { weekday: 'short', timeZone: TZ })
export function weekdayOf(dayKey) {
  const d = new Date(`${dayKey}T12:00:00Z`)
  return WEEKDAY_FMT.format(d).replace('.', '').toUpperCase().slice(0, 3)
}

// Formats "DD/MM/YYYY" for the modal header.
export function formatLongDay(dayKey) {
  const [y, m, d] = dayKey.split('-')
  return `${d}/${m}/${y}`
}

// Formats "HH:MM:SS" in São Paulo time from an ISO string.
const TIME_FMT = new Intl.DateTimeFormat('pt-BR', {
  hour: '2-digit', minute: '2-digit', second: '2-digit', timeZone: TZ,
})
export function formatTimeOnly(iso) {
  return TIME_FMT.format(new Date(iso))
}

// Builds the row label for a station. Examples:
//   "Jovem Pan - FM (94.1)" / "Itajaí / SC"
//   "Massa - AM (1340)" / "Blumenau / SC"
//   "ACME" / "—"   (when band/freq/city/state missing)
export function stationLabel(station) {
  const base = station.band ? `${station.name} - ${station.band}` : station.name
  const withFreq = station.frequency_mhz != null
    ? `${base} (${station.frequency_mhz})`
    : base
  const place = station.city && station.state ? `${station.city} / ${station.state}` : '—'
  return { primary: withFreq, secondary: place }
}
```

- [ ] **Step 2: Verify the file parses (Vite build is fine)**

Run: `cd frontend && npx vite build --mode development` (or just rely on the next task's dev server run).
Expected: no syntax errors. Skip if you'll catch issues at integration time — these are pure functions.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/detections/utils.js
git commit -m "feat(detections): add calendar helpers (day range, bucketing, formatters)"
```

---

## Task 2: `DetectionsCalendar` component (markup + props)

Shell of the grid with no styling yet. Receives data, renders rows and cells with the right click handlers.

**Files:**
- Create: `frontend/src/components/DetectionsCalendar.jsx`

- [ ] **Step 1: Create the component**

```jsx
// frontend/src/components/DetectionsCalendar.jsx
import StationAvatar from './StationAvatar'
import {
  bucketDetections,
  daysBetween,
  countFor,
  formatShortDay,
  weekdayOf,
  stationLabel,
} from '../pages/detections/utils'

export default function DetectionsCalendar({ stations, detections, period, onCellClick }) {
  const days = daysBetween(period.start, period.end)
  const buckets = bucketDetections(detections)

  return (
    <div className="calendar-card">
      <div className="calendar-grid">
        {/* Header row */}
        <div className="calendar-header-row">
          <div className="calendar-corner" />
          <div className="calendar-days-header">
            {days.map(dk => (
              <div key={dk} className="calendar-day-header">
                <div className="calendar-day-date">{formatShortDay(dk)}</div>
                <div className="calendar-day-weekday">{weekdayOf(dk)}</div>
              </div>
            ))}
          </div>
        </div>

        {/* Station rows */}
        {stations.map(station => {
          const { primary, secondary } = stationLabel(station)
          return (
            <div key={station.id} className="calendar-row">
              <div className="calendar-station">
                <StationAvatar station={station} size={40} />
                <div className="calendar-station-text">
                  <div className="calendar-station-name">{primary}</div>
                  <div className="calendar-station-place">{secondary}</div>
                </div>
              </div>
              <div className="calendar-cells">
                {days.map(dk => {
                  const count = countFor(buckets, station.id, dk)
                  if (count === 0) {
                    return <div key={dk} className="calendar-cell calendar-cell-empty">—</div>
                  }
                  return (
                    <button
                      key={dk}
                      type="button"
                      className="calendar-cell calendar-cell-hit"
                      onClick={() => onCellClick(station, dk)}
                      aria-label={`${count} veiculações em ${formatShortDay(dk)}`}
                    >
                      {count}
                    </button>
                  )
                })}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/DetectionsCalendar.jsx
git commit -m "feat(detections): add DetectionsCalendar grid component"
```

---

## Task 3: `DayDetailModal` component

Compact modal: header + list of detections + AudioPlayer reuse. Mirrors the `ConfirmModal` styling pattern but with its own classnames so we can keep both.

**Files:**
- Create: `frontend/src/components/DayDetailModal.jsx`

- [ ] **Step 1: Create the modal**

```jsx
// frontend/src/components/DayDetailModal.jsx
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import {
  detectionsFor,
  formatLongDay,
  formatTimeOnly,
  stationLabel,
} from '../pages/detections/utils'

export default function DayDetailModal({ station, dayKey, buckets, onClose }) {
  const [activePlayerId, setActivePlayerId] = useState(null)

  useEffect(() => {
    function onKey(e) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  if (!station || !dayKey) return null

  const list = detectionsFor(buckets, station.id, dayKey)
  const { primary, secondary } = stationLabel(station)

  return createPortal(
    <div className="day-detail-backdrop" onClick={onClose} role="dialog" aria-modal="true">
      <div className="day-detail-card" onClick={e => e.stopPropagation()}>
        <button className="day-detail-close" onClick={onClose} aria-label="Fechar">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
        </button>

        <div className="day-detail-header">
          <StationAvatar station={station} size={40} />
          <div className="day-detail-station-text">
            <div className="day-detail-station-name">{primary}</div>
            <div className="day-detail-station-place">{secondary}</div>
          </div>
          <div className="day-detail-date">{formatLongDay(dayKey)}</div>
        </div>

        <div className="day-detail-subtitle">
          {list.length} {list.length === 1 ? 'veiculação' : 'veiculações'}
        </div>

        <div className="day-detail-list">
          {list.map(d => (
            <div key={d.id} className="day-detail-item">
              <div className="day-detail-time">{formatTimeOnly(d.detected_at)}</div>
              <div className="day-detail-name">{d.commercial_name}</div>
              <div className="day-detail-audio">
                {d.evidence_status === 'available' ? (
                  <AudioPlayer
                    src={`/v1/internal/detections/${d.id}/evidence`}
                    isPlaying={activePlayerId === d.id}
                    onPlay={() => setActivePlayerId(d.id)}
                    onPause={() => setActivePlayerId(null)}
                  />
                ) : (
                  <span className="text-muted" style={{ fontSize: 11 }}>
                    {d.evidence_status || 'indisponível'}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>,
    document.body
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(detections): add DayDetailModal with audio playback"
```

---

## Task 4: CSS — calendar grid

**Files:**
- Modify: `frontend/src/index.css` (append at end)

- [ ] **Step 1: Append CSS for the calendar grid**

```css
/* ── Detections Calendar ─────────────────────────────────────── */
.calendar-card {
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: 12px;
  overflow: hidden;
}
.calendar-grid {
  display: flex;
  flex-direction: column;
}

.calendar-header-row,
.calendar-row {
  display: grid;
  grid-template-columns: 280px 1fr;
  align-items: stretch;
}
.calendar-header-row {
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--c-surface-2);
  border-bottom: 1px solid var(--c-border);
}
.calendar-row + .calendar-row {
  border-top: 1px solid var(--c-border);
}

.calendar-corner {
  border-right: 1px solid var(--c-border);
}
.calendar-station {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-right: 1px solid var(--c-border);
  min-height: 64px;
}
.calendar-station-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.calendar-station-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--c-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.calendar-station-place {
  font-size: 11px;
  color: var(--c-text-3);
}

.calendar-days-header,
.calendar-cells {
  display: flex;
  overflow-x: auto;
  scrollbar-width: thin;
}
/* Sync horizontal scroll between header and rows: rely on browser-level
   alignment by giving cells the same min-width and no padding outside. */
.calendar-day-header,
.calendar-cell {
  flex: 0 0 64px;
  min-width: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.calendar-day-header {
  flex-direction: column;
  gap: 2px;
  padding: 8px 0;
  border-right: 1px solid var(--c-border);
  font-variant-numeric: tabular-nums;
}
.calendar-day-date    { font-size: 12px; font-weight: 600; color: var(--c-text); }
.calendar-day-weekday { font-size: 10px; color: var(--c-text-3); letter-spacing: 0.04em; }

.calendar-cell {
  height: 64px;
  border-right: 1px solid var(--c-border);
  font-variant-numeric: tabular-nums;
  font-size: 13px;
  font-weight: 600;
  color: var(--c-text);
  background: transparent;
  border-top: none;
  border-bottom: none;
  border-left: none;
}
.calendar-cell-empty {
  color: var(--c-text-3);
  font-weight: 400;
}
.calendar-cell-hit {
  cursor: pointer;
  transition: background 120ms ease;
}
.calendar-cell-hit:hover {
  background: var(--c-action-light);
  color: var(--c-action);
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/index.css
git commit -m "style(detections): add calendar grid styles"
```

---

## Task 5: CSS — day detail modal

**Files:**
- Modify: `frontend/src/index.css` (append at end)

- [ ] **Step 1: Append CSS for the modal**

```css
/* ── Day Detail Modal ────────────────────────────────────────── */
.day-detail-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(6,5,91,0.45);
  backdrop-filter: blur(3px);
  -webkit-backdrop-filter: blur(3px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 9000;
  animation: confirm-fade-in 120ms ease;
}
.day-detail-card {
  position: relative;
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: 14px;
  box-shadow: 0 24px 64px rgba(6,5,91,0.18), 0 4px 16px rgba(6,5,91,0.10);
  padding: 22px 24px 18px;
  width: 100%;
  max-width: 640px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  gap: 14px;
  animation: confirm-slide-in 150ms cubic-bezier(0.34,1.56,0.64,1);
}
.day-detail-close {
  position: absolute;
  top: 12px;
  right: 12px;
  background: transparent;
  border: none;
  color: var(--c-text-3);
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background 120ms ease, color 120ms ease;
}
.day-detail-close:hover { background: var(--c-surface-2); color: var(--c-text); }

.day-detail-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-right: 32px;
}
.day-detail-station-text { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.day-detail-station-name {
  font-family: var(--font-heading);
  font-size: 15px;
  font-weight: 600;
  color: var(--c-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.day-detail-station-place { font-size: 12px; color: var(--c-text-3); }
.day-detail-date {
  font-size: 13px;
  font-weight: 600;
  color: var(--c-text-2);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.day-detail-subtitle {
  font-size: 12px;
  color: var(--c-text-3);
  border-bottom: 1px solid var(--c-border);
  padding-bottom: 8px;
}

.day-detail-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  overflow-y: auto;
  margin: 0 -8px;
  padding: 0 8px;
}
.day-detail-item {
  display: grid;
  grid-template-columns: 90px 1fr auto;
  align-items: center;
  gap: 12px;
  padding: 8px;
  border-radius: 8px;
}
.day-detail-item:hover { background: var(--c-surface-2); }
.day-detail-time {
  font-variant-numeric: tabular-nums;
  font-size: 13px;
  font-weight: 600;
  color: var(--c-text);
}
.day-detail-name {
  font-size: 13px;
  color: var(--c-text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/index.css
git commit -m "style(detections): add day detail modal styles"
```

---

## Task 6: Wire `DetectionsPage.jsx`

Replace the `<table>` block (current lines 292–333) with the new components, add modal state, resolve station catalog, bump the `limit`.

**Files:**
- Modify: `frontend/src/pages/DetectionsPage.jsx`

- [ ] **Step 1: Replace imports and add hook**

Find the existing imports at the top (lines 1–5) and replace them with:

```jsx
import { useState, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useCampaigns, useDetections, useStations } from '../api/hooks'
import RSelect from '../components/RSelect'
import DetectionsCalendar from '../components/DetectionsCalendar'
import DayDetailModal from '../components/DayDetailModal'
import { bucketDetections } from './detections/utils'
```

(`AudioPlayer` is no longer imported here — it's used inside the modal.)

- [ ] **Step 2: Bump the detections limit and add station/modal state**

Inside `DetectionsPage()`, find the `detectionFilters` `useMemo` block (current lines 142–150) and change `limit: 200` to `limit: 5000`:

```jsx
const detectionFilters = useMemo(() => {
  if (!selectedCampaignId) return null
  return {
    campaign_id: selectedCampaignId,
    start_date:  period.start.toISOString(),
    end_date:    period.end.toISOString(),
    limit:       5000,
  }
}, [selectedCampaignId, period])
```

Right after `setActivePlayerId` is removed (see step 3), add new state and pull station catalog. Replace the line `const [activePlayerId, setActivePlayerId] = useState(null)` with:

```jsx
const [modalCell, setModalCell] = useState(null) // { station, dayKey } | null

const { data: stationsResp } = useStations({ limit: 2000 })
const stationCatalog = stationsResp?.data ?? []
```

- [ ] **Step 3: Resolve target stations for the selected campaign**

After `selectedCampaignOption` is computed, add:

```jsx
const selectedCampaign = campaigns.find(c => c.id === selectedCampaignId) ?? null

const targetStations = useMemo(() => {
  if (!selectedCampaign || stationCatalog.length === 0) return []
  const ids = new Set(selectedCampaign.target_stations ?? [])
  return stationCatalog
    .filter(s => ids.has(s.id))
    .sort((a, b) => a.name.localeCompare(b.name, 'pt-BR'))
}, [selectedCampaign, stationCatalog])
```

- [ ] **Step 4: Remove `setActivePlayerId` resets**

In `handleCampaignChange`, `applyPreset`, `handleStartDateChange`, `handleEndDateChange`: delete every line `setActivePlayerId(null)` and replace with `setModalCell(null)` (so the modal closes when filters change). Example:

```jsx
function handleCampaignChange(opt) {
  setSelectedCampaignId(opt?.value ?? '')
  setPeriod(defaultPeriod())
  setModalCell(null)
}
```

Apply the same substitution in the other three handlers.

- [ ] **Step 5: Replace the table block with the calendar**

Find the `<div className="card"><table className="table">...</table></div>` block (current lines 292–333) and replace it with:

```jsx
<DetectionsCalendar
  stations={targetStations}
  detections={detections}
  period={period}
  onCellClick={(station, dayKey) => setModalCell({ station, dayKey })}
/>
```

- [ ] **Step 6: Render the modal at the end of the page**

Just before the closing `</div>` of the outer wrapper (current line 334), add:

```jsx
{modalCell && (
  <DayDetailModal
    station={modalCell.station}
    dayKey={modalCell.dayKey}
    buckets={bucketDetections(detections)}
    onClose={() => setModalCell(null)}
  />
)}
```

Note: `bucketDetections` is called twice (once inside `DetectionsCalendar`, once here). For V1 that's fine — detection lists are small (≤5000). If profiling later shows it matters, lift the bucket map up via `useMemo` and pass to both.

- [ ] **Step 7: Verify the dev server starts and the page renders without console errors**

Run (from repo root):
```
cd frontend && npm run dev
```
Open `http://localhost:3000/detections`. Expected:
- Page loads without runtime errors.
- Empty state shows when no campaign is selected.
- Selecting a campaign that has detections renders the calendar grid.

If errors mention an unresolved import path, double-check `frontend/src/pages/detections/utils.js` exists and the import in `DetectionsPage.jsx` and `DetectionsCalendar.jsx` matches.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/DetectionsPage.jsx
git commit -m "feat(detections): wire calendar grid and day modal in DetectionsPage"
```

---

## Task 7: Manual verification (golden path + edge cases)

No automated tests. Walk through these manually with the dev server up.

**Files:** none (browser-only).

- [ ] **Step 1: No campaign selected**

Navigate to `/detections` with the dropdown empty.
Expected: existing `EmptyNoCampaign` empty state, no calendar visible.

- [ ] **Step 2: Campaign with detections, "Hoje" preset**

Select a campaign, click "Hoje".
Expected: grid with 1 day column, every target station as a row. Cells with detections show counts; cells without show `—`.

- [ ] **Step 3: Switch to "30 dias"**

Click "30 dias".
Expected: 30 day columns with horizontal scroll. Days where the station had no veiculação show `—`. Counts agree with what would be in the old flat list.

- [ ] **Step 4: Click on a non-zero cell**

Click a cell with count ≥ 1.
Expected: modal opens centered, header shows station + city/UF + date, subtitle says "N veiculações", list has N rows sorted by time. Click play → audio loads and plays. Press ESC → modal closes. Click backdrop → modal closes. Click ✕ → modal closes.

- [ ] **Step 5: Click on a zero cell**

Click a cell showing `—`.
Expected: nothing happens (it's not a button).

- [ ] **Step 6: Switch campaign while modal is open**

Open the modal, then change the campaign in the dropdown.
Expected: modal closes, grid re-renders with the new campaign's target stations.

- [ ] **Step 7: Custom date range**

Use the "De" / "Até" date pickers to pick a 3-day window.
Expected: 3 columns. Counts correct.

- [ ] **Step 8: Campaign with target stations but zero detections in the period**

Pick a campaign + period that has no detections.
Expected: existing `EmptyNoDetections` empty state shows (NOT the calendar). This is current page behavior preserved by the `detections.length === 0` branch.

- [ ] **Step 9: Take screenshots and attach to commit message (optional)**

If reviewing visually, capture the grid view and the modal.

- [ ] **Step 10: Commit any tweaks** (if needed during testing)

```bash
git add -A
git commit -m "fix(detections): minor calendar/modal tweaks from manual QA"
```

---

## Task 8: Documentation

Per `CLAUDE.md`: feature docs go in `/docs`, never in `plano_implementacao.md`.

**Files:**
- Create: `docs/detections-calendar.md`

- [ ] **Step 1: Write the doc**

```markdown
# Página de Veiculações — Visão Calendário

A página `/detections` mostra as detecções de uma campanha em formato de **grade calendário**: cada linha é uma emissora-alvo da campanha, cada coluna é um dia do período selecionado, e cada célula traz a quantidade de veiculações daquele dia/emissora.

## Como usar

1. Selecione uma campanha no dropdown no topo da página.
2. Escolha o período via presets (**Hoje**, **7 dias**, **30 dias**) ou via date pickers (**De** / **Até**).
3. A grade lista todas as emissoras-alvo da campanha. Emissoras sem veiculação no período aparecem com células `—`.
4. Clique numa célula com contagem ≥ 1 para abrir o **detalhamento diário**: lista de horários e nomes dos comerciais com player de áudio.

## Notas técnicas

- Bucketização por dia usa o fuso `America/Sao_Paulo`.
- Emissoras-alvo vêm de `campaign.target_stations` cruzadas com o catálogo (`/v1/stations`).
- O limite de detecções por consulta nesta página é **5000** — suficiente para campanhas de até ~30 dias com volume típico. Se uma campanha estourar esse teto, o futuro endpoint de agregação por dia/emissora entra em escopo.
- Esta versão mostra apenas o **veiculado**. A comparação com **plano programado** (PROGRAMADO / DENTRO DA FAIXA / FORA DA FAIXA / FORA DA DATA) depende de integração futura com o sistema externo de plano de mídia e está fora do escopo atual.

## Componentes

- [frontend/src/pages/DetectionsPage.jsx](../frontend/src/pages/DetectionsPage.jsx) — orquestra dropdown, período, estado do modal.
- [frontend/src/components/DetectionsCalendar.jsx](../frontend/src/components/DetectionsCalendar.jsx) — grade emissoras × dias.
- [frontend/src/components/DayDetailModal.jsx](../frontend/src/components/DayDetailModal.jsx) — detalhamento diário.
- [frontend/src/pages/detections/utils.js](../frontend/src/pages/detections/utils.js) — helpers puros (range de dias, bucketização, formatação).
```

- [ ] **Step 2: Commit**

```bash
git add docs/detections-calendar.md
git commit -m "docs(detections): add calendar view operational docs"
```

---

## Self-review — Spec coverage

| Requisito da spec | Coberto por |
|---|---|
| Substituir tabela por grade emissoras × dias | Task 2, 6 |
| Linhas = todas as `target_stations` da campanha | Task 6 (passo 3 — cruzamento `target_stations` × catálogo) |
| Colunas = dias do período (presets/date pickers mantidos) | Task 1 (`daysBetween`) + Task 6 (sem alterar period UI) |
| Cells: count ≥ 1 → clicável; count 0 → `—` não clicável | Task 2 (`calendar-cell-empty` vs `calendar-cell-hit`) |
| Modal com header (avatar, nome, cidade/UF, data) + subtítulo "N veiculações" + lista (play, hora, comercial) | Task 3 |
| Modal sem "Resumo dos Status" e sem "Faixas Programadas" | Task 3 (markup só inclui o necessário) |
| Modal: ESC, backdrop, ✕ fecham | Task 3 (`onKey`, `onClick={onClose}`, botão close) |
| StationRowHeader: avatar + nome `${name} - ${band} (${freq})` + cidade/UF | Task 1 (`stationLabel`) + Task 2 |
| Sem PI / estrelas / "Spot 30" | Task 1 / Task 2 — não renderizados |
| Empty states preservados | Task 6 (não toca os ramos `!showDetections` e `detections.length === 0`) |
| Bump `limit` para 5000 | Task 6 passo 2 |
| Layout: coluna fixa esquerda + scroll horizontal nos dias | Task 4 (CSS `grid-template-columns: 280px 1fr` + `overflow-x: auto`) |
| Sem mudança em backend / migrations / hooks da API | Confirmado — só `useStations` adicionado ao import (já existia) |
| Documentação em `/docs` | Task 8 |
