import { useState } from 'react'
import { useStations, useCreateStation } from '../api/hooks'

const STATUS_CLASS = { active: 'badge-active', paused: 'badge-paused', calibrating: 'badge-paused', error: 'badge-error' }
const STATUS_LABEL = { active: 'Ativo', paused: 'Pausado', calibrating: 'Calibrando', error: 'Erro' }

export default function StationsPage() {
  const { data: stations = [], isLoading } = useStations()
  const createStation = useCreateStation()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })

  function set(k, v) { setForm(f => ({ ...f, [k]: v })) }

  function handleSubmit(e) {
    e.preventDefault()
    createStation.mutate({ ...form, frequency_mhz: Number(form.frequency_mhz) }, {
      onSuccess: () => {
        setShowForm(false)
        setForm({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })
      },
    })
  }

  if (isLoading) return <p className="empty-state">Carregando...</p>

  return (
    <div>
      <div className="page-header">
        <h2>Emissoras</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowForm(v => !v)}>
          {showForm ? 'Cancelar' : '+ Nova emissora'}
        </button>
      </div>

      {showForm && (
        <div className="card" style={{ marginBottom: 20, padding: 16 }}>
          <h3 style={{ marginBottom: 14 }}>Nova emissora</h3>
          <form onSubmit={handleSubmit} className="stack">
            <div className="form-row">
              <div className="field" style={{ flex: 2 }}>
                <label>Nome *</label>
                <input className="input" value={form.name} onChange={e => set('name', e.target.value)} required />
              </div>
              <div className="field">
                <label>Banda</label>
                <select className="select" value={form.band} onChange={e => set('band', e.target.value)}>
                  <option value="FM">FM</option>
                  <option value="AM">AM</option>
                </select>
              </div>
              <div className="field">
                <label>Frequência (MHz)</label>
                <input className="input" type="number" step="0.1" value={form.frequency_mhz} onChange={e => set('frequency_mhz', e.target.value)} />
              </div>
            </div>
            <div className="form-row">
              <div className="field" style={{ flex: 2 }}>
                <label>Cidade</label>
                <input className="input" value={form.city} onChange={e => set('city', e.target.value)} />
              </div>
              <div className="field">
                <label>UF</label>
                <input className="input" maxLength={2} value={form.state} onChange={e => set('state', e.target.value.toUpperCase())} />
              </div>
            </div>
            <div className="field">
              <label>URL do stream *</label>
              <input className="input" value={form.stream_url} onChange={e => set('stream_url', e.target.value)} required />
            </div>
            <div>
              <button className="btn btn-primary btn-sm" type="submit" disabled={createStation.isPending}>
                {createStation.isPending ? 'Salvando...' : 'Salvar'}
              </button>
              {createStation.isError && <p className="text-error">Erro ao salvar. Tente novamente.</p>}
            </div>
          </form>
        </div>
      )}

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th>Nome</th>
              <th>Banda</th>
              <th>Frequência</th>
              <th>Cidade / UF</th>
              <th>Status</th>
              <th>Stream URL</th>
            </tr>
          </thead>
          <tbody>
            {stations.map(s => (
              <tr key={s.id}>
                <td style={{ fontWeight: 500 }}>{s.name}</td>
                <td>{s.band}</td>
                <td>{s.frequency_mhz ? `${s.frequency_mhz} MHz` : '—'}</td>
                <td className="text-muted">{[s.city, s.state].filter(Boolean).join(' / ') || '—'}</td>
                <td><span className={`badge ${STATUS_CLASS[s.monitoring_status] ?? 'badge-ended'}`}>{STATUS_LABEL[s.monitoring_status] ?? s.monitoring_status}</span></td>
                <td className="text-muted" style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.stream_url}</td>
              </tr>
            ))}
            {stations.length === 0 && <tr><td className="table-empty" colSpan={6}>Nenhuma emissora cadastrada.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  )
}
