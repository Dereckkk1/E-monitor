import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import RSelect from '../components/RSelect'
import StationAvatar from '../components/StationAvatar'
import { useAuth } from '../contexts/AuthContext'
import { useClients, useCampaignsPaged, useLiveMap } from '../api/hooks'
import { materialColor } from '../utils/materialColor'
import { safeLogoUrl } from '../utils/logoUrl'
import BrazilMap from '../components/BrazilMap'
import './LiveMapPage.css'

/* Avatar mini de cliente (logo + fallback iniciais) — espelha o que o
 * InsightsPage/AirtimeFiltersBar usam na select de cliente. */
function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgErr, setImgErr] = useState(false)
  const safe = safeLogoUrl(logo)
  if (safe && !imgErr) {
    return (
      <img
        src={safe}
        alt={name}
        width={size}
        height={size}
        className="lm-client-avatar lm-client-avatar--img"
        style={{ width: size, height: size }}
        onError={() => setImgErr(true)}
      />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div
      className="lm-client-avatar lm-client-avatar--fb"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}
    >
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

function pad2(n) { return String(n).padStart(2, '0') }
function fmtDate(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}/${d.getFullYear()}`
}
function fmtTime(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`
}
function freqStr(d) {
  return d.frequency_mhz != null ? String(d.frequency_mhz).replace('.', ',') : null
}

/* ── Linha do feed — espelha o AirtimeDetectionRow (sem play/pricing) ─── */
function LiveAiringRow({ detection }) {
  const place = [detection.city, detection.state].filter(Boolean).join(' / ')
  const freq = freqStr(detection)
  const matColor = materialColor(detection.commercial_id)
  return (
    <article className="la-row" style={{ '--material-color': matColor }}>
      <div className="la-row-time-block">
        <span className="la-row-date">{fmtDate(detection.detected_at)}</span>
        <span className="la-row-time">{fmtTime(detection.detected_at)}</span>
      </div>

      <div className="la-row-station">
        <StationAvatar
          station={{ name: detection.station_name, logo_url: detection.station_logo_url }}
          size={36}
        />
        <div className="la-row-station-text">
          <span className="la-row-station-name">
            {detection.station_name}
            {freq && <span className="la-row-station-freq"> · {detection.band} ({freq})</span>}
          </span>
          {place && <span className="la-row-station-place">{place}</span>}
        </div>
      </div>

      <div className="la-row-material">
        <span className="la-row-material-name" style={{ color: matColor }} title={detection.commercial_name}>
          {detection.commercial_name}
        </span>
        {detection.client_name && (
          <span className="la-row-material-client">{detection.client_name}</span>
        )}
      </div>

      <span className="la-row-stripe" aria-hidden />
    </article>
  )
}

