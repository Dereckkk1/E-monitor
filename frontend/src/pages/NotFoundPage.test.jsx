import { describe, test, expect, vi } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import NotFoundPage from './NotFoundPage'
import { renderComProviders, USUARIO_ADMIN } from '../test-utils/render'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

// A cena 3D (three.js) não roda no jsdom: troca por um marcador.
vi.mock('../three/NotFoundScene', () => ({ default: () => <div data-testid="cena-3d" /> }))

describe('NotFoundPage', () => {
  test('visitante sem sessão: mensagem de sinal perdido e saídas levam ao login', async () => {
    renderComProviders(<><NotFoundPage /><LocalizacaoAtual /></>, { usuario: null, rota: '/rota-que-nao-existe' })
    expect(screen.getByText('SINAL PERDIDO')).toBeTruthy()
    expect(await screen.findByTestId('cena-3d')).toBeTruthy()
    expect(screen.getByText(/ERR_FINGERPRINT_NOT_FOUND · TS:\d{4}-\d{2}-\d{2} /)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ir para o login' }))
    expect(urlAtual(screen)).toBe('/login')
  })

  test('visitante: "Voltar pra home" também cai no login', () => {
    renderComProviders(<><NotFoundPage /><LocalizacaoAtual /></>, { usuario: null, rota: '/x' })
    fireEvent.click(screen.getByRole('button', { name: 'Voltar pra home' }))
    expect(urlAtual(screen)).toBe('/login')
  })

  test('logado: oferece ver as detecções', () => {
    renderComProviders(<><NotFoundPage /><LocalizacaoAtual /></>, { usuario: USUARIO_ADMIN, rota: '/x' })
    fireEvent.click(screen.getByRole('button', { name: 'Ver minhas detecções' }))
    expect(urlAtual(screen)).toBe('/detections')
  })

  test('logado: "Voltar pra home" vai pra raiz', () => {
    renderComProviders(<><NotFoundPage /><LocalizacaoAtual /></>, { usuario: USUARIO_ADMIN, rota: '/x' })
    fireEvent.click(screen.getByRole('button', { name: 'Voltar pra home' }))
    expect(urlAtual(screen)).toBe('/')
  })
})
