import { Navigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'

// Guard de rota por role. Uso:
//   <Route path="/admin/users" element={
//     <RequireRole roles={['admin']}>
//       <AdminUsersPage />
//     </RequireRole>
//   } />
//
// Vocabulário aceito em `roles`: 'admin' | 'client'. O role 'operator' do
// backend é tratado como sinônimo de 'admin' (admin do sistema).
//
// - User não autenticado → redirecionado pra /login.
// - User com role fora da lista → redirecionado pra `redirectTo` (default
//   /campaigns).
export default function RequireRole({ roles, children, redirectTo = '/campaigns' }) {
  const { user } = useAuth()
  const role = user?.role
  if (!role) return <Navigate to="/login" replace />

  // Normaliza: 'operator' → 'admin' pra checagem; 'viewer' → 'client'.
  const normalized = role === 'operator' ? 'admin' : role === 'viewer' ? 'client' : role
  if (!roles.includes(normalized)) {
    return <Navigate to={redirectTo} replace />
  }
  return children
}
