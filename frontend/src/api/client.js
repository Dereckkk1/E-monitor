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
//
// Também não redirecionamos quando o visitante está numa ROTA PÚBLICA. Nessas
// telas a ausência de sessão é o estado normal, então qualquer 401 de chamada
// paralela (telemetria, por exemplo) sequestraria o visitante pro /login.
// Foi exatamente o que aconteceu com /boasvindas: o POST /web-vitals disparava
// sem token, tomava 401, e o interceptor engolia a página antes dela renderizar.
//
// TODA rota pública nova precisa entrar nesta lista.
// `/sso` entra aqui pelo mesmo motivo do `/login`: quem está nela ainda não tem
// sessão, então um 401 ali é resposta de negócio — não "sessão expirada". Sem
// isto, o interceptor limparia o storage e redirecionaria para /login no meio
// da troca do código, trocando a mensagem real por um redirect silencioso.
const PUBLIC_ROUTES = [/^\/login$/, /^\/sso$/, /^\/boasvindas(\/|$)/, /^\/pos-venda(\/|$)/, /^\/404$/]

function onPublicRoute() {
  const path = window.location.pathname
  return PUBLIC_ROUTES.some(re => re.test(path))
}

api.interceptors.response.use(
  (resp) => resp,
  (error) => {
    const status = error?.response?.status
    const url = error?.config?.url || ''
    const isLoginCall = url.endsWith('/auth/login')
    if (status === 401 && !isLoginCall && !onPublicRoute()) {
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
