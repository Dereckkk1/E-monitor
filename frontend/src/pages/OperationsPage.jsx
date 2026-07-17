import { useMemo, useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import api from '../api/client'
import { useWorkersStatus, WORKERS_POLL_MS } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import './OperationsPage.css'

/*
 * OperationsPage — live worker console (`/operations`).
 *
 * Two queries drive this screen:
 *   1. GET /workers       (refetch every WORKERS_POLL_MS, see api/hooks.js) — supervisor snapshot.
 *   2. GET /stream-health (refetch every 30s) — used to enrich worker rows
 *      with name/band/freq/city/state/logo + uptime_pct/is_currently_down.
 *
 * The worker payload from `health.go` only guarantees station_id + active +
 * last_pcm_at + stall_risk today, but the prompt described a richer shape
 * that the supervisor may grow into. We read every field defensively
 * (`worker.X ?? worker.altX ?? '—'`) so this page keeps working as the
 * backend evolves and never crashes on a missing key.
 */

// ── Icons ─────────────────────────────────────────────────────────────────────
function SearchIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
      <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" />
    </svg>
  )
}
function AntennaIcon({ size = 30 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M5 18l7-12 7 12" />
      <path d="M8.5 14h7" />
      <path d="M2 4c1.2 1.4 1.8 3 1.8 5S3.2 12.6 2 14" />
      <path d="M22 4c-1.2 1.4-1.8 3-1.8 5S20.8 12.6 22 14" />
    </svg>
  )
}

// ── Status meta ───────────────────────────────────────────────────────────────
const STATUS_META = {
  running:    { label: 'Em execução', cls: 'op-chip--success', live: true },
  stalled:    { label: 'Sem PCM',     cls: 'op-chip--warning' },
  restarting: { label: 'Reiniciando', cls: 'op-chip--info' },
  down:       { label: 'Caído',       cls: 'op-chip--danger' },
  unknown:    { label: 'Desconhecido', cls: 'op-chip--neutral' },
}

// Fronteira real de stall, espelhando o backend: supervisor.go:1039 marca
// StallRisk quando time.Since(last_pcm_at) > 30s. Este número pertence ao
// backend, NÃO à cadência do nosso poll — derivá-lo de WORKERS_POLL_MS só
// bate por coincidência aritmética no valor atual e desalinha silenciosamente
// na próxima mudança de cadência.
const STALL_RISK_MS = 30_000

// Resolve worker status defensively. The prompt advertised a `status`
// string field but the live `WorkerStatus` Go struct only exposes
// `active` + `stall_risk`. Map both shapes onto the same vocabulary.
function resolveStatus(w) {
  const s = w?.status ?? w?.state
  if (typeof s === 'string') {
    const k = s.toLowerCase()
    if (STATUS_META[k]) return k
  }
  if (w?.active === false || w?.is_active === false) return 'down'
  if (w?.stall_risk === true || w?.stalled === true) return 'stalled'
  if (w?.restarting === true) return 'restarting'
  // Last-PCM staleness is a strong "stalled" signal even when the
  // supervisor itself hasn't flipped the bit yet.
  const last = w?.last_pcm_at ?? w?.lastPCMAt
  if (last) {
    const ageMs = Date.now() - new Date(last).getTime()
    if (ageMs > STALL_RISK_MS) return 'stalled'
  }
  return 'running'
}

// ── Time helpers ──────────────────────────────────────────────────────────────
function relativeShort(iso) {
  if (!iso) return null
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms)) return null
  if (ms < 0) return 'agora'
  if (ms < 1500) return 'agora'
  const s = ms / 1000
  if (s < 60)    return `há ${s.toFixed(s < 10 ? 1 : 0)}s`
  if (s < 3600)  return `há ${Math.floor(s / 60)}min`
  if (s < 86400) return `há ${Math.floor(s / 3600)}h`
  return `há ${Math.floor(s / 86400)}d`
}

function ageMs(iso) {
  if (!iso) return null
  const v = Date.now() - new Date(iso).getTime()
  return Number.isFinite(v) ? v : null
}

