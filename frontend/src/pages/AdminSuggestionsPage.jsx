import { useAuth } from '../contexts/AuthContext'
import FilaNoHub from './suggestions/FilaNoHub'
import MinhasSugestoes from './suggestions/MinhasSugestoes'
import './AdminSuggestionsPage.css'

// Uma rota, duas faces. O e-mail do dev vem por env (espelha o
// SUGGESTIONS_DEV_EMAIL do backend). Desde 2026-10-26 a fila mora no hub: o dev
// tria lá, e aqui vê só o aviso com o link. A CentralDeComando continua no
// código, fora da tela.
const DEV_EMAIL = (import.meta.env.VITE_SUGGESTIONS_DEV_EMAIL || 'tatico3@hubradios.com').toLowerCase()

export default function AdminSuggestionsPage() {
  const { user } = useAuth()
  const isDev = (user?.email || '').toLowerCase() === DEV_EMAIL
  return isDev ? <FilaNoHub /> : <MinhasSugestoes />
}
