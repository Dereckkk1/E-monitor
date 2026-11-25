import { describe, test, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import HubSsoPage from './HubSsoPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

function montar(rotas, rota) {
  const chamadas = mockApi(rotas)
  renderComProviders(<><HubSsoPage /><LocalizacaoAtual /></>, { usuario: null, rota })
  return chamadas
}

describe('HubSsoPage', () => {
  test('troca o código, guarda a sessão e entra pela raiz', async () => {
    const chamadas = montar({
      'POST /auth/sso': { token: 'jwt-sso', user: { id: 'u1', email: 'cliente@example.com', role: 'viewer', client_id: 'c1' } },
    }, '/sso?code=codigo-de-uso-unico')
    expect(screen.getByText('Entrando pela Central de Clientes…')).toBeTruthy()
    await waitFor(() => expect(urlAtual(screen)).toBe('/'))
    expect(chamadas.filter(c => c.chave === 'POST /auth/sso').length).toBe(1)
    expect(chamadas[0].data).toEqual({ code: 'codigo-de-uso-unico' })
    expect(sessionStorage.getItem('rc_token')).toBe('jwt-sso')
  })

  test('sem código na URL explica como entrar e não chama a API', () => {
    const chamadas = montar({}, '/sso')
    expect(screen.getByText('Não deu para entrar')).toBeTruthy()
    expect(screen.getByText('Abra o E-monitor pela Central de Clientes para entrar por aqui.')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Entrar pelo E-monitor' }).getAttribute('href')).toBe('/login')
    expect(chamadas.length).toBe(0)
  })

  test.each([
    [403, 'client_not_provisioned', 'Sua empresa ainda não está liberada no E-monitor.'],
    [403, 'account_disabled', 'Sua conta no E-monitor está desativada. Fale com o suporte.'],
    [403, 'client_disabled', 'O acesso da sua empresa ao E-monitor está suspenso. Fale com o suporte.'],
    [410, 'gone', 'Este link de acesso expirou ou já foi usado.'],
    [500, 'falha no hub', 'Falha no hub'],
  ])('erro %i (%s) mostra a mensagem certa e a saída pelo login', async (status, corpo, trecho) => {
    montar({ 'POST /auth/sso': erroApi(status, corpo) }, '/sso?code=abc')
    expect((await screen.findByText('Não deu para entrar'))).toBeTruthy()
    expect(screen.getByText(trecho, { exact: false })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Entrar pelo E-monitor' })).toBeTruthy()
    expect(sessionStorage.getItem('rc_token')).toBeNull()
  })

  test('erro sem corpo cai na mensagem genérica', async () => {
    montar({ 'POST /auth/sso': erroApi(500, {}) }, '/sso?code=abc')
    expect(await screen.findByText('Não foi possível entrar agora. Tente novamente.')).toBeTruthy()
  })
})
