import { useState, useMemo, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../contexts/AuthContext'
import { useCampaigns, useStreamHealth } from '../api/hooks'
import api from '../api/client'
import StationAvatar from '../components/StationAvatar'
import './DashboardPage.css'

// ── Shared helpers ────────────────────────────────────────────────

function formatDate(isoString) {
  if (!isoString) return '—'
  return new Date(isoString).toLocaleDateString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  })
}

function formatDateFull(date) {
  return date.toLocaleDateString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
}

// Friendly "atualizado há Xs" — recomputes against `dataUpdatedAt` (ms)
function relTimeFromMs(ms) {
  if (!ms) return null
  const diff = Math.max(0, Date.now() - ms)
  const s = Math.floor(diff / 1000)
  if (s < 5)   return 'agora'
  if (s < 60)  return `há ${s}s`
  if (s < 3600) return `há ${Math.floor(s / 60)}min`
  return `há ${Math.floor(s / 3600)}h`
}

// Status keys are the lifecycle values written by the API (§18.2.1 PT-BR).
const STATUS_META = {
  ativa:      { label: 'Ativas',      badge: 'Ativa',      color: '#10b981',          badgeClass: 'badge-ativa'      },
  programada: { label: 'Programadas', badge: 'Programada', color: '#6b7280',          badgeClass: 'badge-programada' },
  concluida:  { label: 'Concluídas',  badge: 'Concluída',  color: '#3b82f6',          badgeClass: 'badge-concluida'  },
  cancelada:  { label: 'Canceladas',  badge: 'Cancelada',  color: 'rgba(239, 68, 68, 0.6)', badgeClass: 'badge-cancelada' },
}

// ═══════════════════════════════════════════════════════════════════
//  CLIENT DASHBOARD — preserved 1:1 from the previous implementation
// ═══════════════════════════════════════════════════════════════════

