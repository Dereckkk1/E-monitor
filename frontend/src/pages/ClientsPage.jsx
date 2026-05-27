import { useState, useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import {
  useClientsPaged, useCreateClient, useUpdateClient, useDeleteClient,
  useDeactivateClient, useActivateClient,
} from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import WebhookModal from '../components/WebhookModal'
import AirtimePaginator from '../components/AirtimePaginator'

const CLIENTS_PAGE_SIZE = 20

const EMPTY_FORM = {
  name: '',
  logo_url: '',
  contact_email: '',
  contact_name: '',
  phone: '',
  cnpj: '',
  cep: '',
  city: '',
  state: '',
}

function nullify(form) {
  return Object.fromEntries(
    Object.entries(form).map(([k, v]) => [k, v.trim() === '' ? null : v.trim()])
  )
}

function formatCNPJ(v) {
  const d = v.replace(/\D/g, '').slice(0, 14)
  if (d.length <= 2) return d
  if (d.length <= 5) return `${d.slice(0,2)}.${d.slice(2)}`
  if (d.length <= 8) return `${d.slice(0,2)}.${d.slice(2,5)}.${d.slice(5)}`
  if (d.length <= 12) return `${d.slice(0,2)}.${d.slice(2,5)}.${d.slice(5,8)}/${d.slice(8)}`
  return `${d.slice(0,2)}.${d.slice(2,5)}.${d.slice(5,8)}/${d.slice(8,12)}-${d.slice(12)}`
}

function formatCEP(v) {
  const d = v.replace(/\D/g, '').slice(0, 8)
  if (d.length <= 5) return d
  return `${d.slice(0,5)}-${d.slice(5)}`
}

function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M7 2v10M2 7h10" />
    </svg>
  )
}

function EditIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9.5 2.5l2 2L4 12H2v-2L9.5 2.5z" />
    </svg>
  )
}

function TrashIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 3.5h10M5.5 3.5V2.5h3v1M3 3.5l.75 8h6.5L11 3.5M5.5 6v4M8.5 6v4" />
    </svg>
  )
}

function ReactivateIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M11.5 7a4.5 4.5 0 1 1-1.32-3.18" />
      <path d="M11.5 1.5V4H9" />
    </svg>
  )
}

function MailIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="1" y="3" width="10" height="7" rx="1.5" />
      <path d="M1 4l5 3.5L11 4" />
    </svg>
  )
}

function PhoneIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M10.5 8.5l-2-2-1.5 1.5C5.5 7.5 4.5 6.5 4 5l1.5-1.5-2-2-2 2c0 4 3.5 7.5 7.5 7.5l2-2z" />
    </svg>
  )
}

function PinIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 1C4.343 1 3 2.343 3 4c0 2.5 3 7 3 7s3-4.5 3-7c0-1.657-1.343-3-3-3z" /><circle cx="6" cy="4" r="1" />
    </svg>
  )
}

function ApiKeyIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="4" cy="9" r="2.5" />
      <path d="M6 7.5l5-5M9 4l1.5 1.5M11 2.5l1.5 1.5" />
    </svg>
  )
}

function DeliveriesIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 3h10M2 7h10M2 11h6" />
      <circle cx="11" cy="11" r="1.25" fill="currentColor" stroke="none" />
    </svg>
  )
}

function WebhookIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="4" cy="3.5" r="1.5" />
      <circle cx="10" cy="3.5" r="1.5" />
      <circle cx="7" cy="10.5" r="1.5" />
      <path d="M5 4.5l1.5 4.5M9 4.5L7.5 9M5.5 3.5h3" />
    </svg>
  )
}

