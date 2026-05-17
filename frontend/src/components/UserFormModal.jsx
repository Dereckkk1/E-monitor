import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import RSelect from './RSelect'
import { useClients } from '../api/hooks'
import { generateStrongPassword } from '../utils/passwordGen'

const ROLE_OPTIONS = [
  { value: 'admin',  label: 'Administrador' },
  { value: 'client', label: 'Cliente' },
]

const EMPTY = {
  role: 'client',
  client_id: null,
  name: '',
  email: '',
  phone: '',
  password: '',
  is_active: true,
}

// Modal de criar/editar usuário.
//
// Props:
//   mode      — 'create' | 'edit'
//   initial   — em edit: a linha de users.User. Em create: ignorado.
//   onSubmit(payload) — chamado no submit. Payload já está no shape do backend.
//   onClose() — fecha o modal.
//   error     — string|object de erro do backend (mostrado embaixo do form).
//   busy      — quando true, desabilita os botões e mostra "Salvando…".
export default function UserFormModal({ mode, initial, onSubmit, onClose, error, busy }) {
  const isEdit = mode === 'edit'
  const [v, setV] = useState(EMPTY)
  const [showPwd, setShowPwd] = useState(false)
  const clientsQ = useClients()

  useEffect(() => {
    if (isEdit && initial) {
      setV({
        role: initial.role === 'viewer' ? 'client' : 'admin',
        client_id: initial.client_id ?? null,
        name: initial.name ?? '',
        email: initial.email,
        phone: initial.phone ?? '',
        password: '',
        is_active: initial.is_active,
      })
    } else {
      setV(EMPTY)
    }
  }, [mode, initial, isEdit])

  function set(k, val) { setV(prev => ({ ...prev, [k]: val })) }

  function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    const payload = {
      role: v.role,
      name: v.name.trim(),
      phone: v.phone.trim() || null,
    }
    if (v.role === 'client') payload.client_id = v.client_id
    if (!isEdit) {
      payload.email = v.email.trim().toLowerCase()
      payload.password = v.password
    } else {
      payload.is_active = v.is_active
    }
    onSubmit(payload)
  }

  const clientOptions = (clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))
  const errorMsg = typeof error === 'string'
    ? error
    : (error?.error || error?.message || (error ? JSON.stringify(error) : null))

  return createPortal(
    <div className="confirm-backdrop" onClick={busy ? undefined : onClose} role="dialog" aria-modal="true">
      <div
        className="confirm-card"
        onClick={e => e.stopPropagation()}
        style={{ maxWidth: 480, width: 'min(480px, 100%)' }}
      >
        <div style={{ padding: '24px 28px 8px' }}>
          <h2 style={{ margin: 0, fontFamily: 'var(--font-heading)', fontSize: 20, color: 'var(--c-text)' }}>
            {isEdit ? 'Editar usuário' : 'Novo usuário'}
          </h2>
        </div>

        <form onSubmit={handleSubmit} style={{ padding: '8px 28px 24px', display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div className="field">
            <label>Tipo</label>
            <RSelect
              value={ROLE_OPTIONS.find(o => o.value === v.role)}
              options={ROLE_OPTIONS}
              onChange={o => set('role', o.value)}
              isDisabled={busy}
            />
          </div>

          {v.role === 'client' && (
            <div className="field">
              <label>Cliente vinculado *</label>
              <RSelect
                value={clientOptions.find(o => o.value === v.client_id) ?? null}
                options={clientOptions}
                onChange={o => set('client_id', o?.value ?? null)}
                placeholder="Selecione um cliente"
                isClearable
                isDisabled={busy}
              />
            </div>
          )}

          <div className="field">
            <label>Nome *</label>
            <input
              className="input"
              value={v.name}
              onChange={e => set('name', e.target.value)}
              required
              disabled={busy}
            />
          </div>

          <div className="field">
            <label>Email *</label>
            <input
              className="input"
              type="email"
              value={v.email}
              disabled={isEdit || busy}
              onChange={e => set('email', e.target.value)}
              required={!isEdit}
            />
            {isEdit && <div className="field-hint">Email é imutável após o cadastro.</div>}
          </div>

          <div className="field">
            <label>Telefone</label>
            <input
              className="input"
              value={v.phone}
              onChange={e => set('phone', e.target.value)}
              placeholder="(11) 91234-5678"
              disabled={busy}
            />
          </div>

          {!isEdit && (
            <div className="field">
              <label>Senha * (mín 12 caracteres)</label>
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  className="input"
                  type={showPwd ? 'text' : 'password'}
                  value={v.password}
                  onChange={e => set('password', e.target.value)}
                  required
                  minLength={12}
                  style={{ flex: 1 }}
                  disabled={busy}
                />
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => setShowPwd(s => !s)}
                  disabled={busy}
                >
                  {showPwd ? 'Ocultar' : 'Mostrar'}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => { set('password', generateStrongPassword(16)); setShowPwd(true) }}
                  disabled={busy}
                >
                  Gerar
                </button>
              </div>
            </div>
          )}

          {isEdit && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, color: 'var(--c-text)' }}>
              <input
                type="checkbox"
                checked={v.is_active}
                onChange={e => set('is_active', e.target.checked)}
                disabled={busy}
              />
              <span>Conta ativa (login permitido)</span>
            </label>
          )}

          {errorMsg && (
            <div style={{
              padding: '10px 12px',
              borderRadius: 'var(--radius-md, 8px)',
              background: '#fee2e2',
              color: '#991b1b',
              fontSize: 13,
            }}>{errorMsg}</div>
          )}

          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
            <button type="button" className="btn btn-secondary" onClick={onClose} disabled={busy}>Cancelar</button>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? 'Salvando…' : isEdit ? 'Salvar' : 'Criar'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  )
}
