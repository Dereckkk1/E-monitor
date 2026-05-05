import { useState } from 'react'
import { useStations, useCreateStation } from '../api/hooks'

export default function StationsPage() {
  const { data: stations = [], isLoading } = useStations()
  const createStation = useCreateStation()
  const [form, setForm] = useState({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' })
  const [showForm, setShowForm] = useState(false)

  function handleSubmit(e) {
    e.preventDefault()
    createStation.mutate({ ...form, frequency_mhz: Number(form.frequency_mhz) }, {
      onSuccess: () => { setShowForm(false); setForm({ name: '', band: 'FM', frequency_mhz: '', city: '', state: '', stream_url: '' }) }
    })
  }

  const statusColor = { active: 'green', paused: 'gray', calibrating: 'orange', error: 'red' }

  if (isLoading) return <div>Carregando...</div>

  return (
    <div>
      <h2>Emissoras</h2>
      <button onClick={() => setShowForm(!showForm)}>Adicionar Emissora</button>
      {showForm && (
        <form onSubmit={handleSubmit} style={{ margin: '12px 0', display: 'flex', flexDirection: 'column', gap: 8, maxWidth: 400 }}>
          <input placeholder="Nome" value={form.name} onChange={e => setForm({...form, name: e.target.value})} required />
          <select value={form.band} onChange={e => setForm({...form, band: e.target.value})}>
            <option value="FM">FM</option>
            <option value="AM">AM</option>
          </select>
          <input placeholder="Frequência (MHz)" type="number" step="0.1" value={form.frequency_mhz} onChange={e => setForm({...form, frequency_mhz: e.target.value})} />
          <input placeholder="Cidade" value={form.city} onChange={e => setForm({...form, city: e.target.value})} />
          <input placeholder="Estado (UF)" maxLength={2} value={form.state} onChange={e => setForm({...form, state: e.target.value})} />
          <input placeholder="URL do Stream" value={form.stream_url} onChange={e => setForm({...form, stream_url: e.target.value})} required />
          <button type="submit" disabled={createStation.isPending}>Salvar</button>
        </form>
      )}
      <table border="1" cellPadding="6" style={{ marginTop: 16, borderCollapse: 'collapse' }}>
        <thead><tr><th>Nome</th><th>Banda</th><th>Frequência</th><th>Cidade/UF</th><th>Status</th><th>Stream URL</th></tr></thead>
        <tbody>
          {stations.map(s => (
            <tr key={s.id}>
              <td>{s.name}</td>
              <td>{s.band}</td>
              <td>{s.frequency_mhz} MHz</td>
              <td>{s.city}/{s.state}</td>
              <td><span style={{ color: statusColor[s.monitoring_status] || 'black' }}>{s.monitoring_status}</span></td>
              <td style={{ fontSize: 12 }}>{s.stream_url}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