function StatusOverview({ campaigns }) {
  const total = campaigns.length
  const counts = useMemo(() => {
    const c = { ativa: 0, programada: 0, concluida: 0, cancelada: 0 }
    campaigns.forEach(camp => {
      if (c[camp.status] !== undefined) c[camp.status]++
    })
    return c
  }, [campaigns])

  return (
    <div className="status-overview">
      <div className="status-overview-label">
        Campanhas &mdash; {total} no total
      </div>

      <div className="status-bar">
        {Object.entries(STATUS_META).map(([key, meta]) => {
          const pct = total > 0 ? (counts[key] / total) * 100 : 0
          if (pct === 0) return null
          return (
            <div
              key={key}
              className="status-bar-segment"
              style={{ width: `${pct}%`, background: meta.color }}
              title={`${meta.label}: ${counts[key]}`}
            />
          )
        })}
        {total === 0 && (
          <div
            className="status-bar-segment"
            style={{ width: '100%', background: 'var(--c-border)' }}
          />
        )}
      </div>

      <div className="status-legend">
        {Object.entries(STATUS_META).map(([key, meta]) => (
          <div key={key} className="status-legend-item">
            <div className="status-legend-dot" style={{ background: meta.color }} />
            <span>{counts[key]} {meta.label.toLowerCase()}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function CampaignCard({ campaign }) {
  const navigate = useNavigate()
  const meta = STATUS_META[campaign.status] ?? STATUS_META.concluida

  return (
    <div className="campaign-card">
      <div className="campaign-card-header">
        <span className={`badge ${meta.badgeClass}`}>{meta.badge ?? campaign.status}</span>
      </div>

      <div className="campaign-card-name">{campaign.name}</div>

      <div className="campaign-card-meta">
        <div className="campaign-card-meta-row">
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
            <rect x="1" y="2" width="10" height="9" rx="1.5" />
            <path d="M4 1v2M8 1v2M1 5h10" />
          </svg>
          {formatDate(campaign.start_date)} &ndash; {formatDate(campaign.end_date)}
        </div>
        {campaign.target_stations != null && (
          <div className="campaign-card-meta-row">
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <circle cx="6" cy="6" r="4.5" />
              <path d="M2 6c0-2.21 1.79-4 4-4" />
              <path d="M10 6c0 2.21-1.79 4-4 4" />
              <circle cx="6" cy="6" r="1" fill="currentColor" stroke="none" />
            </svg>
            {campaign.target_stations} {campaign.target_stations === 1 ? 'emissora alvo' : 'emissoras alvo'}
          </div>
        )}
      </div>

      <div className="campaign-card-actions">
        <button
          className="btn-link"
          onClick={() => navigate(`/detections?campaign_id=${campaign.id}`)}
        >
          Ver Veiculações
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M2 6h8M6 2l4 4-4 4" />
          </svg>
        </button>
      </div>
    </div>
  )
}

function ClientSkeletonCards() {
  return (
    <div className="campaign-grid">
      {[1, 2].map(i => (
        <div key={i} className="campaign-card">
          <div className="skeleton-cell" style={{ width: '30%', height: 20 }} />
          <div className="skeleton-cell" style={{ width: '70%', height: 16, marginTop: 4 }} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 4 }}>
            <div className="skeleton-cell" style={{ width: '55%', height: 12 }} />
            <div className="skeleton-cell" style={{ width: '40%', height: 12 }} />
          </div>
          <div className="skeleton-cell" style={{ width: '35%', height: 14, marginTop: 4 }} />
        </div>
      ))}
    </div>
  )
}

function ClientEmptyState() {
  const navigate = useNavigate()
  return (
    <div className="dashboard-empty">
      <div className="dashboard-empty-action">
        <div className="dashboard-empty-icon">
          <svg width="64" height="64" viewBox="0 0 64 64" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <rect x="8" y="16" width="48" height="38" rx="4" />
            <path d="M8 26h48" />
            <path d="M20 12v8M44 12v8" />
            <path d="M22 38h20M22 44h12" />
          </svg>
        </div>
        <h3>Nenhuma campanha encontrada</h3>
        <p>Quando suas campanhas forem criadas, você verá um resumo do status e acesso rápido às veiculações aqui.</p>
        <button className="btn btn-primary" onClick={() => navigate('/campaigns')}>
          Ir para Campanhas
        </button>
      </div>

      <div className="dashboard-empty-preview" aria-hidden="true">
        <div className="ghost-card">
          <div className="ghost-line" style={{ width: '35%' }} />
          <div className="ghost-line" style={{ width: '65%', height: 16 }} />
          <div className="ghost-line" style={{ width: '50%', height: 11 }} />
          <div className="ghost-line" style={{ width: '30%', height: 11 }} />
          <div className="ghost-line" style={{ width: '40%', height: 13, marginTop: 4 }} />
        </div>
        <div className="ghost-card">
          <div className="ghost-line" style={{ width: '28%' }} />
          <div className="ghost-line" style={{ width: '72%', height: 16 }} />
          <div className="ghost-line" style={{ width: '45%', height: 11 }} />
          <div className="ghost-line" style={{ width: '35%', height: 11 }} />
          <div className="ghost-line" style={{ width: '38%', height: 13, marginTop: 4 }} />
        </div>
      </div>
    </div>
  )
}

function ClientDashboard() {
  const { user } = useAuth()
  const { data: campaigns = [], isLoading } = useCampaigns()
  const [showEnded, setShowEnded] = useState(false)

  const today = useMemo(() => formatDateFull(new Date()), [])

  const activeCampaigns = useMemo(
    () => campaigns.filter(c => c.status !== 'ended'),
    [campaigns]
  )
  const endedCampaigns = useMemo(
    () => campaigns.filter(c => c.status === 'ended'),
    [campaigns]
  )

  const visibleCampaigns = showEnded ? [...activeCampaigns, ...endedCampaigns] : activeCampaigns

  return (
    <div>
      {/* Greeting */}
      <div className="dashboard-greeting">
        <h1>Olá, {user?.name ?? 'Admin'}</h1>
        <div className="dashboard-greeting-sub">Aqui está um resumo das suas campanhas.</div>
        <div className="dashboard-greeting-date">{today}</div>
      </div>

      {/* Status Overview */}
      {!isLoading && campaigns.length > 0 && (
        <StatusOverview campaigns={campaigns} />
      )}

      {/* Campaign grid */}
      {isLoading ? (
        <ClientSkeletonCards />
      ) : campaigns.length === 0 ? (
        <ClientEmptyState />
      ) : (
        <>
          <div className="campaign-grid">
            {visibleCampaigns.map(c => (
              <CampaignCard key={c.id} campaign={c} />
            ))}
          </div>

          {endedCampaigns.length > 0 && (
            <div className="ended-toggle">
              <button
                className="btn btn-muted btn-sm"
                onClick={() => setShowEnded(v => !v)}
              >
                {showEnded
                  ? `Ocultar encerradas`
                  : `Mostrar encerradas (${endedCampaigns.length})`}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════
//  ADMIN DASHBOARD — System Health
// ═══════════════════════════════════════════════════════════════════

// Inline hooks for /health and /workers — kept here (not in api/hooks.js)
// because that file is off-limits for this change.
function useSystemHealth() {
  return useQuery({
    queryKey: ['system-health'],
    queryFn: () => api.get('/health').then(r => r.data),
    refetchInterval: 15_000,
    retry: 1,
  })
}

function useWorkers() {
  return useQuery({
    queryKey: ['workers-overview'],
    queryFn: () => api.get('/workers').then(r => r.data),
    refetchInterval: 10_000,
    retry: 1,
  })
}

// Workers shape from /v1/internal/workers (supervisor.WorkerStatus):
//   { station_id, active, last_pcm_at, stall_risk }
// We derive a UI status from active + stall_risk + last_pcm_at.
function deriveWorkerStatus(w) {
  if (!w?.active) return 'down'
  const last = w.last_pcm_at ? new Date(w.last_pcm_at).getTime() : 0
  if (last && Date.now() - last > 90_000) return 'down'
  if (w.stall_risk) return 'stalled'
  return 'running'
}

const WORKER_STATUS_LABEL = {
  running:    'Operando',
  stalled:    'Lento',
  restarting: 'Reiniciando',
  down:       'Caído',
}

// Tone helpers --------------------------------------------------------
function uptimeTone(pct) {
  if (pct == null || Number.isNaN(pct)) return ''
  if (pct >= 99)  return 'tone-ok'
  if (pct >= 95)  return 'tone-warn'
  return 'tone-bad'
}

// ── Building blocks ────────────────────────────────────────────────

function KpiCard({ label, value, suffix, valueTone, icon, tone, chips, isFetching, tooltip }) {
  return (
    <div className="dh-kpi" title={tooltip || undefined}>
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-kpi-head">
        <div className="dh-kpi-label">{label}</div>
        <div className={`dh-kpi-icon tone-${tone || 'info'}`}>{icon}</div>
      </div>
      <div className={`dh-kpi-value ${valueTone || ''}`}>
        {value}
        {suffix && <span className="dh-kpi-value-suffix">{suffix}</span>}
      </div>
      <div className="dh-kpi-foot">
        {chips?.map((c, i) => (
          <span key={i} className={`dh-chip ${c.tone ? `tone-${c.tone}` : ''}`}>
            <span className="dh-chip-dot" />
            {c.label}
          </span>
        ))}
      </div>
    </div>
  )
}

function KpiSkeleton() {
  return (
    <div className="dh-kpi">
      <div className="dh-kpi-head">
        <div className="dh-skel" style={{ width: 90, height: 11 }} />
        <div className="dh-skel" style={{ width: 36, height: 36, borderRadius: 'var(--radius-md)' }} />
      </div>
      <div className="dh-skel" style={{ width: 110, height: 32, marginTop: 6 }} />
      <div className="dh-kpi-foot">
        <div className="dh-skel" style={{ width: 70, height: 18, borderRadius: 999 }} />
        <div className="dh-skel" style={{ width: 60, height: 18, borderRadius: 999 }} />
      </div>
    </div>
  )
}

// Inline icons (Lucide-shaped, no extra dep) -------------------------
const ICONS = {
  pulse: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 12h4l2-7 4 14 2-7h6" />
    </svg>
  ),
  workers: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="6" rx="1.5" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" />
      <circle cx="7" cy="7" r="0.8" fill="currentColor" />
      <circle cx="7" cy="17" r="0.8" fill="currentColor" />
    </svg>
  ),
  radio: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M4.9 19.1A10 10 0 0 1 4.9 4.9" />
      <path d="M7.8 16.2a6 6 0 0 1 0-8.4" />
      <circle cx="12" cy="12" r="2" />
      <path d="M16.2 16.2a6 6 0 0 0 0-8.4" />
      <path d="M19.1 19.1a10 10 0 0 0 0-14.2" />
    </svg>
  ),
  trending: (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="23 6 13.5 15.5 8.5 10.5 1 18" />
      <polyline points="17 6 23 6 23 12" />
    </svg>
  ),
  programada: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="18" rx="2" />
      <line x1="16" y1="2" x2="16" y2="6" />
      <line x1="8"  y1="2" x2="8"  y2="6" />
      <line x1="3" y1="10" x2="21" y2="10" />
    </svg>
  ),
  ativa: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="5 3 19 12 5 21 5 3" />
    </svg>
  ),
  concluida: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
      <polyline points="22 4 12 14.01 9 11.01" />
    </svg>
  ),
  cancelada: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="15" y1="9" x2="9"  y2="15" />
      <line x1="9"  y1="9" x2="15" y2="15" />
    </svg>
  ),
  arrow: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="5"  y1="12" x2="19" y2="12" />
      <polyline points="12 5 19 12 12 19" />
    </svg>
  ),
  check: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  ),
  alert: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <line x1="12" y1="9"  x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  ),
}

