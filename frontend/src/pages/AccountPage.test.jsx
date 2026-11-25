import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import AccountPage from './AccountPage'
import { renderComProviders, USUARIO_CLIENTE } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const ME_CLIENTE = {
  id: USUARIO_CLIENTE.id,
  email: 'cliente@example.com',
  name: 'Cliente Teste',
  phone: '(11) 91234-5678',
  username: 'cliente.teste',
  role: 'viewer',
  created_at: '2026-03-10T15:00:00Z',
  updated_at: '2026-05-20T15:00:00Z',
  last_login_at: '2026-07-15T13:30:00Z',
}

describe('AccountPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-07-15T15:00:00Z'))
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas = {}, opts = { usuario: USUARIO_CLIENTE }) {
    chamadas = mockApi({
      'GET /auth/me': ME_CLIENTE,
      'GET /clients': { data: [{ id: USUARIO_CLIENTE.client_id, name: 'Cliente Teste Ltda' }] },
      ...rotas,
    })
    return renderComProviders(<AccountPage />, opts)
  }

  test('cliente: identidade, vínculo e ficha técnica vêm do /auth/me e do /clients', async () => {
    montar()
    expect(await screen.findByText('Minha conta')).toBeTruthy()
    expect(screen.getByText('conta ·', { exact: false }).textContent).toContain('cliente')
    // meta strip: papel · vínculo · membro desde
    const meta = document.querySelector('.account-masthead-meta')
    await waitFor(() => expect(meta.textContent).toContain('Cliente Teste Ltda'))
    expect(meta.textContent).toContain('Cliente')
    expect(meta.textContent).toContain('membro desde mar 2026')

    const defs = document.querySelector('.account-defs')
    expect(within(defs).getByText('cliente@example.com')).toBeTruthy()
    expect(within(defs).getByText('cliente.teste')).toBeTruthy()
    expect(within(defs).getByText('vínculo')).toBeTruthy()

    const ficha = screen.getByLabelText('Ficha técnica da conta')
    expect(within(ficha).getByText('10 mar 2026')).toBeTruthy()
    expect(within(ficha).getByText('20 mai 2026')).toBeTruthy()
    expect(within(ficha).getByText('hoje, 10:30')).toBeTruthy()
    // cliente não vê a integração MCP (só admin)
    expect(screen.queryByText('Integração', { exact: false })).toBeNull()
  })

  test('último acesso de ontem e de dias atrás usa o formato certo', async () => {
    montar({ 'GET /auth/me': { ...ME_CLIENTE, last_login_at: '2026-07-14T21:05:00Z' } })
    const ficha = await screen.findByLabelText('Ficha técnica da conta')
    expect(within(ficha).getByText('ontem, 18:05')).toBeTruthy()
  })

  test('último acesso antigo mostra data curta; sem login a linha some', async () => {
    montar({ 'GET /auth/me': { ...ME_CLIENTE, last_login_at: '2026-06-02T12:00:00Z' } })
    const ficha = await screen.findByLabelText('Ficha técnica da conta')
    expect(within(ficha).getByText('02 jun, 09:00')).toBeTruthy()
  })

  test('sem datas de login a ficha não mostra "último acesso"', async () => {
    montar({ 'GET /auth/me': { ...ME_CLIENTE, last_login_at: null } })
    const ficha = await screen.findByLabelText('Ficha técnica da conta')
    expect(within(ficha).queryByText('último acesso')).toBeNull()
    expect(within(ficha).getByText('conta criada')).toBeTruthy()
  })

  test('admin: papel Administrador, meta "E-radios" sem vínculo e seção MCP presente', async () => {
    montar({
      'GET /auth/me': { ...ME_CLIENTE, role: 'admin', username: null },
      'GET /clients': { data: [] },
    }, {})
    expect(await screen.findByText('Minha conta')).toBeTruthy()
    const meta = document.querySelector('.account-masthead-meta')
    expect(meta.textContent).toContain('Administrador')
    expect(meta.textContent).toContain('E-radios')
    expect(screen.getByText('conta ·', { exact: false }).textContent).toContain('operação')
    expect(screen.getByText('§ 04')).toBeTruthy()
  })

  test('dados pessoais: só habilita Salvar com mudança e envia name/phone (vazio vira null)', async () => {
    montar({ 'PATCH /auth/me': { ok: true } })
    await screen.findByText('Minha conta')
    const salvar = screen.getByRole('button', { name: 'Salvar' })
    expect(salvar.disabled).toBe(true)
    expect(screen.getByText('nada a salvar')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('nome'), { target: { value: '  Nome Novo  ' } })
    fireEvent.change(screen.getByLabelText('telefone'), { target: { value: '' } })
    expect(screen.getByText('alterações não salvas')).toBeTruthy()
    expect(screen.getByLabelText('alterações não salvas')).toBeTruthy() // bolinha no título da seção
    expect(salvar.disabled).toBe(false)
    fireEvent.click(salvar)

    await waitFor(() => expect(chamadas.some(c => c.chave === 'PATCH /auth/me')).toBe(true))
    expect(chamadas.find(c => c.chave === 'PATCH /auth/me').data).toEqual({ name: 'Nome Novo', phone: null })
    expect(await screen.findByText('Dados atualizados')).toBeTruthy()
  })

  test('erro ao salvar o perfil aparece no toast com a mensagem do servidor', async () => {
    montar({ 'PATCH /auth/me': erroApi(400, { error: 'nome inválido' }) })
    await screen.findByText('Minha conta')
    fireEvent.change(screen.getByLabelText('nome'), { target: { value: 'X' } })
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByText('Não foi possível salvar')).toBeTruthy()
    expect(screen.getByText('nome inválido')).toBeTruthy()
    // o toast de erro não some sozinho; dá pra fechar
    fireEvent.click(screen.getByLabelText('Fechar'))
    expect(screen.queryByText('Não foi possível salvar')).toBeNull()
  })

  test('força da senha: fraca → média → forte', async () => {
    montar()
    await screen.findByText('Minha conta')
    expect(screen.getByText('mínimo 12 caracteres')).toBeTruthy()
    const nova = screen.getByLabelText('nova senha')
    fireEvent.change(nova, { target: { value: 'abc' } })
    expect(screen.getByText('Fraca')).toBeTruthy()
    fireEvent.change(nova, { target: { value: 'Abcdefghijkl1' } })
    expect(screen.getByText('Média')).toBeTruthy()
    fireEvent.change(nova, { target: { value: 'Abcdefghijkl1234!xyz' } })
    expect(screen.getByText('Forte')).toBeTruthy()
  })

  test('confirmação divergente mostra erro inline e mantém "Alterar senha" desabilitado', async () => {
    montar()
    await screen.findByText('Minha conta')
    fireEvent.change(screen.getByLabelText('senha atual'), { target: { value: 'senha-atual-123' } })
    fireEvent.change(screen.getByLabelText('nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.change(screen.getByLabelText('confirmar nova senha'), { target: { value: 'Nova-senha-diferente' } })
    expect(screen.getByText('As senhas não conferem.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Alterar senha' }).disabled).toBe(true)
    // corrigindo a confirmação o erro some e o botão habilita
    fireEvent.change(screen.getByLabelText('confirmar nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    expect(screen.queryByText('As senhas não conferem.')).toBeNull()
    expect(screen.getByRole('button', { name: 'Alterar senha' }).disabled).toBe(false)
  })

  test('alterar senha envia current/new, limpa o formulário e avisa o sucesso', async () => {
    montar({ 'POST /auth/me/password': { ok: true } })
    await screen.findByText('Minha conta')
    fireEvent.change(screen.getByLabelText('senha atual'), { target: { value: 'senha-atual-123' } })
    fireEvent.change(screen.getByLabelText('nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.change(screen.getByLabelText('confirmar nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Alterar senha' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /auth/me/password')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /auth/me/password').data)
      .toEqual({ current_password: 'senha-atual-123', new_password: 'Nova-senha-forte-1' })
    expect(await screen.findByText('Senha alterada')).toBeTruthy()
    expect(screen.getByLabelText('senha atual').value).toBe('')
    expect(screen.getByLabelText('nova senha').value).toBe('')
  })

  test('senha atual errada: toast com o erro do servidor e campos preservados', async () => {
    montar({ 'POST /auth/me/password': erroApi(403, { error: 'senha atual incorreta' }) })
    await screen.findByText('Minha conta')
    fireEvent.change(screen.getByLabelText('senha atual'), { target: { value: 'errada-errada-1' } })
    fireEvent.change(screen.getByLabelText('nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.change(screen.getByLabelText('confirmar nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Alterar senha' }))
    expect(await screen.findByText('Não foi possível alterar a senha')).toBeTruthy()
    expect(screen.getByText('senha atual incorreta')).toBeTruthy()
    expect(screen.getByLabelText('nova senha').value).toBe('Nova-senha-forte-1')
  })

  test('submit forçado com senha curta ou confirmação diferente não chama a API', async () => {
    montar({ 'POST /auth/me/password': { ok: true } })
    await screen.findByText('Minha conta')
    const form = screen.getByLabelText('nova senha').closest('form')
    fireEvent.change(screen.getByLabelText('nova senha'), { target: { value: 'curta' } })
    fireEvent.submit(form)
    expect(await screen.findByText('Senha muito curta')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('nova senha'), { target: { value: 'Nova-senha-forte-1' } })
    fireEvent.change(screen.getByLabelText('confirmar nova senha'), { target: { value: 'outra-coisa-aqui' } })
    fireEvent.submit(form)
    expect(screen.getByText('As senhas não conferem.')).toBeTruthy()
    expect(chamadas.some(c => c.chave === 'POST /auth/me/password')).toBe(false)
  })

  test('botão do olho alterna a visibilidade do campo de senha', async () => {
    montar()
    await screen.findByText('Minha conta')
    const atual = screen.getByLabelText('senha atual')
    expect(atual.type).toBe('password')
    fireEvent.click(atual.closest('.account-pwd-row').querySelector('.account-eye'))
    expect(atual.type).toBe('text')
  })

  test('falha ao carregar /auth/me mostra o erro e "Tentar novamente" recarrega', async () => {
    let n = 0
    montar({
      'GET /auth/me': () => { n++; return n === 1 ? erroApi(500, {}) : ME_CLIENTE },
    })
    expect(await screen.findByText('Não foi possível carregar sua conta')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Tentar novamente' }))
    expect(await screen.findByText('Minha conta')).toBeTruthy()
  })
})