/* ── Skeleton (shape-matched) ─────────────────────────────────────── */
function FeedSkeleton() {
  const rows = [0, 1, 2, 3, 4, 5]
  return (
    <div className="la-list">
      {rows.map((i) => (
        <div className="la-row la-row--skel" key={i}>
          <div className="la-row-time-block">
            <span className="la-skel" style={{ width: 62, height: 11 }} />
            <span className="la-skel" style={{ width: 52, height: 14, marginTop: 4 }} />
          </div>
          <div className="la-row-station">
            <span className="la-skel la-skel-circle" style={{ width: 36, height: 36 }} />
            <div className="la-row-station-text">
              <span className="la-skel" style={{ width: '78%', height: 13 }} />
              <span className="la-skel" style={{ width: '46%', height: 11, marginTop: 4 }} />
            </div>
          </div>
          <div className="la-row-material">
            <span className="la-skel" style={{ width: '70%', height: 13 }} />
            <span className="la-skel" style={{ width: '40%', height: 11, marginTop: 4 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function MapSkeleton() {
  return (
    <div className="lm-map-skeleton">
      <div className="la-skel lm-map-skeleton-shape" />
    </div>
  )
}

/* ── Empty (tutorial estilizado — design.md §4.7) ────────────────── */
const GHOST_FEED = [
  { id: 'g1', station_name: 'Rádio Exemplo FM', city: 'São Paulo', state: 'SP', band: 'FM', frequency_mhz: 100.5, commercial_name: 'Sua campanha aqui', commercial_id: 'g1', detected_at: new Date().toISOString() },
  { id: 'g2', station_name: 'Rádio Exemplo AM', city: 'Goiânia', state: 'GO', band: 'AM', frequency_mhz: 820, commercial_name: 'Sua campanha aqui', commercial_id: 'g2', detected_at: new Date().toISOString() },
  { id: 'g3', station_name: 'Rádio Exemplo FM', city: 'Curitiba', state: 'PR', band: 'FM', frequency_mhz: 98.1, commercial_name: 'Sua campanha aqui', commercial_id: 'g3', detected_at: new Date().toISOString() },
]

function EmptyTutorial({ title, description, cta }) {
  return (
    <div className="lm-empty">
      <div className="lm-empty-ghost" aria-hidden="true">
        <LiveCanvas
          feedTitle="Últimas Veiculações"
          mapTitle="Emissoras monitoradas"
          mapMeta=""
          feed={<div className="la-list">{GHOST_FEED.map(d => <LiveAiringRow key={d.id} detection={d} />)}</div>}
          map={<BrazilMap stations={[]} />}
        />
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

/* ── Canvas único: feed + divisor + mapa numa só superfície ──────── */
function LiveCanvas({ feedTitle, feedCount, mapTitle, mapMeta, feed, map }) {
  return (
    <section className="lm-canvas">
      <div className="lm-canvas-grid">
        <div className="lm-canvas-feed">
          <div className="lm-canvas-head">
            <span className="lm-canvas-title">{feedTitle}</span>
            {feedCount != null && <span className="lm-count">{feedCount}</span>}
          </div>
          {feed}
        </div>
        <div className="lm-canvas-sep" aria-hidden />
        <div className="lm-canvas-map">
          <div className="lm-canvas-head">
            <span className="lm-canvas-title">{mapTitle}</span>
            {mapMeta && <span className="lm-canvas-meta">{mapMeta}</span>}
          </div>
          {map}
        </div>
      </div>
    </section>
  )
}

/* ── Página ──────────────────────────────────────────────────────── */
export default function LiveMapPage() {
  const { isAdmin, user } = useAuth()
  const [adminClientId, setAdminClientId] = useState(null)
  const clientId = isAdmin ? adminClientId : (user?.client_id ?? null)
  const [campaignId, setCampaignId] = useState(null)

  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data],
  )
  // Para o viewer: resolve o próprio cliente (com logo) a partir da lista
  // scope-aware do /clients (que devolve só ele).
  const ownClient = useMemo(() => {
    if (isAdmin) return null
    return (clientsQ.data || []).find(c => c.id === user?.client_id) || null
  }, [clientsQ.data, isAdmin, user?.client_id])

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
        description="Selecione o cliente e a campanha nos filtros acima para ver, em tempo real, onde as emissoras estão sendo monitoradas e as últimas veiculações."
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

  const mapMeta = !isLoading && stations.length > 0
    ? `${stations.length} emissora${stations.length === 1 ? '' : 's'} · ${activeStates} estado${activeStates === 1 ? '' : 's'}`
    : ''

  return (
    <div className="lm-page">
      <header className="lm-header">
        <h1 className="lm-title">Mapa ao Vivo</h1>
        {campaignId && !isLoading && !isError && (
          <span className="lm-live">
            <span className="lm-live-dot" />
            ao vivo · atualiza a cada 20s
            {isFetching && <span className="lm-refreshing" aria-label="atualizando" />}
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
              formatOptionLabel={formatClientOption}
            />
          </div>
        ) : (
          <div className="lm-filter">
            <label className="lm-filter-label">Cliente</label>
            <div className="lm-locked-chip">
              <ClientMiniAvatar
                name={ownClient?.name || user?.client_name || user?.email || ''}
                logo={ownClient?.logo_url}
                size={20}
              />
              <span>{ownClient?.name || user?.client_name || user?.email || 'Sua conta'}</span>
            </div>
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
        <LiveCanvas
          feedTitle="Últimas Veiculações"
          feedCount={!isLoading ? detections.length : null}
          mapTitle="Emissoras monitoradas"
          mapMeta={mapMeta}
          feed={
            isLoading ? (
              <FeedSkeleton />
            ) : detections.length === 0 ? (
              <div className="la-empty">Nenhuma veiculação recente nesta campanha.</div>
            ) : (
              <div className="la-list la-stagger">
                {detections.map(d => <LiveAiringRow key={d.id} detection={d} />)}
              </div>
            )
          }
          map={
            isLoading ? (
              <MapSkeleton />
            ) : stations.length === 0 ? (
              <div className="lm-map-empty">
                <BrazilMap stations={[]} />
                <span className="lm-map-empty-msg">As emissoras desta campanha ainda não têm localização cadastrada.</span>
              </div>
            ) : (
              <BrazilMap stations={stations} />
            )
          }
        />
      )}
    </div>
  )
}