// ── Hero strip ─────────────────────────────────────────────────────

function HeroStrip({ health, healthFetching, workers, workersFetching, streamHealth, streamHealthFetching, healthError }) {
  const hasHealth = !!health && !healthError
  const isOk = hasHealth && health.status === 'ok'

  // Sistema --------------------------------------------------------
  const systemTone = healthError ? 'bad' : isOk ? 'ok' : 'warn'
  const systemValue = healthError ? 'Indisponível' : isOk ? 'Operacional' : 'Degradado'
  const systemChips = []
  if (hasHealth) {
    const pgOk = health.deps?.postgres === 'ok'
    const natsOk = health.deps?.nats === 'ok'
    systemChips.push({ label: `Postgres ${pgOk ? 'ok' : 'down'}`, tone: pgOk ? 'ok' : 'bad' })
    systemChips.push({ label: `NATS ${natsOk ? 'ok' : 'down'}`,    tone: natsOk ? 'ok' : 'bad' })
  } else if (healthError) {
    systemChips.push({ label: 'sem resposta', tone: 'bad' })
  }

  // Workers --------------------------------------------------------
  const workerList = workers?.workers ?? []
  const total = workerList.length
  const derived = workerList.map(deriveWorkerStatus)
  const running = derived.filter(s => s === 'running').length
  const stalled = derived.filter(s => s === 'stalled').length
  const down    = derived.filter(s => s === 'down').length
  const wTone =
    total === 0      ? 'warn' :
    down > 0         ? 'bad'  :
    stalled > 0      ? 'warn' : 'ok'
  const wValueTone =
    total === 0      ? 'tone-warn' :
    down > 0         ? 'tone-bad'  :
    stalled > 0      ? 'tone-warn' : 'tone-ok'
  const wChips = []
  if (stalled > 0) wChips.push({ label: `${stalled} lento${stalled > 1 ? 's' : ''}`, tone: 'warn' })
  if (down > 0)    wChips.push({ label: `${down} caído${down > 1 ? 's' : ''}`,       tone: 'bad'  })
  if (stalled === 0 && down === 0 && total > 0) {
    wChips.push({ label: 'todos ok', tone: 'ok' })
  }
  const clapTooltip = workers
    ? `CLAP verifier: ${workers.clap_verifier ? 'online' : 'offline'}`
    : ''

  // Streams --------------------------------------------------------
  const stations = streamHealth ?? []
  const stTotal = stations.length
  const stDown = stations.filter(s => s.is_currently_down).length
  const stUp = Math.max(0, stTotal - stDown)
  const stUpPct = stTotal > 0 ? (stUp / stTotal) * 100 : 0
  const stTone =
    stTotal === 0     ? 'warn' :
    stUpPct >= 98     ? 'ok'   :
    stUpPct >= 95     ? 'warn' : 'bad'
  const stValueTone =
    stTotal === 0     ? 'tone-warn' :
    stUpPct >= 98     ? 'tone-ok'   :
    stUpPct >= 95     ? 'tone-warn' : 'tone-bad'
  const stChips = []
  if (stDown > 0) stChips.push({ label: `${stDown} fora do ar`, tone: 'bad' })
  else if (stTotal > 0) stChips.push({ label: 'tudo no ar', tone: 'ok' })

  // Uptime médio --------------------------------------------------
  const avgUptime = stTotal > 0
    ? stations.reduce((acc, s) => acc + (s.uptime_pct ?? 0), 0) / stTotal
    : null
  const upTone =
    avgUptime == null ? 'warn' :
    avgUptime >= 99   ? 'ok'   :
    avgUptime >= 95   ? 'warn' : 'bad'
  const upValueTone =
    avgUptime == null ? '' :
    avgUptime >= 99   ? 'tone-ok'   :
    avgUptime >= 95   ? 'tone-warn' : 'tone-bad'

  return (
    <div className="dh-hero">
      <KpiCard
        label="Sistema"
        icon={ICONS.pulse}
        tone={systemTone}
        valueTone={systemTone === 'ok' ? 'tone-ok' : systemTone === 'warn' ? 'tone-warn' : 'tone-bad'}
        value={systemValue}
        chips={systemChips}
        isFetching={healthFetching}
      />
      <KpiCard
        label="Workers"
        icon={ICONS.workers}
        tone={wTone}
        valueTone={wValueTone}
        value={total > 0 ? `${running}` : '—'}
        suffix={total > 0 ? `/ ${total}` : null}
        chips={wChips}
        isFetching={workersFetching}
        tooltip={clapTooltip}
      />
      <KpiCard
        label="Streams ao ar"
        icon={ICONS.radio}
        tone={stTone}
        valueTone={stValueTone}
        value={stTotal > 0 ? `${stUp}` : '—'}
        suffix={stTotal > 0 ? `/ ${stTotal}` : null}
        chips={stChips}
        isFetching={streamHealthFetching}
      />
      <KpiCard
        label="Uptime médio 24h"
        icon={ICONS.trending}
        tone={upTone}
        valueTone={upValueTone}
        value={avgUptime == null ? '—' : avgUptime.toFixed(1)}
        suffix={avgUptime == null ? null : '%'}
        chips={
          avgUptime == null
            ? [{ label: 'sem dados', tone: 'warn' }]
            : [{ label: 'janela 24h', tone: avgUptime >= 99 ? 'ok' : avgUptime >= 95 ? 'warn' : 'bad' }]
        }
        isFetching={streamHealthFetching}
      />
    </div>
  )
}