// ── Bytes ─────────────────────────────────────────────────────────────────────
function formatBytes(n) {
  if (n == null || !Number.isFinite(Number(n))) return '—'
  const num = Number(n)
  if (num < 1024) return `${num} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = num / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 10 || i === 0 ? 1 : 2)} ${units[i]}`
}

// ── Number safe pick ──────────────────────────────────────────────────────────
function pickNum(...vals) {
  for (const v of vals) {
    if (v == null) continue
    const n = Number(v)
    if (Number.isFinite(n)) return n
  }
  return null
}
function pickStr(...vals) {
  for (const v of vals) {
    if (v == null) continue
    const s = String(v).trim()
    if (s.length > 0) return s
  }
  return null
}

// ── "Live now" ticker ─────────────────────────────────────────────────────────
// Forces a re-render every 1s so the relative timestamps ("há 1.2s") tick
// even between the WORKERS_POLL_MS /workers refetches.
function useNowTicker(intervalMs = 1000) {
  const [, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick(t => (t + 1) % 1_000_000), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
}

// ── KPI card ──────────────────────────────────────────────────────────────────
function KpiCard({ label, value, suffix, foot, tone, children }) {
  const toneCls = tone ? `op-kpi--${tone}` : ''
  return (
    <div className={`op-kpi ${toneCls}`}>
      <div className="op-kpi-label">{label}</div>
      {children ?? (
        <div className="op-kpi-value">
          {value}
          {suffix && <span className="op-kpi-value-suffix">{suffix}</span>}
        </div>
      )}
      {foot && <div className="op-kpi-foot">{foot}</div>}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────
export default function OperationsPage() {
  const navigate = useNavigate()
  useNowTicker(1000)

  // /workers — the live supervisor snapshot. Shared with the admin
  // Dashboard under queryKey ['workers'] (see useWorkersStatus in api/hooks).
  const workersQuery = useWorkersStatus()

  // /stream-health — used to enrich each worker row with station identity
  // and uptime. 30s is fine because that data changes slowly.
  const healthQuery = useQuery({
    queryKey: ['stream-health', { days: 1 }],
    queryFn: () => api.get('/stream-health', { params: { days: 1 } }).then(r => r.data),
    refetchInterval: 30_000,
  })

  const workers = useMemo(() => {
    const raw = workersQuery.data?.workers
    return Array.isArray(raw) ? raw : []
  }, [workersQuery.data])

  const stations = useMemo(() => {
    // /stream-health returns a bare array OR { data: [...] } depending on
    // the handler version. Accept both.
    const raw = healthQuery.data
    if (Array.isArray(raw)) return raw
    if (Array.isArray(raw?.data)) return raw.data
    return []
  }, [healthQuery.data])

  const stationById = useMemo(() => {
    const m = new Map()
    for (const s of stations) {
      if (s?.id) m.set(String(s.id), s)
    }
    return m
  }, [stations])

  const clapVerifier = !!workersQuery.data?.clap_verifier

  // Joined + status-resolved rows.
  const rows = useMemo(() => {
    return workers.map(w => {
      const stationId = pickStr(w?.station_id, w?.stationId) ?? ''
      const station = stationById.get(stationId) ?? null
      const status = resolveStatus(w)
      const lastPcm = w?.last_pcm_at ?? w?.lastPCMAt ?? null
      const lastPcmAge = ageMs(lastPcm)
      return {
        stationId,
        station,
        status,
        // Identity (defensive):
        name: pickStr(w?.station_name, w?.stationName, station?.name) ?? '—',
        band: pickStr(station?.band, w?.band),
        frequencyMhz: pickNum(station?.frequency_mhz, w?.frequency_mhz),
        city: pickStr(station?.city, w?.city),
        state: pickStr(station?.state, w?.state),
        logoUrl: pickStr(station?.logo_url, w?.logo_url),
        // Live signals:
        lastPcm,
        lastPcmAge,
        bytes: pickNum(w?.bytes_received, w?.bytesReceived, w?.bytes),
        reconnects: pickNum(w?.reconnects, w?.reconnect_count) ?? 0,
        stallRestarts: pickNum(w?.stall_restarts, w?.stallRestarts) ?? 0,
        minHashes: pickNum(w?.min_hashes, w?.minHashes),
        pid: pickNum(w?.pid),
        startedAt: w?.started_at ?? w?.startedAt ?? null,
        // Health (from /stream-health):
        uptimePct: pickNum(station?.uptime_pct),
        isCurrentlyDown: !!station?.is_currently_down,
      }
    })
  }, [workers, stationById])

  // Filters.
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState('all') // 'all' | 'issues'

  const issuesCount = useMemo(
    () => rows.filter(r => r.status !== 'running').length,
    [rows],
  )

  const filteredRows = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter(r => {
      if (filter === 'issues' && r.status === 'running') return false
      if (q) {
        const haystack = [r.name, r.city, r.state, r.band].filter(Boolean).join(' ').toLowerCase()
        if (!haystack.includes(q)) return false
      }
      return true
    }).sort((a, b) => {
      // Problems to the top: down → restarting → stalled → running.
      const order = { down: 0, restarting: 1, stalled: 2, running: 3, unknown: 4 }
      const da = order[a.status] ?? 5
      const db = order[b.status] ?? 5
      if (da !== db) return da - db
      return (a.name ?? '').localeCompare(b.name ?? '')
    })
  }, [rows, search, filter])

  // KPIs.
  const totalWorkers = rows.length
  const runningCount = rows.filter(r => r.status === 'running').length
  const stalledCount = rows.filter(r => r.status === 'stalled').length
  const downCount    = rows.filter(r => r.status === 'down').length
  const restartingCount = rows.filter(r => r.status === 'restarting').length

  const workersTone =
    totalWorkers === 0 ? 'info' :
    downCount > 0       ? 'danger' :
    (stalledCount + restartingCount) > 0 ? 'warn' : 'ok'

  // Streams ao ar — cross with stream-health.is_currently_down.
  const monitoredStations = rows.filter(r => r.station != null)
  const streamsUp = monitoredStations.filter(r => !r.isCurrentlyDown).length
  const streamsTotal = monitoredStations.length
  const streamsTone =
    streamsTotal === 0 ? 'info' :
    streamsUp === streamsTotal ? 'ok' :
    streamsUp / streamsTotal >= 0.9 ? 'warn' : 'danger'

  const totalReconnects = rows.reduce((acc, r) => acc + (r.reconnects ?? 0), 0)

  // "Atualizado há Xs" pill. Margem de 3x o poll: absorve um refetch
  // atrasado (retry backoff, soluço de rede) sem virar "falha ao atualizar"
  // por si só.
  const lastUpdate = workersQuery.dataUpdatedAt
  const lastUpdateLabel = lastUpdate ? relativeShort(new Date(lastUpdate).toISOString()) : null
  const isFreshHealthy = !workersQuery.isError && lastUpdate && (Date.now() - lastUpdate) < WORKERS_POLL_MS * 3

  // ── Render ────────────────────────────────────────────────────────────────
  const isInitialLoading = workersQuery.isLoading
  const isRefetching = workersQuery.isFetching && !workersQuery.isLoading
  const showEmpty = !isInitialLoading && rows.length === 0

  return (
    <div className="op-root">
      {/* Header */}
      <div className="op-header">
        <div className="op-header-text">
          <h2>Operações</h2>
          <div className="op-header-sub">Estado ao vivo dos workers de ingestão</div>
        </div>
        <span className="op-pulse" title={`Atualizado automaticamente a cada ${WORKERS_POLL_MS / 1000}s`}>
          <span
            className={`op-pulse-dot ${isFreshHealthy ? 'op-pulse-dot--ok op-pulse-dot--live' : 'op-pulse-dot--bad'}`}
          />
          {workersQuery.isError
            ? 'falha ao atualizar'
            : lastUpdateLabel
              ? `atualizado ${lastUpdateLabel}`
              : 'aguardando dados…'}
        </span>
      </div>

      {/* KPI row */}
      <div className="op-kpis">
        <KpiCard
          label="Workers ativos"
          tone={workersTone}
          foot={
            isInitialLoading
              ? <span className="op-skel-line" style={{ width: 80, height: 10 }} />
              : downCount > 0
                ? <span className="op-cell-num--danger" style={{ fontWeight: 600 }}>{downCount} caído{downCount > 1 ? 's' : ''}</span>
                : (stalledCount + restartingCount) > 0
                  ? <span className="op-cell-warn">{stalledCount + restartingCount} com problemas</span>
                  : totalWorkers > 0
                    ? <span style={{ color: 'var(--c-success)', fontWeight: 600 }}>todos saudáveis</span>
                    : <span style={{ color: 'var(--c-text-3)' }}>nenhum worker</span>
          }
        >
          {isInitialLoading ? (
            <span className="op-skel-line" style={{ width: 90, height: 28, marginTop: 2 }} />
          ) : (
            <div className="op-kpi-value">
              {runningCount}
              <span className="op-kpi-value-suffix">/ {totalWorkers}</span>
            </div>
          )}
        </KpiCard>

        <KpiCard
          label="Streams ao ar"
          tone={streamsTone}
          foot={
            healthQuery.isLoading
              ? <span className="op-skel-line" style={{ width: 80, height: 10 }} />
              : streamsTotal === 0
                ? <span style={{ color: 'var(--c-text-3)' }}>sem dados de saúde</span>
                : streamsUp === streamsTotal
                  ? <span style={{ color: 'var(--c-success)', fontWeight: 600 }}>todos no ar</span>
                  : <span className="op-cell-num--danger" style={{ fontWeight: 600 }}>
                      {streamsTotal - streamsUp} fora do ar
                    </span>
          }
        >
          {healthQuery.isLoading ? (
            <span className="op-skel-line" style={{ width: 90, height: 28, marginTop: 2 }} />
          ) : (
            <div className="op-kpi-value">
              {streamsUp}
              <span className="op-kpi-value-suffix">/ {streamsTotal}</span>
            </div>
          )}
        </KpiCard>

        <KpiCard
          label="Reconexões"
          tone={totalReconnects === 0 ? 'ok' : totalReconnects > 100 ? 'warn' : 'info'}
          foot={<span style={{ color: 'var(--c-text-3)' }}>(acumulado)</span>}
        >
          {isInitialLoading ? (
            <span className="op-skel-line" style={{ width: 70, height: 28, marginTop: 2 }} />
          ) : (
            <div className="op-kpi-value">
              {totalReconnects.toLocaleString('pt-BR')}
            </div>
          )}
        </KpiCard>

        <KpiCard
          label="CLAP verifier"
          tone={clapVerifier ? 'ok' : 'danger'}
          foot={
            <span style={{ color: 'var(--c-text-3)' }}>
              verificação neural opcional
            </span>
          }
        >
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
            <span className={`op-chip ${clapVerifier ? 'op-chip--success' : 'op-chip--danger'}`}>
              <span className="op-chip-dot" />
              {clapVerifier ? 'Online' : 'Offline'}
            </span>
            <span className="op-tip" tabIndex={0} aria-describedby="clap-tip">
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="var(--c-text-3)" strokeWidth="1.75" aria-hidden="true">
                <circle cx="8" cy="8" r="6.5" />
                <path d="M8 7.5v3.5M8 5.25v.01" strokeLinecap="round" />
              </svg>
              <span className="op-tip-bubble" id="clap-tip" role="tooltip">
                Verificador neural opcional usado em detecções incertas
              </span>
            </span>
          </div>
        </KpiCard>
      </div>

      {/* Filters */}
      <div className="op-filters">
        <div className="op-search">
          <span className="op-search-icon"><SearchIcon /></span>
          <input
            type="text"
            className="input op-search-input"
            placeholder="Buscar por emissora ou cidade…"
            value={search}
            onChange={e => setSearch(e.target.value)}
          />
        </div>
        <div className="op-filter-tabs" role="tablist" aria-label="Filtro de status">
          <button
            type="button"
            role="tab"
            aria-selected={filter === 'all'}
            className={`op-filter-tab${filter === 'all' ? ' active' : ''}`}
            onClick={() => setFilter('all')}
          >
            Todos
            <span className="op-filter-count">{rows.length}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={filter === 'issues'}
            className={`op-filter-tab${filter === 'issues' ? ' active' : ''}`}
            onClick={() => setFilter('issues')}
          >
            Só problemas
            <span className="op-filter-count">{issuesCount}</span>
          </button>
        </div>
      </div>

      {/* Table / empty / loading */}
      {showEmpty ? (
        <EmptyState onCta={() => navigate('/campaigns')} />
      ) : (
        <div className="op-table-card">
          {isRefetching && <div className="op-refetch-bar" aria-hidden="true" />}
          <div className="op-table-wrap">
            <table className="op-table">
              <thead>
                <tr>
                  <th>Estação</th>
                  <th>Status</th>
                  <th>Last PCM</th>
                  <th>Bytes</th>
                  <th>Reconnects</th>
                  <th>Stall restarts</th>
                  <th>Min hashes</th>
                  <th>Uptime 24h</th>
                </tr>
              </thead>
              <tbody>
                {isInitialLoading ? (
                  <SkeletonRows rows={6} />
                ) : filteredRows.length === 0 ? (
                  <tr>
                    <td colSpan={8} style={{ padding: '40px 20px', textAlign: 'center', color: 'var(--c-text-3)' }}>
                      Nenhum worker corresponde ao filtro.
                    </td>
                  </tr>
                ) : (
                  filteredRows.map(r => <WorkerRow key={r.stationId || r.name} row={r} onClick={() => r.stationId && navigate(`/stations/${r.stationId}/edit`)} />)
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

// ── Row ───────────────────────────────────────────────────────────────────────
function WorkerRow({ row, onClick }) {
  const meta = STATUS_META[row.status] ?? STATUS_META.unknown
  // Invariante real é STALL_RISK_MS (30s, espelha supervisor.go:1039) — não
  // a cadência do poll. O Math.max com WORKERS_POLL_MS*1.5 é só uma rede de
  // segurança: se algum dia o poll subir acima de 20s, evita que a célula
  // pisque vermelho num worker saudável entre um refetch e outro. Com o
  // poll atual (20s) o max não muda nada — fica em 30s, o invariante real.
  const lastPcmStale = row.lastPcmAge != null && row.lastPcmAge > Math.max(STALL_RISK_MS, WORKERS_POLL_MS * 1.5)

  // Uptime fill rules: ok ≥ 99%, warn ≥ 95%, bad below.
  const uptime = row.uptimePct
  const upCls =
    uptime == null     ? 'op-uptime-fill--warn' :
    uptime >= 99       ? 'op-uptime-fill--ok' :
    uptime >= 95       ? 'op-uptime-fill--warn' :
                         'op-uptime-fill--bad'
  const upWidth = uptime == null ? 0 : Math.max(2, Math.min(100, uptime))

  const stationLike = {
    name: row.name,
    logo_url: row.logoUrl,
  }

  const subBits = []
  if (row.band)            subBits.push(row.band)
  if (row.frequencyMhz)    subBits.push(`${row.frequencyMhz} MHz`)
  const sub = subBits.join(' · ')

  return (
    <tr onClick={onClick} title={row.stationId ? 'Abrir cadastro da emissora' : ''}>
      <td className="op-cell-station-td" data-label="Estação">
        <div className="op-cell-station">
          <StationAvatar station={stationLike} size={36} />
          <div className="op-cell-station-info">
            <div className="op-cell-station-name">{row.name}</div>
            <div className="op-cell-station-meta">
              {sub || <span style={{ color: 'var(--c-text-3)' }}>—</span>}
              {row.city && <> · {row.city}{row.state ? `/${row.state}` : ''}</>}
            </div>
          </div>
        </div>
      </td>

      <td data-label="Status">
        <span className={`op-chip ${meta.cls}${meta.live ? ' op-chip--live' : ''}`}>
          <span className="op-chip-dot" />
          {meta.label}
        </span>
      </td>

      <td data-label="Last PCM" className={`op-cell-num${lastPcmStale ? ' op-cell-num--danger' : ''}`}>
        {row.lastPcm ? relativeShort(row.lastPcm) : <span className="op-cell-num--muted">—</span>}
      </td>

      <td data-label="Bytes" className="op-cell-num">
        {row.bytes != null ? formatBytes(row.bytes) : <span className="op-cell-num--muted">—</span>}
      </td>

      <td data-label="Reconnects" className={`op-cell-num${row.reconnects > 0 ? '' : ' op-cell-num--muted'}`}>
        {row.reconnects.toLocaleString('pt-BR')}
      </td>

      <td data-label="Stall restarts" className={`op-cell-num${row.stallRestarts > 0 ? '' : ' op-cell-num--muted'}`}>
        {row.stallRestarts.toLocaleString('pt-BR')}
      </td>

      <td data-label="Min hashes">
        {row.minHashes != null
          ? <span className="op-chip op-chip--neutral">{row.minHashes.toLocaleString('pt-BR')}</span>
          : <span className="op-cell-num--muted">—</span>}
      </td>

      <td data-label="Uptime 24h">
        <div className="op-uptime">
          <div className="op-uptime-track">
            <div className={`op-uptime-fill ${upCls}`} style={{ width: `${upWidth}%` }} />
          </div>
          <span className="op-uptime-value">
            {uptime != null ? `${uptime.toFixed(1)}%` : '—'}
          </span>
        </div>
      </td>
    </tr>
  )
}

// ── Skeleton rows ─────────────────────────────────────────────────────────────
function SkeletonRows({ rows = 6 }) {
  return Array.from({ length: rows }).map((_, i) => (
    <tr key={`sk-${i}`} style={{ pointerEvents: 'none' }}>
      <td className="op-cell-station-td">
        <div className="op-cell-station">
          <span className="op-skel-circle" style={{ width: 36, height: 36 }} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span className="op-skel-line" style={{ width: 160, height: 12 }} />
            <span className="op-skel-line" style={{ width: 100, height: 10 }} />
          </div>
        </div>
      </td>
      <td><span className="op-skel-line" style={{ width: 88, height: 18, borderRadius: 9999 }} /></td>
      <td><span className="op-skel-line" style={{ width: 64, height: 12 }} /></td>
      <td><span className="op-skel-line" style={{ width: 70, height: 12 }} /></td>
      <td><span className="op-skel-line" style={{ width: 30, height: 12 }} /></td>
      <td><span className="op-skel-line" style={{ width: 30, height: 12 }} /></td>
      <td><span className="op-skel-line" style={{ width: 50, height: 18, borderRadius: 9999 }} /></td>
      <td>
        <div className="op-uptime">
          <span className="op-skel-line" style={{ width: 100, height: 6, borderRadius: 9999 }} />
          <span className="op-skel-line" style={{ width: 38, height: 12 }} />
        </div>
      </td>
    </tr>
  ))
}

// ── Empty state (Tutorial Estilizado) ────────────────────────────────────────
function EmptyState({ onCta }) {
  // Three faded ghost rows with placeholder geometry that mirrors the real
  // table layout, so the user sees what the page WILL look like once a
  // campaign goes active and workers come online.
  return (
    <div className="op-empty">
      <div className="op-empty-ghost" aria-hidden="true">
        {[0, 1, 2, 3].map(i => (
          <div className="op-empty-ghost-row" key={i}>
            <span className="op-skel-circle" style={{ width: 32, height: 32 }} />
            <span className="op-skel-line" style={{ width: 160 + (i % 2) * 30, height: 11 }} />
            <span className="op-skel-line" style={{ width: 70, height: 16, borderRadius: 9999, marginLeft: 'auto' }} />
            <span className="op-skel-line" style={{ width: 60, height: 11 }} />
            <span className="op-skel-line" style={{ width: 80, height: 6, borderRadius: 9999 }} />
          </div>
        ))}
      </div>

      <div className="op-empty-overlay">
        <div className="op-empty-icon">
          <AntennaIcon size={32} />
        </div>
        <h3 className="op-empty-title">Nenhum worker ativo</h3>
        <p className="op-empty-text">
          Workers de ingestão são iniciados automaticamente quando uma campanha entra em status <strong>“ativa”</strong>. Crie ou ative uma campanha para começar a monitorar emissoras em tempo real.
        </p>
        <button type="button" className="btn btn-primary op-empty-cta" onClick={onCta}>
          Ver campanhas
        </button>
      </div>
    </div>
  )
}
