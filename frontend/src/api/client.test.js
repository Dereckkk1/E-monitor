// Interceptors do axios do app (api/client.js): Authorization em toda chamada e o
// tratamento de 401 (limpar sessão + voltar pro /login), que NÃO pode disparar no
// próprio login nem em rota pública.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import api from './client'
import { mockApi, erroApi } from '../test-utils/api'

// jsdom não deixa espiar window.location.replace; substitui o location inteiro.
function estarEm(pathname, search = '') {
  const replace = vi.fn()
  vi.stubGlobal('location', { pathname, search, replace })
  return replace
}

beforeEach(() => {
  sessionStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('interceptor de request: Authorization', () => {
  function capturarConfig() {
    const visto = {}
    api.defaults.adapter = async (config) => {
      visto.config = config
      return { data: {}, status: 200, statusText: 'OK', headers: {}, config }
    }
    return visto
  }

  test('manda Bearer com o token da sessão a cada chamada', async () => {
    const visto = capturarConfig()
    sessionStorage.setItem('rc_token', 'token-de-teste')
    await api.get('/stations')
    expect(visto.config.headers.Authorization).toBe('Bearer token-de-teste')
  })

  test('lê o token na hora da chamada: logout/login refletem sem reinstalar o interceptor', async () => {
    const visto = capturarConfig()
    sessionStorage.setItem('rc_token', 'primeiro')
    await api.get('/stations')
    expect(visto.config.headers.Authorization).toBe('Bearer primeiro')

    sessionStorage.setItem('rc_token', 'segundo')
    await api.get('/stations')
    expect(visto.config.headers.Authorization).toBe('Bearer segundo')

    sessionStorage.removeItem('rc_token')
    await api.get('/stations')
    expect(visto.config.headers.Authorization).toBeUndefined()
  })

  test('usa a baseURL /v1/internal', async () => {
    const visto = capturarConfig()
    await api.get('/stations')
    expect(visto.config.baseURL).toMatch(/\/v1\/internal$/)
  })
})

describe('interceptor de response: 401', () => {
  function logado() {
    sessionStorage.setItem('rc_token', 'token-de-teste')
    sessionStorage.setItem('rc_user', JSON.stringify({ role: 'admin' }))
  }

  test('401 numa tela privada limpa a sessão e manda pro /login guardando a rota atual', async () => {
    logado()
    const replace = estarEm('/campaigns', '?q=teste&page=2')
    mockApi({ 'GET /campaigns': erroApi(401, { error: 'expirou' }) })

    await expect(api.get('/campaigns')).rejects.toMatchObject({ response: { status: 401 } })

    expect(sessionStorage.getItem('rc_token')).toBeNull()
    expect(sessionStorage.getItem('rc_user')).toBeNull()
    expect(replace).toHaveBeenCalledTimes(1)
    expect(replace).toHaveBeenCalledWith(`/login?next=${encodeURIComponent('/campaigns?q=teste&page=2')}`)
  })

  test('401 do próprio /auth/login (senha errada) não limpa nem redireciona', async () => {
    logado()
    const replace = estarEm('/campaigns')
    mockApi({ 'POST /auth/login': erroApi(401, { error: 'credenciais' }) })

    await expect(api.post('/auth/login', {})).rejects.toMatchObject({ response: { status: 401 } })

    expect(replace).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('rc_token')).toBe('token-de-teste')
  })

  test.each(['/login', '/sso', '/boasvindas', '/boasvindas/abc123', '/pos-venda/abc123', '/404'])(
    '401 na rota pública %s não sequestra o visitante pro /login', async (caminho) => {
      logado()
      const replace = estarEm(caminho)
      mockApi({ 'POST /web-vitals': erroApi(401) })

      await expect(api.post('/web-vitals', {})).rejects.toMatchObject({ response: { status: 401 } })

      expect(replace).not.toHaveBeenCalled()
      expect(sessionStorage.getItem('rc_token')).toBe('token-de-teste')
    })

  test.each(['/pos-vendas', '/loginx', '/boasvindasx/a', '/sso/extra', '/admin/login'])(
    'rota parecida mas não pública (%s) continua protegida', async (caminho) => {
      logado()
      const replace = estarEm(caminho)
      mockApi({ 'GET /campaigns': erroApi(401) })

      await expect(api.get('/campaigns')).rejects.toBeDefined()

      expect(replace).toHaveBeenCalledTimes(1)
      expect(sessionStorage.getItem('rc_token')).toBeNull()
    })

  test.each([403, 404, 500])('status %i não derruba a sessão', async (status) => {
    logado()
    const replace = estarEm('/campaigns')
    mockApi({ 'GET /campaigns': erroApi(status) })

    await expect(api.get('/campaigns')).rejects.toMatchObject({ response: { status } })

    expect(replace).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('rc_token')).toBe('token-de-teste')
  })

  test('erro de rede (sem resposta) propaga sem mexer na sessão', async () => {
    logado()
    const replace = estarEm('/campaigns')
    api.defaults.adapter = async () => { throw new Error('Network Error') }

    await expect(api.get('/campaigns')).rejects.toThrow('Network Error')

    expect(replace).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('rc_token')).toBe('token-de-teste')
  })

  test('resposta de sucesso passa intacta', async () => {
    mockApi({ 'GET /campaigns': { data: [{ id: 'k1' }] } })
    const resp = await api.get('/campaigns')
    expect(resp.status).toBe(200)
    expect(resp.data).toEqual({ data: [{ id: 'k1' }] })
  })
})
