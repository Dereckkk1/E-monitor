import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useLiveMap } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import BrazilMap from '../components/BrazilMap'
import './LiveMapPage.css'

function fmtTime(iso) {
  if (!iso) return { time: '', date: '' }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return { time: '', date: '' }
  return {
    time: d.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
    date: d.toLocaleDateString('pt-BR'),
  }
}

function stationLabel(d) {
  const freq = d.frequency_mhz ? ` (${d.frequency_mhz})` : ''
  const band = d.band ? ` ${d.band}` : ''
  return `${d.station_name}${freq}${band}`.trim()
}

/* ── Feed de últimas veiculações ─────────────────────────────────── */
function AiringsFeed({ detections }) {
  return (
    <div className="live-feed-list content-group" key={detections[0]?.id ?? 'empty'}>
      {detections.map((d) => {
        const t = fmtTime(d.detected_at)
        const loc = [d.city, d.state].filter(Boolean).join(' / ')
        const material = [d.commercial_name, d.client_name].filter(Boolean).join(' · ')
        return (
          <div className="live-row" key={d.id}>
            <span className="live-row-pulse" aria-hidden="true" />
            <div className="live-row-body">
              <div className="live-row-top">
                <span className="live-row-station">{stationLabel(d)}</span>
                <span className="live-row-time">{t.time}</span>
              </div>
              <div className="live-row-sub">
                <span className="live-row-loc">{loc}</span>
                <span className="live-row-date">{t.date}</span>
              </div>
              {material && <span className="live-row-material">{material}</span>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

/* ── Skeletons (shape-matched) ───────────────────────────────────── */
function FeedSkeleton() {
  const widths = ['72%', '58%', '80%', '64%', '50%', '76%', '60%']
  return (
    <div className="live-feed-list">
      {widths.map((w, i) => (
        <div className="live-row live-row--skeleton" key={i}>
          <span className="skeleton skeleton-circle" style={{ width: 10, height: 10 }} />
          <div className="live-row-body">
            <div className="live-row-top">
              <span className="skeleton skeleton-text" style={{ width: w, height: 14 }} />
              <span className="skeleton skeleton-text" style={{ width: 56, height: 12 }} />
            </div>
            <span className="skeleton skeleton-text" style={{ width: '40%', height: 11 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function MapSkeleton() {
  return (
    <div className="map-skeleton">
      <div className="skeleton map-skeleton-shape" />
      {[[38, 62], [54, 70], [60, 58], [46, 80], [70, 66]].map(([l, t], i) => (
        <span
          key={i}
          className="skeleton skeleton-circle map-skeleton-dot"
          style={{ left: `${l}%`, top: `${t}%` }}
        />
      ))}
    </div>
  )
}

/* ── Empty state (ghost preview) ─────────────────────────────────── */
const GHOST_FEED = [
  { id: 'g1', station_name: 'Rádio Exemplo FM', city: 'São Paulo', state: 'SP', commercial_name: 'Campanha demonstrativa' },
  { id: 'g2', station_name: 'Rádio Exemplo AM', city: 'Goiânia', state: 'GO', commercial_name: 'Campanha demonstrativa' },
  { id: 'g3', station_name: 'Rádio Exemplo FM', city: 'Curitiba', state: 'PR', commercial_name: 'Campanha demonstrativa' },
  { id: 'g4', station_name: 'Rádio Exemplo FM', city: 'Recife', state: 'PE', commercial_name: 'Campanha demonstrativa' },
]

function EmptyGhost({ isAdmin }) {
  return (
    <div className="live-empty-wrapper">
      <div className="live-empty-ghost" aria-hidden="true">
        <section className="live-panel live-feed">
          <span className="live-feed-title">Últimas Veiculações</span>
          <AiringsFeed detections={GHOST_FEED} />
        </section>
        <section className="live-panel live-map-panel">
          <BrazilMap stations={[]} />
        </section>
      </div>
      <div className="live-empty-overlay">
        <div className="live-empty-card">
          <div className="live-empty-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="34" height="34" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 21s7-6.3 7-11a7 7 0 0 0-14 0c0 4.7 7 11 7 11z" />
              <circle cx="12" cy="10" r="2.5" />
            </svg>
          </div>
          <h3>Nenhuma emissora ao vivo agora</h3>
          <p>
            {isAdmin
              ? 'Quando houver emissoras com monitoramento ativo, elas aparecem pulsando no mapa e as veiculações entram no feed em tempo real.'
              : 'Assim que suas campanhas estiverem ativas, as emissoras monitoradas aparecem no mapa e as veiculações entram aqui em tempo real.'}
          </p>
          <Link className="btn btn-primary" to={isAdmin ? '/monitoring' : '/campaigns'}>
            {isAdmin ? 'Ver streams' : 'Ver campanhas'}
          </Link>
        </div>
      </div>
    </div>
  )
}

/* ── Página ──────────────────────────────────────────────────────── */
export default function LiveMapPage() {
  const { isAdmin } = useAuth()
  const { data, isLoading, isError, isFetching, refetch } = useLiveMap()

  const stations = data?.stations ?? []
  const detections = data?.recent_detections ?? []

  const activeStates = useMemo(() => {
    const set = new Set()
    for (const s of stations) if (s.state) set.add(String(s.state).toUpperCase())
    return set.size
  }, [stations])

  const isEmpty = !isLoading && !isError && stations.length === 0 && detections.length === 0

  return (
    <div className="live-map-page">
      {isFetching && data && <div className="live-top-progress" aria-hidden="true" />}

      <header className="live-header">
        <div className="live-header-titles">
          <span className="live-eyebrow">Em tempo real</span>
          <h1>Mapa ao Vivo</h1>
        </div>
        <div className="live-header-meta">
          <span className="live-badge">
            <span className="live-badge-dot" />
            AO VIVO
          </span>
          {!isLoading && !isError && (
            <div className="live-kpis">
              <span className="live-kpi">
                <strong>{stations.length}</strong> emissora{stations.length === 1 ? '' : 's'}
              </span>
              <span className="live-kpi-sep" />
              <span className="live-kpi">
                <strong>{activeStates}</strong> estado{activeStates === 1 ? '' : 's'}
              </span>
            </div>
          )}
        </div>
      </header>

      {isError && !data ? (
        <div className="live-error">
          <svg viewBox="0 0 24 24" width="40" height="40" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 9v4M12 17h.01" />
            <path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          </svg>
          <p className="live-error-title">Não foi possível carregar o mapa</p>
          <p className="live-error-msg">Verifique a conexão e tente novamente.</p>
          <button className="btn btn-secondary" onClick={() => refetch()}>Tentar de novo</button>
        </div>
      ) : isEmpty ? (
        <EmptyGhost isAdmin={isAdmin} />
      ) : (
        <div className="live-grid">
          <section className="live-panel live-feed">
            <div className="live-feed-head">
              <span className="live-feed-title">Últimas Veiculações</span>
              {!isLoading && <span className="live-feed-count">{detections.length}</span>}
            </div>
            {isLoading ? (
              <FeedSkeleton />
            ) : detections.length === 0 ? (
              <div className="live-feed-empty">Nenhuma veiculação recente.</div>
            ) : (
              <AiringsFeed detections={detections} />
            )}
          </section>

          <section className="live-panel live-map-panel">
            {isLoading ? <MapSkeleton /> : <BrazilMap stations={stations} />}
          </section>
        </div>
      )}
    </div>
  )
}