function ClientFormModal({ initial, onClose, onSave, isSaving, isError }) {
  const [form, setForm] = useState(
    initial
      ? {
          name:          initial.name         ?? '',
          logo_url:      initial.logo_url      ?? '',
          contact_email: initial.contact_email ?? '',
          contact_name:  initial.contact_name  ?? '',
          phone:         initial.phone         ?? '',
          cnpj:          initial.cnpj          ?? '',
          cep:           initial.cep           ?? '',
          city:          initial.city          ?? '',
          state:         initial.state         ?? '',
        }
      : EMPTY_FORM
  )

  function setF(k, v) { setForm(f => ({ ...f, [k]: v })) }

  function handleSubmit(e) {
    e.preventDefault()
    onSave(nullify(form))
  }

  const isEdit = !!initial

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 520 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>{isEdit ? 'Editar cliente' : 'Novo cliente'}</h3>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>
        <form onSubmit={handleSubmit} className="modal-body">
          <div className="field">
            <label>Nome *</label>
            <input
              className="input"
              value={form.name}
              onChange={e => setF('name', e.target.value)}
              placeholder="Nome do cliente"
              required
              autoFocus
            />
          </div>

          <div className="field">
            <label>URL do logo</label>
            <input
              className="input"
              type="url"
              value={form.logo_url}
              onChange={e => setF('logo_url', e.target.value)}
              placeholder="https://exemplo.com/logo.png"
            />
          </div>

          <div className="form-row">
            <div className="field">
              <label>CNPJ</label>
              <input
                className="input"
                value={form.cnpj}
                onChange={e => setF('cnpj', formatCNPJ(e.target.value))}
                placeholder="00.000.000/0000-00"
              />
            </div>
            <div className="field">
              <label>CEP</label>
              <input
                className="input"
                value={form.cep}
                onChange={e => setF('cep', formatCEP(e.target.value))}
                placeholder="00000-000"
              />
            </div>
          </div>

          <div className="form-row">
            <div className="field" style={{ flex: 2 }}>
              <label>Cidade</label>
              <input
                className="input"
                value={form.city}
                onChange={e => setF('city', e.target.value)}
                placeholder="São Paulo"
              />
            </div>
            <div className="field" style={{ flex: '0 0 72px' }}>
              <label>UF</label>
              <input
                className="input"
                maxLength={2}
                value={form.state}
                onChange={e => setF('state', e.target.value.toUpperCase())}
                placeholder="SP"
              />
            </div>
          </div>

          <div className="form-row">
            <div className="field">
              <label>Telefone</label>
              <input
                className="input"
                type="tel"
                value={form.phone}
                onChange={e => setF('phone', e.target.value)}
                placeholder="(11) 99999-9999"
              />
            </div>
            <div className="field">
              <label>E-mail de contato</label>
              <input
                className="input"
                type="email"
                value={form.contact_email}
                onChange={e => setF('contact_email', e.target.value)}
                placeholder="contato@empresa.com"
              />
            </div>
          </div>

          <div className="field">
            <label>Nome do responsável</label>
            <input
              className="input"
              value={form.contact_name}
              onChange={e => setF('contact_name', e.target.value)}
              placeholder="Nome do contato principal"
            />
          </div>

          {isError && (
            <p className="text-error">Erro ao salvar. Tente novamente.</p>
          )}

          <div className="modal-footer">
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancelar
            </button>
            <button type="submit" className="btn btn-primary" disabled={isSaving}>
              {isSaving ? 'Salvando…' : isEdit ? 'Salvar alterações' : 'Criar cliente'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// Search-only filter bar for /clients. Single text input with a leading
// magnifying glass + soft "Limpar" affordance that appears once the user
// has typed something. Visually matches the .stations-search pattern used
// elsewhere so the page reads as part of the same product.
function ClientsFilters({ search, onSearchChange, onClear, showInactive, onToggleInactive }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 10,
      marginBottom: 16, flexWrap: 'wrap',
    }}>
      <div className="stations-search" style={{ flex: '1 1 280px', maxWidth: 420 }}>
        <span className="stations-search-icon">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
            <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
          </svg>
        </span>
        <input
          className="input stations-search-input"
          type="text"
          placeholder="Buscar por nome, CNPJ, cidade, e-mail…"
          value={search}
          onChange={e => onSearchChange(e.target.value)}
        />
      </div>
      {!!search && (
        <button
          type="button"
          onClick={onClear}
          style={{
            height: 38, padding: '0 14px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-surface)', border: '1px solid var(--c-border)',
            color: 'var(--c-text-2)', fontSize: 12, fontWeight: 600,
            cursor: 'pointer', fontFamily: 'var(--font-body)',
          }}
        >
          Limpar
        </button>
      )}
      <button
        type="button"
        onClick={onToggleInactive}
        aria-pressed={showInactive}
        title={showInactive ? 'Ocultar clientes inativos' : 'Mostrar clientes inativos'}
        style={{
          height: 38, padding: '0 14px', borderRadius: 'var(--radius-md)',
          background: showInactive ? 'var(--c-action)' : 'var(--c-surface)',
          border: `1px solid ${showInactive ? 'var(--c-action)' : 'var(--c-border)'}`,
          color: showInactive ? '#fff' : 'var(--c-text-2)',
          fontSize: 12, fontWeight: 600, cursor: 'pointer',
          fontFamily: 'var(--font-body)', whiteSpace: 'nowrap',
        }}
      >
        Mostrar inativos
      </button>
    </div>
  )
}

