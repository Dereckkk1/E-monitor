import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useStationFailures } from '../api/hooks'
import StationFailureCard from '../components/StationFailureCard'
import './AdminStationFailuresPage.css'

const MIN_DOWN_OPTIONS = [
  { value: 0, label: 'tudo' },
  { value: 60, label: '≥ 1 min' },
  { value: 300, label: '≥ 5 min' },
  { value: 1800, label: '≥ 30 min' },
]

const MONTHS_PT = [
  'janeiro', 'fevereiro', 'março', 'abril', 'maio', 'junho',
  'julho', 'agosto', 'setembro', 'outubro', 'novembro', 'dezembro',
]

function isoYesterday() {
  const d = new Date()
  d.setDate(d.getDate() - 1)
  return d.toISOString().slice(0, 10)
}
function isoToday() { return new Date().toISOString().slice(0, 10) }
function isoMinusDays(n) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}

function fmtDuration(sec) {
  if (!sec) return '0min'
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const rem = min - h * 60
  return rem > 0 ? `${h}h ${rem}min` : `${h}h`
}

// Parse YYYY-MM-DD as a local-date (avoids TZ shift that new Date('2026-05-18')
// would apply, which can render "17 de maio" in negative UTC offsets).
function parseLocalDate(iso) {
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(y, m - 1, d)
}

function fmtMasthead(iso) {
  const d = parseLocalDate(iso)
  return {
    day:   String(d.getDate()).padStart(2, '0'),
    month: MONTHS_PT[d.getMonth()],
    year:  d.getFullYear(),
    wday:  ['domingo', 'segunda', 'terça', 'quarta', 'quinta', 'sexta', 'sábado'][d.getDay()],
  }
}

// Build a flat list of {fromMinute, toMinute} for each incident on the page,
// clamped to [0, 1440). Used by the hero 24h ribbon.
function flattenIncidents(stations, dateIso) {
  if (!dateIso || !stations?.length) return []
  const dayStart = parseLocalDate(dateIso).getTime()
  const dayEnd   = dayStart + 86_400_000
  const out = []
  for (const s of stations) {
    for (const inc of s.incidents || []) {
      const t0 = new Date(inc.event_at).getTime()
      const t1 = t0 + (inc.duration_seconds || 0) * 1000
      const a = Math.max(t0, dayStart)
      const b = Math.min(t1, dayEnd)
      if (b <= a) continue
      out.push({
        from: (a - dayStart) / 60_000,
        to:   (b - dayStart) / 60_000,
      })
    }
  }
  return out
}

function HourTicks() {
  // 6 ticks at 04, 08, 12, 16, 20 — labelled subtly under the ribbon.
  return (
    <div className="asf-hours" aria-hidden="true">
      {[4, 8, 12, 16, 20].map(h => (
        <span key={h} className="asf-hour-tick" style={{ left: `${(h / 24) * 100}%` }}>
          {String(h).padStart(2, '0')}h
        </span>
      ))}
    </div>
  )
}

function HeroTimeline({ incidents }) {
  if (!incidents.length) return null
  return (
    <div className="asf-timeline" role="img" aria-label={`${incidents.length} incidentes no dia`}>
      <svg className="asf-timeline-svg" viewBox="0 0 1440 14" preserveAspectRatio="none">
        <line x1="0" y1="7" x2="1440" y2="7" className="asf-timeline-axis" />
        {[4, 8, 12, 16, 20].map(h => (
          <line key={h} x1={h * 60} y1="2" x2={h * 60} y2="12" className="asf-timeline-grid" />
        ))}
        {incidents.map((inc, i) => (
          <rect
            key={i}
            x={inc.from}
            y="3"
            width={Math.max(inc.to - inc.from, 2)}
            height="8"
            rx="1"
            className="asf-timeline-mark"
          />
        ))}
      </svg>
      <HourTicks />
    </div>
  )
}

function SkeletonCard() {
  return (
    <article className="asf-skel-card" aria-hidden="true">
      <div className="asf-skel-row">
        <div className="asf-skel-num" />
        <div className="asf-skel-logo" />
        <div className="asf-skel-identity">
          <div className="asf-skel-line w60" />
          <div className="asf-skel-line w40" />
        </div>
        <div className="asf-skel-severity" />
      </div>
      <div className="asf-skel-ribbon" />
      <div className="asf-skel-chips">
        <div className="asf-skel-chip" />
        <div className="asf-skel-chip" />
      </div>
    </article>
  )
}

