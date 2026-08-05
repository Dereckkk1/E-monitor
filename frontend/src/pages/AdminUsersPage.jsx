import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import RSelect from '../components/RSelect'
import { useConfirm } from '../components/ConfirmModal'
import UserFormModal from '../components/UserFormModal'
import ResetPasswordModal from '../components/ResetPasswordModal'
import { useAuth } from '../contexts/AuthContext'
import {
  useUsersPaged, useCreateUser, useUpdateUser, useRevokeWelcomeInvite,
  useResetUserPassword, useDeleteUser, useClients,
} from '../api/hooks'
import './AdminUsersPage.css'

const STATUS_PILLS = [
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

function formatRoleLabel(role) {
  if (role === 'viewer') return 'Cliente'
  return 'Admin'
}

function formatRelativeTime(iso) {
  if (!iso) return { label: 'nunca logou', tone: 'never', title: '' }
  const date = new Date(iso)
  const now = new Date()
  const sec = Math.floor((now - date) / 1000)
  const title = date.toLocaleString('pt-BR', { dateStyle: 'short', timeStyle: 'short' })
  if (sec < 45) return { label: 'agora', tone: 'fresh', title }
  const min = Math.floor(sec / 60)
  if (min < 60) return { label: `há ${min} min`, tone: 'fresh', title }
  const hour = Math.floor(min / 60)
  if (hour < 24) return { label: `há ${hour} h`, tone: 'fresh', title }
  const day = Math.floor(hour / 24)
  if (day === 1) return { label: 'ontem', tone: 'recent', title }
  if (day < 30) return { label: `há ${day} dias`, tone: 'recent', title }
  const month = Math.floor(day / 30)
  if (month < 12) return { label: `há ${month} ${month === 1 ? 'mês' : 'meses'}`, tone: 'stale', title }
  const year = Math.floor(day / 365)
  return { label: `há ${year} ${year === 1 ? 'ano' : 'anos'}`, tone: 'stale', title }
}

function StatusBadge({ user }) {
  if (user.deleted_at) return <span className="badge badge-error">Excluído</span>
  if (!user.is_active) return <span className="badge badge-ended">Inativo</span>
  return <span className="badge badge-active">Ativo</span>
}

function RoleChip({ role }) {
  const label = formatRoleLabel(role)
  const isAdmin = role !== 'viewer'
  return (
    <span className={`au-role au-role-${isAdmin ? 'admin' : 'client'}`}>
      <span className="au-role-dot" aria-hidden="true" />
      {label}
    </span>
  )
}

/* ─── Icons (inline, 14px stroke style, alinhado com ClientsPage) ─── */
function SearchIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor"
      strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
      <circle cx="7" cy="7" r="4.5" />
      <path d="M10.5 10.5 L14 14" />
    </svg>
  )
}
function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" aria-hidden="true">
      <path d="M7 2v10M2 7h10" />
    </svg>
  )
}
function EditIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M10.5 2.5l2 2L4.5 12.5H2.5v-2L10.5 2.5z" />
    </svg>
  )
}
function KeyIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="5" cy="10" r="2.5" />
      <path d="M7 8.5l5-5M9.5 5L11 6.5M12 3.5l1.5 1.5" />
    </svg>
  )
}
function PauseIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor"
      strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
      <path d="M5.5 3v9M9.5 3v9" />
    </svg>
  )
}
function PlayIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinejoin="round" aria-hidden="true">
      <path d="M4.5 3v9l8-4.5z" fill="currentColor" />
    </svg>
  )
}
function TrashIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 4h9M6 4V3h3v1M3.5 4l.75 8h6.5L11.5 4M6 6.5v4M9 6.5v4" />
    </svg>
  )
}
/* Envelope aberto: o convite de boas-vindas que já saiu. */
function WelcomeIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 10.5V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-8.5" />
      <path d="m3 10.5 9-6.5 9 6.5" />
      <path d="m3 10.5 9 6 9-6" />
    </svg>
  )
}

function BadgeCheckIcon() {
  return (
    <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m20 6-11 11-5-5" />
    </svg>
  )
}

function BadgeAlertIcon() {
  return (
    <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 8v5" />
      <path d="M12 17h.01" />
    </svg>
  )
}

function RevokeIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="m5.6 5.6 12.8 12.8" />
    </svg>
  )
}

