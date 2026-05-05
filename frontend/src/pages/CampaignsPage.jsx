import { useState } from 'react'
import { useCampaigns, useCreateCampaign, useStartCampaign, usePauseCampaign, useClients, useStations } from '../api/hooks'

export default function CampaignsPage() {
  const { data: campaigns = [], isLoading } = useCampaigns()
  const { data: clients = [] } = useClients()
  const { data: stations = [] } = useStations()
  const createCampaign = useCreateCampaign()
  const startCampaign = useStartCampaign()
  const pauseCampaign = usePauseCampaign()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', client_id: '', start_date: '', end_date: '', target_stations: [] })

  function toggleStation(id) {
    setForm(f => ({
      ...f,
      target_stations: f.target_stations.includes(id)
        ? f.target_stations.filter(s => s !== id)
        : [...f.target_stations, id]
    }))
  }

  function handleSubmit(e) {
    e.preventDefault()
    createCampaign.mutate(form, {
      onSuccess: () => { setShowForm(false); setForm({ name: '', client_id: '', start_date: '', end_date: '', target_stations: [] }) }
    })
  }

  if (isLoading) return <div>Carregando...</div>

  return (
    <div>
      <h2>Campanhas</h2>
      <button onClick={() => setShowForm(!showForm)}>Nova Campanha</button>
      {showForm && (
        <form onSubmit={handleSubmit} style={{ margin: '12px 0', display: 'flex', flexDirection: 'column', gap: 8, maxWidth: 400 }}>
          <input placeholder="Nome da campanha" value={form.name} onChange={e => setForm({...form, name: e.target.value})} required />
          <select value={form.client_id} onChange={e => setForm({...form, client_id: e.target.value})} required>
            <option value="">Selecione o cliente</option>
            {clients.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <input type="date" placeholder="Data início" value={form.start_date} onChange={e => setForm({...form, start_date: e.target.value})} required />
          <input type="date" placeholder="Data fim" value={form.end_date} onChange={e => setForm({...form, end_date: e.target.value})} required />
          <fieldset>
            <legend>Emissoras</legend>
            {stations.map(s => (
              <label key={s.id} style={{ display: 'block' }}>
                <input type="checkbox" checked={form.target_stations.includes(s.id)} onChange={() => toggleStation(s.id)} />
                {' '}{s.name} ({s.band})
              </label>
            ))}
          </fieldset>
          <button type="submit" disabled={createCampaign.isPending}>Criar</button>
        </form>
      )}
      <table border="1" cellPadding="6" style={{ marginTop: 16, borderCollapse: 'collapse' }}>
        <thead><tr><th>Nome</th><th>Cliente</th><th>Status</th><th>Início</th><th>Fim</th><th>Ações</th></tr></thead>
        <tbody>
          {campaigns.map(c => (
            <tr key={c.id}>
              <td>{c.name}</td>
              <td>{clients.find(cl => cl.id === c.client_id)?.name || c.client_id}</td>
              <td>{c.status}</td>
              <td>{c.start_date?.slice(0, 10)}</td>
              <td>{c.end_date?.slice(0, 10)}</td>
              <td>
                {c.status !== 'active' && <button onClick={() => startCampaign.mutate(c.id)} disabled={startCampaign.isPending}>Iniciar</button>}
                {c.status === 'active' && <button onClick={() => pauseCampaign.mutate(c.id)} disabled={pauseCampaign.isPending}>Pausar</button>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
