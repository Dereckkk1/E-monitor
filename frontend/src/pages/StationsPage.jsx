import { useState } from 'react'
import { useStations, useCreateStation } from '../api/hooks'

const STATUS_CLASS = {
  active:      'badge-active',
  paused:      'badge-paused',
  calibrating: 'badge-paused',
  error:       'badge-error',
}
const STATUS_LABEL = {
  active:      'Ativo',
  paused:      'Pausado',
  calibrating: 'Calibrando',
  error:       'Erro',
}

function AntennaIcon() {
  return (
    <svg width="64" height="64" viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <circle cx="32" cy="32" r="4" fill="currentColor" />
      <path d="M32 36v16" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      <path d="M24 52h16" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      <path d="M20 28a16 16 0 0 1 24 0" stroke="currentColor" strokeWidth="3" strokeLinecap="round" fill="none" />
      <path d="M13 21a24 24 0 0 1 38 0" stroke="currentColor" strokeWidth="3" strokeLinecap="round" fill="none" />
      <path d="M7 14a32 32 0 0 1 50 0" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" fill="none" strokeOpacity="0.4" />
    </svg>
  )
}

export default function StationsPage() {
  const { data: stations = [], isLoading } = useStations()
  const createStation = useCreateStation()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })

  function set(k, v) { setForm(f => ({ ...f, [k]: v })) }

  function handleSubmit(e) {
    e.preventDefault()
    createStation.mutate({
      ...form,
      frequency_mhz: form.frequency_mhz !== '' ? Number(form.frequency_mhz) : null,
    }, {
      onSuccess: () => {
        setShowForm(false)
        setForm({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })
      },
    })
  }

  return (
    <div>
      {/* Header */}
      <div className="page-header">
        <h2>Emissoras</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowForm(v => !v)}>
          {showForm ? 'Cancelar' : '+ Nova emissora'}
        </button>
      </div>

      {/* Form */}
      {showForm && (
        <div className="card" style={{ marginBottom: 20, padding: 20 }}>
          <h3 style={{ marginBottom: 16 }}>Nova emissora</h3>
          <form onSubmit={handleSubmit} className="stack">
            {/* Row 1: Nome + Banda + Frequência */}
            <div className="form-row">
              <div className="field" style={{ flex: 2 }}>
                <label>Nome *</label>
                <input
                  className="input"
                  placeholder="Ex: Rádio Globo"
                  value={form.name}
                  onChange={e => set('name', e.target.value)}
                  required
                />
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
                <input
                  className="input"
                  type="number"
                  step="0.1"
                  placeholder="98.5"
                  value={form.frequency_mhz}
                  onChange={e => set('frequency_mhz', e.target.value)}
                />
              </div>
            </div>

            {/* Row 2: Cidade + UF */}
            <div className="form-row">
              <div className="field" style={{ flex: 2 }}>
                <label>Cidade</label>
                <input
                  className="input"
                  placeholder="Ex: São Paulo"
                  value={form.city}
                  onChange={e => set('city', e.target.value)}
                />
              </div>
              <div className="field">
                <label>UF</label>
                <input
                  className="input"
                  maxLength={2}
                  placeholder="SP"
                  value={form.state}
                  onChange={e => set('state', e.target.value.toUpperCase())}
                />
              </div>
            </div>

            {/* Row 3: Stream URL */}
            <div className="field">
              <label>URL do stream *</label>
              <input
                className="input"
                placeholder="https://..."
                value={form.stream_url}
                onChange={e => set('stream_url', e.target.value)}
                required
              />
            </div>

            {/* Actions */}
            <div className="cluster">
              <button className="btn btn-primary btn-sm" type="submit" disabled={createStation.isPending}>
                {createStation.isPending ? 'Salvando...' : 'Salvar emissora'}
              </button>
              {createStation.isError && (
                <span className="text-error">Erro ao salvar. Tente novamente.</span>
              )}
            </div>
          </form>
        </div>
      )}

      {/* Loading skeleton */}
      {isLoading && (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>Nome</th><th>Banda</th><th>Frequência</th>
                <th>Cidade / UF</th><th>Status</th><th>Stream URL</th>
              </tr>
            </thead>
            <tbody>
              {[...Array(4)].map((_, i) => (
                <tr key={i}>
                  <td><div className="skeleton-cell" style={{ width: '120px' }} /></td>
                  <td><div className="skeleton-cell" style={{ width: '36px' }} /></td>
                  <td><div className="skeleton-cell" style={{ width: '64px' }} /></td>
                  <td><div className="skeleton-cell" style={{ width: '100px' }} /></td>
                  <td><div className="skeleton-cell" style={{ width: '56px' }} /></td>
                  <td><div className="skeleton-cell" style={{ width: '200px' }} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Empty state */}
      {!isLoading && stations.length === 0 && (
        <div className="stations-empty">
          <div className="stations-empty-action">
            <div style={{ color: 'var(--c-action)', opacity: 0.75 }}>
              <AntennaIcon />
            </div>
            <h3>Nenhuma emissora cadastrada</h3>
            <p>Adicione uma emissora para começar o monitoramento.</p>
            <div>
              <button className="btn btn-primary btn-sm" onClick={() => setShowForm(true)}>
                + Nova emissora
              </button>
            </div>
          </div>
          <div className="stations-empty-preview">
            <table className="table">
              <thead>
                <tr>
                  <th>Nome</th><th>Banda</th><th>Frequência</th>
                  <th>Cidade / UF</th><th>Status</th><th>Stream URL</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td style={{ fontWeight: 700, fontFamily: 'var(--font-heading)', color: 'var(--c-text)' }}>Rádio Exemplo</td>
                  <td><span className="band-pill">FM</span></td>
                  <td style={{ color: 'var(--c-text-2)' }}>98.5 MHz</td>
                  <td style={{ color: 'var(--c-text-3)' }}>São Paulo / SP</td>
                  <td><span className="badge badge-active">Ativo</span></td>
                  <td style={{ color: 'var(--c-text-3)', fontSize: 11, maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    https://stream.exemplo.com.br/radio
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Table */}
      {!isLoading && stations.length > 0 && (
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
                  <td style={{ fontWeight: 700, fontFamily: 'var(--font-heading)', color: 'var(--c-text)' }}>
                    {s.name}
                  </td>
                  <td>
                    <span className="band-pill">{s.band}</span>
                  </td>
                  <td style={{ color: 'var(--c-text-2)' }}>
                    {s.frequency_mhz ? `${s.frequency_mhz} MHz` : '—'}
                  </td>
                  <td style={{ color: 'var(--c-text-3)' }}>
                    {[s.city, s.state].filter(Boolean).join(' / ') || '—'}
                  </td>
                  <td>
                    <span className={`badge ${STATUS_CLASS[s.monitoring_status] ?? 'badge-ended'}`}>
                      {STATUS_LABEL[s.monitoring_status] ?? s.monitoring_status}
                    </span>
                  </td>
                  <td style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--c-text-3)', fontSize: 11 }}>
                    {s.stream_url}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
