# Tela "Materiais" (`/materials`) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a new "Materiais" screen at `/materials` (admin + client) that lists every material a campaign has (programmed or not, playable) and shows how much is scheduled per station — reusing the `/detections` distribution visual but plan-only (no detections, no R$, no impactos).

**Architecture:** New page `MaterialsPage.jsx` mirroring `DetectionsPage`'s filter flow (competência→campanha→período), plus a new read-only `MaterialPlaybackList` component and an opt-in `summary="plan"` prop on the shared `DistributionGrid`. Pure date helpers are extracted from `DetectionsPage` into `utils/dates.js` and reused. No backend changes — all data comes from existing hooks/endpoints.

**Tech Stack:** React 19 + Vite, react-query, react-router-dom, react-select (`RSelect`), axios (`api/client`). **No frontend test runner exists** (only `eslint` + `vite build`), so each task is verified by `npm run lint` + `npm run build` and a final manual smoke task — not unit tests.

**Spec:** [`docs/superpowers/specs/2026-05-27-materials-page-design.md`](../specs/2026-05-27-materials-page-design.md)

---

## File Structure

**Create**
- `frontend/src/pages/MaterialsPage.jsx` — page: filters, plan summary strip, materials panel, plan grid, empty states.
- `frontend/src/components/MaterialPlaybackList.jsx` — read-only playable list of the campaign's materials, grouped by type, with programmed/not-programmed badge.
- `docs/features/materials-page.md` — operational doc (YAML header).

**Modify**
- `frontend/src/utils/dates.js` — gains the extracted pure date helpers.
- `frontend/src/pages/DetectionsPage.jsx` — imports those helpers instead of defining them locally (mechanical, behavior unchanged).
- `frontend/src/components/DistributionGrid.jsx` — opt-in `summary` prop (`'full'` default | `'plan'`).
- `frontend/src/App.jsx` — register `/materials` route (no `RequireRole`).
- `frontend/src/components/Sidebar.jsx` — "Materiais" link in `AdminNav` + `ClientNav` + new icon.
- `CLAUDE.md` + `docs/README.md` — index line to the new doc.

**Working directory for all commands:** `c:/Users/marke/Desktop/Programas/E-Series/E-monitor` (frontend commands run from `frontend/`).

---

## Task 1: Extract pure date helpers to `utils/dates.js`

Move 8 pure helpers out of `DetectionsPage.jsx` so `MaterialsPage` can reuse them without duplicating logic. Mechanical relocation — no behavior change.

**Files:**
- Modify: `frontend/src/utils/dates.js` (append helpers)
- Modify: `frontend/src/pages/DetectionsPage.jsx:20-88` (remove local defs, add imports)

- [ ] **Step 1: Append the helpers to `utils/dates.js`**

Add at the end of `frontend/src/utils/dates.js` (after `parseLocalDate`):

```js

// ── Month / range helpers (shared by /detections and /materials) ──
// Extracted verbatim from DetectionsPage so both pages share one source.

export function pad2(n) { return String(n).padStart(2, '0') }

export function monthFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`
}

export function isoFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

export function monthToRange(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  const start = new Date(y, m - 1, 1, 0, 0, 0, 0)
  const end   = new Date(y, m, 0, 23, 59, 59, 999)
  return { start, end }
}

export function monthLabel(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
}

export function rangeLabel(startISO, endISO) {
  if (!startISO || !endISO) return ''
  const a = new Date(`${startISO}T00:00:00`)
  const b = new Date(`${endISO}T00:00:00`)
  const sameMonth = a.getMonth() === b.getMonth() && a.getFullYear() === b.getFullYear()
  const monthShort = a.toLocaleDateString('pt-BR', { month: 'short' }).replace('.', '')
  const monthShortB = b.toLocaleDateString('pt-BR', { month: 'short' }).replace('.', '')
  if (sameMonth) {
    return a.getDate() === b.getDate()
      ? `${a.getDate()} de ${monthShort}`
      : `${a.getDate()}–${b.getDate()} ${monthShort}`
  }
  return `${a.getDate()} ${monthShort} – ${b.getDate()} ${monthShortB}`
}

// Intersection of (month range) ∩ (campaign range), returned as ISO date strings.
export function defaultRangeForCampaign(ymStr, campaign) {
  if (!ymStr || !campaign?.start_date || !campaign?.end_date) return { start: '', end: '' }
  const { start: monthStart, end: monthEnd } = monthToRange(ymStr)
  const cStart = parseLocalDate(campaign.start_date)
  const cEnd   = parseLocalDate(campaign.end_date)
  cEnd.setHours(23, 59, 59, 999)
  const start = cStart > monthStart ? cStart : monthStart
  const end   = cEnd   < monthEnd   ? cEnd   : monthEnd
  if (start > end) return { start: '', end: '' }
  return { start: isoFromDate(start), end: isoFromDate(end) }
}

// Format a campaign's [start_date, end_date] as pt-BR "dd-mm-aaaa – dd-mm-aaaa".
export function formatCampaignPeriod(startISO, endISO) {
  if (!startISO || !endISO) return ''
  const a = parseLocalDate(startISO)
  const b = parseLocalDate(endISO)
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return ''
  const fmt = d => `${pad2(d.getDate())}-${pad2(d.getMonth() + 1)}-${d.getFullYear()}`
  return `${fmt(a)} – ${fmt(b)}`
}
```

- [ ] **Step 2: Remove the local definitions from `DetectionsPage.jsx`**

Delete the helper block in `frontend/src/pages/DetectionsPage.jsx` that currently spans from the `// ── Helpers ──` comment through `formatCampaignPeriod` (the functions `pad2`, `monthFromDate`, `isoFromDate`, `monthToRange`, `monthLabel`, `rangeLabel`, `defaultRangeForCampaign`, `formatCampaignPeriod` — currently lines ~24-88). Leave the `ClientMiniAvatar` component and everything after it untouched.

- [ ] **Step 3: Update the import in `DetectionsPage.jsx`**

Replace the existing line:

```js
import { parseLocalDate } from '../utils/dates'
```

with:

```js
import {
  parseLocalDate, monthFromDate, isoFromDate, monthToRange,
  monthLabel, rangeLabel, defaultRangeForCampaign, formatCampaignPeriod,
} from '../utils/dates'
```

- [ ] **Step 4: Lint + build**

