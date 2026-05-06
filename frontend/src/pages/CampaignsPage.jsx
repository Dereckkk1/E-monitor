import { useState, useRef } from 'react'
import {
  useCampaigns, useCreateCampaign, useStartCampaign, usePauseCampaign,
  useClients, useStations, useCommercials, useUploadCommercial,
} from '../api/hooks'

const STATUS_LABEL = { planned: 'Planejada', active: 'Ativa', paused: 'Pausada', ended: 'Encerrada' }
const STATUS_CLASS = { planned: 'badge-planned', active: 'badge-active', paused: 'badge-paused', ended: 'badge-ended' }
const FP_LABEL     = { pending: 'aguardando', generating: 'gerando…', ready: 'pronto', failed: 'falhou' }
const FP_CLASS     = { pending: 'badge-fp-pending', generating: 'badge-fp-generating', ready: 'badge-fp-ready', failed: 'badge-fp-failed' }

function CommercialsPanel({ campaignId }) {
  const { data: commercials = [], isLoading } = useCommercials(campaignId)
  const uploadCommercial = useUploadCommercial()
  const [title, setTitle]   = useState('')
  const [cut, setCut]       = useState('')
  const [file, setFile]     = useState(null)
  const fileRef             = useRef()

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
    <div className="card-body">
      <p style={{ fontWeight: 600, fontSize: 13, marginBottom: 12, color: 'var(--c-text-2)' }}>MATERIAIS</p>

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
                <td><span className={FP_CLASS[c.fingerprint_status] ?? ''}>{FP_LABEL[c.fingerprint_status] ?? c.fingerprint_status}</span></td>
              </tr>
            ))}
            {commercials.length === 0 && (
              <tr><td className="table-empty" colSpan={4}>Nenhum material ainda.</td></tr>
            )}
          </tbody>
        </table>
      )}

      <hr className="divider" />
      <p style={{ fontWeight: 600, fontSize: 12, marginBottom: 10, color: 'var(--c-text-2)', textTransform: 'uppercase', letterSpacing: '0.4px' }}>Adicionar material</p>
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
            <input
              ref={fileRef}
              className="input"
              type="file"
              accept=".wav,.mp3,.m4a,.aac,.mpeg"
              onChange={e => setFile(e.target.files[0])}
              required
              style={{ paddingTop: 5 }}
            />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <label style={{ visibility: 'hidden' }}>.</label>
            <button className="btn btn-primary btn-sm" type="submit" disabled={uploadCommercial.isPending}>
              {uploadCommercial.isPending ? 'Enviando…' : 'Enviar'}
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
        <div className="card" style={{ padding: 16, marginBottom: 20 }}>
          <h3 style={{ marginBottom: 14 }}>Nova campanha</h3>
          <form onSubmit={handleSubmit} className="stack">
            <div className="form-row">
              <div className="field" style={{ flex: 3 }}>
                <label>Nome *</label>
                <input className="input" value={form.name} onChange={e => setField('name', e.target.value)} required />
              </div>
              <div className="field" style={{ flex: 2 }}>
                <label>Cliente *</label>
                <select className="select" value={form.client_id} onChange={e => setField('client_id', e.target.value)} required>
                  <option value="">Selecione...</option>
                  {clients.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
              </div>
            </div>
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
            <div className="field">
              <label>Emissoras</label>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 20px', padding: '8px 0' }}>
                {stations.map(s => (
                  <label key={s.id} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'pointer' }}>
                    <input type="checkbox" checked={form.target_stations.includes(s.id)} onChange={() => toggleStation(s.id)} />
                    {s.name} <span className="text-muted">({s.band})</span>
                  </label>
                ))}
                {stations.length === 0 && <span className="text-muted">Nenhuma emissora cadastrada.</span>}
              </div>
            </div>
            <div>
              <button className="btn btn-primary btn-sm" type="submit" disabled={createCampaign.isPending}>
                {createCampaign.isPending ? 'Criando...' : 'Criar campanha'}
              </button>
              {createCampaign.isError && <p className="text-error">Erro ao criar. Tente novamente.</p>}
            </div>
          </form>
        </div>
      )}

      {campaigns.length === 0 && <p className="empty-state">Nenhuma campanha cadastrada.</p>}

      <div className="stack">
        {campaigns.map(c => {
          const client      = clients.find(cl => cl.id === c.client_id)
          const isExpanded  = expanded === c.id
          const stationList = (c.target_stations ?? [])
            .map(id => stations.find(s => s.id === id)?.name)
            .filter(Boolean)
            .join(', ')

          return (
            <div key={c.id} className="card">
              <div className="card-header" onClick={() => setExpanded(isExpanded ? null : c.id)}>
                <span style={{ color: 'var(--c-text-3)', fontSize: 12, width: 14 }}>{isExpanded ? '▾' : '▸'}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontWeight: 600 }}>{c.name}</div>
                  <div className="text-muted" style={{ marginTop: 2 }}>
                    {client?.name ?? '—'}
                    {' · '}
                    {c.start_date?.slice(0, 10)} → {c.end_date?.slice(0, 10)}
                    {stationList && <> · <span>{stationList}</span></>}
                  </div>
                </div>
                <span className={`badge ${STATUS_CLASS[c.status] ?? 'badge-ended'}`}>
                  {STATUS_LABEL[c.status] ?? c.status}
                </span>
                <div className="cluster" onClick={e => e.stopPropagation()}>
                  {c.status !== 'active' && (
                    <button className="btn btn-success btn-sm" onClick={() => startCampaign.mutate(c.id)} disabled={startCampaign.isPending}>
                      Iniciar
                    </button>
                  )}
                  {c.status === 'active' && (
                    <button className="btn btn-muted btn-sm" onClick={() => pauseCampaign.mutate(c.id)} disabled={pauseCampaign.isPending}>
                      Pausar
                    </button>
                  )}
                </div>
              </div>
              {isExpanded && <CommercialsPanel campaignId={c.id} />}
            </div>
          )
        })}
      </div>
    </div>
  )
}
