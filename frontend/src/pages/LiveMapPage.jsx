import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import RSelect from '../components/RSelect'
import { useAuth } from '../contexts/AuthContext'
import { useClients, useCampaignsPaged, useLiveMap } from '../api/hooks'
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
function AiringsFeed({ detections, stagger = true }) {
  return (
    <div className={`lm-feed-list${stagger ? ' lm-stagger' : ''}`} key={detections[0]?.id ?? 'empty'}>
      {detections.map((d) => {
        const t = fmtTime(d.detected_at)
        const loc = [d.city, d.state].filter(Boolean).join(' / ')
        const material = [d.commercial_name, d.client_name].filter(Boolean).join(' · ')
        return (
          <div className="lm-row" key={d.id}>
            <span className="lm-row-pulse" aria-hidden="true" />
            <div className="lm-row-body">
              <div className="lm-row-top">
                <span className="lm-row-station">{stationLabel(d)}</span>
                <span className="lm-row-time">{t.time}</span>
              </div>
              <div className="lm-row-sub">
                <span className="lm-row-loc">{loc}</span>
                <span className="lm-row-date">{t.date}</span>
              </div>
              {material && <span className="lm-row-material">{material}</span>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

/* ── Skeletons (shape-matched) ───────────────────────────────────── */
function FeedSkeleton() {
  const widths = ['72%', '58%', '80%', '64%', '50%', '76%']
  return (
    <div className="lm-feed-list">
      {widths.map((w, i) => (
        <div className="lm-row lm-row--skeleton" key={i}>
          <span className="lm-skel lm-skel-circle" style={{ width: 8, height: 8, marginTop: 5 }} />
          <div className="lm-row-body">
            <div className="lm-row-top">
              <span className="lm-skel" style={{ width: w, height: 13 }} />
              <span className="lm-skel" style={{ width: 50, height: 11 }} />
            </div>
            <span className="lm-skel" style={{ width: '38%', height: 10, marginTop: 6 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function MapSkeleton() {
  return (
    <div className="lm-map-skeleton">
      <div className="lm-skel lm-map-skeleton-shape" />
    </div>
  )
}

/* ── Empty (tutorial estilizado — design.md §4.7) ────────────────── */
const GHOST_FEED = [
  { id: 'g1', station_name: 'Rádio Exemplo FM', city: 'São Paulo', state: 'SP', commercial_name: 'Sua campanha aqui' },
  { id: 'g2', station_name: 'Rádio Exemplo AM', city: 'Goiânia', state: 'GO', commercial_name: 'Sua campanha aqui' },
  { id: 'g3', station_name: 'Rádio Exemplo FM', city: 'Curitiba', state: 'PR', commercial_name: 'Sua campanha aqui' },
]

function EmptyTutorial({ title, description, cta }) {
  return (
    <div className="lm-empty">
      <div className="lm-empty-ghost" aria-hidden="true">
        <section className="lm-panel lm-feed">
          <div className="lm-panel-head"><span className="lm-panel-title">Últimas Veiculações</span></div>
          <AiringsFeed detections={GHOST_FEED} stagger={false} />
        </section>
        <section className="lm-panel lm-map-panel">
          <BrazilMap stations={[]} />
        </section>
      </div>
      <div className="lm-empty-overlay">
        <div className="lm-empty-card">
          <div className="lm-empty-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 21s7-6.3 7-11a7 7 0 0 0-14 0c0 4.7 7 11 7 11z" />
              <circle cx="12" cy="10" r="2.5" />
            </svg>
          </div>
          <h3>{title}</h3>
          <p>{description}</p>
          {cta}
        </div>
      </div>
    </div>
  )
}

/* ── Página ──────────────────────────────────────────────────────── */
export default function LiveMapPage() {
  const { isAdmin, user } = useAuth()
  // Viewer: clientId derivado do próprio user (sem state/efeito). Admin: escolhe.
  const [adminClientId, setAdminClientId] = useState(null)
  const clientId = isAdmin ? adminClientId : (user?.client_id ?? null)
  const [campaignId, setCampaignId] = useState(null)

  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name })),
    [clientsQ.data],
  )

  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })
  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])
  const campOpts = useMemo(() => {
    const rows = isAdmin
      ? allCampaigns.filter(c => !clientId || c.client_id === clientId)
      : allCampaigns
    return rows.map(c => ({ value: c.id, label: c.name }))
  }, [allCampaigns, clientId, isAdmin])

  const { data, isLoading, isError, isFetching, refetch } = useLiveMap(campaignId)
  const stations = useMemo(() => data?.stations ?? [], [data])
  const detections = useMemo(() => data?.recent_detections ?? [], [data])

  const activeStates = useMemo(() => {
    const set = new Set()
    for (const s of stations) if (s.state) set.add(String(s.state).toUpperCase())
    return set.size
  }, [stations])

  // ── Estados de seleção / tutorial ──────────────────────────────────
  let emptyTutorial = null
  if (isAdmin && !clientId) {
    emptyTutorial = (
      <EmptyTutorial
        title="Escolha um cliente e uma campanha"
        description="Selecione o cliente e a campanha nos filtros acima para ver, em tempo real, onde as emissoras estão sendo monitoradas e as últimas veiculações no mapa do Brasil."
      />
    )
  } else if (!campaignId) {
    const noCampaigns = !campaignsQ.isPending && campOpts.length === 0
    emptyTutorial = (
      <EmptyTutorial
        title={noCampaigns ? 'Nenhuma campanha por aqui' : 'Selecione uma campanha'}
        description={
          noCampaigns
            ? 'Quando houver uma campanha ativa, suas emissoras aparecem pulsando no mapa e as veiculações entram no feed em tempo real.'
            : 'Escolha uma campanha no filtro acima para ver as emissoras dela no mapa e o feed de veiculações ao vivo.'
        }
        cta={noCampaigns ? <Link className="btn btn-primary" to="/campaigns">Ver campanhas</Link> : null}
      />
    )
  }

  return (
    <div className="lm-page">
      <header className="lm-header">
        <h1 className="lm-title">Mapa ao Vivo</h1>
        {campaignId && !isLoading && !isError && (
          <span className="lm-live">
            <span className="lm-live-dot" />
            ao vivo · atualiza a cada 20s
          </span>
        )}
      </header>

      {/* Filtros — mesmo padrão das telas de Veiculação */}
      <div className="lm-filters">
        {isAdmin ? (
          <div className="lm-filter">
            <label className="lm-filter-label">Cliente</label>
            <RSelect
              options={clientOpts}
              value={clientOpts.find(o => o.value === clientId) || null}
              onChange={opt => { setAdminClientId(opt?.value || null); setCampaignId(null) }}
              placeholder="Selecione…"
              isLoading={clientsQ.isPending}
              isClearable
            />
          </div>
        ) : (
          <div className="lm-filter">
            <label className="lm-filter-label">Cliente</label>
            <div className="lm-locked-chip">{user?.client_name || user?.email || 'Sua conta'}</div>
          </div>
        )}

        <div className="lm-filter lm-filter--wide">
          <label className="lm-filter-label">Campanha</label>
          <RSelect
            options={campOpts}
            value={campOpts.find(o => o.value === campaignId) || null}
            onChange={opt => setCampaignId(opt?.value || null)}
            placeholder="Selecione uma campanha…"
            isDisabled={isAdmin && !clientId}
            isLoading={campaignsQ.isPending}
            isClearable
          />
        </div>
      </div>

      {emptyTutorial ? (
        emptyTutorial
      ) : isError && !data ? (
        <div className="lm-error">
          <svg viewBox="0 0 24 24" width="38" height="38" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12 9v4M12 17h.01" />
            <path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          </svg>
          <p className="lm-error-title">Não foi possível carregar o mapa</p>
          <button className="btn btn-secondary" onClick={() => refetch()}>Tentar de novo</button>
        </div>
      ) : (
        <div className="lm-grid">
          <section className="lm-panel lm-feed">
            <div className="lm-panel-head">
              <span className="lm-panel-title">Últimas Veiculações</span>
              {!isLoading && <span className="lm-count">{detections.length}</span>}
            </div>
            {isLoading ? (
              <FeedSkeleton />
            ) : detections.length === 0 ? (
              <div className="lm-feed-empty">Nenhuma veiculação recente nesta campanha.</div>
            ) : (
              <AiringsFeed detections={detections} />
            )}
          </section>

          <section className="lm-panel lm-map-panel">
            <div className="lm-panel-head">
              <span className="lm-panel-title">Emissoras monitoradas</span>
              {!isLoading && (
                <span className="lm-map-meta">
                  {isFetching && <span className="lm-map-refreshing" />}
                  {stations.length} emissora{stations.length === 1 ? '' : 's'} · {activeStates} estado{activeStates === 1 ? '' : 's'}
                </span>
              )}
            </div>
            {isLoading ? (
              <MapSkeleton />
            ) : stations.length === 0 ? (
              <div className="lm-map-empty">
                <BrazilMap stations={[]} />
                <span className="lm-map-empty-msg">As emissoras desta campanha ainda não têm localização cadastrada.</span>
              </div>
            ) : (
              <BrazilMap stations={stations} />
            )}
          </section>
        </div>
      )}
    </div>
  )
}
