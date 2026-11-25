import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import ApiKeysPage from './ApiKeysPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const AGORA = new Date('2026-07-15T15:00:00Z')
const ha = (s) => new Date(AGORA.getTime() - s * 1000).toISOString()
const ID = 'cli-1'

const CHAVES = [
  { id: 'k1', key_masked: 'a3f9••••••••••••', scopes: ['detections:read', 'evidence:read'], rate_limit: 120, created_at: ha(5 * 86400), last_used_at: ha(7200) },
  { id: 'k2', masked_key: 'b71c••••••••••••', scopes: [], created_at: ha(40 * 86400), last_used_at: null },
  { id: 'k3', key_masked: '4e2d••••••••••••', scopes: ['detections:read'], created_at: ha(90 * 86400), last_used_at: ha(86400 * 20), revoked_at: ha(86400) },
]

describe('ApiKeysPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas = {}) {
    chamadas = mockApi({
      'GET /clients': { data: [{ id: ID, name: 'Cliente Teste Ltda' }] },
      [`GET /clients/${ID}/api-keys`]: CHAVES,
      ...rotas,
    })
    return renderComProviders(<ApiKeysPage />, { rota: `/clients/${ID}/api-keys`, caminho: '/clients/:id/api-keys' })
  }

  const linhas = () => [...document.querySelectorAll('.ak-table .ak-row')]

  test('lista só as chaves ativas, com máscara, escopos, limite e tempos relativos', async () => {
    montar()
    expect(await screen.findByText('a3f9••••••••••••')).toBeTruthy()
    await waitFor(() => expect(document.querySelector('.ak-title-client').textContent).toBe('Cliente Teste Ltda'))
    expect(linhas().length).toBe(2)             // a revogada fica escondida
    expect(screen.queryByText('4e2d••••••••••••')).toBeNull()

    const k1 = screen.getByText('a3f9••••••••••••').closest('.ak-row')
    expect(within(k1).getByText('detections:read')).toBeTruthy()
    expect(within(k1).getByText('evidence:read')).toBeTruthy()
    expect(within(k1).getByText('120 req/min')).toBeTruthy()
    expect(within(k1).getByText('há 5 d')).toBeTruthy()
    expect(within(k1).getByText('há 2 h')).toBeTruthy()
    expect(within(k1).getByText('Ativa')).toBeTruthy()

    // sem escopos = acesso completo; limite padrão 60; nunca usada; máscara pelo campo alternativo
    const k2 = screen.getByText('b71c••••••••••••').closest('.ak-row')
    expect(within(k2).getByTitle('Acesso completo')).toBeTruthy()
    expect(within(k2).getByText('60 req/min')).toBeTruthy()
    expect(within(k2).getByText('Nunca usada')).toBeTruthy()

    expect(screen.getByText('2 chaves')).toBeTruthy()
  })

  // Regressão: o ramo de meses dizia "há 1 meses".
  test('tempo relativo de 1 mês usa o singular', async () => {
    montar()
    const k2 = (await screen.findByText('b71c••••••••••••')).closest('.ak-row')
    expect(within(k2).getByText('há 1 mês')).toBeTruthy()
  })

  test('"Mostrar revogadas" inclui a chave revogada, sem botão de revogar habilitado', async () => {
    montar()
    await screen.findByText('a3f9••••••••••••')
    fireEvent.click(screen.getByLabelText('Mostrar revogadas'))
    const k3 = screen.getByText('4e2d••••••••••••').closest('.ak-row')
    expect(within(k3).getByText('Revogada')).toBeTruthy()
    expect(within(k3).getByRole('button', { name: 'Revogar' }).disabled).toBe(true)
    expect(within(k3).getByRole('button', { name: 'Revogar' }).title).toBe('Já revogada')
    expect(screen.getByText('3 chaves')).toBeTruthy()
  })

  test('revogar pede confirmação e chama o DELETE da chave', async () => {
    montar({ [`DELETE /clients/${ID}/api-keys/k1`]: {} })
    const k1 = (await screen.findByText('a3f9••••••••••••')).closest('.ak-row')
    fireEvent.click(within(k1).getByRole('button', { name: 'Revogar' }))
    expect(await screen.findByText('Revogar esta API key? Aplicações usando-a vão começar a receber 401.')).toBeTruthy()
    // cancelar não chama
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText('Confirmar ação')).toBeNull())
    expect(chamadas.some(c => c.metodo === 'DELETE')).toBe(false)

    fireEvent.click(within(k1).getByRole('button', { name: 'Revogar' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === `DELETE /clients/${ID}/api-keys/k1`)).toBe(true))
  })

  test('gerar chave: confirma no modal, revela a chave uma única vez e copia pra área de transferência', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    montar({ [`POST /clients/${ID}/api-keys`]: { id: 'k9', key: 'rc_live_chave_de_teste_123' } })
    await screen.findByText('a3f9••••••••••••')
    fireEvent.click(screen.getAllByRole('button', { name: /Gerar nova chave/ })[1]) // botão da barra de ferramentas
    expect(await screen.findByText('Gerar nova API key')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Gerar chave' }))

    expect(await screen.findByText('Sua nova API key')).toBeTruthy()
    expect(screen.getByText('rc_live_chave_de_teste_123')).toBeTruthy()
    expect(screen.queryByText('Gerar nova API key')).toBeNull()
    expect(chamadas.find(c => c.chave === `POST /clients/${ID}/api-keys`).data).toEqual({})

    fireEvent.click(screen.getByRole('button', { name: 'Copiar chave' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('rc_live_chave_de_teste_123'))
    expect(await screen.findByText('Copiado')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: 'Entendi, fechar' }))
    expect(screen.queryByText('Sua nova API key')).toBeNull()
  })

  test('erro ao gerar mantém o modal aberto com a mensagem; cancelar fecha', async () => {
    montar({ [`POST /clients/${ID}/api-keys`]: erroApi(500, {}) })
    await screen.findByText('a3f9••••••••••••')
    fireEvent.click(screen.getAllByRole('button', { name: /Gerar nova chave/ })[1])
    fireEvent.click(await screen.findByRole('button', { name: 'Gerar chave' }))
    expect(await screen.findByText('Falha ao gerar a chave. Tente novamente.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    expect(screen.queryByText('Gerar nova API key')).toBeNull()
  })

  test('cartão "Como usar" lista os endpoints, recolhe e o CTA abre o modal de gerar', async () => {
    montar()
    await screen.findByText('a3f9••••••••••••')
    expect(screen.getByText('GET /v1/detections')).toBeTruthy()
    const toggle = screen.getByRole('button', { name: /Como usar a API Radiocheck/ })
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    fireEvent.click(within(document.querySelector('.ak-howto')).getByRole('button', { name: /Gerar nova chave/ }))
    expect(await screen.findByText('Gerar nova API key')).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Fechar'))
    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByText('GET /v1/detections')).toBeNull()
  })

  test('sem chaves: estado vazio explica e oferece gerar a primeira', async () => {
    montar({ [`GET /clients/${ID}/api-keys`]: [] })
    expect(await screen.findByText('Nenhuma API key cadastrada')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Gerar primeira chave/ }))
    expect(await screen.findByText('Gerar nova API key')).toBeTruthy()
  })

  test('só chaves revogadas: com o filtro padrão mostra a mensagem de lista vazia filtrada', async () => {
    montar({ [`GET /clients/${ID}/api-keys`]: [CHAVES[2]] })
    expect(await screen.findByText('Nenhuma chave para exibir com o filtro atual.')).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Mostrar revogadas'))
    expect(screen.getByText('4e2d••••••••••••')).toBeTruthy()
  })
})
