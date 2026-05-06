import { useState, useRef } from 'react'
import {
  useCampaigns, useCreateCampaign, useStartCampaign, usePauseCampaign,
  useClients, useStations, useCommercials, useUploadCommercial,
} from '../api/hooks'

const STATUS_LABEL = { planned: 'Planejada', active: 'Ativa', paused: 'Pausada', ended: 'Encerrada' }
const STATUS_CLASS = { planned: 'badge-planned', active: 'badge-active', paused: 'badge-paused', ended: 'badge-ended' }
const FP_LABEL    = { pending: 'aguardando', generating: 'gerando…', ready: 'pronto', failed: 'falhou' }
const FP_CLASS    = { pending: 'fp-pending', generating: 'fp-generating', ready: 'fp-ready', failed: 'fp-failed' }

function ChevronIcon({ open }) {
  return (
    <svg
      className={`campaign-chevron${open ? ' open' : ''}`}
      width="14"
      height="14"
      viewBox="0 0 14 14"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
    >
      <path d="M5 3L9 7L5 11" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function SpinnerIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 14 14"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      style={{ animation: 'spin 1s linear infinite', flexShrink: 0 }}
      aria-hidden="true"
    >
      <circle cx="7" cy="7" r="5.5" stroke="currentColor" strokeWidth="1.5" strokeDasharray="20 15" strokeLinecap="round" />
    </svg>
  )
}

function MegaphoneIcon() {
  return (
    <svg width="64" height="64" viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <path d="M52 12C52 12 40 20 24 22H16C13.8 22 12 23.8 12 26V38C12 40.2 13.8 42 16 42H24L28 56H36L32 42C44 44 52 52 52 52V12Z" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <path d="M56 32C56 32 56 26 52 22" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
      <path d="M56 32C56 32 56 38 52 42" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
    </svg>
  )
}

function CommercialsPanel({ campaignId }) {
  const { data: commercials = [], isLoading } = useCommercials(campaignId)
  const uploadCommercial = useUploadCommercial()
  const [title, setTitle] = useState('')
  const [cut, setCut]     = useState('')
  const [file, setFile]   = useState(null)
  const fileRef           = useRef()

  function handleUpload(e) {
    e.preventDefault()
    const fd = new FormData()
    fd.append('campaign_id', campaignId)
    fd.append('title', title)
    if (cut) fd.append('cut_label', cut)
    fd.append('audio', file)
    uploadCommercial.mutate(fd, {
      onSuccess: () => {
        setTitle(''); setCut(''); setFile(null)
        if (fileRef.current) fileRef.current.value = ''
      },
    })
  }

  return (
    <div className="campaign-panel">
      <p className="campaign-section-label">Materiais</p>

      {isLoading ? (
        <p className="empty-state">Carregando...</p>
      ) : (
        <table className="table" style={{ marginBottom: 16 }}>
          <thead>
            <tr>
              <th>Título</th>
              <th>Cut</th>
              <th>Duração</th>
              <th>Fingerprint</th>
            </tr>
          </thead>
          <tbody>
            {commercials.map(c => (
              <tr key={c.id}>
                <td style={{ fontWeight: 500 }}>{c.title}</td>
                <td className="text-muted">{c.cut_label || '—'}</td>
                <td className="text-muted">{c.duration_seconds ? `${c.duration_seconds.toFixed(1)}s` : '—'}</td>
                <td>
                  <span className={FP_CLASS[c.fingerprint_status] ?? ''}>
                    {FP_LABEL[c.fingerprint_status] ?? c.fingerprint_status}
                  </span>
                </td>
              </tr>
            ))}
            {commercials.length === 0 && (
              <tr><td className="table-empty" colSpan={4}>Nenhum material ainda.</td></tr>
            )}
          </tbody>
        </table>
      )}

      <hr className="divider" />
      <p className="campaign-section-label" style={{ marginBottom: 10 }}>Adicionar material</p>
      <form onSubmit={handleUpload}>
        <div className="form-row" style={{ marginBottom: 10 }}>
          <div className="field" style={{ flex: 2 }}>
            <label>Título *</label>
            <input className="input" placeholder="ex: Produto X — verão" value={title} onChange={e => setTitle(e.target.value)} required />
          </div>
          <div className="field">
            <label>Cut label</label>
            <input className="input" placeholder="ex: 30s" value={cut} onChange={e => setCut(e.target.value)} />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>Arquivo * (.wav .mp3 .m4a .aac .mpeg)</label>
            <div className="file-input-wrap">
              <input
                ref={fileRef}
                type="file"
                accept=".wav,.mp3,.m4a,.aac,.mpeg"
                onChange={e => setFile(e.target.files[0] ?? null)}
                required
              />
              <div className={`file-input-display${file ? ' has-file' : ''}`}>
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
                  <path d="M7 1V9M7 1L4.5 3.5M7 1L9.5 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                  <path d="M1 11H13V13H1V11Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
                <span>{file ? file.name : 'Escolher arquivo'}</span>
              </div>
            </div>
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <label style={{ visibility: 'hidden' }}>.</label>
            <button className="btn btn-primary btn-sm" type="submit" disabled={uploadCommercial.isPending} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              {uploadCommercial.isPending ? <><SpinnerIcon /> Enviando</> : 'Enviar'}
            </button>
          </div>
        </div>
        {uploadCommercial.isError && (
          <p className="text-error">{uploadCommercial.error?.response?.data || 'Erro no upload.'}</p>
        )}
      </form>
    </div>
  )
}