/* Descreve o estado do convite em uma frase pro tooltip — é o "controle" que
   o admin quer: saber se saiu, se o cliente abriu e quantas vezes. */
function describeInvite(inv) {
  if (!inv) return null
  const d = iso => {
    if (!iso) return ''
    const dt = new Date(iso)
    return dt.toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', year: '2-digit' })
  }
  if (inv.revoked_at) return `Convite revogado em ${d(inv.revoked_at)}`

  const envio = {
    sent: `Email enviado em ${d(inv.created_at)}`,
    failed: `Falha no envio do email (${d(inv.created_at)})`,
    disabled: `Convite criado em ${d(inv.created_at)} — email desligado, link entregue por fora`,
    pending: `Convite criado em ${d(inv.created_at)}`,
  }[inv.email_status] ?? `Convite criado em ${d(inv.created_at)}`

  const leitura = inv.open_count > 0
    ? `aberto ${inv.open_count}× (1ª vez em ${d(inv.opened_at)})`
    : 'ainda não aberto'

  return `${envio} · ${leitura}`
}

function ClearIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor"
      strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
      <path d="M3 3l8 8M11 3l-8 8" />
    </svg>
  )
}

/* ─── Loading skeleton row ─── */
function SkeletonRow() {
  return (
    <tr className="au-row au-row-skeleton" aria-hidden="true">
      <td><span className="au-skel" style={{ width: '60%' }} /></td>
      <td><span className="au-skel" style={{ width: '78%' }} /></td>
      <td><span className="au-skel au-skel-chip" /></td>
      <td><span className="au-skel" style={{ width: '52%' }} /></td>
      <td><span className="au-skel" style={{ width: '46%' }} /></td>
      <td><span className="au-skel au-skel-chip" /></td>
      <td />
    </tr>
  )
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
  const [welcomeResult, setWelcomeResult] = useState(null)

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
  const revokeM = useRevokeWelcomeInvite()

  const clientById = Object.fromEntries((clientsQ.data ?? []).map(c => [c.id, c]))

  function handleCreate(payload) {
    setFormError(null)
    createM.mutate(payload, {
      onSuccess: (created) => {
        setCreateOpen(false)
        // O link aparece SEMPRE que o convite foi emitido — inclusive quando o
        // email falhou ou o SMTP está desligado. É o que permite ao admin
        // copiar e mandar por WhatsApp, que é o fluxo que a equipe já usa.
        if (created?.welcome) {
          setWelcomeResult({ email: created.email, ...created.welcome })
        }
      },
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

  // Convite de boas-vindas mais recente por usuário, indexado por user_id.
  // Vem no mesmo GET da listagem (sem N+1) — ver handlers/users.go List.
  const invites = list.data?.welcome_invites ?? {}

  async function handleRevokeInvite(u, inv) {
    const ok = await confirm(
      `Revogar o convite de boas-vindas de ${u.email}? O link para de funcionar ` +
      `imediatamente e a senha inicial guardada nele é apagada. A conta continua ativa.`
    )
    if (!ok) return
    revokeM.mutate(inv.invite_id)
  }

  const data = list.data?.data ?? []
  const total = list.data?.total ?? 0
  const totalPages = list.data?.total_pages ?? 1

  const hasFilters = filters.q || filters.role || filters.client_id || filters.status !== 'active'
  const showFilteredEmpty = !list.isLoading && data.length === 0 && hasFilters
  const showAbsoluteEmpty = !list.isLoading && data.length === 0 && !hasFilters

  function clearFilters() {
    setFilters({ status: 'active', role: '', client_id: null, q: '' })
    setPage(1)
  }

  return (
    <div className="au-page">
      <header className="au-header">
        <div className="au-header-titles">
          <h1 className="au-title">Usuários</h1>
          <p className="au-subtitle">
            Equipe interna e clientes anunciantes com acesso ao Radiocheck.
          </p>
        </div>
        <button
          className="btn btn-primary au-new-btn"
          onClick={() => { setFormError(null); setCreateOpen(true) }}
        >
          <PlusIcon /> Novo usuário
        </button>
      </header>

      <div className="au-toolbar" role="search">
        <div className="au-search">
          <span className="au-search-icon" aria-hidden="true"><SearchIcon /></span>
          <input
            className="au-search-input"
            placeholder="Buscar por nome ou email…"
            value={filters.q}
            onChange={e => { setFilters(f => ({ ...f, q: e.target.value })); setPage(1) }}
            aria-label="Buscar usuários"
          />
          {filters.q && (
            <button
              type="button"
              className="au-search-clear"
              onClick={() => { setFilters(f => ({ ...f, q: '' })); setPage(1) }}
              aria-label="Limpar busca"
            >
              <ClearIcon />
            </button>
          )}
        </div>

        <div className="au-toolbar-row">
          <div className="au-pills" role="tablist" aria-label="Filtrar por situação">
            {STATUS_PILLS.map(p => (
              <button
                key={p.value}
                role="tab"
                aria-selected={filters.status === p.value}
                className={`au-pill ${filters.status === p.value ? 'is-active' : ''}`}
                onClick={() => { setFilters(f => ({ ...f, status: p.value })); setPage(1) }}
              >
                {p.label}
              </button>
            ))}
          </div>

          <div className="au-toolbar-selects">
            <div className="au-select">
              <RSelect
                value={ROLE_FILTER.find(o => o.value === filters.role)}
                options={ROLE_FILTER}
                onChange={o => {
                  setFilters(f => ({
                    ...f,
                    role: o.value,
                    client_id: o.value === 'admin' ? null : f.client_id,
                  }))
                  setPage(1)
                }}
                aria-label="Filtrar por tipo"
              />
            </div>
            {filters.role === 'client' && (
              <div className="au-select au-select-client">
                <RSelect
                  value={(clientsQ.data ?? [])
                    .map(c => ({ value: c.id, label: c.name }))
                    .find(o => o.value === filters.client_id) ?? null}
                  options={(clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))}
                  onChange={o => { setFilters(f => ({ ...f, client_id: o?.value ?? null })); setPage(1) }}
                  placeholder="Cliente vinculado"
                  isClearable
                  aria-label="Filtrar por cliente"
                />
              </div>
            )}
          </div>
        </div>
      </div>

      <div className="au-card">
        <div className="au-table-wrap">
          <table className="au-table">
            <thead>
              <tr>
                <th scope="col">Nome</th>
                <th scope="col">Email</th>
                <th scope="col">Tipo</th>
                <th scope="col">Cliente</th>
                <th scope="col">Último login</th>
                <th scope="col">Situação</th>
                <th scope="col" aria-label="Ações" />
              </tr>
            </thead>
            <tbody>
              {list.isLoading && (
                <>
                  <SkeletonRow /><SkeletonRow /><SkeletonRow />
                  <SkeletonRow /><SkeletonRow /><SkeletonRow />
                </>
              )}

              {!list.isLoading && data.map(u => {
                const isSelf = u.id === me?.id
                const isDeleted = !!u.deleted_at
                const t = formatRelativeTime(u.last_login_at)
                const invite = invites[u.id]
                const inviteLive = invite && !invite.revoked_at
                return (
                  <tr key={u.id} className={`au-row ${isDeleted ? 'is-deleted' : ''} ${isSelf ? 'is-self' : ''}`}>
                    <td className="au-cell-name">
                      <div className="au-cell-name-inner">
                        <span className="au-name-text">
                          {u.name || <span className="au-muted">sem nome</span>}
                        </span>
                        <span className="au-name-email-sub">{u.email}</span>
                        {isSelf && <span className="au-self-chip">você</span>}
                      </div>
                    </td>
                    <td className="au-cell-email" title={u.email}>{u.email}</td>
                    <td><RoleChip role={u.role} /></td>
                    <td className="au-cell-client">
                      {/* Carteira: mostra o primeiro cliente e um "+N" pro
                          resto (agências). client_id sozinho é o fallback
                          pra resposta anterior à feature. */}
                      {u.client_ids?.length
                        ? (
                          <span
                            className="au-client-name"
                            title={u.client_ids.length > 1
                              ? u.client_ids.map(id => clientById[id]?.name ?? `#${id.slice(0, 6)}`).join(', ')
                              : undefined}
                          >
                            {clientById[u.client_ids[0]]?.name ?? `#${u.client_ids[0].slice(0, 6)}`}
                            {u.client_ids.length > 1 && ` +${u.client_ids.length - 1}`}
                          </span>
                        )
                        : u.client_id
                          ? <span className="au-client-name">{clientById[u.client_id]?.name ?? `#${u.client_id.slice(0, 6)}`}</span>
                          : <span className="au-muted">—</span>}
                    </td>
                    <td className={`au-cell-time au-time-${t.tone}`} title={t.title || undefined}>
                      {t.label}
                    </td>
                    <td><StatusBadge user={u} /></td>
                    <td className="au-cell-actions">
                      <div className="au-actions">
                        {/* Convite de boas-vindas: abre a MESMA página que o
                            cliente recebeu, pra o admin conferir o que foi
                            enviado. Só aparece pra quem teve convite. */}
                        {inviteLive && (
                          <a
                            className="au-icon-btn au-icon-btn-welcome"
                            href={invite.link}
                            target="_blank"
                            rel="noopener noreferrer"
                            title={describeInvite(invite)}
                            aria-label={`Abrir a página de boas-vindas de ${u.email}`}
                          >
                            <WelcomeIcon />
                          </a>
                        )}
                        {invite && invite.revoked_at && (
                          <span
                            className="au-icon-btn au-icon-btn-off"
                            title={describeInvite(invite)}
                            aria-label={`Convite de boas-vindas de ${u.email} revogado`}
                          >
                            <WelcomeIcon />
                          </span>
                        )}
                        {inviteLive && (
                          <button
                            type="button"
                            className="au-icon-btn"
                            title="Revogar convite de boas-vindas"
                            aria-label={`Revogar convite de boas-vindas de ${u.email}`}
                            onClick={() => handleRevokeInvite(u, invite)}
                            disabled={revokeM.isPending}
                          >
                            <RevokeIcon />
                          </button>
                        )}
                        <button
                          type="button"
                          className="au-icon-btn"
                          title="Editar"
                          aria-label={`Editar ${u.email}`}
                          onClick={() => { setFormError(null); setEditing(u) }}
                          disabled={isDeleted}
                        >
                          <EditIcon />
                        </button>
                        <button
                          type="button"
                          className="au-icon-btn"
                          title="Resetar senha"
                          aria-label={`Resetar senha de ${u.email}`}
                          onClick={() => { setFormError(null); setResetting(u) }}
                          disabled={isDeleted}
                        >
                          <KeyIcon />
                        </button>
                        {!isDeleted && (
                          <button
                            type="button"
                            className="au-icon-btn"
                            title={u.is_active ? 'Desativar' : 'Reativar'}
                            aria-label={`${u.is_active ? 'Desativar' : 'Reativar'} ${u.email}`}
                            onClick={() => handleToggleActive(u)}
                            disabled={isSelf}
                          >
                            {u.is_active ? <PauseIcon /> : <PlayIcon />}
                          </button>
                        )}
                        {!isDeleted && (
                          <button
                            type="button"
                            className="au-icon-btn au-icon-btn-danger"
                            title="Excluir"
                            aria-label={`Excluir ${u.email}`}
                            onClick={() => handleDelete(u)}
                            disabled={isSelf}
                          >
                            <TrashIcon />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>

          {showFilteredEmpty && (
            <div className="au-empty">
              <div className="au-empty-icon" aria-hidden="true">
                <SearchIcon />
              </div>
              <h3 className="au-empty-title">Nenhum usuário com esses filtros</h3>
              <p className="au-empty-text">
                Tente outra busca, troque a situação ou limpe os filtros para ver tudo.
              </p>
              <button type="button" className="btn btn-secondary" onClick={clearFilters}>
                Limpar filtros
              </button>
            </div>
          )}

          {showAbsoluteEmpty && (
            <div className="au-empty">
              <div className="au-empty-icon" aria-hidden="true">
                <PlusIcon />
              </div>
              <h3 className="au-empty-title">Nenhum usuário cadastrado</h3>
              <p className="au-empty-text">
                Crie o primeiro acesso e comunique a senha por fora (WhatsApp, email).
              </p>
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => { setFormError(null); setCreateOpen(true) }}
              >
                <PlusIcon /> Novo usuário
              </button>
            </div>
          )}
        </div>

        {!list.isLoading && data.length > 0 && (
          <div className="au-pagination">
            <span className="au-pagination-summary">
              {total} {total === 1 ? 'usuário' : 'usuários'}
            </span>
            <div className="au-pagination-nav">
              <button
                type="button"
                className="au-page-btn"
                disabled={page <= 1}
                onClick={() => setPage(p => Math.max(1, p - 1))}
                aria-label="Página anterior"
              >
                ‹
              </button>
              <span className="au-page-counter" aria-live="polite">
                Página <strong>{page}</strong> de {totalPages}
              </span>
              <button
                type="button"
                className="au-page-btn"
                disabled={page >= totalPages}
                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                aria-label="Próxima página"
              >
                ›
              </button>
            </div>
          </div>
        )}
      </div>

      {createOpen && (
        <UserFormModal
          mode="create"
          onSubmit={handleCreate}
          onClose={() => setCreateOpen(false)}
          error={formError}
          busy={createM.isPending}
        />
      )}
      {editing && (
        <UserFormModal
          mode="edit"
          initial={editing}
          onSubmit={handleEdit}
          onClose={() => setEditing(null)}
          error={formError}
          busy={updateM.isPending}
        />
      )}
      {resetting && (
        <ResetPasswordModal
          user={resetting}
          onSubmit={handleReset}
          onClose={() => setResetting(null)}
          error={formError}
          busy={resetM.isPending}
        />
      )}
      {welcomeResult && (
        <WelcomeSentModal
          result={welcomeResult}
          onClose={() => setWelcomeResult(null)}
        />
      )}
    </div>
  )
}

/**
 * WelcomeSentModal confirma o convite recém-emitido e entrega o link.
 *
 * Aparece mesmo quando o email FALHOU ou o SMTP está desligado — é justamente
 * aí que ele importa: o convite existe e é válido, e o admin precisa do link
 * pra mandar por fora. Esconder o link no erro deixaria o usuário criado sem
 * nenhum caminho de entrega.
 */
function WelcomeSentModal({ result, onClose }) {
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2200)
    return () => clearTimeout(t)
  }, [copied])

  const tone = {
    sent: {
      cls: 'is-ok',
      title: 'Boas-vindas enviadas',
      text: <>Enviamos o email para <strong>{result.email}</strong>. O link abaixo é o mesmo que ele recebeu.</>,
    },
    failed: {
      cls: 'is-warn',
      title: 'Convite criado, mas o email não saiu',
      text: <>O usuário foi criado e o link é válido — só o envio falhou{result.email_error ? ` (${result.email_error})` : ''}. Copie e mande por fora.</>,
    },
    disabled: {
      cls: 'is-warn',
      title: 'Convite criado — envio de email desligado',
      text: <>Não há SMTP configurado neste ambiente. Copie o link e entregue para <strong>{result.email}</strong>.</>,
    },
    unavailable: {
      cls: 'is-warn',
      title: 'Usuário criado sem boas-vindas',
      text: <>A funcionalidade está desabilitada no servidor (falta <code>WELCOME_ENC_KEY</code>). O usuário foi criado normalmente.</>,
    },
  }[result.email_status] ?? {
    cls: 'is-warn',
    title: 'Convite criado',
    text: <>Copie o link e entregue para <strong>{result.email}</strong>.</>,
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(result.link)
      setCopied(true)
    } catch { /* navegador sem permissão: o link está visível pra seleção manual */ }
  }

  return createPortal(
    <div className="confirm-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-labelledby="aw-title">
      <div className={`au-welcome-card ${tone.cls}`} onClick={e => e.stopPropagation()}>
        <div className="au-welcome-badge" aria-hidden="true">
          {tone.cls === 'is-ok' ? <BadgeCheckIcon /> : <BadgeAlertIcon />}
        </div>
        <h2 className="au-welcome-title" id="aw-title">{tone.title}</h2>
        <p className="au-welcome-text">{tone.text}</p>

        {result.link && (
          <div className="au-welcome-link">
            <code className="au-welcome-url">{result.link}</code>
            <button type="button" className="btn btn-secondary btn-sm" onClick={copy}>
              {copied ? 'Copiado' : 'Copiar'}
            </button>
          </div>
        )}

        {result.link && (
          <p className="au-welcome-note">
            O link não expira. Se vazar, use o botão de revogar na lista de usuários.
          </p>
        )}

        <div className="au-welcome-actions">
          {result.link && (
            <a className="btn btn-secondary" href={result.link} target="_blank" rel="noopener noreferrer">
              Ver a página
            </a>
          )}
          <button type="button" className="btn btn-primary" onClick={onClose}>Entendi</button>
        </div>
      </div>
    </div>,
    document.body
  )
}
