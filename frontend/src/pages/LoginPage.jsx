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
      <div className="login-brand-panel" aria-hidden="true">
        <div className="login-orb login-orb-1" />
        <div className="login-orb login-orb-2" />
        <div className="login-orb login-orb-3" />
        <div className="login-brand-overlay">
          <div className="login-brand-content">
            <span className="login-brand-eyebrow">E-radios</span>
            <div className="login-brand-wordmark">Radiocheck</div>
            <p className="login-brand-tagline">
              Monitoramento de veiculação<br />em tempo real.
            </p>
            <div className="login-feature-pills">
              <span className="login-feature-pill">98% precisão</span>
              <span className="login-feature-pill">&lt; 10s detecção</span>
              <span className="login-feature-pill">24/7 ativo</span>
            </div>
          </div>
          <div className="login-brand-footer">
            <div className="login-signal-rings">
              <div className="login-signal-core" />
              <div className="login-signal-ring login-signal-ring-1" />
              <div className="login-signal-ring login-signal-ring-2" />
              <div className="login-signal-ring login-signal-ring-3" />
            </div>
            <div className="login-stat-badge">
              <span className="login-stat-dot" />
              <span>200+ emissoras monitoradas</span>
            </div>
          </div>
        </div>
      </div>

      <div className="login-form-panel">
        <div className="login-form-inner">
          <div className="login-mobile-brand">
            <span className="login-mobile-wordmark">Radiocheck</span>
            <span className="login-mobile-sub">E-radios</span>
          </div>

          <span className="login-welcome">Bem-vindo de volta</span>
          <h1 className="login-title">Entrar</h1>

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
        </div>
      </div>
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
