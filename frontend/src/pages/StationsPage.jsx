import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useStations, useCreateStation } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import RSelect from '../components/RSelect'
import { useRadioPlayer } from '../contexts/RadioPlayerContext'
import { useAuth } from '../contexts/AuthContext'

const BAND_OPTIONS = [
  { value: 'FM', label: 'FM' },
  { value: 'AM', label: 'AM' },
]

const STATUS_META = {
  active:      { label: 'Ativa',       cls: 'badge-success' },
  calibrating: { label: 'Calibrando',  cls: 'badge-warning' },
  paused:      { label: 'Pausada',     cls: 'badge-neutral' },
  error:       { label: 'Erro',        cls: 'badge-danger'  },
}

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

function CityIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 14V7l3-2 3 2v7" />
      <path d="M8 14V4l3-2 3 2v10" />
      <path d="M1 14h14" />
      <path d="M4 10v0M4 12v0M10.5 7v0M10.5 9.5v0M10.5 12v0" />
    </svg>
  )
}
function CityPinIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 14V8l2.5-1.5L7 8v6" />
      <path d="M7 14V4.5L10 3l3 1.5V14" />
      <path d="M1 14h14" />
    </svg>
  )
}

// Normaliza pra match accent-insensitive client-side (mesma regra do
// helper utils/search.js: NFD + remove combining marks).
function normalize(s) {
  return (s ?? '')
    .toString()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .trim()
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
  const { isAdmin } = useAuth()

  // Search dropdown:
  //   - `searchInput`: o que o usuário digita no campo
  //   - `searchInputDebounced`: usado pra alimentar o autocomplete (limita
  //     hits no backend)
  //   - `selectedOption`: opção clicada — só ela filtra a lista renderizada.
  //   Sem clique, sem filtro (mostra catálogo inteiro).
  const [searchInput, setSearchInput] = useState('')
  const [searchInputDebounced, setSearchInputDebounced] = useState('')
  const [selectedOption, setSelectedOption] = useState(null)

  useEffect(() => {
    const t = setTimeout(() => setSearchInputDebounced(searchInput), 250)
    return () => clearTimeout(t)
  }, [searchInput])

  const [band, setBand] = useState('')
  const [page, setPage] = useState(1)
  useEffect(() => { setPage(1) }, [band])

  // Autocomplete por CIDADE: busca emissoras que casam com a string digitada
  // (server-side faz match amplo: name/city/state/band/freq), e a gente filtra
  // pra deduplicar por cidade e mostrar só as cidades cujo nome casa com o
  // input. Limit alto pra pegar várias cidades distintas num único hit.
  const { data: suggestData, isFetching: suggestLoading } = useStations({
    q: searchInputDebounced,
    band,
    page: 1,
    limit: 50,
    enabled: searchInputDebounced.trim().length >= 2,
  })

  // Reduz pra lista de cidades distintas, em ordem alfabética, contando
  // quantas emissoras a cidade tem nos resultados (apenas as que vieram da
  // página atual — número aproximado, suficiente como pista).
  const normalizedQ = normalize(searchInputDebounced)
  const cityOptions = (() => {
    const byKey = new Map()
    for (const st of suggestData?.data ?? []) {
      const city = (st.city ?? '').trim()
      if (!city) continue
      // Mantém só cidades cujo nome casa com a busca (accent-insensitive).
      // Sem esse filtro, o backend devolveria também stations cujo match foi
      // por name/band/freq, poluindo as sugestões de cidade.
      if (normalizedQ && !normalize(city).includes(normalizedQ)) continue
      const key = `${city}|${st.state ?? ''}`
      const cur = byKey.get(key)
      if (cur) cur.count += 1
      else byKey.set(key, { city, state: st.state ?? null, count: 1 })
    }
    return [...byKey.values()]
      .sort((a, b) => a.city.localeCompare(b.city, 'pt-BR'))
      .slice(0, 10)
      .map(c => ({
        value: c.state ? `${c.city}|${c.state}` : c.city,
        label: c.state ? `${c.city}/${c.state}` : c.city,
        city: c.city,
        state: c.state,
        count: c.count,
      }))
  })()

  // Filtro real aplicado à lista: nome da cidade escolhida.
  // Quando nada selecionado → mostra todas (q vazio). Passar só a cidade
  // como q usa o ILIKE amplo do backend; cidade casa primeiro com o campo
  // `city`. Edge case raro: station com a cidade no `name` apareceria mesmo
  // estando em outra cidade — aceitável até existir filtro `city` no backend.
  const activeQ = selectedOption?.city ?? ''

  const { data, isLoading } = useStations({ q: activeQ, band, page, limit: LIMIT })
  const stations = data?.data ?? []
  const total    = data?.total ?? 0
  const pages    = data?.pages ?? 1

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
          <RSelect
            inputId="stations-search"
            placeholder="Buscar por cidade…"
            options={cityOptions}
            value={selectedOption}
            onChange={opt => {
              setSelectedOption(opt)
              setPage(1)
              if (!opt) {
                setSearchInput('')
                setSearchInputDebounced('')
              }
            }}
            inputValue={searchInput}
            onInputChange={(val, meta) => {
              if (meta.action === 'input-change') setSearchInput(val)
            }}
            isClearable
            isLoading={suggestLoading && searchInputDebounced.trim().length >= 2}
            filterOption={null}
            loadingMessage={() => 'Buscando cidades…'}
            noOptionsMessage={() => {
              const q = searchInput.trim()
              if (q.length === 0) return 'Digite o nome de uma cidade'
              if (q.length < 2)  return 'Digite ao menos 2 caracteres'
              if (suggestLoading) return 'Buscando cidades…'
              return `Nenhuma cidade encontrada para "${q}"`
            }}
            components={{
              DropdownIndicator: () => (
                <div style={{ paddingRight: 10, color: 'var(--c-text-3)', display: 'flex' }}>
                  <CityIcon />
                </div>
              ),
            }}
            formatOptionLabel={(opt, { context }) => {
              if (context === 'value') {
                return (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ color: 'var(--c-text-3)', display: 'inline-flex' }}><CityPinIcon /></span>
                    {opt.label}
                  </span>
                )
              }
              return (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ color: 'var(--c-text-3)', display: 'inline-flex' }}><CityPinIcon /></span>
                  <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.25 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--c-text)' }}>
                      {opt.city}
                      {opt.state ? <span style={{ color: 'var(--c-text-3)', fontWeight: 400 }}>{` / ${opt.state}`}</span> : null}
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
                      {opt.count} emissora{opt.count === 1 ? '' : 's'}
                    </div>
                  </div>
                </div>
              )
            }}
          />
        </div>

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
            {activeQ
              ? `Nenhum resultado para "${activeQ}"`
              : 'Nenhuma emissora cadastrada.'}
          </p>
          {!activeQ && isAdmin && (
            <button className="btn btn-primary" style={{ marginTop: 16 }} onClick={() => setCreating(true)}>
              <PlusIcon /> Adicionar emissora
            </button>
          )}
        </div>
      ) : (
        <div className="stations-list">
          {stations.map(st => {
            const statusMeta = STATUS_META[st.monitoring_status] ?? { label: st.monitoring_status, cls: 'badge-neutral' }
            const cats       = st.meta?.categories ?? []
            const pmm        = formatPMM(st.pmm)
            const pop        = formatPop(st.meta?.total_population)
            const covStates  = st.meta?.coverage_states?.length > 0
              ? `${st.meta.coverage_states.length} estado${st.meta.coverage_states.length > 1 ? 's' : ''}`
              : null
            const domain     = streamDomain(st.stream_url)
            const loc        = [st.city, st.state].filter(Boolean).join('/')

            return (
              <div key={st.id} className="station-row">
                <StationAvatar station={st} size={44} />

                <div className="station-row-main">
                  <div className="station-row-name">{st.name}</div>
                  <div className="station-row-sub">
                    {st.frequency_mhz != null ? `${st.frequency_mhz} ` : ''}{st.band}
                    {loc ? <><span className="station-row-dot">·</span><PinIcon />{loc}</> : null}
                  </div>
                </div>

                <div className="station-row-cats">
                  {cats.slice(0, 3).map(c => (
                    <span key={c} className="category-tag">{c}</span>
                  ))}
                  {cats.length > 3 && (
                    <span className="category-tag category-tag-more">+{cats.length - 3}</span>
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
                      onClick={() => toggleStation({
                        url: st.stream_url,
                        name: st.name,
                        logo: st.logo_url ?? null,
                      })}
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
                      onClick={() => navigate(`/stations/${st.id}/edit`)}
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
