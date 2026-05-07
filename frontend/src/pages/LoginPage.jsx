import { useState } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import api from '../api/client'
import { useAuth } from '../contexts/AuthContext'

// LoginPage — POSTs {email, password} to /v1/internal/auth/login. On success
// stores token+user in context (sessionStorage) and redirects to either the
// route the user was trying to reach (location.state.from) or /stations.
//
// On HTTP 401 a friendly message is shown; other errors fall back to a
// generic message + the response body for diagnostics.
export default function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
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
      <form className="login-card" onSubmit={handleSubmit} noValidate>
        <div className="login-brand">
          <div className="login-brand-name">Radiocheck</div>
          <div className="login-brand-sub">E-radios</div>
        </div>

        <h1 className="login-title">Entrar</h1>
        <p className="login-subtitle">Acesse o painel de monitoramento.</p>

        <label className="login-field">
          <span>E-mail</span>
          <input
            type="email"
            autoComplete="email"
            autoFocus
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            disabled={submitting}
          />
        </label>

        <label className="login-field">
          <span>Senha</span>
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting}
          />
        </label>

        {error ? <div className="login-error" role="alert">{error}</div> : null}

        <button type="submit" className="login-submit" disabled={submitting || !email || !password}>
          {submitting ? 'Entrando…' : 'Entrar'}
        </button>
      </form>
    </div>
  )
}