export default function CampaignsPage() {
  const { data: campaigns = [], isLoading } = useCampaigns()
  const { data: clients   = [] }            = useClients()
  const { data: stations  = [] }            = useStations()
  const createCampaign = useCreateCampaign()
  const startCampaign  = useStartCampaign()
  const pauseCampaign  = usePauseCampaign()

  const emptyForm = { name: '', client_id: '', start_date: '', end_date: '', target_stations: [] }
  const [showForm, setShowForm] = useState(false)
  const [form, setForm]         = useState(emptyForm)
  const [expanded, setExpanded] = useState(null)

  function setField(k, v) { setForm(f => ({ ...f, [k]: v })) }

  function toggleStation(id) {
    setForm(f => ({
      ...f,
      target_stations: f.target_stations.includes(id)
        ? f.target_stations.filter(s => s !== id)
        : [...f.target_stations, id],
    }))
  }

  function handleSubmit(e) {
    e.preventDefault()
    createCampaign.mutate({
      ...form,
      start_date: form.start_date + 'T00:00:00Z',
      end_date:   form.end_date   + 'T00:00:00Z',
    }, {
      onSuccess: () => { setShowForm(false); setForm(emptyForm) },
    })
  }

  if (isLoading) return <p className="empty-state">Carregando...</p>

  return (
    <div>
      <div className="page-header">
        <h2>Campanhas</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowForm(v => !v)}>
          {showForm ? 'Cancelar' : '+ Nova campanha'}
        </button>
      </div>

      {showForm && (
        <div className="card" style={{ padding: '20px 24px', marginBottom: 24 }}>
          <h3 style={{ marginBottom: 20 }}>Nova campanha</h3>
          <form onSubmit={handleSubmit} className="stack">

            <div>
              <p className="campaign-section-label">Informações básicas</p>
              <div className="form-row">
                <div className="field" style={{ flex: 3 }}>
                  <label>Nome *</label>
                  <input className="input" placeholder="ex: Campanha Verão 2025" value={form.name} onChange={e => setField('name', e.target.value)} required />
                </div>
                <div className="field" style={{ flex: 2 }}>
                  <label>Cliente *</label>
                  <select className="select" value={form.client_id} onChange={e => setField('client_id', e.target.value)} required>
                    <option value="">Selecione...</option>
                    {clients.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                  </select>
                </div>
              </div>
            </div>

            <div>
              <p className="campaign-section-label" style={{ marginTop: 8 }}>Período</p>
              <div className="form-row">
                <div className="field">
                  <label>Início *</label>
                  <input className="input" type="date" value={form.start_date} onChange={e => setField('start_date', e.target.value)} required />
                </div>
                <div className="field">
                  <label>Fim *</label>
                  <input className="input" type="date" value={form.end_date} onChange={e => setField('end_date', e.target.value)} required />
                </div>
              </div>
            </div>

            <div>
              <p className="campaign-section-label" style={{ marginTop: 8 }}>Emissoras alvo</p>
              <div className="station-chips">
                {stations.map(s => (
                  <div
                    key={s.id}
                    className={`station-chip${form.target_stations.includes(s.id) ? ' selected' : ''}`}
                    onClick={() => toggleStation(s.id)}
                    role="checkbox"
                    aria-checked={form.target_stations.includes(s.id)}
                    tabIndex={0}
                    onKeyDown={e => (e.key === ' ' || e.key === 'Enter') && toggleStation(s.id)}
                  >
                    {form.target_stations.includes(s.id) && (
                      <svg width="12" height="12" viewBox="0 0 12 12" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
                        <path d="M2 6L5 9L10 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                      </svg>
                    )}
                    {s.name}
                    <span style={{ color: 'var(--c-text-3)', fontWeight: 400 }}>({s.band})</span>
                  </div>
                ))}
                {stations.length === 0 && <span className="text-muted">Nenhuma emissora cadastrada.</span>}
              </div>
            </div>

            <div style={{ marginTop: 4 }}>
              <button className="btn btn-primary btn-sm" type="submit" disabled={createCampaign.isPending}>
                {createCampaign.isPending ? 'Criando...' : 'Criar campanha'}
              </button>
              {createCampaign.isError && <p className="text-error">Erro ao criar. Tente novamente.</p>}
            </div>

          </form>
        </div>
      )}

      {campaigns.length === 0 ? (
        <div className="campaigns-empty">
          <div className="campaigns-empty-action">
            <div style={{ color: 'var(--c-action)', opacity: 0.7 }}>
              <MegaphoneIcon />
            </div>
            <h3>Nenhuma campanha cadastrada</h3>
            <p>Crie a primeira campanha para começar a monitorar a veiculação de comerciais nas emissoras.</p>
            <div>
              <button className="btn btn-primary btn-sm" onClick={() => setShowForm(true)}>
                + Nova campanha
              </button>
            </div>
          </div>
          <div className="campaigns-empty-preview">
            <div className="ghost-card">
              <div className="ghost-line" style={{ width: '60%' }} />
              <div className="ghost-line" style={{ width: '40%', height: 10 }} />
              <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
                <div className="ghost-line" style={{ width: 60, height: 20, borderRadius: 999 }} />
                <div className="ghost-line" style={{ width: 80, height: 20, borderRadius: 999 }} />
              </div>
            </div>
            <div className="ghost-card" style={{ opacity: 0.6 }}>
              <div className="ghost-line" style={{ width: '50%' }} />
              <div className="ghost-line" style={{ width: '35%', height: 10 }} />
            </div>
          </div>
        </div>
      ) : (
        <div className="campaign-list">
          {campaigns.map(c => {
            const client     = clients.find(cl => cl.id === c.client_id)
            const isExpanded = expanded === c.id
            const stationCount = (c.target_stations ?? []).length

            const formatDate = iso => {
              if (!iso) return '—'
              const [y, m, d] = iso.slice(0, 10).split('-')
              return `${d}/${m}/${y}`
            }

            return (
              <div key={c.id} className="campaign-row">
                <div
                  className="campaign-row-header"
                  onClick={() => setExpanded(isExpanded ? null : c.id)}
                >
                  <ChevronIcon open={isExpanded} />

                  <div className="campaign-row-info">
                    <div className="campaign-row-name">{c.name}</div>
                    <div className="campaign-row-meta">
                      {client?.name ?? '—'}
                      {' · '}
                      {formatDate(c.start_date)} — {formatDate(c.end_date)}
                      {' · '}
                      {stationCount} {stationCount === 1 ? 'emissora' : 'emissoras'}
                    </div>
                  </div>

                  <div className="campaign-row-actions">
                    <span className={`badge ${STATUS_CLASS[c.status] ?? 'badge-ended'}`}>
                      {STATUS_LABEL[c.status] ?? c.status}
                    </span>
                    <div onClick={e => e.stopPropagation()}>
                      {c.status !== 'active' && (
                        <button
                          className="btn btn-success btn-sm"
                          onClick={() => startCampaign.mutate(c.id)}
                          disabled={startCampaign.isPending}
                        >
                          Iniciar
                        </button>
                      )}
                      {c.status === 'active' && (
                        <button
                          className="btn btn-muted btn-sm"
                          onClick={() => pauseCampaign.mutate(c.id)}
                          disabled={pauseCampaign.isPending}
                        >
                          Pausar
                        </button>
                      )}
                    </div>
                  </div>
                </div>

                {isExpanded && <CommercialsPanel campaignId={c.id} />}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
