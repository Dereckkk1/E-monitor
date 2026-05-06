import { useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'
import { useCampaigns } from '../api/hooks'

// ── Helpers ──────────────────────────────────────────────────────

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

const STATUS_META = {
  active:  { label: 'Ativas',      badge: 'Ativa',      color: 'var(--c-action)',  badgeClass: 'badge-active'  },
  paused:  { label: 'Pausadas',    badge: 'Pausada',    color: 'var(--c-warning)', badgeClass: 'badge-paused'  },
  planned: { label: 'Planejadas',  badge: 'Planejada',  color: 'var(--c-info)',    badgeClass: 'badge-planned' },
  ended:   { label: 'Encerradas',  badge: 'Encerrada',  color: 'var(--c-text-3)',  badgeClass: 'badge-ended'   },
}

// ── Status Overview ───────────────────────────────────────────────

function StatusOverview({ campaigns }) {
  const total = campaigns.length
  const counts = useMemo(() => {
    const c = { active: 0, paused: 0, planned: 0, ended: 0 }
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

// ── Campaign Card ─────────────────────────────────────────────────

function CampaignCard({ campaign }) {
  const navigate = useNavigate()
  const meta = STATUS_META[campaign.status] ?? STATUS_META.ended

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

// ── Skeleton ──────────────────────────────────────────────────────

function SkeletonCards() {
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

// ── Empty state ───────────────────────────────────────────────────

function EmptyState() {
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

// ── Main page ─────────────────────────────────────────────────────

export default function DashboardPage() {
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
        <SkeletonCards />
      ) : campaigns.length === 0 ? (
        <EmptyState />
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
