import { useState, useMemo } from 'react'
import { useStreamHealth, useStationHealthEvents } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import HealthTimeline from '../components/HealthTimeline'
import { tokenize, matchesAllTokens } from '../utils/search'

// ── Mini 7-day bar ────────────────────────────────────────────────────────────
function MiniHealthBar({ dailySummary }) {
  if (!dailySummary || dailySummary.length === 0) {
    return (
      <div className="health-mini-bar">
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className="health-mini-day health-mini-day--empty" title="Sem dados" />
        ))}
      </div>
    )
  }
  return (
    <div className="health-mini-bar">
      {dailySummary.map(d => {
        const cls = d.uptime_pct < 95 ? 'health-mini-day--warn' : 'health-mini-day--ok'
        return (
          <div
            key={d.date}
            className={`health-mini-day ${cls}`}
            title={`${d.date}: ${d.uptime_pct.toFixed(1)}% uptime`}
          />
        )
      })}
    </div>
  )
}

// ── Status dot ────────────────────────────────────────────────────────────────
// Combina monitoring_status (estado configurado) com is_currently_down (estado
// real do stream). Se o stream caiu, força ponto vermelho mesmo se a campanha
// está ativa — assim a UI reflete o sintoma, não só a intenção.
function StatusDot({ status, isCurrentlyDown }) {
  if (isCurrentlyDown) {
    return <span className="station-status-dot dot-error" />
  }
  const cls =
    status === 'active'      ? 'dot-active'      :
    status === 'error'       ? 'dot-error'        :
    status === 'calibrating' ? 'dot-calibrating'  : 'dot-paused'
  return <span className={`station-status-dot ${cls}`} />
}

// ── Worker pill ───────────────────────────────────────────────────────────────
// Reflete o estado do worker LOCAL — o processo que monitora o stream daquela
// station. Distinto do StatusDot (que é o estado do stream REMOTO). Combinações
// úteis pra debug:
//   stream OK + worker running   → monitorando normalmente
//   stream down + worker running → worker vivo, aguardando o stream voltar
//   worker stalled               → recebeu bytes mas parou há >30s (bug/stall)
//   worker missing               → station ativa sem worker registrado (drift)
function WorkerPill({ status, lastPCMAt }) {
  if (!status) return null
  const styles = {
    running:  { label: 'Worker ativo',    cls: 'worker-pill worker-pill--ok' },
    stalled:  { label: 'Worker travado',  cls: 'worker-pill worker-pill--warn' },
    missing:  { label: 'Worker inativo',  cls: 'worker-pill worker-pill--bad' },
  }
  const s = styles[status] ?? styles.missing
  const title = lastPCMAt
    ? `Última atividade: ${relativeTime(lastPCMAt)}`
    : 'Worker nunca recebeu áudio desde o boot'
  return (
    <span className={s.cls} title={title}>
      <span className="worker-pill-dot" />
      {s.label}
    </span>
  )
}

// ── Relative time ─────────────────────────────────────────────────────────────
function relativeTime(iso) {
  if (!iso) return null
  const diff = Date.now() - new Date(iso)
  const s = Math.floor(diff / 1000)
  if (s < 60)    return `há ${s}s`
  if (s < 3600)  return `há ${Math.floor(s / 60)}min`
  if (s < 86400) return `há ${Math.floor(s / 3600)}h`
  return `há ${Math.floor(s / 86400)}d`
}

// ── Format duration in ms ─────────────────────────────────────────────────────
function fmtDuration(ms) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

