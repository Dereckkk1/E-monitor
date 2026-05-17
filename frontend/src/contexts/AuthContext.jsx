import { createContext, useContext, useState, useCallback } from 'react'

const TOKEN_KEY = 'rc_token'
const USER_KEY = 'rc_user'

const AuthContext = createContext(null)

function readUser() {
  try {
    const raw = sessionStorage.getItem(USER_KEY)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

export function AuthProvider({ children }) {
  // Token + user persist in sessionStorage so a refresh keeps the user
  // signed in but closing the tab forces a new login. PoC trade-off — when
  // refresh tokens land we should switch to httpOnly cookies (TODO).
  const [token, setToken] = useState(() => sessionStorage.getItem(TOKEN_KEY))
  const [user, setUser] = useState(readUser)

  const login = useCallback((newToken, newUser) => {
    sessionStorage.setItem(TOKEN_KEY, newToken)
    if (newUser) sessionStorage.setItem(USER_KEY, JSON.stringify(newUser))
    setToken(newToken)
    setUser(newUser ?? null)
  }, [])

  const logout = useCallback(() => {
    sessionStorage.removeItem(TOKEN_KEY)
    sessionStorage.removeItem(USER_KEY)
    setToken(null)
    setUser(null)
  }, [])

  // Role helpers consumed by Sidebar / role-gated routes.
  //
  // Vocabulário: o backend usa role 'viewer' pra "Cliente". 'admin' e
  // 'operator' são tratados como sinônimos (admin do sistema). Este context
  // expõe isAdmin/isClient/clientId pra componentes não terem que conhecer
  // o detalhe.
  //
  // Nota: o fallback `isAdmin = role == null` foi removido — UI legada que
  // renderizava com admin por default agora vê isAdmin=false até o login
  // popular o user. Componentes que precisam de garantia de role devem
  // usar <RequireRole>.
  const role = user?.role ?? null
  const isAdmin = role === 'admin' || role === 'operator'
  const isClient = role === 'viewer'
  const clientId = user?.client_id ?? null

  const value = {
    token,
    user,
    isAuthenticated: !!token,
    isAdmin,
    isClient,
    clientId,
    login,
    logout,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within <AuthProvider>')
  return ctx
}

// Module-level accessors so non-React code (e.g. axios interceptors) can
// reach the current token / clear it on 401 without subscribing to React
// state. Kept here, next to the consumers, instead of in api/client.js so
// the storage keys live in exactly one file.
export function getStoredToken() {
  return sessionStorage.getItem(TOKEN_KEY)
}

export function clearStoredAuth() {
  sessionStorage.removeItem(TOKEN_KEY)
  sessionStorage.removeItem(USER_KEY)
}
