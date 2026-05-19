import { useState } from 'react'
import { useStationFailures } from '../api/hooks'
import StationFailureCard from '../components/StationFailureCard'
import './AdminStationFailuresPage.css'

function isoYesterday() {
  const d = new Date()
  d.setDate(d.getDate() - 1)
  return d.toISOString().slice(0, 10)
}
function isoToday() {
  return new Date().toISOString().slice(0, 10)
}
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

const MIN_DOWN_OPTIONS = [
  { value: 0, label: 'Tudo' },
  { value: 60, label: '≥ 1 min' },
  { value: 300, label: '≥ 5 min' },
  { value: 1800, label: '≥ 30 min' },
]

export default function AdminStationFailuresPage() {
  const [date, setDate] = useState(isoYesterday())
  const [minDown, setMinDown] = useState(60)

  const { data, isLoading, isFetching, refetch, error } = useStationFailures({
    date, minDownSeconds: minDown,
  })

  const stations = data?.stations ?? []
  const summary = data?.summary

  return (
    <div className="asf-page">
      <header className="asf-header">
        <div>
          <h1 className="asf-title">Emissoras com falha</h1>
          <p className="asf-subtitle">Quais rádios saíram do ar e quais campanhas perderam slot</p>
        </div>
        <div className="asf-controls">
          <label className="asf-control">
            <span>Data</span>
            <input
              type="date"
              value={date}
              onChange={e => setDate(e.target.value)}
              max={isoToday()}
              min={isoMinusDays(90)}
            />
          </label>
          <label className="asf-control">
            <span>Mín. tempo fora</span>
            <select value={minDown} onChange={e => setMinDown(Number(e.target.value))}>
              {MIN_DOWN_OPTIONS.map(o => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
          </label>
          <button className="asf-refresh" onClick={() => refetch()} disabled={isFetching}>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"
                 className={isFetching ? 'asf-spin' : ''}>
              <path d="M2 8a6 6 0 1 0 1.76-4.24M2 2v3.5h3.5" stroke="currentColor" strokeWidth="1.5"
                    strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
            Atualizar
          </button>
        </div>
      </header>

      {summary && summary.stations_with_failure > 0 && (
        <div className="asf-summary-strip">
          <strong>{summary.stations_with_failure}</strong>{' '}
          {summary.stations_with_failure === 1 ? 'emissora' : 'emissoras'}
          <span className="asf-dot">·</span>
          <strong>{fmtDuration(summary.total_down_seconds)}</strong> total fora
          <span className="asf-dot">·</span>
          <strong>{summary.affected_campaigns}</strong>{' '}
          {summary.affected_campaigns === 1 ? 'campanha afetada' : 'campanhas afetadas'}
        </div>
      )}

      {error && (
        <div className="asf-error">Erro ao carregar: {String(error.message || error)}</div>
      )}

      {isLoading && (
        <div className="asf-skeleton-list">
          {[0, 1, 2, 3, 4].map(i => (
            <div key={i} className="asf-skeleton-card" />
          ))}
        </div>
      )}

      {!isLoading && stations.length === 0 && !error && (
        <div className="asf-empty">
          <div className="asf-empty-icon" aria-hidden="true">
            <svg width="56" height="56" viewBox="0 0 56 56" fill="none">
              <circle cx="28" cy="28" r="22" stroke="currentColor" strokeWidth="2" opacity="0.4"/>
              <path d="M19 28l6 6 12-12" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </div>
          <h2>Nenhuma emissora teve falha em {date}</h2>
          <p>Tudo no ar. Pra ver o pulso atual da infra, abra <a href="/admin/overview">Visão geral</a>.</p>
          <div className="asf-empty-shadow">
            <div className="asf-skeleton-card asf-skeleton-card-ghost" />
            <div className="asf-skeleton-card asf-skeleton-card-ghost" />
          </div>
        </div>
      )}

      {!isLoading && stations.length > 0 && (
        <div className="asf-list">
          {stations.map(s => (
            <StationFailureCard key={s.station.id} data={s} />
          ))}
        </div>
      )}
    </div>
  )
}
