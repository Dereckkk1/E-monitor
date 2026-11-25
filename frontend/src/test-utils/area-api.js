// Helpers de teste da área api/contexts/utils (NÃO é código de produção; fica fora
// da cobertura). Só o que os testes de hooks precisam além de render.jsx/api.js.
import { createElement } from 'react'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { novoQueryClient } from './render'

/**
 * Monta um hook do React Query com o QueryClient de teste (sem retry, sem cache).
 * Devolve o `queryClient` pra espiar invalidações e ler/escrever o cache.
 */
export function montarHook(callback, { queryClient } = {}) {
  const qc = queryClient ?? novoQueryClient()
  const wrapper = ({ children }) => createElement(QueryClientProvider, { client: qc }, children)
  const resultado = renderHook(callback, { wrapper })
  return { qc, ...resultado }
}

/** queryKeys passadas ao invalidateQueries (espião criado com vi.spyOn(qc, 'invalidateQueries')). */
export function chavesInvalidadas(espiao) {
  return espiao.mock.calls.map(c => c[0].queryKey)
}

/** FormData -> objeto simples (arquivos viram o próprio File), pra conferir o corpo multipart. */
export function formDataParaObjeto(fd) {
  const obj = {}
  for (const [k, v] of fd.entries()) obj[k] = v
  return obj
}

/**
 * QueryClient cujo cache não é coletado: o novoQueryClient() usa gcTime 0, e uma
 * query semeada com setQueryData sem observador some no próximo tick (teste flaky).
 */
export function clienteComCache() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  })
}