function FilteredClientsEmptyState({ onClear }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center',
      gap: 14, padding: '64px 32px', textAlign: 'center',
      background: 'var(--c-surface)', border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-xl)',
    }}>
      <div style={{
        width: 56, height: 56, borderRadius: 'var(--radius-lg)',
        background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
        display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
        boxShadow: '0 0 0 6px rgba(100, 116, 139, 0.04)',
      }}>
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="11" cy="11" r="7" />
          <path d="m21 21-4.3-4.3" />
        </svg>
      </div>
      <h3 style={{
        fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 20,
        color: 'var(--c-text)', margin: 0, letterSpacing: '-0.01em',
      }}>Nenhum cliente encontrado</h3>
      <p style={{
        margin: 0, color: 'var(--c-text-2)', fontSize: 14, lineHeight: 1.5,
        maxWidth: 380,
      }}>
        Nenhum cliente corresponde à sua busca. Tente outro termo ou limpe pra ver todos.
      </p>
      <button
        type="button"
        onClick={onClear}
        style={{
          marginTop: 4,
          padding: '10px 18px', borderRadius: 'var(--radius-md)',
          background: 'var(--c-action)', color: '#fff',
          border: '1px solid var(--c-action)',
          fontSize: 13, fontWeight: 600, cursor: 'pointer',
          fontFamily: 'var(--font-body)',
        }}
      >
        Limpar busca
      </button>
    </div>
  )
}

