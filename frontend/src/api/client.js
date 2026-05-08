import axios from 'axios'
import { getStoredToken, clearStoredAuth } from '../contexts/AuthContext'

const api = axios.create({
  baseURL: `${import.meta.env.VITE_API_URL ?? ''}/v1/internal`,
  headers: { 'Content-Type': 'application/json' },
})

// Request interceptor: inject Authorization on every call when a token is
// stored. Reading from sessionStorage on each call (instead of capturing
// once at module load) keeps the header in sync with login/logout without
// reinstalling the interceptor.
api.interceptors.request.use((config) => {
  const token = getStoredToken()
  if (token) {
    config.headers = config.headers || {}
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Response interceptor: when the API returns 401 the token is either
// missing, expired or invalid. Clear stored auth and bounce the user to
// /login, preserving the current location so RequireAuth can send them
// back after re-authenticating.
//
// We avoid redirecting when the failing call IS the login endpoint —
// otherwise a wrong password would clobber the page state before the
// LoginPage can render its own error.
api.interceptors.response.use(
  (resp) => resp,
  (error) => {
    const status = error?.response?.status
    const url = error?.config?.url || ''
    const isLoginCall = url.endsWith('/auth/login')
    if (status === 401 && !isLoginCall) {
      clearStoredAuth()
      const here = window.location.pathname + window.location.search
      // Avoid redirect loop if we're already on /login.
      if (window.location.pathname !== '/login') {
        const next = encodeURIComponent(here)
        window.location.replace(`/login?next=${next}`)
      }
    }
    return Promise.reject(error)
  },
)

export default api
