import { useState, useEffect } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useStation, useUpdateStation } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import RSelect from '../components/RSelect'
import CalibrationPanel from '../components/CalibrationPanel'

const BAND_OPTIONS = [
  { value: 'FM', label: 'FM' },
  { value: 'AM', label: 'AM' },
]

function BackIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
      <path d="M10 12l-4-4 4-4" />
    </svg>
  )
}

function Section({ title, children }) {
  return (
    <div className="edit-section">
      <h3 className="edit-section-title">{title}</h3>
      <div className="edit-section-body">{children}</div>
    </div>
  )
}

function Field({ label, hint, children }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </div>
  )
}

function PercentInput({ label, value, onChange }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1 }}>
      <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--c-text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>{label}</label>
      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <input
          className="input"
          type="number"
          min="0"
          max="100"
          step="0.1"
          value={value ?? ''}
          onChange={e => onChange(e.target.value === '' ? null : Number(e.target.value))}
          style={{ width: 72 }}
        />
        <span className="text-muted" style={{ fontSize: 13 }}>%</span>
      </div>
    </div>
  )
}

// ── Categories chip editor ────────────────────────────────────────────────────

const COMMON_CATEGORIES = [
  'Adulto', 'Adulto Contemporâneo', 'Hits', 'News', 'Notícias', 'Esportes',
  'Gospel', 'Sertanejo', 'Pagode', 'Reggae', 'Clássica', 'MPB', 'Rock',
  'Pop', 'Infantil', 'Universitário', 'Eclético', 'Regional',
]

