import { describe, test, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import PendentesPage from './PendentesPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi } from '../test-utils/api'

// Clickjacking: dentro de um quadro de outra página a tela NÃO pode oferecer
// os botões de aprovar — e nem pode buscar os pedidos.
vi.mock('../utils/emoldurada', () => ({ emoldurada: () => true }))

describe('PendentesPage emoldurada por outra página', () => {
  test('mostra só o aviso "Abra numa aba própria" e não consulta a fila', async () => {
    const chamadas = mockApi({
      'GET /mcp/pendentes': { items: [{ id: 'p1', estado: 'pendente', tool: 't', preview: { resumo: 'Algo' } }], destaque: null },
    })
    renderComProviders(<PendentesPage />)
    expect(await screen.findByText('Abra numa aba própria')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Aprovar e executar' })).toBeNull()
    expect(chamadas.some(c => c.chave === 'GET /mcp/pendentes')).toBe(false)
  })
})
