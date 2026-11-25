// Exemplo canônico do padrão de teste de página (render com providers reais +
// API simulada pelo adapter do axios). Copie este formato pros outros testes.
import { describe, test, expect, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import MaterialTypesPage from './MaterialTypesPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const TIPOS = [
  { id: 't1', name: 'Spot 30s', color: '#3b82f6', description: 'Comercial padrão' },
  { id: 't2', name: 'Vinheta', color: '#22c55e', description: null },
]

describe('MaterialTypesPage', () => {
  let chamadas

  beforeEach(() => {
    chamadas = mockApi({
      'GET /material-types': TIPOS,
      'POST /material-types': (config) => ({ id: 't3', ...JSON.parse(config.data) }),
    })
  })

  test('lista os tipos vindos da API, com a descrição quando existe', async () => {
    renderComProviders(<MaterialTypesPage />)
    expect(await screen.findByText('Spot 30s')).toBeTruthy()
    expect(screen.getByText('Vinheta')).toBeTruthy()
    expect(screen.getByText('Comercial padrão')).toBeTruthy()
    expect(chamadas.map(c => c.chave)).toContain('GET /material-types')
  })

  test('criar um tipo: Salvar fica bloqueado sem nome e envia o payload certo', async () => {
    renderComProviders(<MaterialTypesPage />)
    await screen.findByText('Spot 30s')

    fireEvent.click(screen.getByRole('button', { name: '+ Novo tipo' }))
    const salvar = screen.getByRole('button', { name: 'Salvar' })
    expect(salvar.disabled).toBe(true)

    const [nome, , descricao] = screen.getAllByRole('textbox')
    fireEvent.change(nome, { target: { value: 'Testemunhal' } })
    fireEvent.change(descricao, { target: { value: '' } })
    expect(salvar.disabled).toBe(false)
    fireEvent.click(salvar)

    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /material-types')).toBe(true))
    const post = chamadas.find(c => c.chave === 'POST /material-types')
    // descrição vazia vira null (o backend grava NULL, não string vazia)
    expect(post.data).toEqual({ name: 'Testemunhal', color: '#94a3b8', description: null })
  })

  test('erro da API ao listar não derruba a tela', async () => {
    mockApi({ 'GET /material-types': erroApi(500, { error: 'falhou' }) })
    renderComProviders(<MaterialTypesPage />)
    expect(await screen.findByRole('heading', { name: 'Tipos de material' })).toBeTruthy()
    expect(within(document.body).queryByText('Spot 30s')).toBeNull()
  })
})
