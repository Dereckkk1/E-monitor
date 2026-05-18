import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { generateStrongPassword } from '../utils/passwordGen'
import './UserFormModal.css'

function KeyBigIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor"
      strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="7" cy="13" r="3" />
      <path d="M9.5 10.5l7-7M14 5l1.5 1.5M16 3l2 2" />
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

export default function ResetPasswordModal({ user, onSubmit, onClose, error, busy }) {
  const [pwd, setPwd] = useState('')
  const [show, setShow] = useState(false)

  useEffect(() => {
    function onKey(e) {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    onSubmit(pwd)
  }

  const errorMsg = typeof error === 'string'
    ? error
    : (error?.error || error?.message || (error ? JSON.stringify(error) : null))

  return createPortal(
    <div
      className="confirm-backdrop ufm-backdrop"
      onClick={busy ? undefined : onClose}
      role="dialog"
      aria-modal="true"
      aria-labelledby="rpm-title"
    >
      <div className="rpm-card" onClick={e => e.stopPropagation()}>
        <header className="rpm-header">
          <div className="rpm-icon" aria-hidden="true"><KeyBigIcon /></div>
          <div>
            <h2 id="rpm-title" className="rpm-title">Resetar senha</h2>
            <p className="rpm-target">
              Para <strong>{user.email}</strong>
            </p>
          </div>
        </header>

        <form className="rpm-form" onSubmit={handleSubmit}>
          <div className="field">
            <label htmlFor="rpm-pwd">Nova senha *</label>
            <div className="ufm-pwd">
              <input
                id="rpm-pwd"
                className="input ufm-pwd-input"
                type={show ? 'text' : 'password'}
                value={pwd}
                onChange={e => setPwd(e.target.value)}
                minLength={12}
                required
                placeholder="Mínimo 12 caracteres"
                disabled={busy}
                autoFocus
                autoComplete="new-password"
              />
              <button
                type="button"
                className="ufm-pwd-btn"
                onClick={() => setShow(s => !s)}
                disabled={busy}
                aria-label={show ? 'Ocultar senha' : 'Mostrar senha'}
                title={show ? 'Ocultar' : 'Mostrar'}
              >
                {show ? <EyeOffIcon /> : <EyeIcon />}
              </button>
              <button
                type="button"
                className="ufm-pwd-btn ufm-pwd-btn-generate"
                onClick={() => { setPwd(generateStrongPassword(16)); setShow(true) }}
                disabled={busy}
                title="Gerar senha de 16 caracteres"
              >
                <DiceIcon />
                <span>Gerar</span>
              </button>
            </div>
            <p className="ufm-hint">
              Comunique a nova senha por fora (WhatsApp, email). O usuário pode trocá-la
              depois em <em>Minha conta</em>.
            </p>
          </div>

          {errorMsg && (
            <div className="ufm-error" role="alert">{errorMsg}</div>
          )}

          <div className="ufm-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose} disabled={busy}>
              Cancelar
            </button>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? 'Resetando…' : 'Resetar senha'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  )
}
