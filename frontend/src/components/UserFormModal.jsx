import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import RSelect from './RSelect'
import { useClients } from '../api/hooks'
import { generateStrongPassword } from '../utils/passwordGen'
import './UserFormModal.css'

const EMPTY = {
  role: 'client',
  // Carteira do Cliente: uma agência acessa vários clientes com um login só.
  // O caso normal continua sendo uma lista de um elemento.
  client_ids: [],
  name: '',
  email: '',
  phone: '',
  password: '',
  is_active: true,
  // Default LIGADO: mandar as boas-vindas é o caminho desejado; desmarcar é a
  // exceção (conta de serviço, usuário que já foi avisado por fora).
  send_welcome: true,
  // Espelham os DEFAULT das colunas (migrations 0037 e 0061). Só valem pra
  // admin — o formulário nem mostra as opções pra Cliente.
  receive_alert_emails: true,
  receive_post_sale_emails: false,
}

/* ── Helpers ─────────────────────────────────────────────── */
function getInitials(name, email) {
  const src = (name || '').trim() || email || ''
  if (!src) return '?'
  const words = src.split(/[\s.@_-]+/).filter(Boolean)
  if (words.length === 0) return '?'
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase()
  return (words[0][0] + words[words.length - 1][0]).toUpperCase()
}

function getAvatarHue(seed) {
  if (!seed) return 320
  let h = 0
  for (let i = 0; i < seed.length; i++) {
    h = ((h << 5) - h + seed.charCodeAt(i)) | 0
  }
  return Math.abs(h) % 360
}

/* ── Icons ───────────────────────────────────────────────── */
function ShieldIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M9 1.5L3 4v4.5c0 3.5 2.5 6.5 6 8 3.5-1.5 6-4.5 6-8V4l-6-2.5z" />
      <path d="M6.5 9l1.75 1.75L11.5 7.5" />
    </svg>
  )
}
function UserIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="9" cy="6" r="3" />
      <path d="M3 16c0-3 2.7-5 6-5s6 2 6 5" />
    </svg>
  )
}
function DiceIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="2" width="10" height="10" rx="2" />
      <circle cx="4.5" cy="4.5" r="0.6" fill="currentColor" />
      <circle cx="9.5" cy="4.5" r="0.6" fill="currentColor" />
      <circle cx="7"   cy="7"   r="0.6" fill="currentColor" />
      <circle cx="4.5" cy="9.5" r="0.6" fill="currentColor" />
      <circle cx="9.5" cy="9.5" r="0.6" fill="currentColor" />
    </svg>
  )
}
function EyeIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 7s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" />
      <circle cx="7" cy="7" r="1.6" />
    </svg>
  )
}
function EyeOffIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 7s2.5-4 6-4c1.4 0 2.6.5 3.6 1.2M13 7s-1 1.7-3 3M7 11c-3.5 0-6-4-6-4M1 1l12 12" />
    </svg>
  )
}

