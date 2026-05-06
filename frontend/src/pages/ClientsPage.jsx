import { useState } from 'react'
import { useClients, useCreateClient } from '../api/hooks'

function ClientSkeleton() {
  return (
    <div className="card" style={{ overflow: 'hidden' }}>
      {[0, 1, 2].map(i => (
        <div key={i} className="skeleton-row">
          <div className="skeleton-cell" style={{ width: '40%' }} />
          <div className="skeleton-cell" style={{ width: '25%' }} />
        </div>
      ))}
    </div>
  )
}

function ClientsEmptyState({ onAdd }) {
  return (
    <div className="clients-empty">
      <div className="clients-empty-action">
        <svg
          width="64"
          height="64"
          viewBox="0 0 64 64"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
          aria-hidden="true"
          style={{ color: 'var(--c-action)' }}
        >
          <circle cx="32" cy="20" r="12" fill="currentColor" opacity="0.18" />
          <ellipse cx="32" cy="46" rx="20" ry="12" fill="currentColor" opacity="0.18" />
          <circle cx="32" cy="20" r="8" stroke="currentColor" strokeWidth="2.5" />
          <path
            d="M14 52c0-9.941 8.059-18 18-18s18 8.059 18 18"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
          />
        </svg>
        <h3>Nenhum cliente cadastrado</h3>
        <p>Adicione um cliente para começar a criar campanhas.</p>
        <div>
          <button className="btn btn-primary btn-sm" onClick={onAdd}>
            + Novo cliente
          </button>
        </div>
      </div>

      <div className="clients-empty-preview" aria-hidden="true">
        <div className="card" style={{ overflow: 'hidden' }}>
          <table className="table">
            <thead>
              <tr>
                <th>Nome</th>
                <th>ID</th>
              </tr>
            </thead>
            <tbody>
              {['Rádio Cultura', 'CBN São Paulo', 'Band FM'].map((name, i) => (
                <tr key={i}>
                  <td style={{ fontFamily: 'var(--font-heading)', fontWeight: 700, color: 'var(--c-text)' }}>
                    {name}
                  </td>
                  <td style={{ fontFamily: "'Courier New', monospace", fontSize: 11, color: 'var(--c-text-3)' }}>
                    {`00000000-000${i}-0000-0000-000000000000`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

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

  return (
    <div>
      <div className="page-header">
        <h2>Clientes</h2>
        <button
          className="btn btn-primary btn-sm"
          onClick={() => setShowForm(v => !v)}
        >
          {showForm ? 'Cancelar' : '+ Novo cliente'}
        </button>
      </div>

      {showForm && (
        <div className="card" style={{ marginBottom: 20, padding: 16 }}>
          <form onSubmit={handleSubmit} className="cluster" style={{ alignItems: 'flex-end' }}>
            <div className="field" style={{ flex: 1, maxWidth: 360 }}>
              <label>Nome *</label>
              <input
                className="input"
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="Nome do cliente"
                required
                autoFocus
              />
            </div>
            <button
              className="btn btn-primary btn-sm"
              type="submit"
              disabled={createClient.isPending}
            >
              {createClient.isPending ? 'Criando...' : 'Criar'}
            </button>
            {createClient.isError && (
              <p className="text-error" style={{ width: '100%' }}>
                Erro ao criar. Tente novamente.
              </p>
            )}
          </form>
        </div>
      )}

      {isLoading ? (
        <ClientSkeleton />
      ) : clients.length === 0 ? (
        <ClientsEmptyState onAdd={() => setShowForm(true)} />
      ) : (
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
                  <td
                    style={{
                      fontFamily: 'var(--font-heading)',
                      fontWeight: 700,
                      color: 'var(--c-text)',
                    }}
                  >
                    {c.name}
                  </td>
                  <td
                    style={{
                      fontFamily: "'Courier New', monospace",
                      fontSize: 11,
                      color: 'var(--c-text-3)',
                      maxWidth: 200,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                    title={c.id}
                  >
                    {c.id}
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
