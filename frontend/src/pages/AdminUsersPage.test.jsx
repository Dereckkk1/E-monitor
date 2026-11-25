import { describe, test, expect, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import AdminUsersPage from './AdminUsersPage'
import { renderComProviders, USUARIO_ADMIN } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { escolherOpcao, isoHa } from '../test-utils/area-paginas'

const CLIENTES = [
  { id: 'cli-1', name: 'Cliente Teste Alfa' },
  { id: 'cli-2', name: 'Cliente Teste Beta' },
  { id: 'cli-3', name: 'Cliente Teste Gama' },
]

function listaDeUsuarios() {
  return {
    total: 4,
    total_pages: 1,
    data: [
      { id: USUARIO_ADMIN.id, name: 'Admin Teste', email: 'admin@example.com', role: 'admin', is_active: true, last_login_at: isoHa(10) },
      { id: 'u-agencia', name: 'Agência Teste', email: 'agencia@example.com', username: 'agencia.teste', role: 'viewer', is_active: true,
        client_id: 'cli-1', client_ids: ['cli-1', 'cli-2', 'cli-3'], last_login_at: isoHa(3 * 3600) },
      { id: 'u-inativo', name: '', email: 'inativo@example.com', role: 'viewer', is_active: false, client_id: 'cli-2', last_login_at: null },
      { id: 'u-excluido', name: 'Excluído Teste', email: 'excluido@example.com', role: 'viewer', is_active: false,
        deleted_at: '2026-01-01T00:00:00Z', last_login_at: isoHa(40 * 86400) },
    ],
    welcome_invites: {
      'u-agencia': { invite_id: 'inv-1', link: 'https://app.example.com/boasvindas/abc', email_status: 'sent', created_at: '2026-03-10T12:00:00Z', open_count: 2, opened_at: '2026-03-11T12:00:00Z' },
      'u-inativo': { invite_id: 'inv-2', link: 'https://app.example.com/boasvindas/def', revoked_at: '2026-03-12T12:00:00Z', created_at: '2026-03-10T12:00:00Z' },
    },
  }
}

describe('AdminUsersPage', () => {
  let chamadas

  function montar(rotas = {}) {
    chamadas = mockApi({
      'GET /admin/users': listaDeUsuarios(),
      'GET /clients': { data: CLIENTES },
      ...rotas,
    })
    return renderComProviders(<AdminUsersPage />)
  }

  beforeEach(() => { chamadas = null })

  test('lista os usuários com tipo, carteira de clientes, último login e situação', async () => {
    montar()
    const linhaAgencia = (await screen.findByText('agencia@example.com', { selector: '.au-cell-email' })).closest('tr')
    // carteira: primeiro cliente + "+N" e o tooltip com todos
    const cliente = linhaAgencia.querySelector('.au-client-name')
    await waitFor(() => expect(cliente.textContent).toBe('Cliente Teste Alfa +2'))
    expect(cliente.title).toBe('Cliente Teste Alfa, Cliente Teste Beta, Cliente Teste Gama')
    expect(within(linhaAgencia).getByText('Cliente')).toBeTruthy()      // RoleChip viewer
    expect(within(linhaAgencia).getByText('há 3 h')).toBeTruthy()
    expect(within(linhaAgencia).getByText('usuário: agencia.teste', { selector: '.au-username' })).toBeTruthy()

    // o próprio admin: chip "você", sem ação de desativar/excluir
    const linhaEu = screen.getByText('admin@example.com', { selector: '.au-cell-email' }).closest('tr')
    expect(within(linhaEu).getByText('você')).toBeTruthy()
    expect(within(linhaEu).getByText('agora')).toBeTruthy()
    expect(within(linhaEu).getByLabelText('Desativar admin@example.com').disabled).toBe(true)
    expect(within(linhaEu).getByLabelText('Excluir admin@example.com').disabled).toBe(true)

    // inativo sem nome nem login; cliente só por client_id
    const linhaInativo = screen.getByText('inativo@example.com', { selector: '.au-cell-email' }).closest('tr')
    expect(within(linhaInativo).getByText('sem nome')).toBeTruthy()
    expect(within(linhaInativo).getByText('nunca logou')).toBeTruthy()
    expect(within(linhaInativo).getByText('Inativo')).toBeTruthy()
    expect(within(linhaInativo).getByLabelText('Reativar inativo@example.com')).toBeTruthy()

    // excluído: sem editar/resetar, sem desativar/excluir; "há 1 mês"
    const linhaExcluido = screen.getByText('excluido@example.com', { selector: '.au-cell-email' }).closest('tr')
    expect(within(linhaExcluido).getByText('Excluído')).toBeTruthy()
    expect(within(linhaExcluido).getByText('há 1 mês')).toBeTruthy()
    expect(within(linhaExcluido).getByLabelText('Editar excluido@example.com').disabled).toBe(true)
    expect(within(linhaExcluido).queryByLabelText('Excluir excluido@example.com')).toBeNull()

    expect(screen.getByText('4 usuários')).toBeTruthy()
    expect(screen.getByText('de 1', { exact: false })).toBeTruthy()
  })

  test('ícone do convite: link vivo com tooltip do estado e convite revogado só informativo', async () => {
    montar()
    const abrir = await screen.findByLabelText('Abrir a página de boas-vindas de agencia@example.com')
    expect(abrir.getAttribute('href')).toBe('https://app.example.com/boasvindas/abc')
    expect(abrir.title).toContain('Email enviado em 10/03/26')
    expect(abrir.title).toContain('aberto 2×')
    const revogado = screen.getByLabelText('Convite de boas-vindas de inativo@example.com revogado')
    expect(revogado.title).toBe('Convite revogado em 12/03/26')
    expect(screen.queryByLabelText('Revogar convite de boas-vindas de inativo@example.com')).toBeNull()
  })

  test('revogar convite pede confirmação e chama a rota certa; cancelar não chama', async () => {
    montar({ 'POST /admin/welcome-invites/inv-1/revoke': { ok: true } })
    const botao = await screen.findByLabelText('Revogar convite de boas-vindas de agencia@example.com')

    fireEvent.click(botao)
    fireEvent.click(await screen.findByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText('Confirmar ação')).toBeNull())
    expect(chamadas.some(c => c.chave.includes('revoke'))).toBe(false)

    fireEvent.click(botao)
    expect((await screen.findByText(/Revogar o convite de boas-vindas de agencia@example.com/))).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /admin/welcome-invites/inv-1/revoke')).toBe(true))
  })

  test('busca e situação mandam os filtros pra API e voltam pra página 1', async () => {
    montar()
    await screen.findByText('4 usuários')
    fireEvent.change(screen.getByLabelText('Buscar usuários'), { target: { value: 'agencia' } })
    await waitFor(() => {
      const ult = chamadas.filter(c => c.chave === 'GET /admin/users').pop()
      expect(ult.params).toEqual({ status: 'active', page: 1, page_size: 20, q: 'agencia' })
    })
    fireEvent.click(screen.getByRole('tab', { name: 'Inativos' }))
    await waitFor(() => {
      const ult = chamadas.filter(c => c.chave === 'GET /admin/users').pop()
      expect(ult.params).toMatchObject({ status: 'inactive', q: 'agencia' })
    })
    expect(screen.getByRole('tab', { name: 'Inativos' }).getAttribute('aria-selected')).toBe('true')
  })

  test('filtro Clientes revela o seletor de cliente vinculado e o envia como client_id', async () => {
    montar()
    await screen.findByText('4 usuários')
    expect(screen.queryByLabelText('Filtrar por cliente')).toBeNull()
    await escolherOpcao(screen.getByLabelText('Filtrar por tipo'), 'Clientes')
    const seletorCliente = await screen.findByLabelText('Filtrar por cliente')
    await escolherOpcao(seletorCliente, 'Cliente Teste Beta')
    await waitFor(() => {
      const ult = chamadas.filter(c => c.chave === 'GET /admin/users').pop()
      expect(ult.params).toMatchObject({ role: 'client', client_id: 'cli-2' })
    })
    // trocar pra Administradores limpa o cliente e esconde o seletor
    await escolherOpcao(screen.getByLabelText('Filtrar por tipo'), 'Administradores')
    await waitFor(() => expect(screen.queryByLabelText('Filtrar por cliente')).toBeNull())
    const ult = chamadas.filter(c => c.chave === 'GET /admin/users').pop()
    expect(ult.params.client_id).toBeUndefined()
    expect(ult.params.role).toBe('admin')
  })

  test('lista vazia com filtro oferece "Limpar filtros"; sem filtro oferece criar o primeiro acesso', async () => {
    montar({ 'GET /admin/users': { data: [], total: 0, total_pages: 1 } })
    expect(await screen.findByText('Nenhum usuário cadastrado')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Buscar usuários'), { target: { value: 'zzz' } })
    expect(await screen.findByText('Nenhum usuário com esses filtros')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Limpar filtros' }))
    expect(await screen.findByText('Nenhum usuário cadastrado')).toBeTruthy()
    expect(screen.getByLabelText('Buscar usuários').value).toBe('')
  })

  test('paginação: próxima página pede page=2 e o botão trava na última', async () => {
    montar({ 'GET /admin/users': { ...listaDeUsuarios(), total: 45, total_pages: 3 } })
    await screen.findByText('45 usuários')
    const anterior = screen.getByLabelText('Página anterior')
    const proxima = screen.getByLabelText('Próxima página')
    expect(anterior.disabled).toBe(true)
    fireEvent.click(proxima)
    await waitFor(() => expect(chamadas.filter(c => c.chave === 'GET /admin/users').pop().params.page).toBe(2))
    fireEvent.click(proxima)
    await waitFor(() => expect(proxima.disabled).toBe(true))
  })

  test('criar usuário admin: valida o formulário e envia o payload; mostra o link de boas-vindas', async () => {
    montar({
      'POST /admin/users': (config) => ({
        id: 'novo', email: JSON.parse(config.data).email,
        welcome: { link: 'https://app.example.com/boasvindas/xyz', email_status: 'sent' },
      }),
    })
    await screen.findByText('4 usuários')
    fireEvent.click(screen.getByRole('button', { name: /Novo usuário/ }))
    const modal = await screen.findByRole('dialog')
    fireEvent.click(within(modal).getByLabelText(/Administrador/))
    fireEvent.change(within(modal).getByLabelText('Nome *'), { target: { value: 'Novo Admin' } })
    fireEvent.change(within(modal).getByLabelText('Email *'), { target: { value: 'Novo@Example.com' } })
    fireEvent.change(within(modal).getByLabelText('Nome de usuário'), { target: { value: 'NOVO.admin' } })
    fireEvent.change(within(modal).getByLabelText('Senha *'), { target: { value: 'senha-bem-longa-123' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar usuário' }))

    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /admin/users')).toBe(true))
    const post = chamadas.find(c => c.chave === 'POST /admin/users')
    expect(post.data).toEqual({
      role: 'admin', name: 'Novo Admin', phone: null, username: 'novo.admin',
      email: 'novo@example.com', password: 'senha-bem-longa-123', send_welcome: true,
      receive_alert_emails: true, receive_post_sale_emails: false,
    })
    // modal de boas-vindas com o link
    expect(await screen.findByText('Boas-vindas enviadas')).toBeTruthy()
    expect(screen.getByText('https://app.example.com/boasvindas/xyz')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Entendi' }))
    await waitFor(() => expect(screen.queryByText('Boas-vindas enviadas')).toBeNull())
  })

  test('criar usuário Cliente sem cliente vinculado é barrado no formulário', async () => {
    montar()
    await screen.findByText('4 usuários')
    fireEvent.click(screen.getByRole('button', { name: /Novo usuário/ }))
    const modal = await screen.findByRole('dialog')
    fireEvent.change(within(modal).getByLabelText('Nome *'), { target: { value: 'Pessoa Teste' } })
    fireEvent.change(within(modal).getByLabelText('Email *'), { target: { value: 'pessoa@example.com' } })
    fireEvent.change(within(modal).getByLabelText('Senha *'), { target: { value: 'senha-bem-longa-123' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar usuário' }))
    expect((await within(modal).findByRole('alert')).textContent).toBe('Selecione pelo menos um cliente vinculado.')
    expect(chamadas.some(c => c.chave === 'POST /admin/users')).toBe(false)
  })

  test('erro do servidor ao criar (e-mail em uso) aparece traduzido no formulário', async () => {
    montar({ 'POST /admin/users': erroApi(409, 'email_taken') })
    await screen.findByText('4 usuários')
    fireEvent.click(screen.getByRole('button', { name: /Novo usuário/ }))
    const modal = await screen.findByRole('dialog')
    fireEvent.click(within(modal).getByLabelText(/Administrador/))
    fireEvent.change(within(modal).getByLabelText('Nome *'), { target: { value: 'Outro Admin' } })
    fireEvent.change(within(modal).getByLabelText('Email *'), { target: { value: 'admin@example.com' } })
    fireEvent.change(within(modal).getByLabelText('Senha *'), { target: { value: 'senha-bem-longa-123' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar usuário' }))
    expect((await within(modal).findByRole('alert')).textContent).toBe('Já existe um usuário com este e-mail.')
  })

  test('editar: abre com os dados do usuário (email travado) e salva via PATCH com id', async () => {
    montar({ 'PATCH /admin/users/u-agencia': { ok: true } })
    fireEvent.click(await screen.findByLabelText('Editar agencia@example.com'))
    const modal = await screen.findByRole('dialog')
    expect(within(modal).getByText('Editar usuário')).toBeTruthy()
    expect(within(modal).getByLabelText('Email *').disabled).toBe(true)
    expect(within(modal).getByLabelText('Nome *').value).toBe('Agência Teste')
    fireEvent.change(within(modal).getByLabelText('Nome *'), { target: { value: 'Agência Renomeada' } })
    fireEvent.click(within(modal).getByLabelText(/Conta ativa/))
    fireEvent.click(within(modal).getByRole('button', { name: 'Salvar alterações' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'PATCH /admin/users/u-agencia')).toBe(true))
    const patch = chamadas.find(c => c.chave === 'PATCH /admin/users/u-agencia')
    expect(patch.data).toMatchObject({
      role: 'client', name: 'Agência Renomeada', username: 'agencia.teste',
      client_ids: ['cli-1', 'cli-2', 'cli-3'], is_active: false,
    })
    expect(patch.data.id).toBeUndefined()
    await waitFor(() => expect(screen.queryByText('Editar usuário')).toBeNull())
  })

  test('resetar senha envia a nova senha; erro do servidor fica no modal', async () => {
    montar({ 'POST /admin/users/u-agencia/password': erroApi(400, { error: 'senha fraca' }) })
    fireEvent.click(await screen.findByLabelText('Resetar senha de agencia@example.com'))
    const modal = await screen.findByRole('dialog')
    expect(within(modal).getByText('agencia@example.com')).toBeTruthy()
    fireEvent.change(within(modal).getByLabelText('Nova senha *'), { target: { value: 'outra-senha-longa-1' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Resetar senha' }))
    expect((await within(modal).findByRole('alert')).textContent).toBe('senha fraca')
    expect(chamadas.find(c => c.chave === 'POST /admin/users/u-agencia/password').data)
      .toEqual({ password: 'outra-senha-longa-1' })
  })

  test('desativar e excluir pedem confirmação antes de chamar a API', async () => {
    montar({
      'PATCH /admin/users/u-agencia': { ok: true },
      'DELETE /admin/users/u-agencia': { ok: true },
    })
    fireEvent.click(await screen.findByLabelText('Desativar agencia@example.com'))
    expect(await screen.findByText(/Desativar agencia@example.com\? Ele não conseguirá/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'PATCH /admin/users/u-agencia')).toBe(true))
    expect(chamadas.find(c => c.chave === 'PATCH /admin/users/u-agencia').data).toEqual({ is_active: false })

    fireEvent.click(screen.getByLabelText('Excluir agencia@example.com'))
    expect(await screen.findByText(/Excluir agencia@example.com definitivamente/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText('Confirmar ação')).toBeNull())
    expect(chamadas.some(c => c.chave === 'DELETE /admin/users/u-agencia')).toBe(false)

    fireEvent.click(screen.getByLabelText('Excluir agencia@example.com'))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'DELETE /admin/users/u-agencia')).toBe(true))
  })

  test('reativar usuário inativo manda is_active=true', async () => {
    montar({ 'PATCH /admin/users/u-inativo': { ok: true } })
    fireEvent.click(await screen.findByLabelText('Reativar inativo@example.com'))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.find(c => c.chave === 'PATCH /admin/users/u-inativo')?.data).toEqual({ is_active: true }))
  })
})