// ── Drawer ────────────────────────────────────────────────────────────────────
function HealthDrawer({ station, onClose }) {
  const [days, setDays] = useState(7)
  const { data, isLoading } = useStationHealthEvents(station?.id, days)

  if (!station) return null

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
          <WorkerPill status={station.worker_status} lastPCMAt={station.worker_last_pcm_at} />
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

// ── Main page ─────────────────────────────────────────────────────────────────
const HEALTH_FILTERS = ['Todas', 'Com falha', 'Estável', 'Worker problema']

export default function MonitoringPage() {
  const [search, setSearch]         = useState('')
  const [band, setBand]             = useState('Todas')
  const [healthFilter, setHealth]   = useState('Todas')
  const [page, setPage]             = useState(1)
  const [selectedId, setSelectedId] = useState(null)
  const PER_PAGE = 25

  const { data: stations = [], isLoading } = useStreamHealth()

  const filtered = useMemo(() => {
    let list = stations
    const tokens = tokenize(search)
    if (tokens.length > 0) {
      const fields = [
        'name',
        'city',
        'state',
        'band',
        s => s.frequency_mhz != null ? String(s.frequency_mhz) : '',
      ]
      list = list.filter(s => matchesAllTokens(s, fields, tokens))
    }
    if (band !== 'Todas') list = list.filter(s => s.band === band)
    if (healthFilter === 'Com falha') {
      list = list.filter(s => s.uptime_pct < 99.9 || s.monitoring_status === 'error' || s.is_currently_down)
    } else if (healthFilter === 'Estável') {
      list = list.filter(s => s.uptime_pct >= 99.9 && s.monitoring_status !== 'error' && !s.is_currently_down)
    } else if (healthFilter === 'Worker problema') {
      // Mostra apenas stations cujo worker LOCAL (não o stream remoto) está
      // com problema: travado ou não registrado. Útil pra distinguir issue de
      // infra interna vs queda real do broadcaster.
      list = list.filter(s => s.worker_status === 'stalled' || s.worker_status === 'missing')
    }
    return [...list].sort((a, b) => {
      // Stations atualmente caídas vão pro topo, depois com erro de cadastro,
      // depois pior uptime, depois alfabético.
      if (a.is_currently_down && !b.is_currently_down) return -1
      if (b.is_currently_down && !a.is_currently_down) return 1
      if (a.monitoring_status === 'error' && b.monitoring_status !== 'error') return -1
      if (b.monitoring_status === 'error' && a.monitoring_status !== 'error') return 1
      if (a.uptime_pct !== b.uptime_pct) return a.uptime_pct - b.uptime_pct
      return a.name.localeCompare(b.name)
    })
  }, [stations, search, band, healthFilter])

  const totalPages  = Math.max(1, Math.ceil(filtered.length / PER_PAGE))
  const currentPage = Math.min(page, totalPages)
  const pageItems   = filtered.slice((currentPage - 1) * PER_PAGE, currentPage * PER_PAGE)

  const selectedStation = stations.find(s => s.id === selectedId) ?? null

  return (
    <div>
      <div className="health-page-header">
        <div className="health-page-title">
          <h2 style={{ margin: 0 }}>Saúde do Stream</h2>
          {!isLoading && (
            <div className="health-page-count">
              {stations.length} emissora{stations.length !== 1 ? 's' : ''} em campanha ativa
            </div>
          )}
        </div>
      </div>

      <div className="health-filters">
        <div className="stations-search" style={{ maxWidth: 320 }}>
          <span className="stations-search-icon">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
              <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
            </svg>
          </span>
          <input
            className="input stations-search-input"
            placeholder="Buscar emissora…"
            value={search}
            onChange={e => { setSearch(e.target.value); setPage(1) }}
          />
        </div>

        <div className="stations-band-filter">
          {['Todas', 'FM', 'AM'].map(b => (
            <button
              key={b}
              type="button"
              className={`band-tab${band === b ? ' active' : ''}`}
              onClick={() => { setBand(b); setPage(1) }}
            >
              {b}
            </button>
          ))}
        </div>

        <div className="stations-band-filter">
          {HEALTH_FILTERS.map(f => (
            <button
              key={f}
              type="button"
              className={`band-tab${healthFilter === f ? ' active' : ''}`}
              onClick={() => { setHealth(f); setPage(1) }}
            >
              {f}
            </button>
          ))}
        </div>
      </div>

      {isLoading ? (
        <div className="health-list">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="health-row" style={{ pointerEvents: 'none' }}>
              <div className="skeleton" style={{ width: 36, height: 36, borderRadius: 8, flexShrink: 0 }} />
              <div style={{ flex: '0 0 220px', display: 'flex', flexDirection: 'column', gap: 5 }}>
                <div className="skeleton" style={{ width: 140, height: 13 }} />
                <div className="skeleton" style={{ width: 90, height: 11 }} />
              </div>
              <div className="skeleton" style={{ flex: 1, maxWidth: 120, height: 14 }} />
              <div className="skeleton" style={{ width: 60, height: 13 }} />
              <div className="skeleton" style={{ width: 110, height: 11 }} />
              <div className="skeleton" style={{ width: 10, height: 10, borderRadius: '50%' }} />
            </div>
          ))}
        </div>
      ) : (
        <>
          <div className="health-list">
            {pageItems.length === 0 && (
              <div className="health-event-empty">Nenhuma emissora encontrada</div>
            )}
            {pageItems.map(st => (
              <div
                key={st.id}
                className={`health-row${selectedId === st.id ? ' selected' : ''}`}
                onClick={() => setSelectedId(st.id === selectedId ? null : st.id)}
              >
                <StationAvatar station={st} size={36} />
                <div className="health-row-main">
                  <div className="health-row-name">{st.name}</div>
                  <div className="health-row-sub">
                    {st.band}{st.frequency_mhz ? ` · ${st.frequency_mhz}` : ''}
                    {st.city ? ` · ${st.city}` : ''}
                    {st.state ? `/${st.state}` : ''}
                  </div>
                </div>
                <div className="health-row-bar">
                  <MiniHealthBar dailySummary={st.daily_summary} />
                </div>
                <div className={`health-row-uptime ${st.uptime_pct < 99 ? 'health-row-uptime--bad' : 'health-row-uptime--ok'}`}>
                  {st.uptime_pct.toFixed(1)}%
                </div>
                <div className="health-row-incident">
                  {st.last_incident_at
                    ? `Queda ${relativeTime(st.last_incident_at)}`
                    : 'Sem quedas'}
                </div>
                <WorkerPill status={st.worker_status} lastPCMAt={st.worker_last_pcm_at} />
                <StatusDot status={st.monitoring_status} isCurrentlyDown={st.is_currently_down} />
              </div>
            ))}
          </div>

          {totalPages > 1 && (
            <div className="pagination">
              <button className="pagination-btn" disabled={currentPage === 1} onClick={() => setPage(p => p - 1)}>
                ← Anterior
              </button>
              <div className="pagination-pages">
                {Array.from({ length: totalPages }, (_, i) => i + 1)
                  .filter(p => p === 1 || p === totalPages || Math.abs(p - currentPage) <= 1)
                  .reduce((acc, p, i, arr) => {
                    if (i > 0 && p - arr[i - 1] > 1) acc.push('…')
                    acc.push(p)
                    return acc
                  }, [])
                  .map((p, i) =>
                    typeof p === 'string'
                      ? <span key={`e${i}`} className="pagination-ellipsis">{p}</span>
                      : <button
                          key={p}
                          className={`pagination-page${p === currentPage ? ' active' : ''}`}
                          onClick={() => setPage(p)}
                        >{p}</button>
                  )}
              </div>
              <button className="pagination-btn" disabled={currentPage === totalPages} onClick={() => setPage(p => p + 1)}>
                Próxima →
              </button>
            </div>
          )}
        </>
      )}

      {selectedStation && (
        <HealthDrawer
          station={selectedStation}
          onClose={() => setSelectedId(null)}
        />
      )}
    </div>
  )
}