/* ── Modal ───────────────────────────────────────────────── */
export default function UserFormModal({ mode, initial, onSubmit, onClose, error, busy }) {
  const isEdit = mode === 'edit'
  const [v, setV] = useState(EMPTY)
  const [showPwd, setShowPwd] = useState(false)
  // Erro de validação local (não veio do servidor) — divide o mesmo slot do
  // erro do backend.
  const [localError, setLocalError] = useState(null)
  const clientsQ = useClients()

  useEffect(() => {
    if (isEdit && initial) {
      setV({
        role: initial.role === 'viewer' ? 'client' : 'admin',
        // client_ids é a carteira; client_id (o principal) é o fallback pra
        // resposta de um backend anterior à feature.
        client_ids: initial.client_ids?.length
          ? initial.client_ids
          : (initial.client_id ? [initial.client_id] : []),
        name: initial.name ?? '',
        email: initial.email,
        phone: initial.phone ?? '',
        password: '',
        is_active: initial.is_active,
        receive_alert_emails: initial.receive_alert_emails ?? true,
        receive_post_sale_emails: initial.receive_post_sale_emails ?? false,
      })
    } else {
      setV(EMPTY)
    }
  }, [mode, initial, isEdit])

  useEffect(() => {
    function onKey(e) {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  function set(k, val) { setLocalError(null); setV(prev => ({ ...prev, [k]: val })) }

  function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    // Cliente sem nenhum cliente vinculado é recusado pelo servidor; barramos
    // aqui pra mostrar o motivo no mesmo lugar dos outros erros do formulário.
    if (v.role === 'client' && v.client_ids.length === 0) {
      setLocalError('Selecione pelo menos um cliente vinculado.')
      return
    }
    setLocalError(null)
    const payload = {
      role: v.role,
      name: v.name.trim(),
      phone: v.phone.trim() || null,
    }
    // A carteira inteira vai em client_ids e SUBSTITUI a anterior. O servidor
    // mantém o cliente principal (client_id) se ele continuar na lista; se
    // saiu, o primeiro da lista assume.
    if (v.role === 'client') payload.client_ids = v.client_ids
    if (!isEdit) {
      payload.email = v.email.trim().toLowerCase()
      payload.password = v.password
      payload.send_welcome = v.send_welcome
    } else {
      payload.is_active = v.is_active
    }
    // Preferências de email só existem pra admin: as queries de destinatário
    // filtram por role, então mandá-las pra um Cliente seria gravar campo morto.
    if (v.role === 'admin') {
      payload.receive_alert_emails = v.receive_alert_emails
      payload.receive_post_sale_emails = v.receive_post_sale_emails
    }
    onSubmit(payload)
  }

  const clientOptions = (clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))
  const errorMsg = localError ?? (typeof error === 'string'
    ? error
    : (error?.error || error?.message || (error ? JSON.stringify(error) : null)))

  const initials = isEdit ? getInitials(v.name, v.email) : '+'
  const hue = isEdit ? getAvatarHue(v.email || v.name) : 332 // E-radios rose hue
  const avatarStyle = isEdit
    ? {
        background: `linear-gradient(135deg, oklch(70% 0.16 ${hue}) 0%, oklch(58% 0.18 ${hue}) 100%)`,
      }
    : undefined

  return createPortal(
    <div
      className="confirm-backdrop ufm-backdrop"
      onClick={busy ? undefined : onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="ufm-title"
    >
      <div
        className="ufm-card"
        onClick={e => e.stopPropagation()}
      >
        <header className="ufm-header">
          <div className={`ufm-avatar ${isEdit ? '' : 'ufm-avatar-new'}`} style={avatarStyle} aria-hidden="true">
            {initials}
          </div>
          <div className="ufm-header-text">
            <h2 id="ufm-title" className="ufm-title">
              {isEdit ? 'Editar usuário' : 'Novo usuário'}
            </h2>
            {isEdit && (
              <p className="ufm-subtitle" title={v.email}>{v.email}</p>
            )}
            {!isEdit && (
              <p className="ufm-subtitle">
                Defina o acesso. Com as boas-vindas ligadas, o sistema entrega
                as credenciais por email.
              </p>
            )}
          </div>
        </header>

        <form className="ufm-form" onSubmit={handleSubmit}>
          <fieldset className="ufm-fieldset" disabled={busy}>
            <legend className="ufm-legend">Tipo de acesso</legend>
            <div className="ufm-roles">
              <label className={`ufm-role ${v.role === 'admin' ? 'is-checked' : ''}`}>
                <input
                  type="radio"
                  name="role"
                  value="admin"
                  checked={v.role === 'admin'}
                  onChange={() => set('role', 'admin')}
                  disabled={isEdit}
                />
                <span className="ufm-role-icon"><ShieldIcon /></span>
                <span className="ufm-role-text">
                  <span className="ufm-role-name">Administrador</span>
                  <span className="ufm-role-hint">Acesso total ao Radiocheck.</span>
                </span>
              </label>
              <label className={`ufm-role ${v.role === 'client' ? 'is-checked' : ''}`}>
                <input
                  type="radio"
                  name="role"
                  value="client"
                  checked={v.role === 'client'}
                  onChange={() => set('role', 'client')}
                  disabled={isEdit}
                />
                <span className="ufm-role-icon"><UserIcon /></span>
                <span className="ufm-role-text">
                  <span className="ufm-role-name">Cliente</span>
                  <span className="ufm-role-hint">Vê apenas o cliente vinculado.</span>
                </span>
              </label>
            </div>
            {isEdit && (
              <p className="ufm-hint">O tipo de acesso é definido na criação e não pode ser alterado.</p>
            )}
          </fieldset>

          {v.role === 'client' && (
            <div className="field ufm-client-field">
              <label htmlFor="ufm-client">Clientes vinculados *</label>
              <RSelect
                inputId="ufm-client"
                isMulti
                value={clientOptions.filter(o => v.client_ids.includes(o.value))}
                options={clientOptions}
                onChange={opts => set('client_ids', (opts ?? []).map(o => o.value))}
                placeholder="Selecione um ou mais clientes"
                closeMenuOnSelect={false}
                isClearable
                isDisabled={busy}
              />
            </div>
          )}

          <div className="ufm-grid">
            <div className="field">
              <label htmlFor="ufm-name">Nome *</label>
              <input
                id="ufm-name"
                className="input"
                value={v.name}
                onChange={e => set('name', e.target.value)}
                placeholder="Como aparece no painel"
                required
                disabled={busy}
                autoComplete="off"
              />
            </div>

            <div className="field">
              <label htmlFor="ufm-phone">Telefone</label>
              <input
                id="ufm-phone"
                className="input"
                value={v.phone}
                onChange={e => set('phone', e.target.value)}
                placeholder="(11) 91234-5678"
                disabled={busy}
                autoComplete="off"
              />
            </div>
          </div>

          <div className="field">
            <label htmlFor="ufm-email">Email *</label>
            <input
              id="ufm-email"
              className="input"
              type="email"
              value={v.email}
              disabled={isEdit || busy}
              onChange={e => set('email', e.target.value)}
              required={!isEdit}
              placeholder="nome@empresa.com.br"
              autoComplete="off"
            />
            {isEdit && (
              <p className="ufm-hint">Email é imutável após o cadastro.</p>
            )}
          </div>

          {!isEdit && (
            <div className="field">
              <label htmlFor="ufm-pwd">Senha *</label>
              <div className="ufm-pwd">
                <input
                  id="ufm-pwd"
                  className="input ufm-pwd-input"
                  type={showPwd ? 'text' : 'password'}
                  value={v.password}
                  onChange={e => set('password', e.target.value)}
                  required
                  minLength={12}
                  placeholder="Mínimo 12 caracteres"
                  disabled={busy}
                  autoComplete="new-password"
                />
                <button
                  type="button"
                  className="ufm-pwd-btn"
                  onClick={() => setShowPwd(s => !s)}
                  disabled={busy}
                  aria-label={showPwd ? 'Ocultar senha' : 'Mostrar senha'}
                  title={showPwd ? 'Ocultar' : 'Mostrar'}
                >
                  {showPwd ? <EyeOffIcon /> : <EyeIcon />}
                </button>
                <button
                  type="button"
                  className="ufm-pwd-btn ufm-pwd-btn-generate"
                  onClick={() => { set('password', generateStrongPassword(16)); setShowPwd(true) }}
                  disabled={busy}
                  title="Gerar senha de 16 caracteres"
                >
                  <DiceIcon />
                  <span>Gerar</span>
                </button>
              </div>
              <p className="ufm-hint">
                12+ caracteres. O botão Gerar cria uma senha forte de 16 caracteres.
              </p>
            </div>
          )}

          {!isEdit && (
            <label className="ufm-toggle">
              <input
                type="checkbox"
                checked={v.send_welcome}
                onChange={e => set('send_welcome', e.target.checked)}
                disabled={busy}
              />
              <span className="ufm-toggle-text">
                <strong>Enviar boas-vindas</strong>
                <small>
                  Manda um email com um link para a página de boas-vindas, onde
                  o novo usuário vê estas credenciais e um tutorial da plataforma.
                </small>
              </span>
            </label>
          )}

          {isEdit && (
            <label className="ufm-toggle">
              <input
                type="checkbox"
                checked={v.is_active}
                onChange={e => set('is_active', e.target.checked)}
                disabled={busy}
              />
              <span className="ufm-toggle-text">
                <strong>Conta ativa</strong>
                <small>Quando desativada, o login é bloqueado mas o histórico continua acessível.</small>
              </span>
            </label>
          )}

          {v.role === 'admin' && (
            <div className="ufm-group">
              <p className="ufm-legend">Emails que este admin recebe</p>

              <label className="ufm-toggle">
                <input
                  type="checkbox"
                  checked={v.receive_alert_emails}
                  onChange={e => set('receive_alert_emails', e.target.checked)}
                  disabled={busy}
                />
                <span className="ufm-toggle-text">
                  <strong>Alertas diários</strong>
                  <small>Disparos das 8h: campanhas iniciando/terminando, sem material e emissoras fora do ar.</small>
                </span>
              </label>

              <label className="ufm-toggle">
                <input
                  type="checkbox"
                  checked={v.receive_post_sale_emails}
                  onChange={e => set('receive_post_sale_emails', e.target.checked)}
                  disabled={busy}
                />
                <span className="ufm-toggle-text">
                  <strong>Pós-venda dos clientes</strong>
                  <small>
                    Uma cópia de todo pós-venda enviado, de qualquer cliente, com
                    o mesmo link pessoal que o cliente recebe.
                  </small>
                </span>
              </label>
            </div>
          )}

          {errorMsg && (
            <div className="ufm-error" role="alert">{errorMsg}</div>
          )}

          <div className="ufm-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose} disabled={busy}>
              Cancelar
            </button>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? 'Salvando…' : isEdit ? 'Salvar alterações' : 'Criar usuário'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  )
}
