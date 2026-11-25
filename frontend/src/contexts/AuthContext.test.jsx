// AuthContext: sessão no sessionStorage + helpers de papel (admin/cliente) que
// Sidebar e RequireRole consomem.
import { describe, test, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { AuthProvider, useAuth, getStoredToken, clearStoredAuth } from './AuthContext'
import { USUARIO_ADMIN, USUARIO_CLIENTE } from '../test-utils/render'

const wrapper = ({ children }) => <AuthProvider>{children}</AuthProvider>

function montar() {
  return renderHook(() => useAuth(), { wrapper })
}

beforeEach(() => {
  sessionStorage.clear()
})

describe('AuthProvider: estado inicial', () => {
  test('sem sessão: deslogado e sem nenhum papel', () => {
    const { result } = montar()
    expect(result.current).toMatchObject({
      token: null, user: null, isAuthenticated: false,
      isAdmin: false, isClient: false, clientId: null, clientIds: [],
    })
  })

  test('retoma a sessão que o refresh deixou no sessionStorage', () => {
    sessionStorage.setItem('rc_token', 'token-de-teste')
    sessionStorage.setItem('rc_user', JSON.stringify(USUARIO_ADMIN))
    const { result } = montar()
    expect(result.current.isAuthenticated).toBe(true)
    expect(result.current.token).toBe('token-de-teste')
    expect(result.current.user).toEqual(USUARIO_ADMIN)
    expect(result.current.isAdmin).toBe(true)
  })

  test('rc_user corrompido não quebra o app: usuário nulo', () => {
    sessionStorage.setItem('rc_token', 'token-de-teste')
    sessionStorage.setItem('rc_user', '{nao-e-json')
    const { result } = montar()
    expect(result.current.user).toBeNull()
    // token existe, então segue "autenticado" — o papel é que fica indefinido
    expect(result.current.isAuthenticated).toBe(true)
    expect(result.current.isAdmin).toBe(false)
  })
})

describe('papéis', () => {
  test.each([
    ['admin', true, false],
    ['operator', true, false], // operator é sinônimo de admin
    ['viewer', false, true], // viewer = "Cliente" no vocabulário do backend
    ['desconhecido', false, false],
  ])('role %s: isAdmin=%s isClient=%s', (role, admin, cliente) => {
    sessionStorage.setItem('rc_token', 't')
    sessionStorage.setItem('rc_user', JSON.stringify({ id: 'u1', role }))
    const { result } = montar()
    expect(result.current.isAdmin).toBe(admin)
    expect(result.current.isClient).toBe(cliente)
  })

  test('cliente com carteira (agência): clientIds é a lista e clientId o principal', () => {
    const usuario = { ...USUARIO_CLIENTE, client_id: 'c1', client_ids: ['c1', 'c2', 'c3'] }
    sessionStorage.setItem('rc_token', 't')
    sessionStorage.setItem('rc_user', JSON.stringify(usuario))
    const { result } = montar()
    expect(result.current.clientId).toBe('c1')
    expect(result.current.clientIds).toEqual(['c1', 'c2', 'c3'])
  })

  test('sessão antiga sem client_ids cai no cliente principal (nunca lista vazia pra Cliente)', () => {
    sessionStorage.setItem('rc_token', 't')
    sessionStorage.setItem('rc_user', JSON.stringify({ id: 'u1', role: 'viewer', client_id: 'c9' }))
    const { result } = montar()
    expect(result.current.clientIds).toEqual(['c9'])
  })

  test('client_ids vazio também cai no principal', () => {
    sessionStorage.setItem('rc_token', 't')
    sessionStorage.setItem('rc_user', JSON.stringify({ id: 'u1', role: 'viewer', client_id: 'c9', client_ids: [] }))
    const { result } = montar()
    expect(result.current.clientIds).toEqual(['c9'])
  })
})

describe('login e logout', () => {
  test('login grava token e usuário no storage e atualiza o estado', () => {
    const { result } = montar()
    act(() => { result.current.login('novo-token', USUARIO_CLIENTE) })

    expect(sessionStorage.getItem('rc_token')).toBe('novo-token')
    expect(JSON.parse(sessionStorage.getItem('rc_user'))).toEqual(USUARIO_CLIENTE)
    expect(result.current.isAuthenticated).toBe(true)
    expect(result.current.isClient).toBe(true)
    expect(result.current.isAdmin).toBe(false)
    expect(result.current.clientIds).toEqual(USUARIO_CLIENTE.client_ids)
  })

  test('login sem usuário guarda só o token e deixa o usuário nulo', () => {
    const { result } = montar()
    act(() => { result.current.login('so-token') })
    expect(sessionStorage.getItem('rc_token')).toBe('so-token')
    expect(sessionStorage.getItem('rc_user')).toBeNull()
    expect(result.current.user).toBeNull()
    expect(result.current.isAuthenticated).toBe(true)
  })

  test('logout apaga tudo do storage e zera o estado', () => {
    sessionStorage.setItem('rc_token', 't')
    sessionStorage.setItem('rc_user', JSON.stringify(USUARIO_ADMIN))
    const { result } = montar()
    act(() => { result.current.logout() })

    expect(sessionStorage.getItem('rc_token')).toBeNull()
    expect(sessionStorage.getItem('rc_user')).toBeNull()
    expect(result.current).toMatchObject({ token: null, user: null, isAuthenticated: false, isAdmin: false })
  })

  test('trocar de admin pra cliente na mesma sessão atualiza os papéis', () => {
    const { result } = montar()
    act(() => { result.current.login('t1', USUARIO_ADMIN) })
    expect(result.current.isAdmin).toBe(true)
    act(() => { result.current.login('t2', USUARIO_CLIENTE) })
    expect(result.current.isAdmin).toBe(false)
    expect(result.current.isClient).toBe(true)
  })
})

describe('useAuth fora do provider', () => {
  test('lança um erro que diz o que faltou', () => {
    // o React loga o erro do render no console; silencia só neste teste
    const erro = console.error
    console.error = () => {}
    try {
      expect(() => renderHook(() => useAuth())).toThrow('useAuth must be used within <AuthProvider>')
    } finally {
      console.error = erro
    }
  })
})

describe('acessores fora do React (usados pelo axios)', () => {
  test('getStoredToken lê o token atual', () => {
    expect(getStoredToken()).toBeNull()
    sessionStorage.setItem('rc_token', 'abc')
    expect(getStoredToken()).toBe('abc')
  })

  test('clearStoredAuth apaga token e usuário', () => {
    sessionStorage.setItem('rc_token', 'abc')
    sessionStorage.setItem('rc_user', '{}')
    sessionStorage.setItem('outra_chave', 'fica')
    clearStoredAuth()
    expect(getStoredToken()).toBeNull()
    expect(sessionStorage.getItem('rc_user')).toBeNull()
    expect(sessionStorage.getItem('outra_chave')).toBe('fica')
  })
})
