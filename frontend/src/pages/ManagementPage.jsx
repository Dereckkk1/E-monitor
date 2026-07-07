import { useEffect, useMemo, useRef, useState } from 'react'
import html2canvas from 'html2canvas'
import RSelect from '../components/RSelect'
import BrazilMap from '../components/BrazilMap'
import { LiveAiringRow, FeedSkeleton } from '../components/LiveAiringRow'
import { useClients, useCampaignsPaged, useManagementOverview } from '../api/hooks'
import { safeLogoUrl } from '../utils/logoUrl'
import './LiveMapPage.css'
import './ManagementPage.css'

const STATUS_OPTS = [
  { value: 'ativa', label: 'Ativa' },
  { value: 'programada', label: 'Programada' },
  { value: 'concluida', label: 'Concluída' },
  { value: 'cancelada', label: 'Cancelada' },
]

function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgErr, setImgErr] = useState(false)
  const safe = safeLogoUrl(logo)
  if (safe && !imgErr) {
    return (
      <img src={safe} alt={name} width={size} height={size}
        className="lm-client-avatar lm-client-avatar--img"
        style={{ width: size, height: size }} onError={() => setImgErr(true)} />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div className="lm-client-avatar lm-client-avatar--fb"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}>
      {initials}
    </div>
  )
}

function formatClientOption(opt, { context }) {
  const size = context === 'value' ? 18 : 22
  return (
    <div className="lm-client-option">
      <ClientMiniAvatar name={opt.label} logo={opt.raw?.logo_url} size={size} />
      <span className="lm-client-option-label">{opt.label}</span>
    </div>
  )
}

function nf(n) { return new Intl.NumberFormat('pt-BR').format(n ?? 0) }

function KpiCard({ value, label, sub, live, icon }) {
  return (
    <div className={'mg-kpi' + (live ? ' mg-kpi--live' : '')}>
      <span className="mg-kpi-ic" aria-hidden>{icon}</span>
      <div className="mg-kpi-n">{value}</div>
      <div className="mg-kpi-l">{label}</div>
      {sub && <div className="mg-kpi-s">{sub}</div>}
    </div>
  )
}

const ICON_MAP = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 14s5-4.5 5-8A5 5 0 0 0 3 6c0 3.5 5 8 5 8z" /><circle cx="8" cy="6" r="1.75" /></svg>
)
const ICON_LIVE = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="2" /><path d="M5.2 5.2a4 4 0 0 0 0 5.6M10.8 5.2a4 4 0 0 1 0 5.6" /></svg>
)
const ICON_MAT = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M6 13V4l7-1.2V11" /><circle cx="4" cy="13" r="2" /><circle cx="11" cy="11" r="2" /></svg>
)
const ICON_AIR = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6" /><path d="M5.5 8l1.5 1.5L10.5 6" /></svg>
)

