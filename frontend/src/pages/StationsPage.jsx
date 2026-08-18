import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useStations, useCreateStation, useClients } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import StationDetailModal from '../components/StationDetailModal'
import StationSearch, { selectionLabel } from '../components/StationSearch'
import ClientAvatar from '../components/ClientAvatar'
import RSelect from '../components/RSelect'
import { useRadioPlayer } from '../contexts/RadioPlayerContext'
import { useAuth } from '../contexts/AuthContext'
import { statusMetaFor } from '../utils/stationStatus'

const BAND_OPTIONS = [
  { value: 'FM', label: 'FM' },
  { value: 'AM', label: 'AM' },
]

function formatPMM(pmm) {
  if (pmm == null) return null
  return new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 }).format(pmm)
}

function formatPop(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 }).format(n) + ' hab.'
}

function streamDomain(url) {
  if (!url) return null
  try { return new URL(url).hostname.replace(/^www\./, '') } catch { return null }
}

// Formata a data de início da campanha programada como DD/MM.
//
// Fatia a string em vez de passar por `new Date`: o backend manda uma DATE
// serializada como "2026-09-01T00:00:00Z", e construir um Date com isso no
// fuso do Brasil (UTC-3) devolve 31/08. É um dia inteiro de erro num rótulo
// que o cliente lê como "quando minha campanha começa".
function formatStartDate(iso) {
  if (!iso) return null
  const [y, m, d] = String(iso).slice(0, 10).split('-')
  return y && m && d ? `${d}/${m}` : null
}

function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M7 2v10M2 7h10" />
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
function PinIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 1C4.343 1 3 2.343 3 4c0 2.5 3 7 3 7s3-4.5 3-7c0-1.657-1.343-3-3-3z" /><circle cx="6" cy="4" r="1" />
    </svg>
  )
}
function GlobeIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
      <circle cx="6" cy="6" r="5" />
      <path d="M1 6h10M6 1c-1.5 1.5-2 3-2 5s.5 3.5 2 5M6 1c1.5 1.5 2 3 2 5s-.5 3.5-2 5" />
    </svg>
  )
}

const LIMIT = 25

