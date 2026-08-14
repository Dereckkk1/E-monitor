import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import StationAvatar from './StationAvatar'
import { useRadioPlayer } from '../contexts/RadioPlayerContext'
import { useAuth } from '../contexts/AuthContext'
import { statusMetaFor } from '../utils/stationStatus'
import './StationDetailModal.css'

const CITIES_PREVIEW = 12

function fmtInt(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR').format(n)
}

function fmtPct(n) {
  if (n == null || n === 0) return null
  return `${new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 1 }).format(n)}%`
}

function streamDomain(url) {
  if (!url) return null
  try { return new URL(url).hostname.replace(/^www\./, '') } catch { return null }
}

function normalizeHref(url) {
  if (!url) return null
  return /^https?:\/\//i.test(url) ? url : `https://${url}`
}

function CloseIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
      <path d="M3 3l8 8M11 3l-8 8" />
    </svg>
  )
}
function EditIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9.5 2.5l2 2L4 12H2v-2L9.5 2.5z" />
    </svg>
  )
}

function Section({ title, children }) {
  return (
    <div>
      <h4 className="sd-section-title">{title}</h4>
      {children}
    </div>
  )
}

function Kpi({ label, value }) {
  return (
    <div className="sd-kpi">
      <div className="sd-kpi-label">{label}</div>
      <div className="sd-kpi-value">{value}</div>
    </div>
  )
}

function Def({ label, children }) {
  return (
    <div>
      <div className="sd-def-label">{label}</div>
      <div className="sd-def-value">{children}</div>
    </div>
  )
}

function BarGroup({ label, rows }) {
  const shown = rows.filter(r => r.value != null && r.value > 0)
  if (shown.length === 0) return null
  return (
    <div>
      <div className="sd-bar-group-label">{label}</div>
      {shown.map(r => (
        <div key={r.label} className="sd-bar-row">
          <span className="sd-bar-label">{r.label}</span>
          <div className="sd-bar-track">
            <div className="sd-bar-fill" style={{ width: `${Math.min(100, r.value)}%` }} />
          </div>
          <span className="sd-bar-value">{fmtPct(r.value)}</span>
        </div>
      ))}
    </div>
  )
}

/**
 * Ficha read-only da emissora. Abre a partir da linha em /stations e é o
 * caminho pelo qual o usuário CLIENTE enxerga os dados da emissora — antes
 * disso só o admin via as infos, e apenas pela tela de edição.
 *
 * Renderiza a partir do objeto que a listagem já carregou (`GET /stations`
 * devolve `metadata` inteiro), então não dispara request adicional.
 *
 * Campos sensíveis (stream, e-mail comercial, CNPJ/razão social) ficam
 * restritos ao admin; o cliente vê o perfil editorial/comercial da emissora.
 */