Run from `frontend/`:
```bash
npm run lint && npm run build
```
Expected: lint clean (no `no-undef` for the moved helpers, no `no-unused-vars`), build succeeds. If lint flags an unused import, remove only the genuinely-unused name from the import list.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/utils/dates.js frontend/src/pages/DetectionsPage.jsx
git commit -m "refactor(frontend): extract shared month/range date helpers to utils/dates"
```

---

## Task 2: Add opt-in `summary` prop to `DistributionGrid`

`summary='full'` (default) keeps current behavior. `summary='plan'` shows only the gray "programado" pill per row and turns the per-station total cell into a single "N programados" count (no R$/impactos). Layout/columns are unchanged between modes.

**Files:**
- Modify: `frontend/src/components/DistributionGrid.jsx`

- [ ] **Step 1: Add `summary` to the component props**

In the `DistributionGrid({ ... })` destructure (near the top), add `summary = 'full',` alongside the existing props (e.g. right after `inlineStationInfo = false,`).

- [ ] **Step 2: Pass `summary` into the summary cells**

In the rows render, update the two summary-cell call sites:

Change the `RowSummaryCell` usage from:
```jsx
                  <RowSummaryCell
                    row={row}
                    days={days}
                    cellData={cellData}
                    stationTotalWidth={STATION_TOTAL_W}
                  />
```
to:
```jsx
                  <RowSummaryCell
                    row={row}
                    days={days}
                    cellData={cellData}
                    stationTotalWidth={STATION_TOTAL_W}
                    summary={summary}
                  />
```

Change the `StationTotalCell` usage from:
```jsx
                  {ri === 0 && (
                    <StationTotalCell
                      rows={stationRows}
                      days={days}
                      cellData={cellData}
                      pricing={pricingByStation[row.stationId] ?? null}
                      pmm={Number(station.pmm) || 0}
                    />
                  )}
```
to:
```jsx
                  {ri === 0 && (
                    <StationTotalCell
                      rows={stationRows}
                      days={days}
                      cellData={cellData}
                      pricing={pricingByStation[row.stationId] ?? null}
                      pmm={Number(station.pmm) || 0}
                      summary={summary}
                    />
                  )}
