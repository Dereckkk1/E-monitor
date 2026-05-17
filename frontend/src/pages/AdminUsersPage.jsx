import { useState } from 'react'
import RSelect from '../components/RSelect'
import { useConfirm } from '../components/ConfirmModal'
import UserFormModal from '../components/UserFormModal'
import ResetPasswordModal from '../components/ResetPasswordModal'
import { useAuth } from '../contexts/AuthContext'
import {
  useUsersPaged, useCreateUser, useUpdateUser,
  useResetUserPassword, useDeleteUser, useClients,
} from '../api/hooks'
import './AdminUsersPage.css'

const STATUS_OPTIONS = [
  { value: 'active',   label: 'Ativos' },
  { value: 'inactive', label: 'Inativos' },
  { value: 'deleted',  label: 'Excluídos' },
  { value: 'all',      label: 'Todos' },
]
const ROLE_FILTER = [
  { value: '',       label: 'Todos os tipos' },
  { value: 'admin',  label: 'Administradores' },
  { value: 'client', label: 'Clientes' },
]

function formatRole(role) {
  if (role === 'viewer') return 'Cliente'
  return 'Administrador'
}

function formatDate(s) {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString('pt-BR', { dateStyle: 'short', timeStyle: 'short' })
  } catch {
    return s
  }
}

function StatusBadge({ user }) {
  if (user.deleted_at) return <span className="badge badge-danger">Excluído</span>
  if (!user.is_active) return <span className="badge badge-muted">Inativo</span>
  return <span className="badge badge-success">Ativo</span>
}

export default function AdminUsersPage() {
  const { user: me } = useAuth()
  const confirm = useConfirm()
  const clientsQ = useClients()
  const [filters, setFilters] = useState({ status: 'active', role: '', client_id: null, q: '' })
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState(null)
  const [resetting, setResetting] = useState(null)
  const [formError, setFormError] = useState(null)

  const params = {
    status: filters.status,
    page,
    page_size: 20,
    ...(filters.role ? { role: filters.role } : {}),
    ...(filters.client_id ? { client_id: filters.client_id } : {}),
    ...(filters.q ? { q: filters.q } : {}),
  }
  const list = useUsersPaged(params)
  const createM = useCreateUser()
  const updateM = useUpdateUser()
  const resetM = useResetUserPassword()
  const deleteM = useDeleteUser()

  const clientById = Object.fromEntries((clientsQ.data ?? []).map(c => [c.id, c]))

  function handleCreate(payload) {
    setFormError(null)
    createM.mutate(payload, {
      onSuccess: () => setCreateOpen(false),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  function handleEdit(payload) {
    setFormError(null)
    updateM.mutate({ id: editing.id, ...payload }, {
      onSuccess: () => setEditing(null),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  function handleReset(password) {
    setFormError(null)
    resetM.mutate({ id: resetting.id, password }, {
      onSuccess: () => setResetting(null),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  async function handleToggleActive(u) {
    const msg = u.is_active
      ? `Desativar ${u.email}? Ele não conseguirá mais fazer login até ser reativado.`
      : `Reativar ${u.email}? Ele voltará a poder fazer login.`
    if (!await confirm(msg)) return
    updateM.mutate({ id: u.id, is_active: !u.is_active })
  }
  async function handleDelete(u) {
    const msg = `Excluir ${u.email} definitivamente? Esta ação não pode ser desfeita.`
    if (!await confirm(msg)) return
    deleteM.mutate(u.id)
  }

  const data = list.data?.data ?? []
  const total = list.data?.total ?? 0
  const totalPages = list.data?.total_pages ?? 1

  return (
    <div className="admin-users-page">
      <header className="page-header">
        <h1>Usuários</h1>
        <button className="btn btn-primary" onClick={() => { setFormError(null); setCreateOpen(true) }}>
          + Novo usuário
        </button>
      </header>

      <div className="filters-row">
        <input
          className="input"
          placeholder="Buscar por nome ou email..."
          value={filters.q}
          onChange={e => { setFilters(f => ({ ...f, q: e.target.value })); setPage(1) }}
        />
        <RSelect
          value={ROLE_FILTER.find(o => o.value === filters.role)}
          options={ROLE_FILTER}
          onChange={o => { setFilters(f => ({ ...f, role: o.value, client_id: o.value === 'admin' ? null : f.client_id })); setPage(1) }}
        />
        {filters.role === 'client' && (
          <RSelect
            value={(clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name })).find(o => o.value === filters.client_id) ?? null}
            options={(clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))}
            onChange={o => { setFilters(f => ({ ...f, client_id: o?.value ?? null })); setPage(1) }}
            placeholder="Filtrar por cliente"
            isClearable
          />
        )}
        <RSelect
          value={STATUS_OPTIONS.find(o => o.value === filters.status)}
          options={STATUS_OPTIONS}
          onChange={o => { setFilters(f => ({ ...f, status: o.value })); setPage(1) }}
        />
      </div>

      <div className="data-table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>Nome</th>
              <th>Email</th>
              <th>Tipo</th>
              <th>Cliente</th>
              <th>Último login</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr><td colSpan={7} className="empty">Carregando…</td></tr>
            )}
            {!list.isLoading && data.map(u => {
              const isSelf = u.id === me?.id
              const disabled = !!u.deleted_at
              return (
                <tr key={u.id}>
                  <td>{u.name || <span className="muted">—</span>}</td>
                  <td>{u.email}</td>
                  <td>{formatRole(u.role)}</td>
                  <td>{u.client_id ? (clientById[u.client_id]?.name ?? u.client_id) : <span className="muted">—</span>}</td>
                  <td>{formatDate(u.last_login_at)}</td>
                  <td><StatusBadge user={u} /></td>
                  <td className="row-actions">
                    <button className="btn btn-secondary btn-sm" onClick={() => { setFormError(null); setEditing(u) }} disabled={disabled}>Editar</button>
                    <button className="btn btn-secondary btn-sm" onClick={() => { setFormError(null); setResetting(u) }} disabled={disabled}>Resetar senha</button>
                    {!disabled && (
                      <button className="btn btn-secondary btn-sm" onClick={() => handleToggleActive(u)} disabled={isSelf}>
                        {u.is_active ? 'Desativar' : 'Reativar'}
                      </button>
                    )}
                    {!disabled && (
                      <button className="btn btn-danger btn-sm" onClick={() => handleDelete(u)} disabled={isSelf}>Excluir</button>
                    )}
                  </td>
                </tr>
              )
            })}
            {!list.isLoading && data.length === 0 && (
              <tr><td colSpan={7} className="empty">Nenhum usuário encontrado.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="pagination">
        <button className="btn btn-secondary btn-sm" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>Anterior</button>
        <span>Página {page} de {totalPages} ({total} no total)</span>
        <button className="btn btn-secondary btn-sm" disabled={page >= totalPages} onClick={() => setPage(p => p + 1)}>Próxima</button>
      </div>

      {createOpen && (
        <UserFormModal mode="create" onSubmit={handleCreate} onClose={() => setCreateOpen(false)} error={formError} busy={createM.isPending} />
      )}
      {editing && (
        <UserFormModal mode="edit" initial={editing} onSubmit={handleEdit} onClose={() => setEditing(null)} error={formError} busy={updateM.isPending} />
      )}
      {resetting && (
        <ResetPasswordModal user={resetting} onSubmit={handleReset} onClose={() => setResetting(null)} error={formError} busy={resetM.isPending} />
      )}
    </div>
  )
}
