import { useStations } from '../api/hooks'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

// ── Constants ────────────────────────────────────────────────────
const STATUS_ORDER = { error: 0, active: 1, calibrating: 2, paused: 3 }

const STATUS_LABEL = {
  active:      'Ativo',
  paused:      'Pausado',
  calibrating: 'Calibrando',
  error:       'Erro',
}

const STATUS_BADGE_CLASS = {
  active:      'badge badge-active',
  paused:      'badge badge-ended',
  calibrating: 'badge badge-paused',
  error:       'badge badge-error',
}

const DOT_CLASS = {
  active:      'station-status-dot dot-active',
  paused:      'station-status-dot dot-paused',
  calibrating: 'station-status-dot dot-calibrating',
  error:       'station-status-dot dot-error',
}

// ── Helpers ──────────────────────────────────────────────────────
function formatTimestamp(date) {
  if (!date) return '—'
  return date.toLocaleTimeString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    hour:     '2-digit',
    minute:   '2-digit',
    second:   '2-digit',
  })
}

function relativeTime(isoString) {
  if (!isoString) return '—'
  const diff = Date.now() - new Date(isoString).getTime()
  if (diff < 0) return 'agora'
  if (diff < 60_000) return 'agora'
  const mins = Math.round(diff / 60_000)
  if (mins < 60) return `há ${mins} min`
  const hours = Math.round(mins / 60)
  return `há ${hours}h`
}

function sortStations(stations) {
  return [...stations].sort((a, b) => {
    const oa = STATUS_ORDER[a.monitoring_status] ?? 99
    const ob = STATUS_ORDER[b.monitoring_status] ?? 99
    if (oa !== ob) return oa - ob
    return a.name.localeCompare(b.name, 'pt-BR')
  })
}

// ── Sub-components ───────────────────────────────────────────────

function RefreshIcon({ spinning }) {
  return (
    <svg
      className={`refresh-icon${spinning ? ' spin' : ''}`}
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M21 2v6h-6" />
      <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
      <path d="M3 22v-6h6" />
      <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
    </svg>
  )
}

function AntennaIcon() {
  return (
    <svg
      width="48"
      height="48"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M12 2L2 7l10 5 10-5-10-5z" />
      <path d="M2 17l10 5 10-5" />
      <path d="M2 12l10 5 10-5" />
    </svg>
  )
}

function StatusPills({ stations }) {
  const counts = stations.reduce(
    (acc, s) => {
      const key = s.monitoring_status
      acc[key] = (acc[key] ?? 0) + 1
      return acc
    },
    {}
  )

  const pills = [
    { key: 'active',      label: 'ativas',      dotStyle: { background: 'var(--c-success)' } },
    { key: 'error',       label: 'com erro',     dotStyle: { background: 'var(--c-danger)' } },
    { key: 'paused',      label: 'pausadas',     dotStyle: { background: 'var(--c-text-3)' } },
    { key: 'calibrating', label: 'calibrando',   dotStyle: { background: 'var(--c-warning)' } },
  ]

  return (
    <div className="monitoring-status-pills">
      {pills.map(({ key, label, dotStyle }) => {
        const count = counts[key] ?? 0
        return (
          <div key={key} className="monitoring-pill">
            <span className="monitoring-pill-dot" style={dotStyle} />
            <span>
              <strong>{count}</strong> {label}
            </span>
          </div>
        )
      })}
    </div>
  )
}

function SkeletonGrid() {
  return (
    <div className="station-grid">
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="station-card" aria-hidden="true">
          <div className="station-card-header">
            <div
              className="skeleton-cell"
              style={{ width: 10, height: 10, borderRadius: 'var(--radius-full)', flexShrink: 0 }}
            />
            <div className="skeleton-cell" style={{ width: '60%', height: 14 }} />
          </div>
          <div className="skeleton-cell" style={{ width: '80%', height: 12 }} />
          <div className="station-footer">
            <div className="skeleton-cell" style={{ width: 56, height: 20, borderRadius: 'var(--radius-full)' }} />
            <div className="skeleton-cell" style={{ width: 48, height: 12 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function EmptyState() {
  return (
    <div className="detection-empty" style={{ marginTop: 8 }}>
      <div className="detection-empty-icon">
        <AntennaIcon />
      </div>
      <h3>Nenhuma emissora monitorada</h3>
      <p>Cadastre emissoras para começar a monitorar veiculações em tempo real.</p>
      <Link to="/stations" className="btn btn-primary btn-sm">
        Ir para Emissoras
      </Link>
    </div>
  )
}

function StationCard({ station }) {
  const isError = station.monitoring_status === 'error'

  const meta = [
    station.band,
    station.frequency_mhz ? `${station.frequency_mhz} MHz` : null,
    [station.city, station.state].filter(Boolean).join('/') || null,
  ]
    .filter(Boolean)
    .join(' · ')

  return (
    <div className={`station-card${isError ? ' status-error' : ''}`}>
      <div className="station-card-header">
        <span
          className={DOT_CLASS[station.monitoring_status] ?? 'station-status-dot dot-paused'}
          title={STATUS_LABEL[station.monitoring_status] ?? station.monitoring_status}
        />
        <span className="station-name">{station.name}</span>
      </div>

      <div className="station-meta">{meta || '—'}</div>

      <div className="station-footer">
        <span className={STATUS_BADGE_CLASS[station.monitoring_status] ?? 'badge badge-ended'}>
          {STATUS_LABEL[station.monitoring_status] ?? station.monitoring_status}
        </span>
        <span className="station-meta" style={{ flexShrink: 0 }}>
          {relativeTime(station.last_health_check)}
        </span>
      </div>

      {station.consecutive_failures > 0 && (
        <div className="station-failures">
          {station.consecutive_failures} falha{station.consecutive_failures !== 1 ? 's' : ''} consecutiva{station.consecutive_failures !== 1 ? 's' : ''}
        </div>
      )}
    </div>
  )
}

// ── Page ─────────────────────────────────────────────────────────
export default function MonitoringPage() {
  const { data: stations = [], isLoading, isFetching, refetch } = useStations()

  const [lastUpdated, setLastUpdated] = useState(null)
  const prevFetchingRef = useRef(false)

  // Track when a fetch transitions from true → false (completed)
  useEffect(() => {
    if (prevFetchingRef.current && !isFetching) {
      setLastUpdated(new Date())
    }
    prevFetchingRef.current = isFetching
  }, [isFetching])

  // Auto-refresh every 120 seconds
  useEffect(() => {
    const id = setInterval(() => {
      refetch()
    }, 120_000)
    return () => clearInterval(id)
  }, [refetch])

  const sorted = sortStations(stations)

  return (
    <div>
      {/* Header */}
      <div className="monitoring-header">
        <h2>Monitoramento</h2>
        <div className="monitoring-meta">
          {lastUpdated && (
            <span className="monitoring-timestamp">
              Última atualização: {formatTimestamp(lastUpdated)}
            </span>
          )}
          <button
            className={`refresh-btn${isFetching ? ' refreshing' : ''}`}
            onClick={refetch}
            disabled={isFetching}
            title="Atualizar agora"
          >
            <RefreshIcon spinning={isFetching} />
            Atualizar
          </button>
        </div>
      </div>

      {/* Status pills (shown even while loading, uses available data) */}
      {!isLoading && stations.length > 0 && <StatusPills stations={stations} />}

      {/* Content */}
      {isLoading ? (
        <SkeletonGrid />
      ) : stations.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="station-grid">
          {sorted.map(s => (
            <StationCard key={s.id} station={s} />
          ))}
        </div>
      )}
    </div>
  )
}