```

- [ ] **Step 3: Make `RowSummaryCell` honor `summary='plan'`**

Replace the `RowSummaryCell` function signature and its returned pill cluster. Change the signature from:
```jsx
function RowSummaryCell({ row, days, cellData, stationTotalWidth }) {
```
to:
```jsx
function RowSummaryCell({ row, days, cellData, stationTotalWidth, summary = 'full' }) {
```

Then replace the inner pill cluster `<div>` (the one containing the six `SumPill`s) with:
```jsx
      <div style={{ display: 'flex', gap: 3, alignItems: 'center', flex: 1, minWidth: 0 }}>
        <SumPill variant="dark"   value={expected} />
        {summary !== 'plan' && (
          <>
            <SumPill variant="green"  value={inSlot} dim={inSlot === 0} />
            <SumPill variant="blue"   value={bonus}   prefix="+" dim={bonus === 0} />
            <SumPill variant="red"    value={deficit} prefix="-" dim={deficit === 0} />
            <SumPill variant="yellow" value={outSlot} prefix="+" dim={outSlot === 0} />
            <SumPill variant="purple" value={outDate} prefix="+" dim={outDate === 0} />
          </>
        )}
      </div>
```
(The `expected/inSlot/deficit/bonus/outSlot/outDate` accumulation loop above it stays as-is — `inSlot` etc. are just unused in plan mode, which is fine.)

- [ ] **Step 4: Make `StationTotalCell` honor `summary='plan'`**

Change the `StationTotalCell` signature from:
```jsx
function StationTotalCell({ rows, days, cellData, pricing, pmm }) {
```
to:
```jsx
function StationTotalCell({ rows, days, cellData, pricing, pmm, summary = 'full' }) {
```

Immediately after the signature, add an early plan-mode branch that computes the station's total programmed plays and renders a single neutral pill — before any pricing math:
```jsx
  // Plan-only mode (/materials): the per-station cell shows how many plays are
  // SCHEDULED for the station in the visible range — no R$, no impactos.
  if (summary === 'plan') {
    let stationExpected = 0
    for (const row of rows) {
      for (const d of days) {
        const dateISO = d.toISOString().slice(0, 10)
        const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
        stationExpected += c.expected ?? 0
      }
    }
    return (
      <div style={{
        gridColumn: '-2 / -1',
        gridRow: `span ${rows.length}`,
        borderBottom: '1px solid #f1f5f9',
        borderLeft: '1px solid #f1f5f9',
        background: '#fff',
        padding: '10px 12px',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        position: 'sticky', right: 0, zIndex: 2,
      }}>
        <ValuePill
          tone="pink"
          icon={<IconHeadset />}
          label={`${fmtInt(stationExpected)} programad${stationExpected === 1 ? 'o' : 'os'}`}
          hint={`${fmtInt(stationExpected)} inserções programadas na emissora no período`}
        />
      </div>
    )
  }
```
(`ValuePill`, `IconHeadset`, `fmtInt` already exist in this file. The existing `'full'` body below stays untouched.)

- [ ] **Step 5: Lint + build**

Run from `frontend/`:
```bash
npm run lint && npm run build
```
Expected: clean. No new warnings.

- [ ] **Step 6: Manual check — existing screens unchanged**

Start the dev server (`npm run dev`), open `/detections`, pick a competência + campaign with detections, and confirm the grid still shows the 6 pills per row + impactos/R$ per station (i.e. `summary='full'` default is intact). Also open a campaign in the wizard's distribution step and confirm it's unchanged.
Expected: no visual difference on `/detections` or the wizard.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/DistributionGrid.jsx
git commit -m "feat(frontend): DistributionGrid summary='plan' (programado-only, no R$)"
```

---

## Task 3: Create `MaterialPlaybackList` component

Read-only list of the campaign's materials, grouped by type, each playable + downloadable, with a programmed/not-programmed badge.

**Files:**
- Create: `frontend/src/components/MaterialPlaybackList.jsx`

- [ ] **Step 1: Write the component**

Create `frontend/src/components/MaterialPlaybackList.jsx` with this exact content:

```jsx
import { useState, useRef, useEffect } from 'react'
import api from '../api/client'
import TypeIconPill from './TypeIconPill'

// Duration in seconds → "12.3s" | "—"
function fmtDuration(secs) {
  if (secs == null) return '—'
  return `${Number(secs).toFixed(1)}s`
}

const FP_BADGE = {
  ready:      { label: 'Pronto',   bg: '#dcfce7', fg: 'var(--c-success)', dot: 'var(--c-success)' },
  generating: { label: 'Gerando',  bg: '#fef9c3', fg: '#a16207',          dot: '#ca8a04' },
  pending:    { label: 'Pendente', bg: 'var(--c-surface-2)', fg: 'var(--c-text-2)', dot: 'var(--c-text-3)' },
  failed:     { label: 'Falhou',   bg: '#fee2e2', fg: 'var(--c-danger)',  dot: 'var(--c-danger)' },
}

/**
 * Read-only playable list of a campaign's materials, grouped by type.
 *
 * Props:
 *  - materials: Array<Material>  (hydrated; each has id, title, type_id,
 *               duration_seconds, fingerprint_status, master_storage_path)
 *  - typeById: Record<typeId, {id, name, color}>
 *  - programmedTypeIds: Set<typeId>  (types that appear in any distribution rule)
 */
export default function MaterialPlaybackList({ materials, typeById, programmedTypeIds }) {
  const [playingId, setPlayingId] = useState(null)

  // Group by type, then sort groups by type name; untyped goes last.
  const groups = (() => {
    const m = new Map()
    for (const mat of materials) {
      const key = mat.type_id ?? '__none__'
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(mat)
    }
    const entries = [...m.entries()].map(([typeId, list]) => ({
      typeId,
      type: typeId === '__none__' ? null : typeById[typeId] ?? null,
      list: list.slice().sort((a, b) => (a.title ?? '').localeCompare(b.title ?? '')),
    }))
    entries.sort((a, b) => {
      if (a.typeId === '__none__') return 1
      if (b.typeId === '__none__') return -1
      return (a.type?.name ?? '').localeCompare(b.type?.name ?? '')
    })
    return entries
  })()

  const programmedCount = materials.filter(
    m => m.type_id && programmedTypeIds.has(m.type_id),
  ).length

  return (
    <div style={{
      background: 'var(--c-surface)',
      border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-md)',
      overflow: 'hidden',
      marginBottom: 14,
    }}>
      {/* Header / counter */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '12px 16px', borderBottom: '1px solid var(--c-border)',
        background: 'var(--c-bg)',
      }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--c-action)" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
          <path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" />
        </svg>
        <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--c-text)', fontFamily: 'var(--font-heading)' }}>
          Materiais da campanha
        </span>
        <span style={{ fontSize: 11, color: 'var(--c-text-3)', marginLeft: 4 }}>
          {materials.length} {materials.length === 1 ? 'material' : 'materiais'} · {programmedCount} programad{programmedCount === 1 ? 'o' : 'os'}
        </span>
      </div>

      {/* Grouped rows */}
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        {groups.map(({ typeId, type, list }) => (
          <div key={typeId}>
            <div style={{
              padding: '7px 16px', background: 'var(--c-bg)',
              borderBottom: '1px solid var(--c-border)',
              display: 'flex', alignItems: 'center', gap: 8,
              fontSize: 10.5, fontWeight: 700, letterSpacing: '0.05em',
              textTransform: 'uppercase', color: 'var(--c-text-3)',
              fontFamily: 'var(--font-heading)',
            }}>
              <TypeIconPill color={type?.color ?? '#94a3b8'} />
              {type?.name ?? 'Sem tipo'}
            </div>
            {list.map(mat => (
              <MaterialRow
                key={mat.id}
                material={mat}
                typeColor={type?.color ?? '#94a3b8'}
                programmed={!!(mat.type_id && programmedTypeIds.has(mat.type_id))}
                isPlaying={playingId === mat.id}
                onPlay={() => setPlayingId(mat.id)}
                onPause={() => setPlayingId(null)}
              />
            ))}
          </div>
        ))}
      </div>
      <style>{`@keyframes mpl-spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}

function MaterialRow({ material, typeColor, programmed, isPlaying, onPlay, onPause }) {
  const [audioBlobUrl, setAudioBlobUrl] = useState(null)
  const [audioLoading, setAudioLoading] = useState(false)
  const [downloadLoading, setDownloadLoading] = useState(false)
  const audioRef = useRef(null)

  useEffect(() => () => {
    if (audioBlobUrl) URL.revokeObjectURL(audioBlobUrl)
  }, [audioBlobUrl])

  async function fetchAudioBlob() {
    if (audioBlobUrl) return audioBlobUrl
    const resp = await api.get(`/materials/${material.id}/audio`, { responseType: 'blob' })
    const url = URL.createObjectURL(resp.data)
    setAudioBlobUrl(url)
    return url
  }

  async function togglePlay() {
    if (isPlaying) { onPause(); return }
    if (audioLoading) return
    setAudioLoading(true)
    try {
      await fetchAudioBlob()
      onPlay()
    } catch {
      window.alert('Não foi possível carregar o áudio.')
    } finally {
      setAudioLoading(false)
    }
  }

  async function handleDownload() {
    if (downloadLoading) return
    setDownloadLoading(true)
    try {
      const url = await fetchAudioBlob()
      const ext = material.master_storage_path?.split('.').pop() ?? 'mp3'
      const a = document.createElement('a')
      a.href = url
      a.download = `${material.title}.${ext}`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      window.alert('Não foi possível baixar o áudio.')
    } finally {
      setDownloadLoading(false)
    }
  }

  // Drive the <audio> element from isPlaying.
  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    if (isPlaying && audioBlobUrl) {
      el.play().catch(() => onPause())
    } else {
      el.pause()
    }
  }, [isPlaying, audioBlobUrl, onPause])

  const fp = FP_BADGE[material.fingerprint_status] ?? FP_BADGE.pending

  return (
    <div style={{
      padding: '10px 16px',
      borderBottom: '1px solid var(--c-border)',
      display: 'flex', alignItems: 'center', gap: 14,
      borderLeft: `3px solid ${typeColor}`,
    }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {material.title}
        </div>
        <div style={{ marginTop: 5, display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, color: 'var(--c-text-2)', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, color: 'var(--c-text)' }}>{fmtDuration(material.duration_seconds)}</span>
          <span style={{ color: 'var(--c-text-3)' }}>·</span>
          <span style={{
            padding: '2px 8px', borderRadius: 'var(--radius-full)',
            background: fp.bg, color: fp.fg, fontSize: 10, fontWeight: 700,
            display: 'inline-flex', alignItems: 'center', gap: 5,
          }}>
            <span style={{ width: 5, height: 5, borderRadius: '50%', background: fp.dot }} />
            {fp.label}
          </span>
        </div>
      </div>

      {/* Programmed badge */}
      <span style={{
        padding: '4px 10px', borderRadius: 'var(--radius-full)',
        fontSize: 10.5, fontWeight: 700, fontFamily: 'var(--font-heading)',
        whiteSpace: 'nowrap',
        background: programmed ? '#dcfce7' : 'var(--c-surface-2)',
        color: programmed ? 'var(--c-success)' : 'var(--c-text-3)',
      }}>
        {programmed ? 'programado' : 'sem programação'}
      </span>

      {/* Play */}
      <IconBtn onClick={togglePlay} disabled={audioLoading} isActive={isPlaying}
               title={isPlaying ? 'Pausar' : 'Ouvir material'}>
        {audioLoading ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ animation: 'mpl-spin 0.8s linear infinite' }}>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
            <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </svg>
        ) : isPlaying ? (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
            <rect x="3.5" y="3" width="3" height="10" rx="0.5" /><rect x="9.5" y="3" width="3" height="10" rx="0.5" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M4.5 2.5v11l9-5.5z" /></svg>
        )}
      </IconBtn>

      {/* Download */}
      <IconBtn onClick={handleDownload} disabled={downloadLoading} title="Baixar áudio">
        {downloadLoading ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ animation: 'mpl-spin 0.8s linear infinite' }}>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
            <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M8 2v8M4.5 7L8 10.5 11.5 7" /><path d="M2.5 12.5h11" />
          </svg>
        )}
      </IconBtn>

      <audio ref={audioRef} src={audioBlobUrl ?? undefined} onEnded={onPause} style={{ display: 'none' }} />
    </div>
  )
}

