import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import LoginPage from './LoginPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

const USUARIO = { id: 'u1', email: 'admin@example.com', name: 'Admin Teste', role: 'admin' }

function montar(rotas = {}, rota = '/login') {
  const chamadas = mockApi({ 'POST /auth/login': { token: 'jwt-de-teste', user: USUARIO }, ...rotas })
  renderComProviders(<><LoginPage /><LocalizacaoAtual /></>, { usuario: null, rota })
  return chamadas
}

const campoUsuario = () => screen.getByPlaceholderText('seu@email.com ou usuário')
const campoSenha = () => screen.getByPlaceholderText('••••••••')
const entrar = () => screen.getByRole('button', { name: 'Entrar' })

describe('LoginPage', () => {
  test('o botão Entrar só habilita com usuário e senha preenchidos', () => {
    montar()
    expect(entrar().disabled).toBe(true)
    fireEvent.change(campoUsuario(), { target: { value: '   ' } })
    fireEvent.change(campoSenha(), { target: { value: 'segredo' } })
    expect(entrar().disabled).toBe(true) // usuário só com espaços não vale
    fireEvent.change(campoUsuario(), { target: { value: 'admin@example.com' } })
    expect(entrar().disabled).toBe(false)
  })

  test('login com sucesso guarda a sessão e vai pra raiz; o identificador é aparado', async () => {
    const chamadas = montar()
    fireEvent.change(campoUsuario(), { target: { value: '  admin@example.com ' } })
    fireEvent.change(campoSenha(), { target: { value: 'segredo-123' } })
    fireEvent.click(entrar())
    await waitFor(() => expect(urlAtual(screen)).toBe('/'))
    expect(chamadas.find(c => c.chave === 'POST /auth/login').data).toEqual({ email: 'admin@example.com', password: 'segredo-123' })
    expect(sessionStorage.getItem('rc_token')).toBe('jwt-de-teste')
    expect(JSON.parse(sessionStorage.getItem('rc_user')).email).toBe('admin@example.com')
  })

  test('volta pro destino do parâmetro ?next= depois de entrar', async () => {
    montar({}, '/login?next=' + encodeURIComponent('/campaigns?status=ativa'))
    fireEvent.change(campoUsuario(), { target: { value: 'meuusuario' } })
    fireEvent.change(campoSenha(), { target: { value: 'segredo-123' } })
    fireEvent.click(entrar())
    await waitFor(() => expect(urlAtual(screen)).toBe('/campaigns?status=ativa'))
  })

  test.each([
    [401, 'Credenciais inválidas.'],
    [403, 'Acesso desativado. Procure o administrador.'],
    [400, 'Requisição inválida.'],
    [500, 'Não foi possível entrar. Tente novamente em instantes.'],
  ])('erro %i mostra "%s" e não grava sessão', async (status, mensagem) => {
    montar({ 'POST /auth/login': erroApi(status, { error: 'x' }) })
    fireEvent.change(campoUsuario(), { target: { value: 'admin@example.com' } })
    fireEvent.change(campoSenha(), { target: { value: 'errada' } })
    fireEvent.click(entrar())
    expect((await screen.findByRole('alert')).textContent).toContain(mensagem)
    expect(sessionStorage.getItem('rc_token')).toBeNull()
    expect(urlAtual(screen)).toBe('/login')
    // digitar de novo limpa o aviso
    fireEvent.change(campoSenha(), { target: { value: 'outra' } })
    expect(screen.queryByRole('alert')).toBeNull()
  })

  test('o olho alterna entre mostrar e ocultar a senha', () => {
    montar()
    expect(campoSenha().type).toBe('password')
    fireEvent.click(screen.getByLabelText('Mostrar senha'))
    expect(campoSenha().type).toBe('text')
    fireEvent.click(screen.getByLabelText('Ocultar senha'))
    expect(campoSenha().type).toBe('password')
  })
})
