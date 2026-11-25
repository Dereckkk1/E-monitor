import { describe, test, expect, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import ClientsPage from './ClientsPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const CLIENTES = [
  { id: 'cli-1', name: 'Cliente Teste Alfa', city: 'Cidade Teste', state: 'SP', contact_name: 'Contato Teste',
    cnpj: '11.222.333/0001-44', contact_email: 'alfa@example.com', phone: '(11) 99999-0000', is_active: true },
  { id: 'cli-2', name: 'Cliente Teste Beta', city: 'Vila Teste', state: 'MG', is_active: false },
]

const listagem = (data = CLIENTES, extra = {}) => ({ data, total: data.length, total_pages: 1, ...extra })

describe('ClientsPage', () => {
  let chamadas

  function montar(rotas = {}) {
    chamadas = mockApi({ 'GET /clients': listagem(), ...rotas })
    return renderComProviders(<ClientsPage />)
  }
  const ultimaListagem = () => chamadas.filter(c => c.chave === 'GET /clients').pop()

  beforeEach(() => { chamadas = null })

  test('lista os clientes com local, contato, chips e links pras telas do cliente', async () => {
    montar()
    const linhaAlfa = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    expect(linhaAlfa.textContent).toContain('Cidade Teste, SP')
    expect(linhaAlfa.textContent).toContain('Contato Teste')
    expect(within(linhaAlfa).getByText('11.222.333/0001-44')).toBeTruthy()
    expect(within(linhaAlfa).getByText('alfa@example.com')).toBeTruthy()
    expect(within(linhaAlfa).getByText('(11) 99999-0000')).toBeTruthy()
    expect(within(linhaAlfa).getByTitle('Entregas de webhook').getAttribute('href')).toBe('/clients/cli-1/webhooks')
    expect(within(linhaAlfa).getByTitle('API Keys').getAttribute('href')).toBe('/clients/cli-1/api-keys')
    expect(within(linhaAlfa).getByTitle('PMM no target').getAttribute('href')).toBe('/clients/cli-1/target-pmm')
    // o contador do cabeçalho e o rodapé da paginação
    expect(screen.getByText('Clientes').closest('div').textContent).toContain('2')
    expect(screen.getByText('2 clientes')).toBeTruthy()
    expect(ultimaListagem().params).toEqual({ page: 1, page_size: 20 })
  })

  test('cliente inativo é marcado e oferece reativar em vez de excluir', async () => {
    montar()
    const linhaBeta = (await screen.findByText('Cliente Teste Beta')).closest('.client-row')
    expect(within(linhaBeta).getByText('Inativo')).toBeTruthy()
    expect(within(linhaBeta).getByTitle('Reativar')).toBeTruthy()
    expect(within(linhaBeta).queryByTitle('Excluir')).toBeNull()
    const linhaAlfa = screen.getByText('Cliente Teste Alfa').closest('.client-row')
    expect(within(linhaAlfa).getByTitle('Excluir')).toBeTruthy()
    expect(within(linhaAlfa).queryByText('Inativo')).toBeNull()
  })

  test('busca com debounce manda q pra API e "Limpar" volta à lista completa', async () => {
    montar()
    await screen.findByText('Cliente Teste Alfa')
    fireEvent.change(screen.getByPlaceholderText('Buscar por nome, CNPJ, cidade, e-mail…'), { target: { value: 'alfa' } })
    // debounce de 300ms: logo depois da digitação ainda não buscou
    expect(ultimaListagem().params.q).toBeUndefined()
    await waitFor(() => expect(ultimaListagem().params.q).toBe('alfa'))
    fireEvent.click(screen.getByRole('button', { name: 'Limpar' }))
    expect(screen.getByPlaceholderText('Buscar por nome, CNPJ, cidade, e-mail…').value).toBe('')
    await waitFor(() => expect(ultimaListagem().params.q).toBeUndefined())
  })

  test('busca sem resultado mostra o estado filtrado com "Limpar busca"', async () => {
    montar()
    await screen.findByText('Cliente Teste Alfa')
    mockApi({ 'GET /clients': listagem([]) })
    fireEvent.change(screen.getByPlaceholderText('Buscar por nome, CNPJ, cidade, e-mail…'), { target: { value: 'zzz' } })
    expect(await screen.findByText('Nenhum cliente encontrado')).toBeTruthy()
    mockApi({ 'GET /clients': listagem() })
    fireEvent.click(screen.getByRole('button', { name: 'Limpar busca' }))
    expect(await screen.findByText('Cliente Teste Alfa')).toBeTruthy()
  })

  test('"Mostrar inativos" liga include_inactive na consulta', async () => {
    montar()
    await screen.findByText('Cliente Teste Alfa')
    const botao = screen.getByRole('button', { name: 'Mostrar inativos' })
    expect(botao.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(botao)
    expect(botao.getAttribute('aria-pressed')).toBe('true')
    await waitFor(() => expect(ultimaListagem().params.include_inactive).toBe(1))
  })

  test('catálogo vazio mostra o convite pra cadastrar o primeiro cliente', async () => {
    montar({ 'GET /clients': listagem([]) })
    expect(await screen.findByText('Nenhum cliente cadastrado')).toBeTruthy()
    fireEvent.click(within(document.querySelector('.clients-empty')).getByRole('button', { name: /Novo cliente/ }))
    expect(within(document.querySelector('.modal')).getByText('Novo cliente')).toBeTruthy()
  })

  test('paginação: com 3 páginas, clicar na 2 pede page=2', async () => {
    montar({ 'GET /clients': listagem(CLIENTES, { total: 45, total_pages: 3 }) })
    await screen.findByText('Cliente Teste Alfa')
    expect(screen.getByText('Mostrando 1–20 de 45 clientes')).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Página 2'))
    await waitFor(() => expect(ultimaListagem().params.page).toBe(2))
  })

  test('novo cliente: aplica máscara de CNPJ/CEP, aparece vazio como null e envia o payload', async () => {
    montar({ 'POST /clients': { id: 'cli-3' } })
    await screen.findByText('Cliente Teste Alfa')
    fireEvent.click(screen.getAllByRole('button', { name: /Novo cliente/ })[0])
    const modal = document.querySelector('.modal')
    fireEvent.change(within(modal).getByPlaceholderText('Nome do cliente'), { target: { value: '  Cliente Novo Teste  ' } })
    fireEvent.change(within(modal).getByPlaceholderText('00.000.000/0000-00'), { target: { value: '12345678000199' } })
    expect(within(modal).getByPlaceholderText('00.000.000/0000-00').value).toBe('12.345.678/0001-99')
    fireEvent.change(within(modal).getByPlaceholderText('00000-000'), { target: { value: '01310100' } })
    expect(within(modal).getByPlaceholderText('00000-000').value).toBe('01310-100')
    fireEvent.change(within(modal).getByPlaceholderText('SP'), { target: { value: 'sp' } })
    fireEvent.change(within(modal).getByPlaceholderText('Homens 25-49, classe AB'), { target: { value: 'Mulheres 25-49' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar cliente' }))

    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /clients')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /clients').data).toEqual({
      name: 'Cliente Novo Teste', logo_url: null, contact_email: null, contact_name: null, phone: null,
      cnpj: '12.345.678/0001-99', cep: '01310-100', city: null, state: 'SP', target_label: 'Mulheres 25-49',
    })
    await waitFor(() => expect(document.querySelector('.modal')).toBeNull())
  })

  test('erro ao criar mantém o formulário aberto com a mensagem', async () => {
    montar({ 'POST /clients': erroApi(500, {}) })
    await screen.findByText('Cliente Teste Alfa')
    fireEvent.click(screen.getAllByRole('button', { name: /Novo cliente/ })[0])
    const modal = document.querySelector('.modal')
    fireEvent.change(within(modal).getByPlaceholderText('Nome do cliente'), { target: { value: 'Cliente X' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar cliente' }))
    expect(await within(modal).findByText('Erro ao salvar. Tente novamente.')).toBeTruthy()
    fireEvent.click(within(modal).getByRole('button', { name: 'Cancelar' }))
    expect(document.querySelector('.modal')).toBeNull()
  })

  test('CNPJ repetido (409): avisa, fecha o form e filtra a lista pelo cliente existente', async () => {
    montar({ 'POST /clients': erroApi(409, { existente: { nome: 'Cliente Teste Alfa' } }) })
    await screen.findByText('Cliente Teste Alfa')
    fireEvent.click(screen.getAllByRole('button', { name: /Novo cliente/ })[0])
    const modal = document.querySelector('.modal')
    fireEvent.change(within(modal).getByPlaceholderText('Nome do cliente'), { target: { value: 'Duplicado Teste' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar cliente' }))
    expect(await screen.findByText('Já existe Cliente Teste Alfa com este CNPJ. Use esse cliente em vez de criar outro.')).toBeTruthy()
    await waitFor(() => expect(document.querySelector('.modal')).toBeNull())
    expect(screen.getByPlaceholderText('Buscar por nome, CNPJ, cidade, e-mail…').value).toBe('Cliente Teste Alfa')
    await waitFor(() => expect(ultimaListagem().params.q).toBe('Cliente Teste Alfa'))
  })

  test('editar abre com os dados do cliente e salva por PUT', async () => {
    montar({ 'PUT /clients/cli-1': { ok: true } })
    const linha = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Editar'))
    const modal = document.querySelector('.modal')
    expect(within(modal).getByText('Editar cliente')).toBeTruthy()
    expect(within(modal).getByPlaceholderText('Nome do cliente').value).toBe('Cliente Teste Alfa')
    fireEvent.change(within(modal).getByPlaceholderText('São Paulo'), { target: { value: '' } })
    fireEvent.click(within(modal).getByRole('button', { name: 'Salvar alterações' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'PUT /clients/cli-1')).toBe(true))
    const put = chamadas.find(c => c.chave === 'PUT /clients/cli-1').data
    expect(put).toMatchObject({ name: 'Cliente Teste Alfa', city: null, state: 'SP', cnpj: '11.222.333/0001-44' })
    expect(put.id).toBeUndefined()
  })

  test('excluir pede confirmação; cancelar não chama; confirmar chama DELETE', async () => {
    montar({ 'DELETE /clients/cli-1': {} })
    const linha = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Excluir'))
    expect(await screen.findByText('Excluir o cliente "Cliente Teste Alfa"? Essa ação não pode ser desfeita.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText('Confirmar ação')).toBeNull())
    expect(chamadas.some(c => c.chave === 'DELETE /clients/cli-1')).toBe(false)

    fireEvent.click(within(linha).getByTitle('Excluir'))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'DELETE /clients/cli-1')).toBe(true))
  })

  test('excluir cliente com vínculos (409) explica o que bloqueia e oferece desativar', async () => {
    montar({
      'DELETE /clients/cli-1': erroApi(409, { campaigns: 2, materials: 1, users: 3 }),
      'POST /clients/cli-1/deactivate': { ok: true },
    })
    const linha = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Excluir'))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    expect(await screen.findByText(/tem 2 campanhas, 1 material, 3 usuários/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /clients/cli-1/deactivate')).toBe(true))
  })

  test('falha genérica ao excluir avisa com um alerta', async () => {
    montar({ 'DELETE /clients/cli-1': erroApi(500, {}) })
    const linha = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Excluir'))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    expect(await screen.findByText('Não foi possível excluir o cliente. Tente novamente.')).toBeTruthy()
  })

  test('reativar cliente inativo confirma e chama a rota de ativação', async () => {
    montar({ 'POST /clients/cli-2/activate': { ok: true } })
    const linha = (await screen.findByText('Cliente Teste Beta')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Reativar'))
    expect(await screen.findByText('Reativar o cliente "Cliente Teste Beta"? Os usuários dele voltam a conseguir entrar.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /clients/cli-2/activate')).toBe(true))
  })

  test('o botão de webhook abre o modal de configuração do cliente', async () => {
    montar({ 'GET /clients/cli-1/webhook': { webhook_url: null, webhook_enabled: false, webhook_events: [] } })
    const linha = (await screen.findByText('Cliente Teste Alfa')).closest('.client-row')
    fireEvent.click(within(linha).getByTitle('Webhook (configurar)'))
    expect(await screen.findByText('Webhook · Cliente Teste Alfa')).toBeTruthy()
  })
})
