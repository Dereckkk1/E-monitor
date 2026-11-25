// Botão "Copiar" do /admin/monitoring (aba Identidades): copia o IP do ator e
// só confirma ("✓") quando a área de transferência aceitou — writeText rejeita
// sem permissão, e aí o ícone não pode mentir que copiou.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import AdminMonitoringPage from './AdminMonitoringPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi } from '../test-utils/api'

const ATOR_ANONIMO = {
  ip: '203.0.113.7', userId: null, userEmail: null,
  totalRequests: 42, uniqueRouteCount: 3, errorCount: 0, slowCount: 0,
  riskLevel: 'low', lastSeen: '2026-03-10T12:00:00Z', isIPBlocked: false,
}

let writeText
const clipboardOriginal = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

function instalarClipboard(impl) {
  writeText = vi.fn(impl)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
}

async function abrirAtores() {
  renderComProviders(<AdminMonitoringPage />, { rota: '/admin/monitoring' })
  fireEvent.click(await screen.findByRole('button', { name: /Identidades/ }))
  await screen.findByText('203.0.113.7')
  return screen.getAllByTitle('Copiar')[0]
}

describe('AdminMonitoringPage — copiar IP', () => {
  beforeEach(() => {
    mockApi({
      'GET /admin/monitoring/top-actors': { actors: [ATOR_ANONIMO] },
      'GET /admin/monitoring/blocked-ips': { blockedIPs: [] },
    })
  })

  afterEach(() => {
    if (clipboardOriginal) Object.defineProperty(navigator, 'clipboard', clipboardOriginal)
    else delete navigator.clipboard
  })

  test('copia o IP, mostra a confirmação e volta ao ícone de copiar', async () => {
    instalarClipboard(() => Promise.resolve())
    const botao = await abrirAtores()
    const iconeCopiar = botao.innerHTML

    fireEvent.click(botao)

    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText).toHaveBeenCalledWith('203.0.113.7')
    await waitFor(() => expect(botao.innerHTML).not.toBe(iconeCopiar))
    // a confirmação é temporária (1,2s)
    await waitFor(() => expect(botao.innerHTML).toBe(iconeCopiar), { timeout: 4000 })
  })

  test('sem permissão (writeText rejeita) não finge que copiou', async () => {
    instalarClipboard(() => Promise.reject(new Error('NotAllowedError')))
    const botao = await abrirAtores()
    const iconeCopiar = botao.innerHTML

    fireEvent.click(botao)

    expect(writeText).toHaveBeenCalledWith('203.0.113.7')
    // dá tempo da promessa rejeitar e de um eventual re-render
    await new Promise(r => setTimeout(r, 50))
    expect(botao.innerHTML).toBe(iconeCopiar)
  })
})
