import { useState } from 'react'
import { createPortal } from 'react-dom'
import { generateStrongPassword } from '../utils/passwordGen'

// Modal de reset de senha disparado pelo admin a partir da página
// /admin/users. NÃO exige senha atual (decisão admin override).
//
// Props:
//   user      — a linha de users.User
//   onSubmit(password) — chamado no submit com a nova senha.
//   onClose() — fecha o modal.
//   error     — erro do backend.
//   busy      — quando true, desabilita controles.
export default function ResetPasswordModal({ user, onSubmit, onClose, error, busy }) {
  const [pwd, setPwd] = useState('')
  const [show, setShow] = useState(false)

  function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    onSubmit(pwd)
  }

  const errorMsg = typeof error === 'string' ? error : (error?.error || error?.message || (error ? JSON.stringify(error) : null))

  return createPortal(
    <div className="confirm-backdrop" onClick={busy ? undefined : onClose} role="dialog" aria-modal="true">
      <div
        className="confirm-card"
        onClick={e => e.stopPropagation()}
        style={{ maxWidth: 460, width: 'min(460px, 100%)' }}
      >
        <div style={{ padding: '24px 28px 8px' }}>
          <h2 style={{ margin: 0, fontFamily: 'var(--font-heading)', fontSize: 20, color: 'var(--c-text)' }}>
            Resetar senha
          </h2>
          <p style={{ margin: '8px 0 0', color: '#475569', fontSize: 14 }}>
            Definir nova senha para <strong>{user.email}</strong>. Comunique a nova senha ao usuário; ele pode trocar depois em "Minha conta".
          </p>
        </div>

        <form onSubmit={handleSubmit} style={{ padding: '8px 28px 24px', display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div className="field">
            <label>Nova senha (mín 12 caracteres)</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                className="input"
                type={show ? 'text' : 'password'}
                value={pwd}
                onChange={e => setPwd(e.target.value)}
                minLength={12}
                required
                style={{ flex: 1 }}
                disabled={busy}
              />
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => setShow(s => !s)}
                disabled={busy}
              >
                {show ? 'Ocultar' : 'Mostrar'}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => { setPwd(generateStrongPassword(16)); setShow(true) }}
                disabled={busy}
              >
                Gerar
              </button>
            </div>
          </div>

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
              {busy ? 'Resetando…' : 'Resetar senha'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body
  )
}
