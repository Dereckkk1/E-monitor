import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import AuthorizeAiPage from './AuthorizeAiPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const PEDIDO = { cliente: 'Assistente Teste', destino: 'https://assistente.example.com/callback' }

function montar(rotas = {}, rota = '/autorizar-ia?pedido=pedido-assinado') {
  const chamadas = mockApi({ 'GET /mcp/oauth/pedido': PEDIDO, ...rotas })
  renderComProviders(<AuthorizeAiPage />, { rota })
  return chamadas
}

describe('AuthorizeAiPage', () => {
  test('sem ?pedido= explica que a página é aberta pelo assistente e não chama a API', () => {
    const chamadas = montar({}, '/autorizar-ia')
    expect(screen.getByText(/Nenhum pedido de autorização nesta página/)).toBeTruthy()
    expect(chamadas.length).toBe(0)
  })

  test('mostra quem pede acesso, pra onde volta e em nome de quem age', async () => {
    const chamadas = montar()
    expect(await screen.findByText('Um assistente quer acessar o E-monitor')).toBeTruthy()
    expect(screen.getByText('Assistente Teste')).toBeTruthy()
    expect(screen.getByText('https://assistente.example.com/callback')).toBeTruthy()
    expect(screen.getByText('Admin Teste')).toBeTruthy()
    expect(chamadas.find(c => c.chave === 'GET /mcp/oauth/pedido').params).toEqual({ p: 'pedido-assinado' })
  })

  test('pedido inválido mostra o erro da API', async () => {
    montar({ 'GET /mcp/oauth/pedido': erroApi(400, { error: 'pedido expirado' }) })
    expect((await screen.findByRole('alert')).textContent).toBe('pedido expirado')
  })

  test('recusar manda aprovar=false pro endpoint de consentimento', async () => {
    const chamadas = montar({ 'POST /mcp/oauth/grant': { redirect: 'javascript:alert(1)' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Recusar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /mcp/oauth/grant')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /mcp/oauth/grant').data).toEqual({ pedido: 'pedido-assinado', aprovar: false })
  })

  test('redirect com esquema perigoso (javascript:) NÃO é seguido: mostra o erro genérico', async () => {
    const chamadas = montar({ 'POST /mcp/oauth/grant': { redirect: 'javascript:alert(1)' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Autorizar' }))
    expect((await screen.findByRole('alert')).textContent).toBe('Não foi possível concluir. Volte ao assistente e conecte de novo.')
    expect(chamadas.find(c => c.chave === 'POST /mcp/oauth/grant').data).toEqual({ pedido: 'pedido-assinado', aprovar: true })
  })

  test('falha ao conceder mostra o erro e deixa tentar de novo', async () => {
    montar({ 'POST /mcp/oauth/grant': erroApi(403, { error: 'sem permissão' }) })
    fireEvent.click(await screen.findByRole('button', { name: 'Autorizar' }))
    expect((await screen.findByRole('alert')).textContent).toBe('sem permissão')
    expect(screen.getByRole('button', { name: 'Autorizar' }).disabled).toBe(false)
  })
})
