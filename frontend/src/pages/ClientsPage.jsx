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

  if (isLoading) return <p className="empty-state">Carregando...</p>

  return (
    <div>
      <div className="page-header">
        <h2>Clientes</h2>
        <button className="btn btn-primary btn-sm" onClick={() => setShowForm(v => !v)}>
          {showForm ? 'Cancelar' : '+ Novo cliente'}
        </button>
      </div>

      {showForm && (
        <div className="card" style={{ marginBottom: 20, padding: 16 }}>
          <h3 style={{ marginBottom: 14 }}>Novo cliente</h3>
          <form onSubmit={handleSubmit} className="cluster">
            <div className="field" style={{ flex: 1, maxWidth: 360 }}>
              <label>Nome *</label>
              <input className="input" value={name} onChange={e => setName(e.target.value)} required />
            </div>
            <button className="btn btn-primary btn-sm" type="submit" style={{ alignSelf: 'flex-end' }} disabled={createClient.isPending}>
              {createClient.isPending ? 'Criando...' : 'Criar'}
            </button>
            {createClient.isError && <p className="text-error" style={{ width: '100%' }}>Erro ao criar. Tente novamente.</p>}
          </form>
        </div>
      )}

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th>Nome</th>
              <th>ID</th>
            </tr>
          </thead>
          <tbody>
            {clients.map(c => (
              <tr key={c.id}>
                <td style={{ fontWeight: 500 }}>{c.name}</td>
                <td className="text-muted" style={{ fontFamily: 'monospace', fontSize: 11 }}>{c.id}</td>
              </tr>
            ))}
            {clients.length === 0 && <tr><td className="table-empty" colSpan={2}>Nenhum cliente cadastrado.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  )
}
