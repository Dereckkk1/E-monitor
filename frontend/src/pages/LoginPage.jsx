import { useState } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import api from '../api/client'
import { useAuth } from '../contexts/AuthContext'

export default function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState(null)

  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  function nextDestination() {
    const fromState = location.state?.from?.pathname
    if (fromState) return fromState + (location.state.from.search ?? '')
    const params = new URLSearchParams(location.search)
    const nextParam = params.get('next')
    if (nextParam) {
      try { return decodeURIComponent(nextParam) } catch { return '/' }
    }
    return '/'
  }

  async function handleSubmit(e) {
    e.preventDefault()
    if (submitting) return
    setError(null)
    setSubmitting(true)
    try {
      const { data } = await api.post('/auth/login', { email, password })
      login(data.token, data.user)
      navigate(nextDestination(), { replace: true })
    } catch (err) {
      const status = err?.response?.status
      if (status === 401) {
        setError('Credenciais inválidas.')
      } else if (status === 403) {
        // Conta desativada (account_disabled) ou empresa do cliente
        // desativada (client_disabled). Mesmo recado: procurar o admin.
        setError('Acesso desativado. Procure o administrador.')
      } else if (status === 400) {
        setError('Requisição inválida.')
      } else {
        setError('Não foi possível entrar. Tente novamente em instantes.')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login-shell">
      <aside className="login-brand-panel" aria-hidden="true">
        <div className="login-brand-overlay">
          <div className="login-brand-content">
            <img
              src="/E-monitor%20logo.png"
              alt="E-monitor"
              className="login-brand-wordmark-img"
            />
            <p className="login-brand-tagline">
              Cada comercial que vai ao ar, registrado.
            </p>
          </div>
          <div className="login-brand-signature">
            <img
              src="/eradios-logo.png"
              alt=""
              className="login-brand-signature-mark"
            />
            <span className="login-brand-signature-text">
              parte do ecossistema E-radios
            </span>
          </div>
        </div>
      </aside>

      <main className="login-form-panel">
        <div className="login-form-inner">
          <div className="login-mobile-brand">
            <img
              src="/E-monitor%20logo.png"
              alt="E-monitor"
              className="login-mobile-wordmark-img"
            />
            <span className="login-mobile-sub">parte do ecossistema E-radios</span>
          </div>

          <span className="login-welcome">Bem-vindo de volta</span>
          <h1 className="login-title">Entrar</h1>
          <p className="login-subtitle">Acesse sua conta para continuar.</p>

          <form onSubmit={handleSubmit} noValidate className="login-form">
            <label className="login-field">
              <span>E-mail</span>
              <div className="login-input-wrap">
                <span className="login-field-icon-left" aria-hidden="true"><EnvelopeIcon /></span>
                <input
                  type="email"
                  autoComplete="email"
                  autoFocus
                  required
                  value={email}
                  onChange={(e) => { setEmail(e.target.value); setError(null) }}
                  disabled={submitting}
                  placeholder="seu@email.com"
                  className="login-iconed"
                />
              </div>
            </label>

            <label className="login-field">
              <span>Senha</span>
              <div className="login-input-wrap">
                <span className="login-field-icon-left" aria-hidden="true"><LockIcon /></span>
                <input
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => { setPassword(e.target.value); setError(null) }}
                  disabled={submitting}
                  placeholder="••••••••"
                  className="login-iconed login-pwd"
                />
                <button
                  type="button"
                  className="login-eye"
                  onClick={() => setShowPassword(v => !v)}
                  tabIndex={-1}
                  aria-label={showPassword ? 'Ocultar senha' : 'Mostrar senha'}
                >
                  {showPassword ? <EyeOff /> : <Eye />}
                </button>
              </div>
            </label>

            {error ? (
              <p className="login-error" role="alert">
                <AlertIcon />
                {error}
              </p>
            ) : null}

            <button
              type="submit"
              className="login-submit"
              disabled={submitting || !email || !password}
            >
              {submitting
                ? <><span className="login-spinner" aria-hidden="true" />Entrando…</>
                : 'Entrar'}
            </button>
          </form>

          <p className="login-form-footer">
            Problemas para entrar?<br />Fale com o administrador.
          </p>
        </div>
      </main>
    </div>
  )
}

function EnvelopeIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="4" width="20" height="16" rx="2" />
      <path d="m2 7 10 7 10-7" />
    </svg>
  )
}

function LockIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="3" y="11" width="18" height="11" rx="2" />
      <path d="M7 11V7a5 5 0 0 1 10 0v4" />
    </svg>
  )
}

function AlertIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0 }}>
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
  )
}

function Eye() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  )
}

function EyeOff() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
      <line x1="1" y1="1" x2="23" y2="23" />
    </svg>
  )
}