export default function StationsPage() {
  const navigate = useNavigate()
  const { toggleStation, isStationPlaying } = useRadioPlayer()
  const { isAdmin, clientIds } = useAuth()

  // O que o usuário escolheu no campo de busca. União discriminada: emissora,
  // cidade, UF ou texto livre (Enter sem escolher nada). `null` = sem filtro.
  const [selection, setSelection] = useState(null)

  // Recorte por contrato. O CLIENTE abre já nas dele: cair num catálogo de
  // 7.500 emissoras das quais ~25 são suas responde uma pergunta que ele não
  // fez. O ADMIN abre no catálogo e escolhe o cliente quando quiser.
  //   null                  → catálogo inteiro
  //   { ids: [...], label } → só as contratadas por esses clientes
  const ownWallet = clientIds.length > 0 ? { ids: clientIds, label: 'minhas' } : null
  const [contract, setContract] = useState(() => (isAdmin ? null : ownWallet))

  const [band, setBand] = useState('')
  const [page, setPage] = useState(1)
  useEffect(() => { setPage(1) }, [band, selection, contract])

  // Cada tipo de seleção vira um filtro diferente no backend. A cidade usa o
  // filtro `city` exato em vez de `q`: passar o nome da cidade como busca ampla
  // traria de quebra emissoras de OUTRA cidade que tenham esse nome no `name`.
  const listFilter = (() => {
    if (!selection) return {}
    if (selection.kind === 'station') return { station_id: selection.id }
    if (selection.kind === 'city')    return { city: selection.city, state: selection.state ?? undefined }
    if (selection.kind === 'state')   return { state: selection.state }
    return { q: selection.q }
  })()

  const { data, isLoading } = useStations({
    ...listFilter,
    contracted_by: contract ? contract.ids.join(',') : undefined,
    band, page, limit: LIMIT,
  })

  // Seletor de cliente do admin. O cliente não carrega isso: ele não escolhe
  // cliente nenhum, e a rota é admin-only.
  const clientsQ = useClients({ enabled: isAdmin })
  const clientOpts = (clientsQ.data ?? []).map(c => ({
    value: c.id,
    label: c.name,
    logo: c.logo_url ?? null,
  }))

  // Logo + nome, como no seletor de cliente de /reports/airtime. Com ~110
  // clientes na lista, a marca é o que o admin reconhece antes de ler.
  function formatClientOption(opt, { context }) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
        <ClientAvatar client={{ name: opt.label, logo_url: opt.logo }} size={context === 'value' ? 18 : 22} />
        <span style={{
          fontWeight: 600, fontSize: 13, color: 'var(--c-text)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>{opt.label}</span>
      </div>
    )
  }
  const stations = data?.data ?? []
  const total    = data?.total ?? 0
  const pages    = data?.pages ?? 1

  // Ficha read-only da emissora. É o caminho pelo qual o usuário CLIENTE vê os
  // dados de cada emissora — a tela de edição é admin-only.
  const [detailStation, setDetailStation] = useState(null)

  // Create modal
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })
  const createStation = useCreateStation()

  function setF(k, v) { setForm(f => ({ ...f, [k]: v })) }

  async function handleCreate(e) {
    e.preventDefault()
    await createStation.mutateAsync({
      name: form.name,
      band: form.band,
      frequency_mhz: form.frequency_mhz !== '' ? Number(form.frequency_mhz) : null,
      city: form.city || null,
      state: form.state || null,
      stream_url: form.stream_url,
    })
    setCreating(false)
    setForm({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })
  }

  function buildPages(current, total) {
    if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1)
    const pages = []
    if (current <= 4) {
      pages.push(1, 2, 3, 4, 5, '…', total)
    } else if (current >= total - 3) {
      pages.push(1, '…', total - 4, total - 3, total - 2, total - 1, total)
    } else {
      pages.push(1, '…', current - 1, current, current + 1, '…', total)
    }
    return pages
  }

  return (
    <div>
      {/* Header */}
      <div className="page-header">
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
          <h2>Emissoras</h2>
          {total > 0 && (
            <span className="text-muted" style={{ fontSize: 13, fontWeight: 400 }}>
              {total.toLocaleString('pt-BR')}
              {contract ? ` contratada${total === 1 ? '' : 's'}${contract.label === 'minhas' ? '' : ` por ${contract.label}`}` : ''}
            </span>
          )}
        </div>
        {isAdmin && (
          <button className="btn btn-primary" onClick={() => setCreating(true)}>
            <PlusIcon /> Nova emissora
          </button>
        )}
      </div>

      {/* Filters */}
      <div className="stations-filters">
        <div className="stations-search-dropdown">
          <StationSearch value={selection} onChange={setSelection} band={band} />
        </div>

        {/* Recorte por contrato. Admin escolhe o cliente; cliente alterna
            entre as dele e o catálogo. Quem não é nem um nem outro (usuário
            sem carteira) não vê controle nenhum. */}
        {isAdmin ? (
          <div className="stations-contract-picker">
            <RSelect
              inputId="stations-contract"
              placeholder="Contratadas por…"
              options={clientOpts}
              value={contract ? clientOpts.find(o => o.value === contract.ids[0]) ?? null : null}
              onChange={opt => setContract(opt ? { ids: [opt.value], label: opt.label } : null)}
              formatOptionLabel={formatClientOption}
              isClearable
              isLoading={clientsQ.isLoading}
              noOptionsMessage={() => 'Nenhum cliente'}
            />
          </div>
        ) : ownWallet ? (
          <div className="stations-band-filter">
            <button
              className={`band-tab${contract ? ' active' : ''}`}
              onClick={() => setContract(ownWallet)}
            >
              Minhas
            </button>
            <button
              className={`band-tab${contract ? '' : ' active'}`}
              onClick={() => setContract(null)}
            >
              Todas
            </button>
          </div>
        ) : null}

        <div className="stations-band-filter">
          {['', 'FM', 'AM'].map(b => (
            <button
              key={b || 'all'}
              className={`band-tab${band === b ? ' active' : ''}`}
              onClick={() => setBand(b)}
            >
              {b || 'Todas'}
            </button>
          ))}
        </div>
      </div>

      {/* List */}
      {isLoading ? (
        <div className="stations-list">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="station-row">
              <div className="skeleton" style={{ width: 44, height: 44, borderRadius: 8, flexShrink: 0 }} />
              <div className="station-row-main">
                <div className="skeleton" style={{ height: 14, width: '55%', borderRadius: 4, marginBottom: 6 }} />
                <div className="skeleton" style={{ height: 11, width: '35%', borderRadius: 4 }} />
              </div>
              <div className="station-row-cats" style={{ gap: 4 }}>
                <div className="skeleton" style={{ height: 20, width: 60, borderRadius: 10 }} />
                <div className="skeleton" style={{ height: 20, width: 72, borderRadius: 10 }} />
              </div>
              <div className="station-row-meta">
                <div className="skeleton" style={{ height: 11, width: 70, borderRadius: 4 }} />
              </div>
              <div className="station-row-actions">
                <div className="skeleton" style={{ height: 20, width: 54, borderRadius: 10 }} />
              </div>
            </div>
          ))}
        </div>
      ) : stations.length === 0 ? (
        <div className="card" style={{ padding: '48px 24px', textAlign: 'center' }}>
          <p className="text-muted" style={{ fontSize: 15 }}>
            {selection
              ? `Nenhum resultado para "${selectionLabel(selection)}"${contract ? ' entre as contratadas' : ''}`
              : contract
                ? 'Nenhuma emissora contratada no momento.'
                : 'Nenhuma emissora cadastrada.'}
          </p>
          {/* Lista vazia sem explicação vira chamado de suporte. Diz o que
              "contratada" significa aqui — só campanha vigente conta. */}
          {contract && !selection && (
            <p className="text-muted" style={{ fontSize: 13, marginTop: 8 }}>
              Só entram emissoras de campanhas em andamento ou já programadas.
              Campanhas encerradas não aparecem aqui.
            </p>
          )}
          {contract && (
            <button className="btn" style={{ marginTop: 16 }} onClick={() => setContract(null)}>
              Ver todas as emissoras
            </button>
          )}
          {!selection && !contract && isAdmin && (
            <button className="btn btn-primary" style={{ marginTop: 16 }} onClick={() => setCreating(true)}>
              <PlusIcon /> Adicionar emissora
            </button>
          )}
        </div>
      ) : (
        <div className="stations-list">
          {stations.map(st => {
            const statusMeta = statusMetaFor(st.monitoring_status)
            const cats       = st.meta?.categories ?? []
            const pmm        = formatPMM(st.pmm)
            const pop        = formatPop(st.meta?.total_population)
            const covStates  = st.meta?.coverage_states?.length > 0
              ? `${st.meta.coverage_states.length} estado${st.meta.coverage_states.length > 1 ? 's' : ''}`
              : null
            const domain     = streamDomain(st.stream_url)
            const loc        = [st.city, st.state].filter(Boolean).join('/')
            // Só existe quando a listagem foi escopada por cliente.
            const ct         = st.contract ?? null
            const startsAt   = formatStartDate(ct?.starts_at)

            return (
              <div
                key={st.id}
                className="station-row station-row-clickable"
                role="button"
                tabIndex={0}
                aria-label={`Ver detalhes de ${st.name}`}
                onClick={() => setDetailStation(st)}
                onKeyDown={e => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    setDetailStation(st)
                  }
                }}
              >
                <StationAvatar station={st} size={44} />

                <div className="station-row-main">
                  <div className="station-row-name">{st.name}</div>
                  <div className="station-row-sub">
                    {st.frequency_mhz != null ? `${st.frequency_mhz} ` : ''}{st.band}
                    {loc ? <><span className="station-row-dot">·</span><PinIcon />{loc}</> : null}
                  </div>
                </div>

                {/* No recorte por contrato, o vínculo ganha o espaço dos
                    gêneros: aqui a pergunta é "por que esta emissora é minha",
                    não que estilo ela toca. Os gêneros cedem lugar, não somem. */}
                <div className="station-row-cats">
                  {ct && (
                    <span className={`contract-tag${ct.on_air ? ' contract-tag-onair' : ' contract-tag-soon'}`}>
                      {ct.on_air
                        ? 'veiculando'
                        : startsAt ? `a partir de ${startsAt}` : 'programada'}
                    </span>
                  )}
                  {ct && (
                    <span className="contract-tag">
                      {ct.campaigns} campanha{ct.campaigns === 1 ? '' : 's'}
                    </span>
                  )}
                  {cats.slice(0, ct ? 1 : 3).map(c => (
                    <span key={c} className="category-tag">{c}</span>
                  ))}
                  {cats.length > (ct ? 1 : 3) && (
                    <span className="category-tag category-tag-more">+{cats.length - (ct ? 1 : 3)}</span>
                  )}
                </div>

                <div className="station-row-meta">
                  {pop && <span>{pop}</span>}
                  {covStates && <span>{covStates}</span>}
                  {!pop && !covStates && domain && (
                    <span className="station-row-domain"><GlobeIcon />{domain}</span>
                  )}
                </div>

                <div className="station-row-actions">
                  <span className={`badge ${statusMeta.cls}`}>{statusMeta.label}</span>
                  {pmm && <span className="station-pmm">PMM {pmm}</span>}
                  {st.stream_url && (
                    <button
                      className={
                        'station-listen-btn' +
                        (isStationPlaying(st.stream_url) ? ' is-playing' : '')
                      }
                      title={isStationPlaying(st.stream_url) ? 'Parar' : 'Ouvir ao vivo'}
                      aria-label={isStationPlaying(st.stream_url) ? 'Parar stream' : `Ouvir ${st.name}`}
                      onClick={e => {
                        e.stopPropagation()
                        toggleStation({
                          url: st.stream_url,
                          name: st.name,
                          logo: st.logo_url ?? null,
                        })
                      }}
                    >
                      {isStationPlaying(st.stream_url) ? (
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
                      title="Editar"
                      onClick={e => {
                        e.stopPropagation()
                        navigate(`/stations/${st.id}/edit`)
                      }}
                    >
                      <EditIcon />
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Pagination */}
      {pages > 1 && (
        <div className="pagination">
          <button className="pagination-btn" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>
            ← Anterior
          </button>
          <div className="pagination-pages">
            {buildPages(page, pages).map((pg, i) =>
              pg === '…'
                ? <span key={`e-${i}`} className="pagination-ellipsis">…</span>
                : <button
                    key={`p-${pg}`}
                    className={`pagination-page${pg === page ? ' active' : ''}`}
                    onClick={() => setPage(pg)}
                  >{pg}</button>
            )}
          </div>
          <button className="pagination-btn" disabled={page >= pages} onClick={() => setPage(p => p + 1)}>
            Próxima →
          </button>
        </div>
      )}

      {/* Ficha da emissora (read-only, admin + cliente) */}
      {detailStation && (
        <StationDetailModal
          station={detailStation}
          onClose={() => setDetailStation(null)}
        />
      )}

      {/* Create modal */}
      {creating && isAdmin && (
        <div className="modal-overlay" onClick={() => setCreating(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <div className="modal-header">
              <h3>Nova emissora</h3>
              <button className="modal-close" onClick={() => setCreating(false)}>✕</button>
            </div>
            <form onSubmit={handleCreate} className="modal-body">
              <div className="field">
                <label>Nome *</label>
                <input className="input" value={form.name} onChange={e => setF('name', e.target.value)} required />
              </div>
              <div className="cluster" style={{ alignItems: 'flex-end' }}>
                <div className="field" style={{ flex: '0 0 90px' }}>
                  <label>Banda *</label>
                  <RSelect
                    options={BAND_OPTIONS}
                    value={BAND_OPTIONS.find(o => o.value === form.band) ?? null}
                    onChange={opt => setF('band', opt?.value ?? 'FM')}
                    isSearchable={false}
                  />
                </div>
                <div className="field" style={{ flex: 1 }}>
                  <label>Frequência</label>
                  <input className="input" type="number" step="0.1" placeholder="100.5" value={form.frequency_mhz} onChange={e => setF('frequency_mhz', e.target.value)} />
                </div>
              </div>
              <div className="cluster" style={{ alignItems: 'flex-end' }}>
                <div className="field" style={{ flex: 1 }}>
                  <label>Cidade</label>
                  <input className="input" value={form.city} onChange={e => setF('city', e.target.value)} />
                </div>
                <div className="field" style={{ flex: '0 0 72px' }}>
                  <label>UF</label>
                  <input className="input" maxLength={2} placeholder="SP" value={form.state} onChange={e => setF('state', e.target.value.toUpperCase())} />
                </div>
              </div>
              <div className="field">
                <label>URL do stream *</label>
                <input className="input" type="url" value={form.stream_url} onChange={e => setF('stream_url', e.target.value)} required />
              </div>
              <div className="modal-footer">
                <button type="button" className="btn btn-secondary" onClick={() => setCreating(false)}>Cancelar</button>
                <button type="submit" className="btn btn-primary" disabled={createStation.isPending}>
                  {createStation.isPending ? 'Salvando…' : 'Criar emissora'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