function IconBtn({ onClick, disabled, isActive, title, children }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={title}
      style={{
        width: 32, height: 32, borderRadius: 'var(--radius-md)',
        background: isActive ? 'color-mix(in srgb, var(--c-action) 10%, transparent)' : 'transparent',
        border: `1px solid ${isActive ? 'color-mix(in srgb, var(--c-action) 35%, transparent)' : 'var(--c-border)'}`,
        color: isActive ? 'var(--c-action)' : 'var(--c-text-3)',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        transition: 'all 100ms',
      }}
    >
      {children}
    </button>
  )
}
```

- [ ] **Step 2: Lint + build**

Run from `frontend/`:
```bash
npm run lint && npm run build
```
Expected: clean. (The component isn't imported anywhere yet — `vite build` won't complain about an unused module; eslint won't either since it's a default export.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/MaterialPlaybackList.jsx
git commit -m "feat(frontend): MaterialPlaybackList — read-only playable material list"
```

---

## Task 4: Create `MaterialsPage`

The page: competência→campanha→período filters, plan summary strip, the materials panel, the plan-only grid, and empty states.

**Files:**
- Create: `frontend/src/pages/MaterialsPage.jsx`

- [ ] **Step 1: Write the page**

Create `frontend/src/pages/MaterialsPage.jsx` with this exact content:

```jsx
import { useState, useMemo, useRef, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  useCampaigns, useStations, useClients,
  useCampaignMaterials, useMaterials, useDistributionRules,
  useMaterialTypes, useDailySummary,
} from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import RSelect from '../components/RSelect'
import DistributionGrid from '../components/DistributionGrid'
import FlowStepper from '../components/FlowStepper'
import AirtimePaginator from '../components/AirtimePaginator'
import MaterialPlaybackList from '../components/MaterialPlaybackList'
import { tokenize, matchesAllTokens } from '../utils/search'
import { safeLogoUrl } from '../utils/logoUrl'
import {
  parseLocalDate, monthFromDate, isoFromDate, monthToRange,
  monthLabel, rangeLabel, defaultRangeForCampaign, formatCampaignPeriod,
} from '../utils/dates'

const STEP_LABELS = ['Competência', 'Campanha', 'Período']
const PAGE_SIZE_OPTIONS = [5, 10, 15]
const DEFAULT_PAGE_SIZE = 5

// ── Client mini avatar (campaign select) ─────────────────────────
function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  const safeLogo = safeLogoUrl(logo)
  if (safeLogo && !imgError) {
    return (
      <img
        src={safeLogo} alt={name} width={size} height={size}
        style={{ width: size, height: size, borderRadius: 4, objectFit: 'cover', flexShrink: 0, border: '1px solid #e2e8f0', display: 'block' }}
        onError={() => setImgError(true)}
      />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div style={{
      width: size, height: size, borderRadius: 4, background: '#fce7f3', color: '#E81E75',
      fontSize: Math.round(size * 0.42), fontWeight: 700,
      display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
      fontFamily: "'Fira Sans Condensed', sans-serif", userSelect: 'none',
    }}>{initials}</div>
  )
}

// ── Empty state ──────────────────────────────────────────────────
const ICONS = {
  calendar: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M3 10h18M8 3v4M16 3v4" /></svg>),
  campaign: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1z" /><path d="M16 8a4 4 0 0 1 0 8" /><path d="M19 5a8 8 0 0 1 0 14" /></svg>),
  alert: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 9v4" /><path d="M12 17h.01" /><path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" /></svg>),
  search: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></svg>),
}

function MaterialsEmpty({ variant, monthLabelText, campaignName, rangeLabelText, campaignCount, isAdmin, onPickMonth, onPickCampaign, onEditCampaign }) {
  let step = 3, icon = ICONS.search, title = null, body = null, cta = null
  if (variant === 'no-month') {
    step = 1; icon = ICONS.calendar
    title = <>Comece pela <strong>competência</strong></>
    body = <>Escolha o mês de referência. As campanhas que cruzam esse período ficam disponíveis em seguida.</>
    cta = <button type="button" className="detection-empty-cta" onClick={onPickMonth}>Escolher competência</button>
  } else if (variant === 'no-campaign') {
    step = 2; icon = ICONS.campaign
    const count = campaignCount ?? 0
    title = <>Escolha uma <strong>campanha</strong> de {monthLabelText}</>
    body = count === 0
      ? <>Nenhuma campanha vigente em <strong>{monthLabelText}</strong>. Troque a competência.</>
      : <>{count === 1 ? '1 campanha vigente' : `${count} campanhas vigentes`} nesse mês. Selecione uma pra ver os materiais e o programado.</>
    cta = count > 0
      ? <button type="button" className="detection-empty-cta" onClick={onPickCampaign}>Abrir lista de campanhas</button>
      : <button type="button" className="detection-empty-cta detection-empty-cta--ghost" onClick={onPickMonth}>Trocar competência</button>
  } else if (variant === 'no-rules') {
    step = 3; icon = ICONS.alert
    title = <>Campanha sem materiais nem regras</>
    body = <><strong>{campaignName}</strong> ainda não tem materiais vinculados nem regras de distribuição.</>
    cta = isAdmin
      ? <button type="button" className="detection-empty-cta" onClick={onEditCampaign}>Editar campanha</button>
      : null
  } else {
    step = 3; icon = ICONS.search
    title = <>Nada programado no período</>
    body = <>Sem programação entre <strong>{rangeLabelText || monthLabelText}</strong> pra essa campanha.</>
    cta = null
  }
  return (
    <div className="detection-empty" role="status" aria-live="polite">
      <div className="detection-empty-card">
        <FlowStepper step={step} steps={STEP_LABELS} />
        <div className={`detection-empty-icon${variant === 'no-rules' ? ' detection-empty-icon--warn' : variant === 'no-plan' ? ' detection-empty-icon--mute' : ''}`}>{icon}</div>
        <h3>{title}</h3>
        <p>{body}</p>
        {cta && <div className="detection-empty-actions">{cta}</div>}
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────
export default function MaterialsPage() {
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const { data: campaigns = [], isLoading: loadingCampaigns } = useCampaigns()
  const { data: clients = [] } = useClients()

  const [searchParams] = useSearchParams()
  const deepLinkCampaignId = searchParams.get('campaign_id') ?? ''

  const [selectedCampaignId, setSelectedCampaignId] = useState(deepLinkCampaignId)
  const [selectedMonthRaw, setSelectedMonthRaw] = useState('')
  const [userRange, setUserRange] = useState({ start: '', end: '' })
  const [search, setSearch] = useState('')
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [page, setPage] = useState(1)

  const monthInputRef = useRef(null)
  const focusCampaignSelect = useCallback(() => {
    document.getElementById('materials-campaign')?.focus()
  }, [])

  // Stations: fetched for EVERYONE (admin + client). The /stations endpoint is
  // readable by any authenticated user (see App.jsx /stations route), so unlike
  // /detections we don't gate by isAdmin — the grid needs station objects to
  // render rows for client users too.
  const { data: stationsResp } = useStations({ limit: 2000 })
  const stationCatalog = useMemo(() => stationsResp?.data ?? [], [stationsResp])

  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const selectedCampaign = useMemo(
    () => campaigns.find(c => c.id === selectedCampaignId) ?? null,
    [campaigns, selectedCampaignId])

  const monthFromDeepLink = useMemo(() => {
    if (!deepLinkCampaignId || campaigns.length === 0) return ''
    const c = campaigns.find(c => c.id === deepLinkCampaignId)
    if (!c?.start_date) return ''
    const cs = parseLocalDate(c.start_date)
    const now = new Date()
    return monthFromDate(cs > now ? cs : now)
  }, [deepLinkCampaignId, campaigns])

  const selectedMonth = selectedMonthRaw || monthFromDeepLink || monthFromDate(new Date())

  const defaultRange = useMemo(
    () => defaultRangeForCampaign(selectedMonth, selectedCampaign),
    [selectedMonth, selectedCampaign])
  const rangeStart = userRange.start || defaultRange.start
  const rangeEnd   = userRange.end   || defaultRange.end

  const monthRangeISO = useMemo(() => {
    if (!selectedMonth) return { from: '', to: '' }
    const { start, end } = monthToRange(selectedMonth)
    return { from: isoFromDate(start), to: isoFromDate(end) }
  }, [selectedMonth])

  // Campaign-scoped data
  const { data: clientLibrary = [] } = useMaterials(selectedCampaign?.client_id ?? null)
  const materialsById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary])
  const { data: campaignMaterials = [] } = useCampaignMaterials(selectedCampaignId || null)
  const { data: distributionRules = [] } = useDistributionRules(selectedCampaignId || null)
  const { data: materialTypes = [] } = useMaterialTypes()
  const { data: summary = [], isLoading: loadingSummary, isFetching } =
    useDailySummary(selectedCampaignId || null, monthRangeISO.from, monthRangeISO.to)

  // All materials the campaign has (programmed or not).
  const linkedMaterials = useMemo(
    () => campaignMaterials.map(cm => materialsById[cm.material_id]).filter(Boolean),
    [campaignMaterials, materialsById])

  // Type ids that appear in any distribution rule → "programmed".
  const programmedTypeIds = useMemo(
    () => new Set(distributionRules.map(r => r.type_id)),
    [distributionRules])

  const typeById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t])),
    [materialTypes])

  // station → Set<typeId> in scope (has a linked material of that type)
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) {
        if (!m.has(sid)) m.set(sid, new Set())
        m.get(sid).add(mat.type_id)
      }
    }
    return m
  }, [campaignMaterials, materialsById])

  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeSet] of typesInScopeByStation.entries()) {
      for (const tid of typeSet) {
        const type = typeById[tid]
        if (!type) continue
        const matching = distributionRules.filter(rule =>
          rule.type_id === tid && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          materialId: tid,
          materialTitle: type.name,
          typeColor: type.color ?? '#94a3b8',
          ruleSummary: first ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}` : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [typesInScopeByStation, typeById, distributionRules])

  // Client-side narrow to the picked range (drives summary + grid).
  const rangedSummary = useMemo(() => {
    if (!rangeStart || !rangeEnd) return summary
    return summary.filter(s => {
      const d = (s.for_date ?? '').slice(0, 10)
      return d >= rangeStart && d <= rangeEnd
    })
  }, [summary, rangeStart, rangeEnd])

  // cellData: PROGRAMADO ONLY — keep just `expected`, drop everything else so
  // DayCell renders only the gray pill.
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of rangedSummary) {
      const key = `${s.station_id}|${s.type_id}|${s.for_date.slice(0, 10)}`
      m.set(key, { expected: s.expected })
    }
    return m
  }, [rangedSummary])

  const monthDate = useMemo(() => {
    if (!selectedMonth) return new Date()
    const [y, m] = selectedMonth.split('-').map(Number)
    return new Date(y, m - 1, 1)
  }, [selectedMonth])

  const gridStart = rangeStart || selectedCampaign?.start_date || ''
  const gridEnd   = rangeEnd   || selectedCampaign?.end_date   || ''

  // ── Campaign options ──────────────────────────────────────────
  const allCampaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id, label: c.name,
      clientName: client?.name ?? '', clientLogo: client?.logo_url ?? null,
      startDate: c.start_date, endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  const campaignOptions = useMemo(() => {
    if (!selectedMonth) return []
    const { start, end } = monthToRange(selectedMonth)
    return allCampaignOptions.filter(o => {
      if (!o.startDate || !o.endDate) return false
      const cs = parseLocalDate(o.startDate)
      const ce = parseLocalDate(o.endDate)
      return cs <= end && ce >= start
    })
  }, [allCampaignOptions, selectedMonth])

  const selectedCampaignOption = useMemo(
    () => allCampaignOptions.find(o => o.value === selectedCampaignId) ?? null,
    [allCampaignOptions, selectedCampaignId])

  function formatCampaignOption(opt, { context }) {
    const isValue = context === 'value'
    const size = isValue ? 18 : 22
    const period = formatCampaignPeriod(opt.startDate, opt.endDate)
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
        <ClientMiniAvatar name={opt.clientName} logo={opt.clientLogo} size={size} />
        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, gap: 1 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 5, minWidth: 0, overflow: 'hidden' }}>
            {opt.clientName && <span style={{ fontWeight: 600, color: '#06055B', whiteSpace: 'nowrap', fontSize: 13 }}>{opt.clientName}</span>}
            {opt.clientName && <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>|</span>}
            <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{opt.label}</span>
            {isValue && period && (<>
              <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>•</span>
              <span style={{ color: '#94a3b8', fontSize: 12, whiteSpace: 'nowrap', flexShrink: 0 }}>{period}</span>
            </>)}
          </div>
          {!isValue && period && <span style={{ color: '#94a3b8', fontSize: 11, whiteSpace: 'nowrap' }}>{period}</span>}
        </div>
      </div>
    )
  }

  // ── Search filter on the grid rows ────────────────────────────
  const filteredRows = useMemo(() => {
    const tokens = tokenize(search)
    if (tokens.length === 0) return rows
    const stationById = new Map(stationCatalog.map(s => [s.id, s]))
    const fields = ['name', 'city', 'state', 'band', 'freq', 'title']
    return rows.filter(r => {
      const st = stationById.get(r.stationId)
      if (!st) return false
      const haystack = {
        name: st.name ?? '', city: st.city ?? '', state: st.state ?? '',
        band: st.band ?? '', freq: st.frequency_mhz != null ? String(st.frequency_mhz) : '',
        title: r.materialTitle ?? '',
      }
      return matchesAllTokens(haystack, fields, tokens)
    })
  }, [rows, search, stationCatalog])

  // ── Pagination (by station) ───────────────────────────────────
  const uniqueStationIds = useMemo(() => {
    const seen = new Set(); const list = []
    for (const r of filteredRows) {
      if (!seen.has(r.stationId)) { seen.add(r.stationId); list.push(r.stationId) }
    }
    return list
  }, [filteredRows])
  const totalStations = uniqueStationIds.length
  const totalPages = Math.max(1, Math.ceil(totalStations / pageSize))
  const safePage = Math.min(page, totalPages)
  const pagedStationIds = useMemo(() => {
    const start = (safePage - 1) * pageSize
    return new Set(uniqueStationIds.slice(start, start + pageSize))
  }, [uniqueStationIds, safePage, pageSize])
  const pagedRows = useMemo(
    () => filteredRows.filter(r => pagedStationIds.has(r.stationId)),
    [filteredRows, pagedStationIds])

  // ── Plan summary stats ────────────────────────────────────────
  const planStats = useMemo(() => {
    const stationSet = new Set()
    let totalExpected = 0
    for (const s of rangedSummary) {
      totalExpected += s.expected ?? 0
      if ((s.expected ?? 0) > 0) stationSet.add(s.station_id)
    }
    const programmedMaterials = linkedMaterials.filter(m => m.type_id && programmedTypeIds.has(m.type_id)).length
    return { materials: linkedMaterials.length, programmedMaterials, stations: stationSet.size, totalExpected }
  }, [rangedSummary, linkedMaterials, programmedTypeIds])

  // ── Filter step + empty variant ───────────────────────────────
  const filterStep = !selectedMonth ? 1 : !selectedCampaignId ? 2 : 3
  const showContent = filterStep === 3
  const isLoadingData = showContent && (loadingSummary || isFetching)
  const hasRows = rows.length > 0
  const hasMaterials = linkedMaterials.length > 0
  const hasPlanInRange = rangedSummary.some(s => (s.expected ?? 0) > 0)

  let emptyVariant = null
  if (filterStep === 1) emptyVariant = 'no-month'
  else if (filterStep === 2) emptyVariant = 'no-campaign'
  else if (!isLoadingData && !hasRows && !hasMaterials) emptyVariant = 'no-rules'
  // If there ARE materials but nothing programmed in range, we still show the
  // materials panel (below) and a plan-empty note instead of a full takeover.

  const campaignCount = campaignOptions.length

  // ── Handlers ──────────────────────────────────────────────────
  function handleMonthChange(e) {
    const v = e.target.value
    setSelectedMonthRaw(v)
    setUserRange({ start: '', end: '' })
    setPage(1)
    if (selectedCampaignId) {
      const c = campaigns.find(cc => cc.id === selectedCampaignId)
      if (c && v) {
        const { start, end } = monthToRange(v)
        const cs = parseLocalDate(c.start_date)
        const ce = parseLocalDate(c.end_date)
        if (!(cs <= end && ce >= start)) setSelectedCampaignId('')
      } else { setSelectedCampaignId('') }
    }
  }
  function handleCampaignChange(opt) {
    setSelectedCampaignId(opt?.value ?? '')
    setUserRange({ start: '', end: '' })
    setPage(1)
  }
  function handleSearchChange(e) { setSearch(e.target.value); setPage(1) }
  function handlePageSizeChange(n) { setPageSize(n); setPage(1) }
  function handleRangeStart(e) {
    const v = e.target.value
    setUserRange(prev => {
      const next = { start: v, end: prev.end || rangeEnd }
      if (v && next.end && v > next.end) next.end = v
      return next
    })
  }
  function handleRangeEnd(e) {
    const v = e.target.value
    setUserRange(prev => {
      const next = { start: prev.start || rangeStart, end: v }
      if (v && next.start && v < next.start) next.start = v
      return next
    })
  }
  function handleResetRange() { setUserRange({ start: '', end: '' }) }

  const rangeBounds = defaultRange
  const rangeIsCustom = !!(userRange.start || userRange.end) &&
    rangeBounds.start && rangeBounds.end &&
    (rangeStart !== rangeBounds.start || rangeEnd !== rangeBounds.end)

  // ── Render ────────────────────────────────────────────────────
  return (
    <div>
      <div className="page-header">
        <h2>Materiais</h2>
      </div>

      {/* 3-step filter bar */}
      <div className="flow-filters">
        <div className={`flow-filter ${filterStep === 1 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="materials-month">
            <span className="flow-filter-label-step">1</span>Competência
          </label>
          <input id="materials-month" ref={monthInputRef} className="flow-month-input" type="month"
                 value={selectedMonth} onChange={handleMonthChange} placeholder="Selecione o mês" />
        </div>

        <div className={`flow-filter ${!selectedMonth ? 'flow-filter--locked' : filterStep === 2 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="materials-campaign">
            <span className="flow-filter-label-step">2</span>Campanha
            {selectedMonth && filterStep === 2 && campaignCount > 0 && (
              <span style={{ marginLeft: 'auto', textTransform: 'none', letterSpacing: 0, fontSize: 11, fontWeight: 600, color: 'var(--c-text-3)' }}>
                {campaignCount === 1 ? '1 disponível' : `${campaignCount} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="materials-campaign"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={handleCampaignChange}
            formatOptionLabel={formatCampaignOption}
            isDisabled={!selectedMonth || loadingCampaigns}
            isLoading={loadingCampaigns}
            placeholder={
              !selectedMonth ? 'Escolha uma competência primeiro' :
              loadingCampaigns ? 'Carregando…' :
              campaignCount === 0 ? `Nenhuma campanha em ${monthLabel(selectedMonth)}` :
              `${campaignCount === 1 ? '1 campanha' : `${campaignCount} campanhas`} em ${monthLabel(selectedMonth)}`
            }
            isClearable
            noOptionsMessage={() => `Nenhuma campanha em ${monthLabel(selectedMonth)}`}
          />
        </div>

        <div className={`flow-filter ${filterStep < 3 ? 'flow-filter--locked' : 'flow-filter--active'}`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">3</span>Período
            {rangeIsCustom && (
              <button type="button" className="flow-range-reset" onClick={handleResetRange}
                      title="Resetar pro intervalo completo da campanha no mês" style={{ marginLeft: 'auto' }}>
                Resetar
              </button>
            )}
          </label>
          <div className="flow-range">
            <input type="date" value={rangeStart} min={rangeBounds.start || undefined} max={rangeBounds.end || undefined}
                   onChange={handleRangeStart} disabled={filterStep < 3} aria-label="Data de início" />
            <span className="flow-range-arrow">→</span>
            <input type="date" value={rangeEnd} min={rangeBounds.start || undefined} max={rangeBounds.end || undefined}
                   onChange={handleRangeEnd} disabled={filterStep < 3} aria-label="Data de fim" />
          </div>
        </div>
      </div>

      {/* Secondary toolbar: search (only when a campaign is picked) */}
      {showContent && (
        <div className="flow-toolbar">
          <div className="stations-search">
            <span className="stations-search-icon">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
                <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
              </svg>
            </span>
            <input className="input stations-search-input" type="text" placeholder="Buscar emissora, cidade…"
                   value={search} onChange={handleSearchChange} />
          </div>
        </div>
      )}

      {/* Content */}
      {emptyVariant ? (
        <MaterialsEmpty
          variant={emptyVariant}
          monthLabelText={selectedMonth ? monthLabel(selectedMonth) : ''}
          campaignName={selectedCampaign?.name ?? ''}
          rangeLabelText={rangeLabel(rangeStart, rangeEnd)}
          campaignCount={campaignCount}
          isAdmin={isAdmin}
          onPickMonth={() => {
            const el = monthInputRef.current
            if (!el) return
            el.focus()
            if (typeof el.showPicker === 'function') { try { el.showPicker() } catch { /* focus race */ } }
          }}
          onPickCampaign={focusCampaignSelect}
          onEditCampaign={() => { if (selectedCampaignId) navigate(`/campaigns/${selectedCampaignId}/edit`) }}
        />
      ) : (
        <>
          {/* Plan summary strip */}
          <div style={{
            display: 'flex', alignItems: 'center', gap: 24, flexWrap: 'wrap',
            padding: '14px 18px', marginBottom: 14,
            background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
          }}>
            <Stat label="Materiais" value={`${planStats.materials}`} hint={`${planStats.programmedMaterials} programados`} />
            <Divider />
            <Stat label="Emissoras com programação" value={`${planStats.stations}`} />
            <Divider />
            <Stat label="Inserções programadas" value={`${planStats.totalExpected}`} hint="no período" />
          </div>

          {/* Materials panel — ALL campaign materials (programmed or not) */}
          {hasMaterials && (
            <MaterialPlaybackList
              materials={linkedMaterials}
              typeById={typeById}
              programmedTypeIds={programmedTypeIds}
            />
          )}

          {/* Plan grid (programado-only) */}
          {isLoadingData ? (
            <div style={{ padding: '40px 0', textAlign: 'center', color: 'var(--c-text-3)', fontSize: 13 }}>
              Carregando programação…
            </div>
          ) : !hasRows ? (
            <div style={{
              padding: '28px 18px', textAlign: 'center', color: 'var(--c-text-2)',
              background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12, fontSize: 13,
            }}>
              Nenhuma regra de distribuição nesta campanha — nada programado pra exibir na grade.
            </div>
          ) : !hasPlanInRange ? (
            <div style={{
              padding: '28px 18px', textAlign: 'center', color: 'var(--c-text-2)',
              background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12, fontSize: 13,
            }}>
              Sem programação entre <strong>{rangeLabel(rangeStart, rangeEnd) || monthLabel(selectedMonth)}</strong>.
            </div>
          ) : (
            <>
              <DistributionGrid
                mode="view"
                summary="plan"
                month={monthDate}
                campaignStart={gridStart}
                campaignEnd={gridEnd}
                stations={stationCatalog}
                rows={pagedRows}
                cellData={cellData}
                capAtToday={false}
                inlineStationInfo
              />
              {totalStations > 0 && (
                <AirtimePaginator
                  page={safePage} totalPages={totalPages} total={totalStations}
                  pageSize={pageSize} pageSizeOptions={PAGE_SIZE_OPTIONS}
                  onPageSizeChange={handlePageSizeChange} onChange={setPage}
                  singular="emissora" plural="emissoras"
                />
              )}
            </>
          )}
        </>
      )}
    </div>
  )
}

function Stat({ label, value, hint }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: '0.04em', textTransform: 'uppercase', color: 'var(--c-text-3)', fontFamily: 'var(--font-heading)' }}>{label}</span>
      <span style={{ fontSize: 22, fontWeight: 700, color: 'var(--c-text)', fontFamily: 'var(--font-heading)', lineHeight: 1.1 }}>{value}</span>
      {hint && <span style={{ fontSize: 11, color: 'var(--c-text-3)' }}>{hint}</span>}
    </div>
  )
}
function Divider() { return <div style={{ height: 40, width: 1, background: '#e2e8f0' }} /> }
```

- [ ] **Step 2: Lint + build**

Run from `frontend/`:
```bash
npm run lint && npm run build
```
Expected: clean. If lint reports `useCallback`/`useRef` unused or similar, re-check the imports against usage. The page isn't routed yet — that's Task 5.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/MaterialsPage.jsx
git commit -m "feat(frontend): MaterialsPage — plano por campanha + materiais tocáveis"
```

---

## Task 5: Wire route + sidebar links

**Files:**
- Modify: `frontend/src/App.jsx`
- Modify: `frontend/src/components/Sidebar.jsx`

- [ ] **Step 1: Import + route in `App.jsx`**

Add the import alongside the other page imports:
```jsx
import MaterialsPage from './pages/MaterialsPage'
```

Add the route inside the `AppShell` `<Routes>`, right after the `/detections` route (no `RequireRole` — backend scopes data):
```jsx
            <Route path="/materials"   element={<MaterialsPage />} />
```

- [ ] **Step 2: Add a sidebar icon to `Sidebar.jsx`**

Add this icon component near the other `Icon*` definitions in `frontend/src/components/Sidebar.jsx`:
```jsx
function IconMaterials() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 13V4l7-1.2V11" />
      <circle cx="4" cy="13" r="2" />
      <circle cx="11" cy="11" r="2" />
    </svg>
  )
}
```

- [ ] **Step 3: Add the link to both navs**

In `AdminNav`, under the `Veiculação` section, add the link right after the `/detections` link:
```jsx
      <SidebarLink to="/materials"       icon={<IconMaterials />}     onClose={onClose}>Materiais</SidebarLink>
```

In `ClientNav`, under its `Veiculação` section, add the same line right after the `/detections` link:
```jsx
      <SidebarLink to="/materials"       icon={<IconMaterials />}     onClose={onClose}>Materiais</SidebarLink>
```

- [ ] **Step 4: Lint + build**

Run from `frontend/`:
```bash
npm run lint && npm run build
```
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(frontend): rota /materials + link na sidebar (admin + cliente)"
```

---

## Task 6: Documentation

**Files:**
- Create: `docs/features/materials-page.md`
- Modify: `CLAUDE.md` (consult map)
- Modify: `docs/README.md` (index)

- [ ] **Step 1: Write the feature doc**

Create `docs/features/materials-page.md`:
```markdown
---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - frontend/src/pages/MaterialsPage.jsx
  - frontend/src/components/MaterialPlaybackList.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/utils/dates.js
---

# Tela "Materiais" (`/materials`)

Tela da seção **Veiculação**, acessível a **admin e cliente** (cliente só com
as próprias campanhas/materiais — escopo aplicado pelo backend). Mostra, por
campanha: **todos os materiais** que ela tem (programados ou não, com play +
download) e **quanto está programado pra rodar em cada emissora**.

> Spec: [`docs/superpowers/specs/2026-05-27-materials-page-design.md`](../superpowers/specs/2026-05-27-materials-page-design.md)

## Diferença pra /detections

A `/detections` mostra o **veiculado** (verde/vermelho/bônus) contra o plano,
com R$ e impactos. A `/materials` mostra **só o programado** (células cinza),
**sem** veiculados, **sem** R$, **sem** impactos. O foco é o material e o
volume programado por emissora.

## Filtros (igual /detections)

Competência (mês) → Campanha → Período (início→fim). A campanha lista só as
que cruzam a competência. O período faz o narrow client-side da grade.

## Painel de materiais

Lista todos os materiais vinculados à campanha, agrupados por tipo. Cada item:
duração, status de fingerprint, **selo "programado" / "sem programação"**
(programado = o tipo do material aparece em alguma regra de distribuição),
play/pause e download. O áudio é buscado via `GET /materials/{id}/audio`
(blob autenticado → object URL). Só um material toca por vez.

## Grade de plano

`DistributionGrid` com `summary="plan"` e `capAtToday={false}` (mostra o
período inteiro, inclusive dias futuros — é um plano). `cellData` carrega só
`expected` → células cinza. A coluna de total por emissora vira "N programados"
(Σ expected da emissora), sem R$/impactos. Paginada por emissora (5/10/15).

## DistributionGrid — prop `summary`

| Valor | Resumo por linha | Total por emissora |
|-------|------------------|--------------------|
| `'full'` (default) | 6 pílulas | impactos + R$ |
| `'plan'` | só pílula cinza (programado) | "N programados" (Σ expected) |

Default `'full'` preserva /detections e o wizard sem mudança.

## Limitações

- Sem export CSV/PDF (isso é da /detections via CampaignReportsMenu).
- Busca é substring case-insensitive (mesma da /detections), não fuzzy.
```

- [ ] **Step 2: Add the index line to `CLAUDE.md`**

In the "Mapa de consulta" table of `CLAUDE.md`, add a row right after the `/detections` row:
```markdown
| Página `/materials` (materiais por campanha + Σ programado por emissora, admin + cliente) | [docs/features/materials-page.md](docs/features/materials-page.md) |
```

- [ ] **Step 3: Add the index line to `docs/README.md`**

Add a pointer to `docs/features/materials-page.md` in the features section of `docs/README.md`, matching the surrounding format (open the file first to match its exact list style).

- [ ] **Step 4: Commit**

```bash
git add docs/features/materials-page.md CLAUDE.md docs/README.md
git commit -m "docs: tela Materiais (/materials) + índices"
```

---

## Task 7: Manual end-to-end verification

No automated tests exist; verify by running the app.

**Files:** none (verification only)

- [ ] **Step 1: Start the app**

```bash
cd frontend && npm run dev
```
(Backend must be running per the project's usual local setup; the page hits `/campaigns`, `/clients`, `/stations`, `/materials/*`, `/campaigns/*/materials`, `/campaigns/*/distribution-rules`, `/campaigns/*/daily-summary`.)

- [ ] **Step 2: Admin flow**

Log in as admin. Sidebar → Veiculação → **Materiais**. Verify:
- Step 1 empty state asks for competência; pick a month.
- Step 2 lists campaigns of that month (client name inline); pick one.
- Plan summary strip shows N materiais / K programados / M emissoras / Σ.
- Materials panel lists ALL campaign materials grouped by type; each plays + downloads; programmed badge correct (a material whose type has no rule shows "sem programação").
- Grid shows ONLY gray cells; per-station column shows "N programados" (no R$/impactos); future days are visible (not capped at today).
- Período início/fim narrows the grid; "Resetar" appears when custom.
- Search filters stations/materials; pagination works.

- [ ] **Step 3: Client flow**

Log in as a client user. Sidebar → Veiculação → **Materiais**. Verify:
- Only the client's own campaigns appear.
- Grid renders station names correctly (stations fetched for clients too).
- No "Editar campanha" CTA on the `no-rules` empty state.

- [ ] **Step 4: Regression check on /detections + wizard**

Open `/detections` (admin) — 6 pills + impactos/R$ intact. Open a campaign in the wizard distribution step — unchanged.

- [ ] **Step 5: Final lint + build**

```bash
cd frontend && npm run lint && npm run build
```
Expected: clean.

---

## Self-Review notes (for the implementer)

- **Spec coverage:** D1 (single-campaign focus) → Task 4. D2 (filters) → Task 4. D3 (materials panel) → Tasks 3+4. D4 (`capAtToday=false`) → Task 4. D5 (no R$/impactos) → Task 2 `summary='plan'`. D6 (helper extraction) → Task 1. Programmed/not badge + Σ-per-station → Tasks 2+3. Role gating → Task 5 (no `RequireRole`) + stations-for-all note in Task 4. Docs → Task 6.
- **Type/name consistency:** `summary` prop name identical across DistributionGrid (Task 2), RowSummaryCell, StationTotalCell, and the `MaterialsPage` call (Task 4). `programmedTypeIds` (Set) passed from page → list with the same name. `cellData` key shape `${station}|${type}|${date}` matches DayCell/grid lookups.
- **No placeholders:** every code step contains full content.