function ClientsEmptyState({ onAdd }) {
  const mockClients = [
    { name: 'Anunciante Exemplo', city: 'São Paulo', state: 'SP', cnpj: '12.345.678/0001-99', contact_email: 'contato@ex.com' },
    { name: 'Marca Nacional', city: 'Rio de Janeiro', state: 'RJ', cnpj: '98.765.432/0001-11' },
    { name: 'Agência Digital', city: 'Belo Horizonte', state: 'MG' },
  ]

  return (
    <div className="clients-empty">
      <div className="clients-empty-action">
        <svg width="56" height="56" viewBox="0 0 56 56" fill="none" aria-hidden="true" style={{ color: 'var(--c-action)' }}>
          <circle cx="28" cy="18" r="10" fill="currentColor" opacity="0.12" />
          <ellipse cx="28" cy="40" rx="18" ry="10" fill="currentColor" opacity="0.10" />
          <circle cx="28" cy="18" r="7" stroke="currentColor" strokeWidth="2.5" />
          <path d="M12 46c0-8.837 7.163-16 16-16s16 7.163 16 16" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
        <h3>Nenhum cliente cadastrado</h3>
        <p>Cadastre anunciantes e agências para vincular nas campanhas.</p>
        <button className="btn btn-primary btn-sm" onClick={onAdd}>
          <PlusIcon /> Novo cliente
        </button>
      </div>

      <div className="clients-empty-preview" aria-hidden="true">
        <div className="clients-list clients-list-ghost">
          {mockClients.map((c, i) => (
            <div key={i} className="client-row">
              <StationAvatar station={c} size={40} />
              <div className="client-row-main">
                <div className="client-row-name">{c.name}</div>
                {(c.city || c.state) && (
                  <div className="client-row-sub">
                    <PinIcon />
                    {[c.city, c.state].filter(Boolean).join(', ')}
                  </div>
                )}
              </div>
              <div className="client-row-chips">
                {c.cnpj && <span className="client-chip">{c.cnpj}</span>}
                {c.contact_email && <span className="client-chip"><MailIcon />{c.contact_email}</span>}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function ClientRowSkeleton() {
  return (
    <div className="clients-list">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="client-row">
          <div className="skeleton" style={{ width: 40, height: 40, borderRadius: 8, flexShrink: 0 }} />
          <div className="client-row-main">
            <div className="skeleton" style={{ height: 13, width: '45%', borderRadius: 4, marginBottom: 6 }} />
            <div className="skeleton" style={{ height: 11, width: '28%', borderRadius: 4 }} />
          </div>
          <div className="client-row-chips" style={{ gap: 4 }}>
            <div className="skeleton" style={{ height: 20, width: 120, borderRadius: 10 }} />
          </div>
          <div className="client-row-actions">
            <div className="skeleton" style={{ height: 28, width: 28, borderRadius: 6 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

export default function ClientsPage() {
  // Two-tier search state — see CampaignsPage for the rationale: typing
  // shouldn't refire the query (or rebuild the page) on every keystroke.
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  // Inativos ficam escondidos por padrão; o toggle os traz pra poder reativar.
  const [showInactive, setShowInactive] = useState(false)
  const { data: pagedResp, isLoading, isFetching } = useClientsPaged({
    q: search, page, pageSize: CLIENTS_PAGE_SIZE, includeInactive: showInactive,
  })
  const clients     = pagedResp?.data ?? []
  const total       = pagedResp?.total ?? 0
  const totalPages  = pagedResp?.total_pages ?? 1

  const debounceRef = useRef(null)
  function changeSearch(v) {
    setSearchInput(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      setSearch(v)
      setPage(1)
    }, 300)
  }
  useEffect(() => () => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
  }, [])
  function clearSearch() {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    setSearchInput('')
    setSearch('')
    setPage(1)
  }

  const createClient = useCreateClient()
  const updateClient = useUpdateClient()
  const deleteClient = useDeleteClient()
  const deactivateClient = useDeactivateClient()
  const activateClient = useActivateClient()

  const [creating, setCreating] = useState(false)
  const [editing, setEditing]   = useState(null)
  const [webhookFor, setWebhookFor] = useState(null)

  // total === 0 + no search = real empty catalog. total === 0 + search =
  // filter excluded everything (different empty state with "Limpar busca").
  // Both decisions need isLoading to be false so we don't flash the empty
  // state during the first fetch.
  const initialEmpty  = !search && total === 0 && !isLoading
  const filteredEmpty = !!search && total === 0 && !isLoading

  function handleCreate(data) {
    createClient.mutate(data, { onSuccess: () => setCreating(false) })
  }

  function handleUpdate(data) {
    updateClient.mutate({ id: editing.id, ...data }, { onSuccess: () => setEditing(null) })
  }

  async function handleDelete(c) {
    if (!await window.confirm(`Excluir o cliente "${c.name}"? Essa ação não pode ser desfeita.`)) return
    try {
      await deleteClient.mutateAsync(c.id)
    } catch (err) {
      if (err?.response?.status === 409) {
        // Cliente tem vínculos (campanhas/materiais/usuários). Em vez de
        // falhar mudo, explica o que bloqueia e oferece desativar.
        const d = err.response.data || {}
        const parts = []
        if (d.campaigns) parts.push(`${d.campaigns} campanha${d.campaigns > 1 ? 's' : ''}`)
        if (d.materials) parts.push(`${d.materials} ${d.materials > 1 ? 'materiais' : 'material'}`)
        if (d.users) parts.push(`${d.users} usuário${d.users > 1 ? 's' : ''}`)
        const vinc = parts.length ? parts.join(', ') : 'registros vinculados'
        const ok = await window.confirm(
          `Não dá pra excluir "${c.name}": tem ${vinc}.\n\n` +
          `Deseja DESATIVAR o cliente? Ele some da lista e os usuários dele não ` +
          `conseguem mais entrar, mas os dados ficam preservados e dá pra reativar depois.`
        )
        if (ok) {
          try {
            await deactivateClient.mutateAsync(c.id)
          } catch {
            window.alert('Não foi possível desativar o cliente. Tente novamente.')
          }
        }
      } else {
        window.alert('Não foi possível excluir o cliente. Tente novamente.')
      }
    }
  }

  async function handleReactivate(c) {
    if (!await window.confirm(`Reativar o cliente "${c.name}"? Os usuários dele voltam a conseguir entrar.`)) return
    try {
      await activateClient.mutateAsync(c.id)
    } catch {
      window.alert('Não foi possível reativar o cliente. Tente novamente.')
    }
  }

  function toggleInactive() {
    setShowInactive(v => !v)
    setPage(1)
  }

  return (
    <div>
      <div className="page-header">
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
          <h2>Clientes</h2>
          {total > 0 && (
            <span className="text-muted" style={{ fontSize: 13, fontWeight: 400 }}>
              {total}
            </span>
          )}
        </div>
        <button className="btn btn-primary" onClick={() => setCreating(true)}>
          <PlusIcon /> Novo cliente
        </button>
      </div>

      {/* Sempre renderizado (mesmo no estado vazio) pra que o toggle "mostrar
          inativos" continue acessível quando todos os clientes estão desativados
          — senão não haveria como reativá-los. */}
      <ClientsFilters
        search={searchInput}
        onSearchChange={changeSearch}
        onClear={clearSearch}
        showInactive={showInactive}
        onToggleInactive={toggleInactive}
      />

      {isLoading ? (
        <ClientRowSkeleton />
      ) : initialEmpty ? (
        <ClientsEmptyState onAdd={() => setCreating(true)} />
      ) : filteredEmpty ? (
        <FilteredClientsEmptyState onClear={clearSearch} />
      ) : (
        <div className="clients-list" style={{ opacity: isFetching ? 0.7 : 1, transition: 'opacity 150ms' }}>
          {clients.map(c => {
            const loc = [c.city, c.state].filter(Boolean).join(', ')
            // Inativo SÓ quando is_active === false explícito. Campo ausente
            // (API antiga, sem a coluna) ou true = ativo — bate com o DEFAULT
            // TRUE da coluna no banco e evita marcar todo mundo como inativo
            // quando o backend ainda não foi migrado.
            const inactive = c.is_active === false
            return (
              <div key={c.id} className="client-row" style={inactive ? { opacity: 0.62 } : undefined}>
                <StationAvatar station={{ name: c.name, logo_url: c.logo_url }} size={40} />

                <div className="client-row-main">
                  <div className="client-row-name" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    {c.name}
                    {inactive && (
                      <span style={{
                        fontSize: 10, fontWeight: 700, letterSpacing: '0.04em',
                        textTransform: 'uppercase', padding: '2px 7px',
                        borderRadius: 999, color: 'var(--c-text-2)',
                        background: 'var(--c-surface-2)', border: '1px solid var(--c-border)',
                      }}>Inativo</span>
                    )}
                  </div>
                  {(loc || c.contact_name) && (
                    <div className="client-row-sub">
                      {loc && <><PinIcon />{loc}</>}
                      {c.contact_name && loc && <span className="client-row-dot">·</span>}
                      {c.contact_name && <span>{c.contact_name}</span>}
                    </div>
                  )}
                </div>

                <div className="client-row-chips">
                  {c.cnpj && <span className="client-chip">{c.cnpj}</span>}
                  {c.contact_email && (
                    <span className="client-chip">
                      <MailIcon />{c.contact_email}
                    </span>
                  )}
                  {c.phone && (
                    <span className="client-chip">
                      <PhoneIcon />{c.phone}
                    </span>
                  )}
                </div>

                <div className="client-row-actions">
                  <button
                    className="btn-icon btn-secondary"
                    style={{ borderRadius: 'var(--radius-md)' }}
                    title="Webhook (configurar)"
                    onClick={() => setWebhookFor(c)}
                  >
                    <WebhookIcon />
                  </button>
                  <Link
                    className="btn-icon btn-secondary"
                    style={{ borderRadius: 'var(--radius-md)' }}
                    title="Entregas de webhook"
                    to={`/clients/${c.id}/webhooks`}
                  >
                    <DeliveriesIcon />
                  </Link>
                  <Link
                    className="btn-icon btn-secondary"
                    style={{ borderRadius: 'var(--radius-md)' }}
                    title="API Keys"
                    to={`/clients/${c.id}/api-keys`}
                  >
                    <ApiKeyIcon />
                  </Link>
                  <button
                    className="btn-icon btn-secondary"
                    style={{ borderRadius: 'var(--radius-md)' }}
                    title="Editar"
                    onClick={() => setEditing(c)}
                  >
                    <EditIcon />
                  </button>
                  {inactive ? (
                    <button
                      className="btn-icon btn-secondary"
                      style={{ borderRadius: 'var(--radius-md)' }}
                      title="Reativar"
                      onClick={() => handleReactivate(c)}
                      disabled={activateClient.isPending}
                    >
                      <ReactivateIcon />
                    </button>
                  ) : (
                    <button
                      className="btn-icon btn-danger-ghost"
                      style={{ borderRadius: 'var(--radius-md)' }}
                      title="Excluir"
                      onClick={() => handleDelete(c)}
                      disabled={deleteClient.isPending}
                    >
                      <TrashIcon />
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {!initialEmpty && !filteredEmpty && total > 0 && (
        <AirtimePaginator
          page={page}
          totalPages={totalPages}
          total={total}
          pageSize={CLIENTS_PAGE_SIZE}
          onChange={setPage}
          singular="cliente"
          plural="clientes"
        />
      )}

      {creating && (
        <ClientFormModal
          initial={null}
          onClose={() => { setCreating(false); createClient.reset?.() }}
          onSave={handleCreate}
          isSaving={createClient.isPending}
          isError={createClient.isError}
        />
      )}

      {editing && (
        <ClientFormModal
          initial={editing}
          onClose={() => { setEditing(null); updateClient.reset?.() }}
          onSave={handleUpdate}
          isSaving={updateClient.isPending}
          isError={updateClient.isError}
        />
      )}

      {webhookFor && (
        <WebhookModal
          client={webhookFor}
          onClose={() => setWebhookFor(null)}
        />
      )}
    </div>
  )
}
