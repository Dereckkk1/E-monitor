import { useAuth } from '../contexts/AuthContext'
import CentralDeComando from './suggestions/CentralDeComando'
import MinhasSugestoes from './suggestions/MinhasSugestoes'
import './AdminSuggestionsPage.css'

// Uma rota, duas faces. O e-mail do dev vem por env (espelha o
// SUGGESTIONS_DEV_EMAIL do backend). O gating de dados de verdade é imposto no
// servidor — aqui é só qual UI renderizar.
const DEV_EMAIL = (import.meta.env.VITE_SUGGESTIONS_DEV_EMAIL || 'tatico3@hubradios.com').toLowerCase()

export default function AdminSuggestionsPage() {
  const { user } = useAuth()
  const isDev = (user?.email || '').toLowerCase() === DEV_EMAIL
  return isDev ? <CentralDeComando /> : <MinhasSugestoes />
}