function EmptyState({ dateIso }) {
  const { day, month, year } = fmtMasthead(dateIso)
  return (
    <div className="asf-empty">
      <div className="asf-empty-text">
        <div className="asf-empty-mark" aria-hidden="true">
          <svg width="44" height="44" viewBox="0 0 44 44" fill="none">
            <circle cx="22" cy="22" r="20" stroke="currentColor" strokeWidth="1.5" opacity="0.35" />
            <path d="M14 22.5l5.5 5.5L31 16" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </div>
        <h2>Zero falhas em {day} de {month} de {year}</h2>
        <p>Nenhuma emissora caiu e nenhuma campanha perdeu slot.<br/>Pra ver o pulso da infra agora, vá pra <Link to="/admin/overview">visão geral</Link>.</p>
      </div>
      <div className="asf-empty-preview" aria-hidden="true">
        <div className="asf-empty-preview-label">Quando algo falhar, aparece assim:</div>
        <div className="asf-empty-card">
          <div className="asf-empty-card-head">
            <span className="asf-empty-num">01</span>
            <div className="asf-empty-logo" />
            <div className="asf-empty-meta">
              <div className="asf-empty-name" />
              <div className="asf-empty-dial" />
            </div>
            <div className="asf-empty-severity" />
          </div>
          <div className="asf-empty-ribbon">
            <span className="asf-empty-ribbon-mark" style={{ left: '34%', width: '8%' }} />
            <span className="asf-empty-ribbon-mark" style={{ left: '58%', width: '4%' }} />
          </div>
          <div className="asf-empty-chips">
            <div className="asf-empty-chip" />
            <div className="asf-empty-chip" />
          </div>
        </div>
      </div>
    </div>
  )
}

export default function AdminStationFailuresPage() {
  const [date, setDate] = useState(isoYesterday())
  const [minDown, setMinDown] = useState(60)

  const { data, isLoading, isFetching, refetch, error } = useStationFailures({
    date, minDownSeconds: minDown,
  })

  const stations = data?.stations ?? []
  const summary  = data?.summary
  const masthead = useMemo(() => fmtMasthead(date), [date])
  const incidents = useMemo(() => flattenIncidents(stations, date), [stations, date])

  const hasData = !isLoading && stations.length > 0

  return (
    <div className="asf" data-mode={hasData ? 'incident' : 'nominal'}>
      {/* ── Hero masthead ─────────────────────────────────────────── */}
      <header className="asf-hero">
        <div className="asf-hero-top">
          <div className="asf-masthead">
            <span className="asf-masthead-day">{masthead.day}</span>
            <div className="asf-masthead-stack">
              <span className="asf-masthead-month">{masthead.month} {masthead.year}</span>
              <span className="asf-masthead-wday">{masthead.wday} · falhas do dia</span>
            </div>
          </div>

          <div className="asf-hero-controls">
            <label className="asf-field">
              <span>Data</span>
              <input
                type="date"
                value={date}
                onChange={e => e.target.value && setDate(e.target.value)}
                max={isoToday()}
                min={isoMinusDays(90)}
              />
            </label>
            <label className="asf-field">
              <span>Filtro</span>
              <select value={minDown} onChange={e => setMinDown(Number(e.target.value))}>
                {MIN_DOWN_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </label>
            <button className="asf-refresh" onClick={() => refetch()} disabled={isFetching} title="Atualizar">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true"
                   className={isFetching ? 'asf-spin' : ''}>
                <path d="M1.5 7a5.5 5.5 0 1 0 1.65-3.9M1.5 1.5v3.2h3.2"
                      stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
            </button>
          </div>
        </div>

        {summary && summary.stations_with_failure > 0 && (
          <dl className="asf-stats">
            <div className="asf-stat">
              <dt>emissoras</dt>
              <dd><strong>{summary.stations_with_failure}</strong></dd>
            </div>
            <div className="asf-stat-sep" aria-hidden="true" />
            <div className="asf-stat">
              <dt>tempo fora total</dt>
              <dd><strong>{fmtDuration(summary.total_down_seconds)}</strong></dd>
            </div>
            <div className="asf-stat-sep" aria-hidden="true" />
            <div className="asf-stat">
              <dt>campanhas afetadas</dt>
              <dd><strong>{summary.affected_campaigns}</strong></dd>
            </div>
          </dl>
        )}

        <HeroTimeline incidents={incidents} />
      </header>

      {error && (
        <div className="asf-error" role="alert">
          Erro ao carregar: {String(error.message || error)}
        </div>
      )}

      {isLoading && (
        <div className="asf-list">
          {[0, 1, 2, 3].map(i => <SkeletonCard key={i} />)}
        </div>
      )}

      {!isLoading && stations.length === 0 && !error && <EmptyState dateIso={date} />}

      {hasData && (
        <ol className="asf-list" aria-label="Emissoras com falha, ordenadas por gravidade">
          {stations.map((s, idx) => (
            <li key={s.station.id}>
              <StationFailureCard data={s} rank={idx + 1} dateIso={date} />
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
