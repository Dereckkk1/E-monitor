// Infra de teste compartilhada (NÃO é código de produção; fica fora da cobertura).
//
// renderComProviders monta o componente com os MESMOS providers do app real
// (React Query + Router + Auth + Confirm + RadioPlayer — ver main.jsx/App.jsx),
// já logado como o usuário pedido. A sessão do app mora no sessionStorage
// (rc_token / rc_user — ver contexts/AuthContext.jsx), então "logar" num teste
// é só gravar ali antes de renderizar.
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { AuthProvider } from '../contexts/AuthContext'
import { ConfirmProvider } from '../components/ConfirmModal'
import { RadioPlayerProvider } from '../contexts/RadioPlayerContext'

export const USUARIO_ADMIN = {
  id: '00000000-0000-0000-0000-0000000000a1',
  email: 'admin@example.com',
  name: 'Admin Teste',
  role: 'admin',
}

export const USUARIO_CLIENTE = {
  id: '00000000-0000-0000-0000-0000000000c1',
  email: 'cliente@example.com',
  name: 'Cliente Teste',
  role: 'viewer',
  client_id: '00000000-0000-0000-0000-00000000c001',
  client_ids: ['00000000-0000-0000-0000-00000000c001'],
}

export function logarComo(usuario) {
  sessionStorage.setItem('rc_token', 'token-de-teste')
  sessionStorage.setItem('rc_user', JSON.stringify(usuario))
}

export function novoQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  })
}

/**
 * @param {import('react').ReactElement} ui
 * @param {{ rota?: string, caminho?: string, usuario?: object|null, queryClient?: QueryClient }} [opts]
 *   rota: URL inicial do MemoryRouter (ex.: '/campaigns/123?aba=x')
 *   caminho: padrão de rota pra useParams funcionar (ex.: '/campaigns/:id')
 *   usuario: USUARIO_ADMIN (padrão), USUARIO_CLIENTE, ou null (deslogado)
 */
export function renderComProviders(ui, { rota = '/', caminho, usuario = USUARIO_ADMIN, queryClient } = {}) {
  sessionStorage.clear()
  if (usuario) logarComo(usuario)
  const qc = queryClient ?? novoQueryClient()
  const conteudo = caminho
    ? (
      <Routes>
        <Route path={caminho} element={ui} />
        <Route path="*" element={<div data-testid="rota-fora">fora da rota</div>} />
      </Routes>
    )
    : ui
  const resultado = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[rota]}>
        <AuthProvider>
          <ConfirmProvider>
            <RadioPlayerProvider>{conteudo}</RadioPlayerProvider>
          </ConfirmProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { queryClient: qc, ...resultado }
}