// ── Campaigns by status row ────────────────────────────────────────

function CampaignsByStatus({ campaigns }) {
  const navigate = useNavigate()
  const counts = useMemo(() => {
    const c = { programada: 0, ativa: 0, concluida: 0, cancelada: 0 }
    campaigns.forEach(camp => { if (c[camp.status] !== undefined) c[camp.status]++ })
    return c
  }, [campaigns])

  const total = campaigns.length

  const tiles = [
    { key: 'programada', label: 'Programadas', icon: ICONS.programada },
    { key: 'ativa',      label: 'Ativas',      icon: ICONS.ativa      },
    { key: 'concluida',  label: 'Concluídas',  icon: ICONS.concluida  },
    { key: 'cancelada',  label: 'Canceladas',  icon: ICONS.cancelada  },
  ]

  return (
    <div className="dh-status-card">
      <div className="dh-status-head">
        <div className="dh-status-title">Campanhas por status</div>
        <div className="dh-status-total">{total} no total</div>
      </div>
      <div className="dh-status-grid">
        {tiles.map(t => (
          <button
            key={t.key}
            type="button"
            className="dh-status-tile"
            onClick={() => navigate(`/campaigns?status=${t.key}`)}
          >
            <div className={`dh-status-tile-icon tone-${t.key}`}>{t.icon}</div>
            <div className="dh-status-tile-body">
              <div className="dh-status-tile-count">{counts[t.key]}</div>
              <div className="dh-status-tile-label">{t.label}</div>
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}

// ── Station health panel ───────────────────────────────────────────

function relativeTime(iso) {
  if (!iso) return null
  const diff = Date.now() - new Date(iso)
  const s = Math.floor(diff / 1000)
  if (s < 60)    return `há ${s}s`
  if (s < 3600)  return `há ${Math.floor(s / 60)}min`
  if (s < 86400) return `há ${Math.floor(s / 3600)}h`
  return `há ${Math.floor(s / 86400)}d`
}

function StationHealthPanel({ stations, isLoading, isFetching }) {
  const navigate = useNavigate()

  const sorted = useMemo(() => {
    return [...(stations ?? [])].sort((a, b) => {
      if (a.is_currently_down && !b.is_currently_down) return -1
      if (b.is_currently_down && !a.is_currently_down) return 1
      const au = a.uptime_pct ?? 100
      const bu = b.uptime_pct ?? 100
      if (au !== bu) return au - bu
      return (a.name ?? '').localeCompare(b.name ?? '')
    })
  }, [stations])

  const top = sorted.slice(0, 10)
  const hasMore = sorted.length > 10

  return (
    <div className="dh-panel">
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-panel-head">
        <div>
          <div className="dh-panel-title">Saúde por estação</div>
          <div className="dh-panel-sub">
            {isLoading ? 'carregando…' : `Piores ${top.length} de ${sorted.length} ativas`}
          </div>
        </div>
        {hasMore && (
          <button className="dh-panel-link" onClick={() => navigate('/monitoring')}>
            Ver todas {ICONS.arrow}
          </button>
        )}
      </div>

      <div className="dh-station-list">
        {isLoading ? (
          Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="dh-skel-row">
              <div className="dh-skel" style={{ width: 28, height: 28, borderRadius: 8 }} />
              <div className="dh-skel" style={{ flex: 1, height: 12 }} />
              <div className="dh-skel" style={{ width: 80, height: 6, borderRadius: 999 }} />
              <div className="dh-skel" style={{ width: 50, height: 12 }} />
            </div>
          ))
        ) : top.length === 0 ? (
          <div className="dh-empty">Nenhuma estação ativa no momento.</div>
        ) : (
          top.map(st => {
            const tone = uptimeTone(st.uptime_pct)
            const fillTone = tone === 'tone-bad' ? 'tone-bad' : tone === 'tone-warn' ? 'tone-warn' : ''
            return (
              <button
                key={st.id}
                type="button"
                className="dh-station-row"
                onClick={() => navigate(`/stations/${st.id}/edit`)}
              >
                <StationAvatar station={st} size={28} />
                <div className="dh-station-name-wrap">
                  <div className="dh-station-name">{st.name}</div>
                  <div className="dh-station-meta">
                    {st.band}{st.frequency_mhz ? ` · ${st.frequency_mhz}` : ''}
                    {st.city ? ` · ${st.city}` : ''}{st.state ? `/${st.state}` : ''}
                  </div>
                </div>
                <div className="dh-uptime-bar-wrap">
                  <div
                    className={`dh-uptime-bar-fill ${fillTone}`}
                    style={{ width: `${Math.max(0, Math.min(100, st.uptime_pct ?? 0))}%` }}
                  />
                </div>
                <div className={`dh-uptime-pct ${tone}`}>
                  {(st.uptime_pct ?? 0).toFixed(1)}%
                </div>
                <div className={`dh-station-incident ${st.is_currently_down ? 'has-incident' : ''}`}>
                  {st.is_currently_down
                    ? 'Fora do ar'
                    : st.last_incident_at
                      ? `Queda ${relativeTime(st.last_incident_at)}`
                      : 'Sem quedas'}
                </div>
                <div className="dh-station-arrow">{ICONS.arrow}</div>
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}

// ── Workers in alert panel ────────────────────────────────────────

function WorkersAlertPanel({ workers, isLoading, isFetching }) {
  const navigate = useNavigate()
  const list = workers?.workers ?? []

  const alerts = useMemo(() => {
    return list
      .map(w => ({ ...w, derived: deriveWorkerStatus(w) }))
      .filter(w => w.derived !== 'running')
  }, [list])

  return (
    <div className="dh-panel">
      {isFetching && <div className="dh-kpi-refresh-bar" />}
      <div className="dh-panel-head">
        <div>
          <div className="dh-panel-title">Workers em alerta</div>
          <div className="dh-panel-sub">
            {isLoading ? 'carregando…' : `${list.length} worker${list.length === 1 ? '' : 's'} ativo${list.length === 1 ? '' : 's'}`}
          </div>
        </div>
        {alerts.length > 0 && (
          <button className="dh-panel-link" onClick={() => navigate('/operations')}>
            Operações {ICONS.arrow}
          </button>
        )}
      </div>

      {isLoading ? (
        <div className="dh-skel-row">
          <div className="dh-skel" style={{ flex: 1, height: 14 }} />
          <div className="dh-skel" style={{ width: 60, height: 18, borderRadius: 999 }} />
          <div className="dh-skel" style={{ width: 80, height: 12 }} />
        </div>
      ) : alerts.length === 0 ? (
        <div className="dh-worker-empty">
          <div className="dh-worker-empty-icon">{ICONS.check}</div>
          <div className="dh-worker-empty-text">
            <div className="dh-worker-empty-title">Tudo certo</div>
            <div className="dh-worker-empty-sub">Todos os workers operando normalmente.</div>
          </div>
        </div>
      ) : (
        <>
          <div className="dh-worker-list">
            {alerts.map(w => {
              const tone = w.derived === 'down' ? 'bad' : 'warn'
              const last = w.last_pcm_at
                ? `último PCM ${relativeTime(w.last_pcm_at)}`
                : 'sem PCM recente'
              const id = (w.station_id ?? '').slice(0, 8)
              return (
                <div key={w.station_id} className={`dh-worker-row tone-${tone}`}>
                  <div className="dh-worker-id">{id}…</div>
                  <span className={`dh-worker-status tone-${tone}`}>
                    {WORKER_STATUS_LABEL[w.derived] ?? w.derived}
                  </span>
                  <div className="dh-worker-meta">{last}</div>
                </div>
              )
            })}
          </div>
          <div className="dh-worker-link-row">
            <button className="dh-panel-link" onClick={() => navigate('/operations')}>
              Abrir painel de operações {ICONS.arrow}
            </button>
          </div>
        </>
      )}
    </div>
  )
}

// ── Admin shell ───────────────────────────────────────────────────

function AdminDashboard() {
  const health        = useSystemHealth()
  const workers       = useWorkers()
  const streamHealth  = useStreamHealth({ days: 1 })
  const campaignsQ    = useCampaigns()

  // Determine the most recent successful refresh among the four sources
  // so the header timestamp reflects "freshest data".
  const newest = Math.max(
    health.dataUpdatedAt        || 0,
    workers.dataUpdatedAt       || 0,
    streamHealth.dataUpdatedAt  || 0,
    campaignsQ.dataUpdatedAt    || 0,
  )

  // Re-render every 5s so "atualizado há Xs" stays alive between fetches.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick(x => x + 1), 5000)
    return () => clearInterval(t)
  }, [])

  // /health is the only endpoint that gates the "API indisponível" banner —
  // a real 5xx indicates the management API itself is unreachable, not just
  // a slow query somewhere downstream.
  const apiDown =
    !!health.error &&
    !health.data &&
    !health.isLoading

  const heroLoading = health.isLoading || workers.isLoading || streamHealth.isLoading

  return (
    <div className="dh-shell">
      <div className="dh-header">
        <div className="dh-header-titles">
          <h1 className="dh-header-title">Operação Radiocheck</h1>
          <div className="dh-header-sub">Visão consolidada do sistema</div>
        </div>
        <div className="dh-header-stamp">
          atualizado {newest ? relTimeFromMs(newest) : '…'}
        </div>
      </div>

      {apiDown && (
        <div className="dh-banner" role="alert">
          <span className="dh-banner-icon">{ICONS.alert}</span>
          API indisponível — tentando reconectar...
        </div>
      )}

      {heroLoading && !health.data && !workers.data && !streamHealth.data ? (
        <div className="dh-hero">
          <KpiSkeleton />
          <KpiSkeleton />
          <KpiSkeleton />
          <KpiSkeleton />
        </div>
      ) : (
        <HeroStrip
          health={health.data}
          healthFetching={health.isFetching}
          healthError={apiDown}
          workers={workers.data}
          workersFetching={workers.isFetching}
          streamHealth={streamHealth.data}
          streamHealthFetching={streamHealth.isFetching}
        />
      )}

      <CampaignsByStatus campaigns={campaignsQ.data ?? []} />

      <div className="dh-cols">
        <StationHealthPanel
          stations={streamHealth.data ?? []}
          isLoading={streamHealth.isLoading}
          isFetching={streamHealth.isFetching}
        />
        <WorkersAlertPanel
          workers={workers.data}
          isLoading={workers.isLoading}
          isFetching={workers.isFetching}
        />
      </div>
    </div>
  )
}

// ═══════════════════════════════════════════════════════════════════
//  Page entry — toggle by role
// ═══════════════════════════════════════════════════════════════════

export default function DashboardPage() {
  const { isAdmin } = useAuth()
  return isAdmin ? <AdminDashboard /> : <ClientDashboard />
}
