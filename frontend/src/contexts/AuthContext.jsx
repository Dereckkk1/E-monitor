import { createContext, useContext } from 'react'

// Placeholder — will be wired to E-radios auth
const AuthContext = createContext({
  isAdmin: true,
  isClient: false,
  user: { name: 'Admin', email: '' },
})

export function AuthProvider({ children }) {
  // Hardcoded admin for now; swap with real auth later
  const value = {
    isAdmin: true,
    isClient: false,
    user: { name: 'Admin', email: '' },
  }
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  return useContext(AuthContext)
}
