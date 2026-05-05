import { useState } from 'react'
import { useClients, useCreateClient } from '../api/hooks'

export default function ClientsPage() {
  const { data: clients = [], isLoading } = useClients()
  const createClient = useCreateClient()
  const [showForm, setShowForm] = useState(false)
  const [name, setName] = useState('')

  function handleSubmit(e) {
    e.preventDefault()
    createClient.mutate({ name }, {
      onSuccess: () => { setShowForm(false); setName('') },
    })
  }

  if (isLoading) return <div>Carregando...</div>

  return (
    <div>
      <h2>Clientes</h2>
      <button onClick={() => setShowForm(!showForm)}>Novo Cliente</button>
      {showForm && (
        <form onSubmit={handleSubmit} style={{ margin: '12px 0', display: 'flex', gap: 8 }}>
          <input
            placeholder="Nome do cliente"
            value={name}
            onChange={e => setName(e.target.value)}
            required
          />
          <button type="submit" disabled={createClient.isPending}>Criar</button>
          <button type="button" onClick={() => setShowForm(false)}>Cancelar</button>
        </form>
      )}
      <table border="1" cellPadding="6" style={{ marginTop: 16, borderCollapse: 'collapse' }}>
        <thead><tr><th>Nome</th><th>ID</th></tr></thead>
        <tbody>
          {clients.map(c => (
            <tr key={c.id}>
              <td>{c.name}</td>
              <td style={{ fontSize: 11, color: '#888' }}>{c.id}</td>
            </tr>
          ))}
          {clients.length === 0 && <tr><td colSpan={2}>Nenhum cliente cadastrado.</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