function CategoriesEditor({ value, onChange }) {
  const [input, setInput] = useState('')

  function addCategory(cat) {
    const c = cat.trim()
    if (!c || value.includes(c)) return
    onChange([...value, c])
    setInput('')
  }

  function removeCategory(cat) {
    onChange(value.filter(c => c !== cat))
  }

  function handleKeyDown(e) {
    if (e.key === 'Enter') { e.preventDefault(); addCategory(input) }
  }

  const suggested = COMMON_CATEGORIES.filter(c => !value.includes(c))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div className="categories-editor">
        {value.map(c => (
          <button key={c} className="category-tag category-tag-removable" type="button" onClick={() => removeCategory(c)}>
            {c} <span style={{ opacity: 0.6, marginLeft: 2 }}>✕</span>
          </button>
        ))}
        <input
          className="categories-editor-input"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Adicionar categoria…"
        />
      </div>
      {suggested.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {suggested.slice(0, 12).map(c => (
            <button key={c} className="category-tag category-tag-suggestion" type="button" onClick={() => addCategory(c)}>
              + {c}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Sk({ w, h = 14, r, style }) {
  return (
    <div
      className="skeleton"
      style={{ width: w ?? '100%', height: h, borderRadius: r ?? 'var(--radius-sm)', flexShrink: 0, ...style }}
    />
  )
}

function SectionSk({ title, children }) {
  return (
    <div className="edit-section">
      <div className="edit-section-title">{title}</div>
      <div className="edit-section-body">{children}</div>
    </div>
  )
}

function FieldSk({ labelW }) {
  return (
    <div className="field">
      <Sk w={labelW ?? 90} h={11} />
      <Sk h={38} r="var(--radius-md)" />
    </div>
  )
}

function EditPageSkeleton() {
  return (
    <div>
      <div className="page-header" style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Sk w={30} h={30} r="var(--radius-md)" />
          <Sk w={36} h={36} r={8} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <Sk w={164} h={18} />
            <Sk w={110} h={11} />
          </div>
        </div>
        <div style={{ display: 'flex', gap: 10 }}>
          <Sk w={88} h={34} r="var(--radius-md)" />
          <Sk w={130} h={34} r="var(--radius-md)" />
        </div>
      </div>

      <div className="edit-layout">
        <div className="edit-main">
          <SectionSk title="Identificação">
            <FieldSk labelW={130} />
            <div className="cluster" style={{ alignItems: 'flex-end' }}>
              <div className="field" style={{ flex: '0 0 110px' }}>
                <Sk w={50} h={11} />
                <Sk h={38} r="var(--radius-md)" />
              </div>
              <div className="field" style={{ flex: 1 }}>
                <Sk w={110} h={11} />
                <Sk h={38} r="var(--radius-md)" />
              </div>
            </div>
            <FieldSk labelW={105} />
          </SectionSk>

          <SectionSk title="Localização e Transmissão">
            <div className="cluster" style={{ alignItems: 'flex-end' }}>
              <div className="field" style={{ flex: 1 }}>
                <Sk w={50} h={11} />
                <Sk h={38} r="var(--radius-md)" />
              </div>
              <div className="field" style={{ flex: '0 0 80px' }}>
                <Sk w={72} h={11} />
                <Sk h={38} r="var(--radius-md)" />
              </div>
            </div>
            <FieldSk labelW={100} />
          </SectionSk>

          <SectionSk title="Categorias">
            <Sk h={44} r="var(--radius-md)" />
          </SectionSk>

          <SectionSk title="Perfil de Audiência">
            <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
              <div>
                <Sk w={55} h={11} style={{ marginBottom: 8 }} />
                <div style={{ display: 'flex', gap: 16 }}>
                  <FieldSk labelW={72} />
                  <FieldSk labelW={60} />
                </div>
              </div>
              <div>
                <Sk w={80} h={11} style={{ marginBottom: 8 }} />
                <div style={{ display: 'flex', gap: 16 }}>
                  <FieldSk labelW={84} />
                  <FieldSk labelW={84} />
                  <FieldSk labelW={84} />
                </div>
              </div>
              <div>
                <Sk w={92} h={11} style={{ marginBottom: 8 }} />
                <div style={{ display: 'flex', gap: 16 }}>
                  <FieldSk labelW={78} />
                  <FieldSk labelW={62} />
                  <FieldSk labelW={78} />
                </div>
              </div>
            </div>
          </SectionSk>

          <SectionSk title="Cobertura">
            <FieldSk labelW={185} />
            <FieldSk labelW={140} />
            <FieldSk labelW={160} />
          </SectionSk>
        </div>

        <aside className="edit-sidebar">
          <SectionSk title="Dados comerciais">
            <FieldSk labelW={40} />
            <FieldSk labelW={112} />
            <FieldSk labelW={65} />
            <FieldSk labelW={100} />
          </SectionSk>

          <SectionSk title="Pré-visualização">
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '4px 0' }}>
              <Sk w={48} h={48} r={8} />
              <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 6 }}>
                <Sk w="72%" h={14} />
                <Sk w="52%" h={11} />
                <div style={{ display: 'flex', gap: 4, marginTop: 4 }}>
                  <Sk w={52} h={20} r="var(--radius-full)" />
                  <Sk w={46} h={20} r="var(--radius-full)" />
                </div>
              </div>
            </div>
          </SectionSk>
        </aside>
      </div>
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export default function StationEditPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { data: station, isLoading, error } = useStation(id)
  const updateStation = useUpdateStation()

  const [form, setForm] = useState(null)
  const [saved, setSaved] = useState(false)

  // Populate form when station loads
  useEffect(() => {
    if (!station) return
    const m = station.meta ?? {}
    const ap = m.audience_profile ?? {}
    setForm({
      // Identificação
      name: station.name ?? '',
      band: station.band ?? 'FM',
      frequency_mhz: station.frequency_mhz ?? '',
      logo_url: station.logo_url ?? '',
      // Localização & transmissão
      city: station.city ?? '',
      state: station.state ?? '',
      stream_url: station.stream_url ?? '',
      // PMM
      pmm: station.pmm ?? '',
      // Audiência
      categories: m.categories ?? [],
      gender_male: ap.gender?.male ?? '',
      gender_female: ap.gender?.female ?? '',
      age_18_24: ap.ageRanges?.range18to24 ?? '',
      age_25_49: ap.ageRanges?.range25to49 ?? '',
      age_50_plus: ap.ageRanges?.range50plus ?? '',
      age_range_legado: ap.ageRangeLegado ?? ap.ageRange ?? '',
      classe_ab: ap.socialClass?.classeAB ?? '',
      classe_c: ap.socialClass?.classeC ?? '',
      classe_de: ap.socialClass?.classeDE ?? '',
      // Cobertura
      coverage_states: (m.coverage_states ?? []).join(', '),
      coverage_cities: (m.coverage_cities ?? []).join('\n'),
      total_population: m.total_population ?? '',
      // Outros
      website: m.website ?? '',
      commercial_email: m.commercial_email ?? '',
      foundation_year: m.foundation_year ?? '',
    })
  }, [station])

  function setF(k, v) { setForm(f => ({ ...f, [k]: v })) }

  async function handleSubmit(e) {
    e.preventDefault()
    setSaved(false)

    const meta = {
      categories: form.categories,
      audience_profile: {
        gender: {
          male: Number(form.gender_male) || 0,
          female: Number(form.gender_female) || 0,
        },
        ageRanges: {
          range18to24: Number(form.age_18_24)   || 0,
          range25to49: Number(form.age_25_49)   || 0,
          range50plus: Number(form.age_50_plus) || 0,
        },
        ...(form.age_range_legado && { ageRangeLegado: form.age_range_legado }),
        socialClass: {
          classeAB: Number(form.classe_ab) || 0,
          classeC:  Number(form.classe_c)  || 0,
          classeDE: Number(form.classe_de) || 0,
        },
      },
      coverage_states: form.coverage_states.split(',').map(s => s.trim()).filter(Boolean),
      coverage_cities: form.coverage_cities.split('\n').map(s => s.trim()).filter(Boolean),
      total_population: form.total_population !== '' ? Number(form.total_population) : null,
      website: form.website || null,
      commercial_email: form.commercial_email || null,
      foundation_year: form.foundation_year !== '' ? Number(form.foundation_year) : null,
      // Preserve other meta fields that we don't edit
      ...(station.meta?.social_media   && { social_media:   station.meta.social_media }),
      ...(station.meta?.power_watts    && { power_watts:    station.meta.power_watts }),
      ...(station.meta?.antenna_class  && { antenna_class:  station.meta.antenna_class }),
      ...(station.meta?.company_name   && { company_name:   station.meta.company_name }),
      ...(station.meta?.fantasy_name   && { fantasy_name:   station.meta.fantasy_name }),
      ...(station.meta?.cnpj           && { cnpj:           station.meta.cnpj }),
      ...(station.meta?.business_rules && { business_rules: station.meta.business_rules }),
    }

    await updateStation.mutateAsync({
      id,
      name: form.name,
      band: form.band,
      frequency_mhz: form.frequency_mhz !== '' ? Number(form.frequency_mhz) : null,
      city: form.city || null,
      state: form.state || null,
      stream_url: form.stream_url,
      logo_url: form.logo_url || null,
      pmm: form.pmm !== '' ? Number(form.pmm) : null,
      meta,
    })

    setSaved(true)
    setTimeout(() => setSaved(false), 3000)
  }

  if (isLoading) return <EditPageSkeleton />
  if (error || !station) return <p className="empty-state">Emissora não encontrada.</p>
  if (!form) return null

  return (
    <div>
      {/* ── Header ── */}
      <div className="page-header" style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <button className="btn-icon" onClick={() => navigate('/stations')} title="Voltar">
            <BackIcon />
          </button>
          <StationAvatar station={station} size={36} />
          <div>
            <h2 style={{ margin: 0 }}>{station.name}</h2>
            <span className="text-muted" style={{ fontSize: 12 }}>
              {station.band}{station.frequency_mhz ? ` · ${station.frequency_mhz} MHz` : ''}
              {station.city ? ` · ${station.city}` : ''}
              {station.state ? `/${station.state}` : ''}
            </span>
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          {saved && <span className="text-muted" style={{ fontSize: 13, color: 'var(--c-success)' }}>Salvo ✓</span>}
          <button type="button" className="btn btn-secondary" onClick={() => navigate('/stations')}>Cancelar</button>
          <button type="submit" form="edit-form" className="btn btn-primary" disabled={updateStation.isPending}>
            {updateStation.isPending ? 'Salvando…' : 'Salvar alterações'}
          </button>
        </div>
      </div>

      <form id="edit-form" onSubmit={handleSubmit}>
        <div className="edit-layout">

          {/* ── Coluna principal ── */}
          <div className="edit-main">

            <Section title="Identificação">
              <Field label="Nome da emissora *">
                <input className="input" value={form.name} onChange={e => setF('name', e.target.value)} required />
              </Field>
              <div className="cluster" style={{ alignItems: 'flex-end' }}>
                <div className="field" style={{ flex: '0 0 110px' }}>
                  <label>Banda *</label>
                  <RSelect
                    options={BAND_OPTIONS}
                    value={BAND_OPTIONS.find(o => o.value === form.band) ?? null}
                    onChange={opt => setF('band', opt?.value ?? 'FM')}
                    isSearchable={false}
                  />
                </div>
                <div className="field" style={{ flex: 1 }}>
                  <label>Frequência (MHz)</label>
                  <input className="input" type="number" step="0.1" min="0" value={form.frequency_mhz} onChange={e => setF('frequency_mhz', e.target.value)} placeholder="100.5" />
                </div>
              </div>
              <Field label="URL do logotipo" hint="Caminho no bucket GCS (preenchido automaticamente pelo import)">
                <input className="input" value={form.logo_url} onChange={e => setF('logo_url', e.target.value)} placeholder="Rádios 2_Images/…" />
              </Field>
            </Section>

            <Section title="Localização e Transmissão">
              <div className="cluster" style={{ alignItems: 'flex-end' }}>
                <div className="field" style={{ flex: 1 }}>
                  <label>Cidade</label>
                  <input className="input" value={form.city} onChange={e => setF('city', e.target.value)} />
                </div>
                <div className="field" style={{ flex: '0 0 80px' }}>
                  <label>Estado (UF)</label>
                  <input className="input" maxLength={2} value={form.state} onChange={e => setF('state', e.target.value.toUpperCase())} placeholder="SP" />
                </div>
              </div>
              <Field label="URL do stream *">
                <input className="input" type="url" value={form.stream_url} onChange={e => setF('stream_url', e.target.value)} required />
              </Field>
            </Section>

            <Section title="Categorias">
              <CategoriesEditor
                value={form.categories}
                onChange={v => setF('categories', v)}
              />
            </Section>

            <Section title="Perfil de Audiência">
              <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
                <div>
                  <label style={{ display: 'block', marginBottom: 8, fontSize: 12, fontWeight: 600, color: 'var(--c-text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Gênero</label>
                  <div style={{ display: 'flex', gap: 16 }}>
                    <PercentInput label="Masculino" value={form.gender_male} onChange={v => setF('gender_male', v)} />
                    <PercentInput label="Feminino" value={form.gender_female} onChange={v => setF('gender_female', v)} />
                  </div>
                </div>
                <div>
                  <label style={{ display: 'block', marginBottom: 8, fontSize: 12, fontWeight: 600, color: 'var(--c-text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Faixa etária</label>
                  <div style={{ display: 'flex', gap: 16 }}>
                    <PercentInput label="18 a 24 anos"   value={form.age_18_24}   onChange={v => setF('age_18_24',   v)} />
                    <PercentInput label="25 a 49 anos"   value={form.age_25_49}   onChange={v => setF('age_25_49',   v)} />
                    <PercentInput label="Acima de 50"    value={form.age_50_plus} onChange={v => setF('age_50_plus', v)} />
                  </div>
                  {(() => {
                    const sum = (Number(form.age_18_24) || 0) + (Number(form.age_25_49) || 0) + (Number(form.age_50_plus) || 0)
                    if (sum > 0 && Math.abs(sum - 100) > 0.05) {
                      return <span className="field-hint" style={{ marginTop: 6, color: 'var(--c-warning, #b8860b)' }}>Soma atual: {sum.toFixed(1)}% — esperado 100%</span>
                    }
                    return null
                  })()}
                  {form.age_range_legado && (
                    <span className="field-hint" style={{ marginTop: 6 }}>Faixa etária anterior preservada: <em>{form.age_range_legado}</em></span>
                  )}
                </div>
                <div>
                  <label style={{ display: 'block', marginBottom: 8, fontSize: 12, fontWeight: 600, color: 'var(--c-text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Classe social</label>
                  <div style={{ display: 'flex', gap: 16 }}>
                    <PercentInput label="Classe A/B" value={form.classe_ab} onChange={v => setF('classe_ab', v)} />
                    <PercentInput label="Classe C"   value={form.classe_c}  onChange={v => setF('classe_c',  v)} />
                    <PercentInput label="Classe D/E" value={form.classe_de} onChange={v => setF('classe_de', v)} />
                  </div>
                </div>
              </div>
            </Section>

            <Section title="Cobertura">
              <Field label="Estados (separados por vírgula)" hint="Ex: SP, RJ, MG">
                <input className="input" value={form.coverage_states} onChange={e => setF('coverage_states', e.target.value)} placeholder="SP, RJ, MG" />
              </Field>
              <Field label="Cidades (uma por linha)">
                <textarea
                  className="input"
                  rows={4}
                  value={form.coverage_cities}
                  onChange={e => setF('coverage_cities', e.target.value)}
                  placeholder="São Paulo (100km)&#10;Campinas (80km)"
                  style={{ resize: 'vertical' }}
                />
              </Field>
              <Field label="População total alcançada">
                <input className="input" type="number" min="0" value={form.total_population} onChange={e => setF('total_population', e.target.value)} placeholder="5000000" />
              </Field>
            </Section>

            <CalibrationPanel stationId={id} />

          </div>

          {/* ── Sidebar ── */}
          <aside className="edit-sidebar">
            <Section title="Dados comerciais">
              <Field label="PMM">
                <input className="input" type="number" min="0" value={form.pmm} onChange={e => setF('pmm', e.target.value)} />
              </Field>
              <Field label="E-mail comercial">
                <input className="input" type="email" value={form.commercial_email} onChange={e => setF('commercial_email', e.target.value)} />
              </Field>
              <Field label="Website">
                <input className="input" type="url" value={form.website} onChange={e => setF('website', e.target.value)} placeholder="https://…" />
              </Field>
              <Field label="Ano de fundação">
                <input className="input" type="number" min="1900" max="2100" value={form.foundation_year} onChange={e => setF('foundation_year', e.target.value)} />
              </Field>
            </Section>

            {/* Quick preview */}
            <Section title="Pré-visualização">
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '4px 0' }}>
                <StationAvatar station={{ ...station, name: form.name || station.name, logo_url: form.logo_url || null }} size={48} />
                <div>
                  <div style={{ fontWeight: 600, fontSize: 14, color: 'var(--c-text)' }}>
                    {form.name || station.name}
                  </div>
                  <div className="text-muted" style={{ fontSize: 12 }}>
                    {form.band}{form.frequency_mhz ? ` · ${form.frequency_mhz}` : ''}
                    {form.city ? ` · ${form.city}` : ''}
                    {form.state ? `/${form.state}` : ''}
                  </div>
                  {form.categories.length > 0 && (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 6 }}>
                      {form.categories.slice(0, 3).map(c => (
                        <span key={c} className="category-tag" style={{ fontSize: 10, padding: '1px 6px' }}>{c}</span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </Section>
          </aside>

        </div>
      </form>
    </div>
  )
}
