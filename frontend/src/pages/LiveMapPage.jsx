import { useCallback, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import html2canvas from 'html2canvas'
import RSelect from '../components/RSelect'
import { useAuth } from '../contexts/AuthContext'
import { useClients, useCampaignsPaged, useLiveMap, useLiveMapCoverage } from '../api/hooks'
import { safeLogoUrl } from '../utils/logoUrl'
import BrazilMap from '../components/BrazilMap'
import CoverageMap from '../components/CoverageMap'
import { LiveAiringRow, FeedSkeleton } from '../components/LiveAiringRow'
import FlowEmptyState from '../components/FlowEmptyState'
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

const STEP_LABELS = ['Cliente', 'Campanhas']

const ICON_PIN = (
  <svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 21s7-6.3 7-11a7 7 0 0 0-14 0c0 4.7 7 11 7 11z" />
    <circle cx="12" cy="10" r="2.5" />
  </svg>
)

function EmptyTutorial({ step, title, description, cta, steps = STEP_LABELS }) {
  return (
    <FlowEmptyState
      className="lm-empty detection-empty--veil"
      step={step}
      steps={steps}
      icon={ICON_PIN}
      title={title}
      description={description}
      actions={cta}
      ghost={(
        <LiveCanvas
          feedTitle="Últimas Veiculações"
          mapTitle="Emissoras monitoradas"
          mapMeta=""
          feed={<div className="la-list">{GHOST_FEED.map(d => <LiveAiringRow key={d.id} detection={d} />)}</div>}
          map={<BrazilMap stations={[]} />}
        />
      )}
    />
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
            {mapMeta}
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
  const [pickedClientId, setPickedClientId] = useState(null)
  // Seleção múltipla, igual /insights: o mapa passa a mostrar a UNIÃO das
  // emissoras e o feed mistura as veiculações das campanhas escolhidas.
  const [campaignIds, setCampaignIds] = useState([])
  // Single-player coordination: só uma row toca por vez.
  const [playingId, setPlayingId] = useState(null)
  const [downloading, setDownloading] = useState(false)
  const mapRef = useRef(null)

  // /clients é scope-aware: admin recebe a lista inteira, Cliente recebe a
  // própria carteira (1 no caso normal, N quando é agência). O seletor só
  // aparece pra quem tem escolha — os demais seguem com o chip travado.
  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data],
  )
  const canPickClient = isAdmin || clientOpts.length > 1
  // Sem seletor, o cliente é o único da carteira. Com seletor, null = a
  // carteira inteira (o mapa ao vivo não exige escolher um cliente).
  const clientId = canPickClient ? pickedClientId : (clientOpts[0]?.value ?? null)
  // Para quem não escolhe: resolve o próprio cliente (com logo) a partir da
  // lista scope-aware do /clients (que devolve só ele).
  const ownClient = useMemo(() => {
    if (canPickClient) return null
    return clientOpts[0]?.raw || null
  }, [clientOpts, canPickClient])

  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })
  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])
  const campOpts = useMemo(() => {
    // Canceladas (terminais) não entram no seletor de mapa ao vivo — o backend
    // também as bloqueia (404) porque "ao vivo" implica campanha rodando.
    // O recorte por cliente vale pra qualquer role: no Cliente de um cliente
    // só é no-op, na agência é o que separa a carteira.
    const rows = (clientId
      ? allCampaigns.filter(c => c.client_id === clientId)
      : allCampaigns
    ).filter(c => c.status !== 'cancelada')
    return rows.map(c => ({ value: c.id, label: c.name }))
  }, [allCampaigns, clientId])

  const hasCampaigns = campaignIds.length > 0
  const multiCampaign = campaignIds.length > 1

  const { data, isLoading, isError, isFetching, refetch } = useLiveMap(campaignIds)
  const stations = useMemo(() => data?.stations ?? [], [data])
  const detections = useMemo(() => data?.recent_detections ?? [], [data])

  // Cobertura vem de endereco proprio, sem polling: e dado estatico. Carrega
  // em paralelo e o mapa ja e util antes dela chegar (so a ficha de foco
  // depende dela).
  const coverageQ = useLiveMapCoverage(campaignIds)
  const coverage = useMemo(() => coverageQ.data?.cities ?? [], [coverageQ.data])

  // Ponte feed <-> mapa: passar o mouse numa veiculacao acende a emissora no
  // mapa, e vice-versa. Sem isso os dois paineis da tela se ignoram.
  const [hoverStationId, setHoverStationId] = useState(null)
  const handleMapHover = useCallback((id) => setHoverStationId(id), [])

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
        step={1}
        title={<>Comece pelo <strong>cliente</strong></>}
        description="Escolha o cliente no filtro acima. As campanhas dele ficam disponíveis logo em seguida."
      />
    )
  } else if (!hasCampaigns) {
    const noCampaigns = !campaignsQ.isPending && campOpts.length === 0
    emptyTutorial = (
      <EmptyTutorial
        step={2}
        title={noCampaigns ? 'Nenhuma campanha por aqui' : <>Escolha as <strong>campanhas</strong></>}
        description={
          noCampaigns
            ? 'Quando houver uma campanha ativa, suas emissoras aparecem pulsando no mapa e as veiculações entram no feed em tempo real.'
            : 'Escolha uma ou mais campanhas no filtro acima para ver as emissoras delas no mapa e o feed de veiculações ao vivo.'
        }
        cta={noCampaigns ? <Link className="btn btn-primary" to="/campaigns">Ver campanhas</Link> : null}
      />
    )
  }

  const mapMetaText = !isLoading && stations.length > 0
    ? `${stations.length} emissora${stations.length === 1 ? '' : 's'} · ${activeStates} estado${activeStates === 1 ? '' : 's'}`
    : ''

  // Vira o slug do PNG do mapa. Com várias campanhas nenhum nome representa a
  // imagem, então a contagem é o rótulo honesto.
  const campaignName = useMemo(() => {
    if (campaignIds.length === 0) return ''
    if (campaignIds.length > 1) return `${campaignIds.length} campanhas`
    return (allCampaigns.find(c => c.id === campaignIds[0])?.name || '').trim()
  }, [campaignIds, allCampaigns])

  async function handleDownloadMap() {
    const el = mapRef.current
    if (!el || downloading) return
    setDownloading(true)
    try {
      const canvas = await html2canvas(el, {
        backgroundColor: '#ffffff',
        scale: 2,
        logging: false,
        // A barra de controles do mapa é chrome de interação, não conteúdo —
        // não faz sentido no arquivo exportado.
        ignoreElements: (node) => node.classList?.contains('cvm-controls'),
        // O html2canvas refaz o download de cada <img> por conta própria, e o
        // CDN dos logos não manda Access-Control-Allow-Origin: eles saíam como
        // quadrados vazios no PNG mesmo aparecendo normais na tela (e com
        // useCORS o navegador ainda logava um erro por imagem). Na CÓPIA que é
        // fotografada, o logo vira as iniciais — que é o mesmo fallback que a
        // tela já usa quando a imagem falha.
        onclone: (doc) => {
          doc.querySelectorAll('[data-initials]').forEach((box) => {
            const img = box.querySelector('img')
            if (!img) return
            img.remove()
            const span = doc.createElement('span')
            span.className = box.classList.contains('cvm-focus-logo')
              ? 'cvm-focus-initials'
              : 'cvm-marker-initials'
            span.textContent = box.dataset.initials
            box.appendChild(span)
          })
        },
      })
      canvas.toBlob((blob) => {
        if (!blob) return
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        const slug = (campaignName || 'campanha').toLowerCase()
          .normalize('NFD').replace(/[̀-ͯ]/g, '')
          .replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '') || 'campanha'
        const ts = new Date().toISOString().slice(0, 10)
        a.download = `mapa-ao-vivo-${slug}-${ts}.png`
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 1500)
      }, 'image/png')
    } finally {
      setDownloading(false)
    }
  }

  const mapMeta = (
    <span className="lm-canvas-meta">
      {mapMetaText && <span>{mapMetaText}</span>}
      {!isLoading && stations.length > 0 && (
        <button
          type="button"
          className="lm-map-dl"
          onClick={handleDownloadMap}
          disabled={downloading}
          title="Baixar imagem do mapa"
          aria-label="Baixar imagem do mapa"
        >
          {downloading ? (
            <span className="la-row-spinner" aria-hidden />
          ) : (
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
              <path d="M8 2v8M4.5 6.5L8 10l3.5-3.5" />
              <path d="M3 12v1.5A1.5 1.5 0 0 0 4.5 15h7a1.5 1.5 0 0 0 1.5-1.5V12" />
            </svg>
          )}
          <span>Baixar</span>
        </button>
      )}
    </span>
  )

  return (
    <div className="lm-page">
      <header className="lm-header">
        <h1 className="lm-title">Mapa ao Vivo</h1>
        {hasCampaigns && !isLoading && !isError && (
          <span className="lm-live">
            <span className="lm-live-dot" />
            ao vivo · atualiza a cada 20s
            {isFetching && <span className="lm-refreshing" aria-label="atualizando" />}
          </span>
        )}
      </header>

      {/* Barra de filtros em passos — mesma das telas de Veiculação.
          Cadeia real: sem cliente o backend não tem o que listar, então o
          passo 2 nasce bloqueado. Quem enxerga um cliente só já entra com o
          passo 1 concluído (chip travado). */}
      <div className="flow-filters flow-filters--auto lm-flow">
        <div className={`flow-filter ${
          clientId ? 'flow-filter--done' :
          isAdmin ? 'flow-filter--active' : 'flow-filter--optional'
        }`}>
          <label className="flow-filter-label" htmlFor="lm-client">
            <span className="flow-filter-label-step">1</span>
            Cliente
            {/* Agência (carteira 2+) pode ver a carteira inteira: aqui o
                cliente é recorte, não pré-requisito. Só o admin é obrigado. */}
            {canPickClient && !isAdmin && !clientId && (
              <span className="flow-filter-tag">opcional</span>
            )}
          </label>
          {canPickClient ? (
            <RSelect
              inputId="lm-client"
              options={clientOpts}
              value={clientOpts.find(o => o.value === clientId) || null}
              onChange={opt => { setPickedClientId(opt?.value || null); setCampaignIds([]) }}
              placeholder="Selecione…"
              isLoading={clientsQ.isPending}
              isClearable
              formatOptionLabel={formatClientOption}
            />
          ) : (
            <div className="flow-locked-chip">
              <ClientMiniAvatar
                name={ownClient?.name || user?.client_name || user?.email || ''}
                logo={ownClient?.logo_url}
                size={22}
              />
              <span>{ownClient?.name || user?.client_name || user?.email || 'Sua conta'}</span>
            </div>
          )}
        </div>

        <div className={`flow-filter ${
          isAdmin && !clientId ? 'flow-filter--locked' :
          hasCampaigns ? 'flow-filter--done' : 'flow-filter--active'
        }`}>
          <label className="flow-filter-label" htmlFor="lm-campaign">
            <span className="flow-filter-label-step">2</span>
            Campanhas
            {!(isAdmin && !clientId) && campOpts.length > 0 && (
              <span className="flow-filter-hint">
                {hasCampaigns
                  ? `${campaignIds.length} de ${campOpts.length}`
                  : campOpts.length === 1 ? '1 disponível' : `${campOpts.length} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="lm-campaign"
            isMulti
            options={campOpts}
            value={campOpts.filter(o => campaignIds.includes(o.value))}
            onChange={opts => setCampaignIds((opts || []).map(o => o.value))}
            placeholder={
              isAdmin && !clientId ? 'Escolha um cliente primeiro' :
              campaignsQ.isPending ? 'Carregando…' :
              campOpts.length === 0 ? 'Nenhuma campanha ao vivo' :
              'Selecione 1 ou mais…'
            }
            isDisabled={isAdmin && !clientId}
            isLoading={campaignsQ.isPending}
            closeMenuOnSelect={false}
            noOptionsMessage={() => 'Nenhuma campanha ao vivo'}
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
              <div className="la-empty">
                {multiCampaign
                  ? 'Nenhuma veiculação recente nestas campanhas.'
                  : 'Nenhuma veiculação recente nesta campanha.'}
              </div>
            ) : (
              <div className="la-list la-stagger">
                {detections.map(d => {
                  // Com multi-atribuição a MESMA tocada pode projetar em duas
                  // campanhas selecionadas e vir duas vezes (uma por campanha,
                  // cada uma rotulada). A chave — e o "quem está tocando" —
                  // precisa então da campanha junto, senão as duas linhas
                  // colidem e tocam ao mesmo tempo.
                  const rowKey = d.campaign_id ? `${d.id}:${d.campaign_id}` : d.id
                  return (
                    <div
                      key={rowKey}
                      onMouseEnter={() => setHoverStationId(d.station_id)}
                      onMouseLeave={() => setHoverStationId(cur => (cur === d.station_id ? null : cur))}
                    >
                      <LiveAiringRow
                        detection={d}
                        isPlaying={playingId === rowKey}
                        onPlayRequest={() => setPlayingId(rowKey)}
                        onPlayClose={() => setPlayingId(null)}
                      />
                    </div>
                  )
                })}
              </div>
            )
          }
          map={
            isLoading ? (
              <MapSkeleton />
            ) : stations.length === 0 ? (
              <div className="lm-map-empty" ref={mapRef}>
                <BrazilMap stations={[]} />
                <span className="lm-map-empty-msg">As emissoras desta campanha ainda não têm localização cadastrada.</span>
              </div>
            ) : (
              <div className="lm-map-capture" ref={mapRef}>
                <CoverageMap
                  // Remonta ao trocar a seleção: zera foco, tour e câmera de
                  // uma vez. É o idioma React pra "resetar estado quando a
                  // identidade da entrada muda" — mais confiável que sincronizar
                  // três estados na mão.
                  key={campaignIds.join(',')}
                  stations={stations}
                  coverage={coverage}
                  coverageLoading={coverageQ.isPending}
                  highlightId={hoverStationId}
                  onHoverStation={handleMapHover}
                  recentDetections={detections}
                />
              </div>
            )
          }
        />
      )}
    </div>
  )
}
