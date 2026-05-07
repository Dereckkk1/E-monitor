import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'

// Route guard: if no token is in context, redirect to /login while
// remembering where the user was headed. LoginPage reads location.state.from
// (or ?next=) to send them back after a successful authentication.
export default function RequireAuth({ children }) {
  const { isAuthenticated } = useAuth()
  const location = useLocation()
  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }
  return children
}