export default function ManagementPage() {
  const [clientId, setClientId] = useState(null)
  const [campaignIds, setCampaignIds] = useState([])
  const [status, setStatus] = useState(null)
  const [playingId, setPlayingId] = useState(null)
  const [downloading, setDownloading] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const mapRef = useRef(null)

  // Tela cheia: esconde a navegação (sidebar + topbar mobile) via classe no
  // <body> e usa a viewport inteira. Puramente visual — nada muda nos dados.
  useEffect(() => {
    document.body.classList.toggle('mg-fs', fullscreen)
    return () => document.body.classList.remove('mg-fs')
  }, [fullscreen])

  useEffect(() => {
    if (!fullscreen) return
    const onKey = (e) => { if (e.key === 'Escape') setFullscreen(false) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [fullscreen])

  // Período default = ano corrente (01/01 → hoje). Datas em ISO YYYY-MM-DD.
  const year = new Date().getFullYear()
  const [from, setFrom] = useState(`${year}-01-01`)
  const [to, setTo] = useState(() => {
    const d = new Date()
    const p = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
  })

  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data],
  )

  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })
  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])
  const campOpts = useMemo(() => {
    const rows = clientId ? allCampaigns.filter(c => c.client_id === clientId) : allCampaigns
    return rows.map(c => ({ value: c.id, label: c.name }))
  }, [allCampaigns, clientId])

  const { data, isLoading, isError, isFetching, refetch } = useManagementOverview({
    clientId, campaignIds, status, from, to,
  })

  const kpis = data?.kpis ?? {}
  const stations = useMemo(() => data?.stations ?? [], [data])
  const detections = useMemo(() => data?.recent_detections ?? [], [data])
  const livePct = kpis.stations_monitored
    ? Math.round((kpis.stations_live / kpis.stations_monitored) * 100)
    : 0

  async function handleDownloadMap() {
    const el = mapRef.current
    if (!el || downloading) return
    setDownloading(true)
    try {
      const canvas = await html2canvas(el, { backgroundColor: '#ffffff', scale: 2, logging: false })
      canvas.toBlob((blob) => {
        if (!blob) return
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        const ts = new Date().toISOString().slice(0, 10)
        a.download = `visao-gerencial-${ts}.png`
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 1500)
      }, 'image/png')
    } finally {
      setDownloading(false)
    }
  }

  return (
    <div className={'mg-page' + (fullscreen ? ' mg-page--fs' : '')}>
      <header className="mg-header">
        <h1 className="mg-title">Visão Gerencial</h1>
        <div className="mg-header-right">
          {!isLoading && !isError && (
            <span className="mg-live">
              <span className="mg-live-dot" />
              ao vivo · atualiza a cada 20s
              {isFetching && <span className="mg-refreshing" aria-label="atualizando" />}
            </span>
          )}
          <button
            type="button"
            className="mg-fs-btn"
            onClick={() => setFullscreen(v => !v)}
            aria-pressed={fullscreen}
            title={fullscreen ? 'Sair da tela cheia (Esc)' : 'Tela cheia'}
          >
            {fullscreen ? (
              <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="M6 2v2.5A1.5 1.5 0 0 1 4.5 6H2M14 6h-2.5A1.5 1.5 0 0 1 10 4.5V2M10 14v-2.5a1.5 1.5 0 0 1 1.5-1.5H14M2 10h2.5A1.5 1.5 0 0 1 6 11.5V14" /></svg>
            ) : (
              <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="M2 6V3.5A1.5 1.5 0 0 1 3.5 2H6M10 2h2.5A1.5 1.5 0 0 1 14 3.5V6M14 10v2.5a1.5 1.5 0 0 1-1.5 1.5H10M6 14H3.5A1.5 1.5 0 0 1 2 12.5V10" /></svg>
            )}
            <span>{fullscreen ? 'Sair' : 'Tela cheia'}</span>
          </button>
        </div>
      </header>

      <div className="mg-filters">
        <div className="mg-filter">
          <label className="mg-filter-label">Cliente</label>
          <RSelect
            options={clientOpts}
            value={clientOpts.find(o => o.value === clientId) || null}
            onChange={opt => { setClientId(opt?.value || null); setCampaignIds([]) }}
            placeholder="Todos os clientes"
            isLoading={clientsQ.isPending}
            isClearable
            formatOptionLabel={formatClientOption}
          />
        </div>
        <div className="mg-filter">
          <label className="mg-filter-label">Campanhas</label>
          <RSelect
            options={campOpts}
            value={campOpts.filter(o => campaignIds.includes(o.value))}
            onChange={opts => setCampaignIds((opts || []).map(o => o.value))}
            placeholder="Todas as campanhas"
            isLoading={campaignsQ.isPending}
            isMulti
            isClearable
          />
        </div>
        <div className="mg-filter mg-filter--date">
          <label className="mg-filter-label">De</label>
          <input type="date" className="mg-date" value={from} max={to} onChange={e => setFrom(e.target.value)} />
        </div>
        <div className="mg-filter mg-filter--date">
          <label className="mg-filter-label">Até</label>
          <input type="date" className="mg-date" value={to} min={from} onChange={e => setTo(e.target.value)} />
        </div>
        <div className="mg-filter mg-filter--narrow">
          <label className="mg-filter-label">Status</label>
          <RSelect
            options={STATUS_OPTS}
            value={STATUS_OPTS.find(o => o.value === status) || null}
            onChange={opt => setStatus(opt?.value || null)}
            placeholder="Todos"
            isClearable
          />
        </div>
      </div>

      {isError && !data ? (
        <div className="mg-error">
          <svg viewBox="0 0 24 24" width="38" height="38" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M12 9v4M12 17h.01" /><path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" /></svg>
          <p className="mg-error-title">Não foi possível carregar a visão gerencial</p>
          <button className="btn btn-secondary" onClick={() => refetch()}>Tentar de novo</button>
        </div>
      ) : (
        <div className="mg-grid">
          <div className="mg-top">
            <div className="mg-kpis">
            <KpiCard icon={ICON_MAP} value={isLoading ? '—' : nf(kpis.stations_monitored)}
              label="Emissoras monitoradas"
              sub={isLoading ? '' : `no período · ${nf(kpis.states_count)}/26 estado${kpis.states_count === 1 ? '' : 's'}`} />
            <KpiCard icon={ICON_LIVE} live value={isLoading ? '—' : nf(kpis.stations_live)}
              label="Monitorando agora"
              sub={isLoading ? '' : `worker saudável · ${livePct}% online`} />
            <KpiCard icon={ICON_MAT} value={isLoading ? '—' : nf(kpis.materials_monitored)}
              label="Materiais monitorados"
              sub={isLoading ? '' : `em ${nf(kpis.campaigns_count)} campanha${kpis.campaigns_count === 1 ? '' : 's'}`} />
            <KpiCard icon={ICON_AIR} value={isLoading ? '—' : nf(kpis.airings_total)}
              label="Veiculações no período"
              sub={isLoading ? '' : `+${nf(kpis.airings_today)} hoje`} />
            </div>

            <section className="mg-card mg-map-card">
              <div className="mg-card-head">
                <span className="mg-card-title">Emissoras monitoradas</span>
                <span className="mg-card-meta">
                  {!isLoading && stations.length > 0 && (
                    <span>{nf(kpis.stations_live)} ao vivo</span>
                  )}
                  {!isLoading && stations.length > 0 && (
                    <button type="button" className="mg-map-dl" onClick={handleDownloadMap} disabled={downloading}
                      title="Baixar imagem do mapa" aria-label="Baixar imagem do mapa">
                      {downloading ? <span className="la-row-spinner" aria-hidden /> : (
                        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="M8 2v8M4.5 6.5L8 10l3.5-3.5" /><path d="M3 12v1.5A1.5 1.5 0 0 0 4.5 15h7a1.5 1.5 0 0 0 1.5-1.5V12" /></svg>
                      )}
                      <span>Baixar</span>
                    </button>
                  )}
                </span>
              </div>
              {isLoading ? (
                <div className="mg-map-skeleton"><div className="la-skel mg-map-skeleton-shape" /></div>
              ) : stations.length === 0 ? (
                <div className="mg-map-empty" ref={mapRef}>
                  <BrazilMap stations={[]} />
                  <span className="mg-map-empty-msg">Nenhuma emissora monitorada no filtro/período selecionado.</span>
                </div>
              ) : (
                <div ref={mapRef}><BrazilMap stations={stations} /></div>
              )}
            </section>
          </div>

          <section className="mg-card mg-feed-card">
              <div className="mg-card-head">
                <span className="mg-card-title">Veiculações ao vivo · todas as campanhas</span>
                {!isLoading && <span className="mg-feed-count">{detections.length}</span>}
              </div>
              {isLoading ? (
                <FeedSkeleton />
              ) : detections.length === 0 ? (
                <div className="mg-feed-empty">Nenhuma veiculação recente no recorte selecionado.</div>
              ) : (
                <div className="la-list la-stagger">
                  {detections.map(d => (
                    <LiveAiringRow key={d.id} detection={d}
                      isPlaying={playingId === d.id}
                      onPlayRequest={(id) => setPlayingId(id)}
                      onPlayClose={() => setPlayingId(null)} />
                  ))}
                </div>
              )}
          </section>
        </div>
      )}
    </div>
  )
}