export default function StationDetailModal({ station, onClose }) {
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const { toggleStation, isStationPlaying } = useRadioPlayer()
  const [showAllCities, setShowAllCities] = useState(false)

  useEffect(() => {
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  if (!station) return null

  const meta       = station.meta ?? {}
  const ap         = meta.audience_profile ?? {}
  const statusMeta = statusMetaFor(station.monitoring_status)
  const loc        = [station.city, station.state].filter(Boolean).join('/')
  const cats       = meta.categories ?? []
  const covStates  = meta.coverage_states ?? []
  const covCities  = meta.coverage_cities ?? []
  const playing    = station.stream_url && isStationPlaying(station.stream_url)

  const kpis = [
    station.pmm != null       && { label: 'PMM',        value: fmtInt(station.pmm) },
    meta.total_population     && { label: 'População',  value: fmtInt(meta.total_population) },
    covStates.length > 0      && { label: 'Estados',    value: covStates.length },
    covCities.length > 0      && { label: 'Cidades',    value: covCities.length },
    meta.foundation_year      && { label: 'Fundação',   value: meta.foundation_year },
  ].filter(Boolean)

  const genderRows = [
    { label: 'Masculino', value: ap.gender?.male },
    { label: 'Feminino',  value: ap.gender?.female },
  ]
  const ageRows = [
    { label: '18 a 24 anos', value: ap.ageRanges?.range18to24 },
    { label: '25 a 49 anos', value: ap.ageRanges?.range25to49 },
    { label: 'Acima de 50',  value: ap.ageRanges?.range50plus },
  ]
  const classRows = [
    { label: 'Classe A/B', value: ap.socialClass?.classeAB },
    { label: 'Classe C',   value: ap.socialClass?.classeC },
    { label: 'Classe D/E', value: ap.socialClass?.classeDE },
  ]
  const hasAudience = [...genderRows, ...ageRows, ...classRows].some(r => r.value > 0)

  const website = normalizeHref(meta.website)
  const hasContact = website || (isAdmin && (meta.commercial_email || station.stream_url))
  const hasTech = isAdmin && (meta.company_name || meta.fantasy_name || meta.cnpj
    || meta.power_watts != null || meta.antenna_class)

  const isEmpty = kpis.length === 0 && cats.length === 0 && !hasAudience
    && covStates.length === 0 && covCities.length === 0 && !hasContact && !hasTech

  const cities = showAllCities ? covCities : covCities.slice(0, CITIES_PREVIEW)

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div
        className="sd-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`Detalhes de ${station.name}`}
        onClick={e => e.stopPropagation()}
      >
        {/* ── Header ── */}
        <div className="sd-header">
          <StationAvatar station={station} size={48} />
          <div className="sd-header-main">
            <h3 className="sd-header-name">{station.name}</h3>
            <div className="sd-header-sub">
              {station.frequency_mhz != null ? `${station.frequency_mhz} ` : ''}{station.band}
              {loc ? <span>· {loc}</span> : null}
            </div>
            <div className="sd-header-badges">
              <span className={`badge ${statusMeta.cls}`}>{statusMeta.label}</span>
              {cats.slice(0, 3).map(c => <span key={c} className="category-tag">{c}</span>)}
              {cats.length > 3 && <span className="category-tag category-tag-more">+{cats.length - 3}</span>}
            </div>
          </div>
          <div className="sd-header-actions">
            {station.stream_url && (
              <button
                className={'station-listen-btn' + (playing ? ' is-playing' : '')}
                title={playing ? 'Parar' : 'Ouvir ao vivo'}
                aria-label={playing ? 'Parar stream' : `Ouvir ${station.name}`}
                onClick={() => toggleStation({
                  url: station.stream_url,
                  name: station.name,
                  logo: station.logo_url ?? null,
                })}
              >
                {playing ? (
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
                    <rect x="4" y="3" width="3" height="10" rx="0.5" />
                    <rect x="9" y="3" width="3" height="10" rx="0.5" />
                  </svg>
                ) : (
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
                    <path d="M4.5 2.5v11l9-5.5z" />
                  </svg>
                )}
              </button>
            )}
            {isAdmin && (
              <button
                className="btn-icon"
                title="Editar emissora"
                onClick={() => navigate(`/stations/${station.id}/edit`)}
              >
                <EditIcon />
              </button>
            )}
            <button className="btn-icon" title="Fechar" onClick={onClose}>
              <CloseIcon />
            </button>
          </div>
        </div>

        {/* ── Body ── */}
        <div className="sd-body">
          {isEmpty && (
            <p className="sd-empty">Sem informações adicionais cadastradas para esta emissora.</p>
          )}

          {kpis.length > 0 && (
            <div className="sd-kpis">
              {kpis.map(k => <Kpi key={k.label} label={k.label} value={k.value} />)}
            </div>
          )}

          {cats.length > 0 && (
            <Section title="Categorias">
              <div className="sd-chips">
                {cats.map(c => <span key={c} className="category-tag">{c}</span>)}
              </div>
            </Section>
          )}

          {hasAudience && (
            <Section title="Perfil de audiência">
              <div className="sd-bars">
                <BarGroup label="Gênero" rows={genderRows} />
                <BarGroup label="Faixa etária" rows={ageRows} />
                <BarGroup label="Classe social" rows={classRows} />
              </div>
            </Section>
          )}

          {(covStates.length > 0 || covCities.length > 0) && (
            <Section title="Cobertura">
              {covStates.length > 0 && (
                <div className="sd-chips" style={{ marginBottom: covCities.length > 0 ? 12 : 0 }}>
                  {covStates.map(s => <span key={s} className="category-tag">{s}</span>)}
                </div>
              )}
              {covCities.length > 0 && (
                <>
                  <div className="sd-chips">
                    {cities.map(c => <span key={c} className="category-tag">{c}</span>)}
                  </div>
                  {covCities.length > CITIES_PREVIEW && (
                    <button className="sd-more-btn" onClick={() => setShowAllCities(v => !v)}>
                      {showAllCities
                        ? 'Mostrar menos'
                        : `Mostrar todas as ${covCities.length} cidades`}
                    </button>
                  )}
                </>
              )}
            </Section>
          )}

          {hasContact && (
            <Section title="Contato">
              <div className="sd-defs">
                {website && (
                  <Def label="Website">
                    <a href={website} target="_blank" rel="noreferrer noopener">{meta.website}</a>
                  </Def>
                )}
                {isAdmin && meta.commercial_email && (
                  <Def label="E-mail comercial">
                    <a href={`mailto:${meta.commercial_email}`}>{meta.commercial_email}</a>
                  </Def>
                )}
                {isAdmin && station.stream_url && (
                  <Def label="Stream">{streamDomain(station.stream_url) ?? station.stream_url}</Def>
                )}
              </div>
            </Section>
          )}

          {hasTech && (
            <Section title="Dados cadastrais">
              <div className="sd-defs">
                {meta.company_name  && <Def label="Razão social">{meta.company_name}</Def>}
                {meta.fantasy_name  && <Def label="Nome fantasia">{meta.fantasy_name}</Def>}
                {meta.cnpj          && <Def label="CNPJ">{meta.cnpj}</Def>}
                {meta.power_watts != null && <Def label="Potência">{fmtInt(meta.power_watts)} W</Def>}
                {meta.antenna_class && <Def label="Classe de antena">{meta.antenna_class}</Def>}
              </div>
            </Section>
          )}

          {!isAdmin && !isEmpty && (
            <p className="sd-footnote">
              Estes dados são cadastrais e mantidos pela equipe E-monitor.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
