import { useMemo, useState, useEffect } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { useStationFailures, useCampaignFailures } from '../api/hooks'
import StationFailureCard from '../components/StationFailureCard'
import CampaignFailureCard from '../components/CampaignFailureCard'
import CampaignFailureRow from '../components/CampaignFailureRow'
import CampaignFailureDrawer from '../components/CampaignFailureDrawer'
import FailuresDailyView from '../components/FailuresDailyView'
import DeficitSplit from '../components/DeficitSplit'
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
    wday:  ['domingo', 'segunda-feira', 'terça-feira', 'quarta-feira', 'quinta-feira', 'sexta-feira', 'sábado'][d.getDay()],
  }
}

// Concise relative-day pill ("Ontem" / "Hoje" / "DD/MM"), surfaced next to
// the page title so the date being inspected is obvious without reading the
// long subtitle. Anything older than 7 days shows the absolute date.
function fmtRelativeDate(iso) {
  const target = parseLocalDate(iso)
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const ms = today - target
  const days = Math.round(ms / 86_400_000)
  if (days === 0) return 'Hoje'
  if (days === 1) return 'Ontem'
  if (days >= 2 && days <= 6) return `Há ${days} dias`
  const [, m, d] = iso.split('-')
  return `${d}/${m}`
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

// Merge overlapping/adjacent ranges so the "up" complement is computed against
// a clean set. Multiple stations can fail at the same minute — each contributes
// an incident — but the user-facing timeline shows aggregate down-time.
function mergeRanges(ranges) {
  if (!ranges.length) return []
  const sorted = [...ranges].sort((a, b) => a.from - b.from)
  const out = [{ ...sorted[0] }]
  for (let i = 1; i < sorted.length; i++) {
    const last = out[out.length - 1]
    const cur = sorted[i]
    if (cur.from <= last.to) last.to = Math.max(last.to, cur.to)
    else out.push({ ...cur })
  }
  return out
}

// Texto de horas a cada 2h (13 marks) + tracinhos sutis a cada 1h (25 marks).
// Labels "primárias" (mostradas sempre, mesmo em telas estreitas) ficam a cada 4h.
const HOUR_LABELS  = [0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24]
const HOUR_PRIMARY = new Set([0, 4, 8, 12, 16, 20, 24])
const HOUR_TICKS   = Array.from({ length: 25 }, (_, i) => i)

function HeroTimeline({ incidents, dateIso }) {
  const downRanges = useMemo(() => mergeRanges(incidents), [incidents])

  // UP periods = complement of merged DOWN ranges over [0, 1440)
  const upRanges = useMemo(() => {
    const out = []
    let cursor = 0
    for (const r of downRanges) {
      if (r.from > cursor) out.push({ from: cursor, to: r.from })
      cursor = Math.max(cursor, r.to)
    }
    if (cursor < 1440) out.push({ from: cursor, to: 1440 })
    return out
  }, [downRanges])

  // "Agora" marker only when inspecting today's date
  const nowMinute = useMemo(() => {
    const today = new Date()
    if (today.toISOString().slice(0, 10) !== dateIso) return null
    return today.getHours() * 60 + today.getMinutes()
  }, [dateIso])

  return (
    <div
      className="asf-timeline"
      role="img"
      aria-label={`${incidents.length} incidentes no dia, ${downRanges.length} períodos fora do ar`}
    >
      <div className="asf-timeline-row">
        <span className="asf-timeline-label asf-timeline-label--up">no ar</span>
        <div className="asf-timeline-track">
          <svg className="asf-timeline-svg" viewBox="0 0 1440 24" preserveAspectRatio="none">
            {HOUR_TICKS.map(h => (
              <line
                key={h}
                x1={h * 60} y1="0" x2={h * 60} y2="24"
                className={`asf-timeline-grid ${HOUR_LABELS.includes(h) ? 'asf-timeline-grid--major' : ''}`}
              />
            ))}
            {upRanges.map((r, i) => (
              <rect
                key={i}
                x={r.from}
                y="2"
                width={Math.max(r.to - r.from, 1)}
                height="20"
                rx="2"
                className="asf-timeline-rect asf-timeline-rect--up"
              />
            ))}
          </svg>
        </div>
      </div>

      <div className="asf-timeline-row">
        <span className="asf-timeline-label asf-timeline-label--down">fora</span>
        <div className="asf-timeline-track">
          <svg className="asf-timeline-svg" viewBox="0 0 1440 24" preserveAspectRatio="none">
            {HOUR_TICKS.map(h => (
              <line
                key={h}
                x1={h * 60} y1="0" x2={h * 60} y2="24"
                className={`asf-timeline-grid ${HOUR_LABELS.includes(h) ? 'asf-timeline-grid--major' : ''}`}
              />
            ))}
            {downRanges.map((r, i) => (
              <rect
                key={i}
                x={r.from}
                y="2"
                width={Math.max(r.to - r.from, 3)}
                height="20"
                rx="2"
                className="asf-timeline-rect asf-timeline-rect--down"
              />
            ))}
          </svg>
        </div>
      </div>

      {nowMinute != null && (
        <div
          className="asf-timeline-now"
          style={{ left: `calc(var(--asf-timeline-label-w) + (100% - var(--asf-timeline-label-w)) * ${nowMinute / 1440})` }}
          aria-label="agora"
        >
          <span className="asf-timeline-now-tag">agora</span>
        </div>
      )}

      <div className="asf-timeline-row asf-timeline-row--axis">
        <span className="asf-timeline-label" aria-hidden="true" />
        <div className="asf-timeline-hours" aria-hidden="true">
          {HOUR_TICKS.map(h => (
            <span
              key={h}
              className={`asf-timeline-tick ${HOUR_LABELS.includes(h) ? 'asf-timeline-tick--major' : ''}`}
              style={{ left: `${(h / 24) * 100}%` }}
            />
          ))}
          {HOUR_LABELS.map(h => (
            <span
              key={h}
              className={`asf-timeline-hour ${HOUR_PRIMARY.has(h) ? 'asf-timeline-hour--primary' : 'asf-timeline-hour--minor'}`}
              style={{ left: `${(h / 24) * 100}%` }}
            >
              {String(h).padStart(2, '0')}h
            </span>
          ))}
        </div>
      </div>
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

// Campaign-mode skeleton — matches CampaignFailureCard layout precisely so it
// doesn't morph on swap. Header (logo + identity + count) + 3 station rows.
function CampaignSkeletonCard() {
  return (
    <article className="asf-cskel-card" aria-hidden="true">
      <div className="asf-cskel-head">
        <div className="asf-cskel-logo" />
        <div className="asf-cskel-id">
          <div className="asf-cskel-line w50" />
          <div className="asf-cskel-line w70" />
          <div className="asf-cskel-strip">
            {Array.from({ length: 10 }).map((_, i) => (
              <span key={i} className="asf-cskel-dot" />
            ))}
          </div>
        </div>
        <div className="asf-cskel-count" />
      </div>
      {[0, 1, 2].map(i => (
        <div key={i} className="asf-cskel-row">
          <div className="asf-cskel-avatar" />
          <div className="asf-cskel-id">
            <div className="asf-cskel-line w40" />
            <div className="asf-cskel-line w25" />
          </div>
          <div className="asf-cskel-bar" />
        </div>
      ))}
      <div className="asf-cskel-foot">
        <div className="asf-cskel-line w30" />
      </div>
    </article>
  )
}

// Shadow UI empty state for campaign mode (§4.7 DESIGN.md).
// Two-column layout: action/value left, mockup right.
function CampaignEmptyState({ dateIso, isHistorical }) {
  const { day, month } = fmtMasthead(dateIso)
  return (
    <div className="asf-cempty">
      <div className="asf-cempty-text">
        <div className="asf-cempty-mark" aria-hidden="true">
          <svg width="44" height="44" viewBox="0 0 44 44" fill="none">
            <circle cx="22" cy="22" r="20" stroke="currentColor" strokeWidth="1.5" opacity=".25" />
            <path d="M14 22.5l5.5 5.5L31 16" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </div>
        <h2>
          {isHistorical
            ? 'Sem campanhas com falha registrada.'
            : `Nenhuma campanha falhou em ${day} de ${month}.`}
        </h2>
        <p>
          {isHistorical
            ? 'Quando uma campanha perder slot em qualquer dia da vigência, ela vai listar aqui.'
            : 'Tudo dentro do programado. Quando algo falhar, cada campanha vira um card com as emissoras que precisam ser cobradas.'}
        </p>
      </div>
      <div className="asf-cempty-preview" aria-hidden="true">
        <div className="asf-cempty-preview-label">Quando algo falhar, aparece assim:</div>
        <div className="asf-cempty-card">
          <div className="asf-cempty-card-head">
            <div className="asf-cempty-logo" />
            <div className="asf-cempty-meta">
              <div className="asf-cempty-line w55" />
              <div className="asf-cempty-line w70" />
              <div className="asf-cempty-strip">
                {[0,1,2,3,4,5,6,7,8,9].map(i => (
                  <span key={i} className={`asf-cempty-dot ${i < 3 ? 'asf-cempty-dot--on' : ''}`} />
                ))}
              </div>
            </div>
            <div className="asf-cempty-count" />
          </div>
          {[0, 1].map(i => (
            <div key={i} className="asf-cempty-row">
              <div className="asf-cempty-avatar" />
              <div className="asf-cempty-meta">
                <div className="asf-cempty-line w40" />
              </div>
              <div className="asf-cempty-bar" />
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// Campaign-mode KPI strip — replaces the station-first stats when viewMode is by_campaign.
function CampaignKpiStrip({ summary, mode }) {
  if (!summary) return null
  const isHistorical = mode === 'historical'
  return (
    <dl className="asf-ckpis" aria-label="Resumo de campanhas">
      <div className="asf-ckpi">
        <dd>{summary.campaigns ?? 0}</dd>
        <dt>{isHistorical ? 'campanhas com falha' : 'campanhas'}</dt>
      </div>
      <div className="asf-ckpi-sep" aria-hidden="true" />
      <div className="asf-ckpi">
        <dd>
          {isHistorical
            ? (summary.total_failure_days ?? 0)
            : (summary.stations ?? 0)}
        </dd>
        <dt>{isHistorical ? 'dias com falha acumulados' : 'emissoras'}</dt>
      </div>
      <div className="asf-ckpi-sep" aria-hidden="true" />
      <div className="asf-ckpi asf-ckpi--deficit">
        <dd>{(isHistorical ? null : summary.total_deficit) ?? '—'}</dd>
        <dt>veiculações faltam</dt>
        {/* O déficit do dia separado nos dois tipos (D7): a emissora que tocou
            fora da faixa não pode ser lida como silêncio. Os dois somam o total
            acima. No histórico o summary não traz déficit — nada a separar. */}
        {!isHistorical && (
          <DeficitSplit
            total={summary.total_deficit}
            absent={summary.total_deficit_absent}
            offSlot={summary.total_deficit_off_slot}
          />
        )}
      </div>
    </dl>
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
  // Deep-link via query params (vindo do sininho de notificações):
  //   /admin/station-failures?view=by_campaign&date=YYYY-MM-DD&campaign=<uuid>
  // Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md §4.4
  const [searchParams, setSearchParams] = useSearchParams()
  const qpView     = searchParams.get('view')      // 'by_campaign' | 'by_station' | null
  const qpDate     = searchParams.get('date')      // YYYY-MM-DD | null
  const qpCampaign = searchParams.get('campaign')  // uuid | null

  const [date, setDate] = useState(qpDate || isoYesterday())
  const [minDown, setMinDown] = useState(60)

  // Toggle viewMode + sub-tab + drill-in state
  const [viewMode, setViewMode] = useState(
    qpView === 'by_campaign' ? 'by_campaign' : qpView === 'by_day' ? 'by_day' : 'by_station'
  ) // 'by_station' | 'by_campaign' | 'by_day'
  const [subTab, setSubTab] = useState('daily')          // 'daily' | 'historical'
  const [historyPage, setHistoryPage] = useState(1)
  const [drillCampaignId, setDrillCampaignId] = useState(qpCampaign || null)

  // Limpa os query params 1× no mount pra que navegações subsequentes
  // (mudar de data, fechar drawer) não fiquem referenciando a entrada antiga.
  useEffect(() => {
    if (qpView || qpDate || qpCampaign) {
      setSearchParams({}, { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const isByStation  = viewMode === 'by_station'
  const isByCampaign = viewMode === 'by_campaign'
  const isByDay      = viewMode === 'by_day'

  const queryClient = useQueryClient()

  const stationQ = useStationFailures({
    date, minDownSeconds: minDown,
    enabled: isByStation,
  })
  const campaignQ = useCampaignFailures({
    mode: subTab === 'historical' ? 'historical' : 'by_date',
    date: subTab === 'daily' ? date : undefined,
    page: historyPage,
    pageSize: 50,
    enabled: isByCampaign,
  })

  // A aba "Por dia" é dona da própria query (ela tem seu seletor de período),
  // então o botão de atualizar do header invalida a chave em vez de chamar um
  // refetch que a página não segura.
  const activeQ = isByStation ? stationQ : campaignQ
  const isFetching = isByDay
    ? queryClient.isFetching({ queryKey: ['failures-daily'] }) > 0
    : activeQ.isFetching
  const refetch = isByDay
    ? () => queryClient.invalidateQueries({ queryKey: ['failures-daily'] })
    : activeQ.refetch
  const isLoading = isByDay ? false : activeQ.isLoading
  const error     = isByDay ? null  : activeQ.error

  // Station view derived
  const stations = stationQ.data?.stations ?? []
  const summary  = stationQ.data?.summary
  const masthead = useMemo(() => fmtMasthead(date), [date])
  const incidents = useMemo(() => flattenIncidents(stations, date), [stations, date])
  const hasData   = !stationQ.isLoading && stations.length > 0

  // Campaign view derived
  const campaigns = campaignQ.data?.campaigns ?? []
  const campaignTotal = campaignQ.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(campaignTotal / 50))

  return (
    <div className="asf">
      {/* ── Page header (padrão admin: title-icon + título + subtítulo + controles) ── */}
      <header className="asf-header">
        <div className="asf-title-row">
          <div className="asf-title-icon" aria-hidden="true">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none"
                 stroke="currentColor" strokeWidth="1.8"
                 strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2 L2.5 19 H21.5 Z" />
              <path d="M12 9 V13.5" />
              <circle cx="12" cy="16.5" r="0.9" fill="currentColor" stroke="none" />
            </svg>
          </div>
          <div className="asf-title-stack">
            <div className="asf-title-line">
              <h1 className="asf-title">Falhas das emissoras</h1>
              {!isByDay && (
                <span className="asf-date-pill" title={`${masthead.wday}, ${masthead.day} de ${masthead.month} de ${masthead.year}`}>
                  {fmtRelativeDate(date)}
                </span>
              )}
            </div>
            <p className="asf-subtitle">
              {isByDay ? (
                <>Série diária <span aria-hidden="true">·</span> qual parte do mês concentra as falhas</>
              ) : (
                <>
                  {masthead.wday}, {masthead.day} de {masthead.month} de {masthead.year} <span aria-hidden="true">·</span>{' '}
                  cruzamento de stream-down e slot perdido por dia
                </>
              )}
            </p>
          </div>
        </div>

        <div className="asf-controls">
          {/* Em "Por dia" o período mora dentro da própria aba (range, não um
              dia só) — dois seletores de data competindo confundiria. */}
          {!isByDay && (
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
          )}
          {(isByStation || isByDay) && (
            <label className="asf-field">
              <span>Severidade</span>
              <select value={minDown} onChange={e => setMinDown(Number(e.target.value))}>
                {MIN_DOWN_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </label>
          )}
          <button className="asf-refresh" onClick={() => refetch()} disabled={isFetching}
                  title="Atualizar" aria-label="Atualizar dados">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true"
                 className={isFetching ? 'asf-spin' : ''}>
              <path d="M1.5 7a5.5 5.5 0 1 0 1.65-3.9M1.5 1.5v3.2h3.2"
                    stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </button>
        </div>
      </header>

      {/* ── KPI strip (por emissora) ─────────────────────────────────── */}
      {isByStation && summary && summary.stations_with_failure > 0 && (
        <section className="asf-hero">
          <dl className="asf-stats">
            <div className="asf-stat">
              <dt>emissoras</dt>
              <dd>{summary.stations_with_failure}</dd>
            </div>
            <div className="asf-stat-sep" aria-hidden="true" />
            <div className="asf-stat">
              <dt>tempo fora total</dt>
              <dd>{fmtDuration(summary.total_down_seconds)}</dd>
            </div>
            <div className="asf-stat-sep" aria-hidden="true" />
            <div className="asf-stat">
              <dt>campanhas afetadas</dt>
              <dd>{summary.affected_campaigns}</dd>
            </div>
          </dl>
          {incidents.length > 0 && (
            <div className="asf-hero-ribbon">
              <span className="asf-hero-ribbon-label">24 horas do dia</span>
              <HeroTimeline incidents={incidents} dateIso={date} />
            </div>
          )}
        </section>
      )}

      {/* ── KPI strip (por campanha) ─────────────────────────────────── */}
      {isByCampaign && campaignQ.data?.summary && (
        <section className="asf-hero">
          <CampaignKpiStrip
            summary={campaignQ.data.summary}
            mode={subTab === 'historical' ? 'historical' : 'daily'}
          />
        </section>
      )}

      {/* ── Toggle Por emissora / Por campanha / Por dia ─────────────── */}
      <div className="asf-mode-toggle" role="tablist" aria-label="Modo de visualização">
        <button
          role="tab"
          aria-selected={isByStation}
          className={`asf-mode-btn ${isByStation ? 'asf-mode-btn--active' : ''}`}
          onClick={() => setViewMode('by_station')}
        >
          Por emissora
        </button>
        <button
          role="tab"
          aria-selected={isByCampaign}
          className={`asf-mode-btn ${isByCampaign ? 'asf-mode-btn--active' : ''}`}
          onClick={() => setViewMode('by_campaign')}
        >
          Por campanha
        </button>
        <button
          role="tab"
          aria-selected={isByDay}
          className={`asf-mode-btn ${isByDay ? 'asf-mode-btn--active' : ''}`}
          onClick={() => setViewMode('by_day')}
        >
          Por dia
        </button>
      </div>

      {error && (
        <div className="asf-error" role="alert">
          Erro ao carregar: {String(error.message || error)}
        </div>
      )}

      {/* ── Conteúdo: por emissora (atual) ─────────────────────────── */}
      {isByStation && isLoading && (
        <div className="asf-list">
          {[0, 1, 2, 3].map(i => <SkeletonCard key={i} />)}
        </div>
      )}
      {isByStation && !isLoading && stations.length === 0 && !error && <EmptyState dateIso={date} />}
      {isByStation && hasData && (
        <ol className="asf-list" aria-label="Emissoras com falha, ordenadas por gravidade">
          {stations.map((s, idx) => (
            <li key={s.station.id}>
              <StationFailureCard data={s} rank={idx + 1} dateIso={date} />
            </li>
          ))}
        </ol>
      )}

      {/* ── Conteúdo: por dia (série temporal) ─────────────────────── */}
      {isByDay && (
        <FailuresDailyView
          minDownSeconds={minDown}
          onPickDay={iso => {
            setDate(iso)
            setViewMode('by_station')
            // O toggle fica no topo da página; sem isso o usuário clica numa
            // barra no meio do gráfico e o card do dia abre fora da viewport.
            window.scrollTo({
              top: 0,
              behavior: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
                ? 'auto' : 'smooth',
            })
          }}
        />
      )}

      {/* ── Conteúdo: por campanha ─────────────────────────────────── */}
      {isByCampaign && (
        <>
          <div className="asf-subtabs" role="tablist" aria-label="Sub-modo">
            <button
              role="tab"
              aria-selected={subTab === 'daily'}
              className={`asf-subtab ${subTab === 'daily' ? 'asf-subtab--active' : ''}`}
              onClick={() => setSubTab('daily')}
            >
              Falhas de {masthead.day}/{String(parseLocalDate(date).getMonth() + 1).padStart(2, '0')}
            </button>
            <button
              role="tab"
              aria-selected={subTab === 'historical'}
              className={`asf-subtab ${subTab === 'historical' ? 'asf-subtab--active' : ''}`}
              onClick={() => { setSubTab('historical'); setHistoryPage(1) }}
            >
              Por Campanha (histórico)
            </button>
          </div>

          {isLoading ? (
            <div className="asf-camp-grid" aria-hidden="true">
              {[0, 1, 2, 3].map(i => <CampaignSkeletonCard key={i} />)}
            </div>
          ) : campaigns.length === 0 ? (
            <CampaignEmptyState dateIso={date} isHistorical={subTab === 'historical'} />
          ) : subTab === 'daily' ? (
            <div className="asf-camp-grid">
              {campaigns.map(entry => (
                <CampaignFailureCard
                  key={String(entry.campaign.id)}
                  entry={entry}
                  onOpen={() => setDrillCampaignId(entry.campaign.id)}
                />
              ))}
            </div>
          ) : (
            <>
              <div className="asf-hist-table-wrap">
                <table className="asf-hist-table">
                  <thead>
                    <tr>
                      <th>Campanha</th>
                      <th>Status</th>
                      <th>Emissoras c/ falha</th>
                      <th>Dias c/ falha</th>
                      <th>Déficit</th>
                      <th>Bonificada</th>
                      <th aria-label="Ações" />
                    </tr>
                  </thead>
                  <tbody>
                    {campaigns.map(entry => (
                      <CampaignFailureRow
                        key={String(entry.campaign.id)}
                        entry={entry}
                        onOpen={() => setDrillCampaignId(entry.campaign.id)}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
              {campaignTotal > 50 && (
                <div className="asf-pager">
                  <button
                    disabled={historyPage <= 1}
                    onClick={() => setHistoryPage(p => Math.max(1, p - 1))}
                  >← Anterior</button>
                  <span>página {historyPage} de {totalPages}</span>
                  <button
                    disabled={historyPage >= totalPages}
                    onClick={() => setHistoryPage(p => p + 1)}
                  >Próxima →</button>
                </div>
              )}
            </>
          )}
        </>
      )}

      {drillCampaignId && (
        <CampaignFailureDrawer
          campaignId={drillCampaignId}
          onClose={() => setDrillCampaignId(null)}
        />
      )}
    </div>
  )
}
