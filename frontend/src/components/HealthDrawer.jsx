import { useState, useMemo } from 'react'
import { useStationHealthEvents } from '../api/hooks'
import StationAvatar from './StationAvatar'
import HealthTimeline from './HealthTimeline'

function fmtDuration(ms) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

export default function HealthDrawer({ station, onClose }) {
  const [days, setDays] = useState(7)
  const { data, isLoading } = useStationHealthEvents(station?.id, days)

  const events      = data?.events ?? []
  const periodEnd   = data?.period_end   ? new Date(data.period_end).getTime()   : Date.now()
  const periodStart = data?.period_start ? new Date(data.period_start).getTime() : periodEnd - days * 86400000

  const stats = useMemo(() => {
    const downs = events.filter(e => e.event_type === 'down')
    const totalDownMs = downs.reduce((acc, e) => acc + (e.duration_seconds ?? 0) * 1000, 0)
    const periodMs = periodEnd - periodStart
    const uptime = periodMs > 0 ? Math.max(0, (1 - totalDownMs / periodMs) * 100) : 100
    const longest = downs.reduce((max, e) => Math.max(max, (e.duration_seconds ?? 0) * 1000), 0)
    const avg = downs.length > 0 ? totalDownMs / downs.length : 0
    return { uptime, incidents: downs.length, longestMs: longest, avgMs: avg }
  }, [events, periodStart, periodEnd])

  if (!station) return null

  const uptimeCls = stats.uptime >= 99 ? 'health-stat-value--ok'
    : stats.uptime >= 95 ? 'health-stat-value--warn'
    : 'health-stat-value--bad'

  // Online só se a campanha está ativa E o stream não está em outage agora.
  const currentStatus =
    station.monitoring_status === 'active' && !station.is_currently_down ? 'up' : 'down'

  return (
    <>
      <div className="health-drawer-overlay" onClick={onClose} />
      <div className="health-drawer" role="dialog" aria-label={`Saúde: ${station.name}`}>
        <div className="health-drawer-header">
          <StationAvatar station={station} size={40} />
          <div className="health-drawer-station-info">
            <div className="health-drawer-station-name">{station.name}</div>
            <div className="health-drawer-station-meta">
              {station.band}{station.frequency_mhz ? ` · ${station.frequency_mhz} MHz` : ''}
              {station.city ? ` · ${station.city}` : ''}
              {station.state ? `/${station.state}` : ''}
            </div>
          </div>
          <span className={`health-drawer-status health-drawer-status--${currentStatus}`}>
            {currentStatus === 'up' ? '● Online' : '● Offline'}
          </span>
          <button className="health-drawer-close" onClick={onClose} aria-label="Fechar">✕</button>
        </div>

        <div className="health-drawer-body">
          <div className="health-drawer-period-tabs">
            {[1, 7].map(d => (
              <button
                key={d}
                type="button"
                className={`health-drawer-period-tab${days === d ? ' active' : ''}`}
                onClick={() => setDays(d)}
              >
                {d === 1 ? '1 dia' : '7 dias'}
              </button>
            ))}
          </div>

          {isLoading ? (
            <div className="skeleton" style={{ height: 36, borderRadius: 'var(--radius-md)' }} />
          ) : (
            <HealthTimeline
              events={events}
              periodStart={periodStart}
              periodEnd={periodEnd}
            />
          )}

          <div className="health-stats-grid">
            <div className="health-stat">
              <span className="health-stat-label">Uptime</span>
              <span className={`health-stat-value ${uptimeCls}`}>{stats.uptime.toFixed(1)}%</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Quedas</span>
              <span className="health-stat-value">{stats.incidents}</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Maior queda</span>
              <span className="health-stat-value" style={{ fontSize: 15 }}>
                {stats.longestMs > 0 ? fmtDuration(stats.longestMs) : '—'}
              </span>
            </div>
            <div className="health-stat">
              <span className="health-stat-label">Média por queda</span>
              <span className="health-stat-value" style={{ fontSize: 15 }}>
                {stats.avgMs > 0 ? fmtDuration(stats.avgMs) : '—'}
              </span>
            </div>
          </div>

          <div className="health-events-section">
            <div className="health-events-label">Histórico de quedas</div>
            <div className="health-events-list">
              {events.filter(e => e.event_type === 'down').length === 0 ? (
                <div className="health-event-empty">Nenhuma queda no período</div>
              ) : (
                [...events]
                  .filter(e => e.event_type === 'down')
                  .sort((a, b) => new Date(b.event_at) - new Date(a.event_at))
                  .map(ev => (
                    <div key={ev.id} className="health-event-row">
                      <span className="health-event-time">
                        {new Date(ev.event_at).toLocaleString('pt-BR', {
                          day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
                        })}
                      </span>
                      <span className="health-event-dur">
                        {ev.duration_seconds != null ? fmtDuration(ev.duration_seconds * 1000) : '—'}
                      </span>
                      <span style={{ color: 'var(--c-text-3)', fontSize: 12 }}>offline</span>
                    </div>
                  ))
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  )
}
