import { describe, test, expect, beforeEach, vi } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import BasicDataStep from './BasicDataStep'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'
import { Controlado, escolherOpcao } from '../../test-utils/area-componentes'

// spy único no arquivo: restaurar e recriar o spy de window.alert perde as chamadas
const alerta = vi.spyOn(window, 'alert').mockImplementation(() => {})
beforeEach(() => alerta.mockClear())

const CLIENTE_A = { id: '11111111-1111-1111-1111-111111111111', name: 'Cliente Teste A' }
const CLIENTE_B = { id: '22222222-2222-2222-2222-222222222222', name: 'Cliente Teste B' }
const CLIENTES = [CLIENTE_A, CLIENTE_B]

const VAZIO = { name: '', client_id: '', start_date: '', end_date: '', hub_code: '' }

function montar({ inicial = VAZIO, extra = {}, onMudou = vi.fn() } = {}) {
  const onBloqueioChange = vi.fn()
  const r = renderComProviders(
    <Controlado Comp={BasicDataStep} inicial={inicial} onMudou={onMudou}
      extra={{ clients: CLIENTES, isEditMode: false, onBloqueioChange, ...extra }} />,
  )
  return { ...r, onMudou, onBloqueioChange }
}

const campoCodigo = () => document.getElementById('hub-code')
const ultimo = (fn) => fn.mock.calls[fn.mock.calls.length - 1][0]

describe('BasicDataStep — formulário', () => {
  beforeEach(() => { mockApi({}) })

  test('digitar o nome devolve o valor completo via onChange e atualiza a pré-visualização', () => {
    const { onMudou } = montar({ inicial: { ...VAZIO, start_date: '2026-03-01' } })
    expect(screen.getByText('Sua campanha aparece aqui')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('ex: Verão 2026 — Cliente Hotel'), { target: { value: 'Campanha Teste' } })
    expect(ultimo(onMudou)).toEqual({ ...VAZIO, start_date: '2026-03-01', name: 'Campanha Teste' })
    expect(screen.getAllByText('Campanha Teste').length).toBeGreaterThan(0)
    expect(screen.queryByText('Sua campanha aparece aqui')).toBeNull()
  })

  test('duração em dias corridos: plural, singular e nada com período invertido', () => {
    montar({ inicial: { ...VAZIO, start_date: '2026-03-01', end_date: '2026-03-10' } })
    expect(screen.getByText('Campanha de 10 dias corridos')).toBeTruthy()
    expect(screen.getByText('01/03/2026')).toBeTruthy()
    expect(screen.getByText('10/03/2026')).toBeTruthy()
    expect(screen.getByText('10 dias')).toBeTruthy()
  })

  test('um dia só usa o singular; período invertido some com a duração', () => {
    montar({ inicial: { ...VAZIO, start_date: '2026-03-05', end_date: '2026-03-05' } })
    expect(screen.getByText(/^Campanha de 1 dia/)).toBeTruthy()
    expect(screen.getByText('1 dia')).toBeTruthy()
  })

  test('período invertido não mostra a duração', () => {
    montar({ inicial: { ...VAZIO, start_date: '2026-03-10', end_date: '2026-03-01' } })
    expect(screen.queryByText(/Campanha de/)).toBeNull()
    expect(screen.getByText('—', { selector: 'span' })).toBeTruthy()
  })

  test('datas: início e fim mudam o campo certo; o fim tem min = início', () => {
    const { onMudou, container } = montar({ inicial: { ...VAZIO, start_date: '2026-03-01' } })
    const [inicio, fim] = container.querySelectorAll('input[type="date"]')
    expect(fim.getAttribute('min')).toBe('2026-03-01')
    fireEvent.change(fim, { target: { value: '2026-03-31' } })
    expect(ultimo(onMudou).end_date).toBe('2026-03-31')
    fireEvent.change(inicio, { target: { value: '2026-02-20' } })
    expect(ultimo(onMudou)).toMatchObject({ start_date: '2026-02-20', end_date: '2026-03-31' })
  })

  test('escolher o cliente grava client_id e mostra o nome na pré-visualização', () => {
    const { onMudou } = montar()
    expect(screen.getByText('Cliente não selecionado')).toBeTruthy()
    const [caixaCliente] = screen.getAllByRole('combobox')
    escolherOpcao(caixaCliente, 'Cliente Teste B')
    expect(ultimo(onMudou).client_id).toBe(CLIENTE_B.id)
    expect(screen.getAllByText('Cliente Teste B').length).toBeGreaterThan(0)
  })

  test('em modo edição o cliente fica travado e o código do hub deixa de ser obrigatório', () => {
    montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id }, extra: { isEditMode: true } })
    expect(screen.getByText(/Cliente trava após criar a campanha/)).toBeTruthy()
    expect(document.querySelector('[aria-disabled="true"]')).not.toBeNull()
    expect(screen.queryByRole('combobox')).toBeNull()
    expect(campoCodigo().required).toBe(false)
  })

  test('na criação o código do hub é obrigatório e a dica pede pra criar no hub primeiro', () => {
    montar()
    expect(campoCodigo().required).toBe(true)
    expect(screen.getByText(/Crie a campanha no hub primeiro/)).toBeTruthy()
  })

  test('com código digitado e ainda sem sair do campo mostra "Confere quando você sair do campo"', () => {
    montar({ inicial: { ...VAZIO, hub_code: 'EH-7K4M2X' } })
    expect(screen.getByText('Confere quando você sair do campo.')).toBeTruthy()
  })
})

describe('BasicDataStep — conferência do código do hub', () => {
  test('código válido: confere no hub com o cliente, mostra campanha e período e grava a forma canônica', async () => {
    const chamadas = mockApi({
      'GET /hub-codes/eh7k4m2x': {
        nome: 'Verão Teste', codigo: 'EH-7K4M2X', inicio: '2026-12-01', fim: '2027-02-28',
        ligacao: { estado: 'ligado', mensagem: 'ok' },
      },
    })
    const { onMudou } = montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'eh7k4m2x' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText('Verão Teste')).toBeTruthy()
    expect(screen.getByText('Confere:')).toBeTruthy()
    expect(screen.getByText(/01\/12\/2026 a 28\/02\/2027/)).toBeTruthy()
    const get = chamadas.find(c => c.chave === 'GET /hub-codes/eh7k4m2x')
    expect(get.params).toEqual({ client_id: CLIENTE_A.id })
    // o servidor devolve o código canônico e a tela passa a guardar ele
    await waitFor(() => expect(ultimo(onMudou).hub_code).toBe('EH-7K4M2X'))
  })

  test('não reconfere um código já conferido ao sair do campo de novo (poupa o balde do hub)', async () => {
    const chamadas = mockApi({
      'GET /hub-codes/EH-AAAAAA': { nome: 'Campanha Teste', codigo: 'EH-AAAAAA', ligacao: { estado: 'ligado', mensagem: '' } },
    })
    montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-AAAAAA' } })
    fireEvent.blur(campoCodigo())
    await screen.findByText('Confere:')
    fireEvent.blur(campoCodigo())
    fireEvent.blur(campoCodigo())
    expect(chamadas.filter(c => c.chave === 'GET /hub-codes/EH-AAAAAA').length).toBe(1)
  })

  test('campo vazio ao sair não vai ao servidor e mostra a dica inicial', async () => {
    const chamadas = mockApi({})
    montar()
    fireEvent.blur(campoCodigo())
    expect(chamadas.length).toBe(0)
    expect(screen.getByText(/Crie a campanha no hub primeiro/)).toBeTruthy()
  })

  test('404 é o único vermelho: "Código não encontrado na Central"', async () => {
    mockApi({ 'GET /hub-codes/EH-XXXXXX': erroApi(404, {}) })
    montar({ inicial: { ...VAZIO, hub_code: 'EH-XXXXXX' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText(/Código não encontrado na Central/)).toBeTruthy()
    expect(campoCodigo().getAttribute('aria-invalid')).toBe('true')
  })

  test.each([[503], [502], [500]])('erro %i vira âmbar ("não deu para conferir") e NÃO trava o Avançar', async (status) => {
    mockApi({ 'GET /hub-codes/EH-YYYYYY': erroApi(status, {}) })
    const { onBloqueioChange } = montar({ inicial: { ...VAZIO, hub_code: 'EH-YYYYYY' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText(/Não deu para conferir agora/)).toBeTruthy()
    expect(campoCodigo().getAttribute('aria-invalid')).toBe('false')
    expect(onBloqueioChange).not.toHaveBeenCalledWith(true)
  })

  test('200 com corpo inutilizável não vira verde: cai em "não conferido"', async () => {
    mockApi({ 'GET /hub-codes/EH-ZZZZZZ': {} })
    montar({ inicial: { ...VAZIO, hub_code: 'EH-ZZZZZZ' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText(/Não deu para conferir agora/)).toBeTruthy()
    expect(screen.queryByText('Confere:')).toBeNull()
  })

  test('servidor antigo sem `ligacao`: ponte de outro cliente vira "Esse código é da campanha …"', async () => {
    mockApi({
      'GET /hub-codes/EH-BBBBBB': {
        nome: 'Campanha Alheia', codigo: 'EH-BBBBBB',
        cliente: { nome: 'Cliente Teste B', idNaPlataforma: CLIENTE_B.id },
      },
    })
    montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-BBBBBB' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText('Campanha Alheia')).toBeTruthy()
    expect(screen.getByText(/Escolha esse cliente acima ou cole outro código/)).toBeTruthy()
    expect(campoCodigo().getAttribute('aria-invalid')).toBe('true')
  })

  test('servidor antigo: a ponte é comparada por UUID (sem hífen e urn: contam como o mesmo cliente)', async () => {
    mockApi({
      'GET /hub-codes/EH-CCCCCC': {
        nome: 'Campanha Teste', codigo: 'EH-CCCCCC',
        cliente: { nome: 'Cliente Teste A', idNaPlataforma: 'urn:uuid:' + CLIENTE_A.id.replace(/-/g, '').toUpperCase() },
      },
    })
    montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-CCCCCC' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText('Confere:')).toBeTruthy()
    expect(screen.queryByText(/Esse código é da campanha/)).toBeNull()
  })

  test('em modo edição confere ao abrir, sem reescrever o código do campo', async () => {
    const chamadas = mockApi({
      'GET /hub-codes/eh-ddddd': { nome: 'Campanha Teste', codigo: 'EH-DDDDD', ligacao: { estado: 'ligado', mensagem: '' } },
    })
    const { onMudou } = montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'eh-ddddd' }, extra: { isEditMode: true } })
    expect(await screen.findByText('Confere:')).toBeTruthy()
    expect(chamadas.filter(c => c.chave === 'GET /hub-codes/eh-ddddd').length).toBe(1)
    // editar nome depois não leva um hub_code "corrigido" pelo servidor
    expect(onMudou).not.toHaveBeenCalled()
  })
})

describe('BasicDataStep — ligação entre os clientes', () => {
  const faltaLigar = {
    nome: 'Campanha Teste', codigo: 'EH-EEEEEE',
    ligacao: { estado: 'faltaLigar', mensagem: 'O cliente do hub ainda não está ligado a este.' },
  }

  async function conferirFaltaLigar(extraRotas = {}) {
    const chamadas = mockApi({ 'GET /hub-codes/EH-EEEEEE': faltaLigar, ...extraRotas })
    const r = montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-EEEEEE' } })
    fireEvent.blur(campoCodigo())
    await screen.findByText('O cliente do hub ainda não está ligado a este.')
    return { chamadas, ...r }
  }

  test('"faltaLigar" mostra a mensagem do servidor, o botão e trava o Avançar', async () => {
    const { onBloqueioChange } = await conferirFaltaLigar()
    expect(screen.getByRole('button', { name: 'Ligar os dois clientes' })).toBeTruthy()
    await waitFor(() => expect(onBloqueioChange).toHaveBeenCalledWith(true))
    // o código em si é válido: não fica marcado como inválido
    expect(campoCodigo().getAttribute('aria-invalid')).toBe('false')
  })

  test('Ligar envia o client_id; ao dar certo o botão some, o Avançar destrava e a caixa ganha foco', async () => {
    const { chamadas, onBloqueioChange } = await conferirFaltaLigar({
      'POST /hub-codes/EH-EEEEEE/ligar': { ligacao: { estado: 'ligado', mensagem: 'Clientes ligados.' } },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Ligar os dois clientes' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: /Ligar os dois clientes|Ligando/ })).toBeNull())
    const post = chamadas.find(c => c.chave === 'POST /hub-codes/EH-EEEEEE/ligar')
    expect(post.data).toEqual({ client_id: CLIENTE_A.id })
    expect(screen.getByText('Confere:')).toBeTruthy()
    expect(onBloqueioChange).toHaveBeenLastCalledWith(false)
    expect(document.activeElement).toBe(screen.getByRole('status'))
  })

  test('409 com ligação "bloqueado" aplica o veredito do servidor (vermelho, sem botão, trava)', async () => {
    const { onBloqueioChange } = await conferirFaltaLigar({
      'POST /hub-codes/EH-EEEEEE/ligar': erroApi(409, { ligacao: { estado: 'bloqueado', mensagem: 'Já ligado a outro cliente.' } }),
    })
    fireEvent.click(screen.getByRole('button', { name: 'Ligar os dois clientes' }))
    expect(await screen.findByText('Já ligado a outro cliente.')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Ligar os dois clientes' })).toBeNull()
    expect(campoCodigo().getAttribute('aria-invalid')).toBe('true')
    expect(onBloqueioChange).toHaveBeenLastCalledWith(true)
  })

  test('erro sem `ligacao` vira alerta genérico e o botão continua lá', async () => {
    await conferirFaltaLigar({ 'POST /hub-codes/EH-EEEEEE/ligar': erroApi(500, 'falhou') })
    fireEvent.click(screen.getByRole('button', { name: 'Ligar os dois clientes' }))
    await waitFor(() => expect(alerta).toHaveBeenCalledWith('Não deu para ligar agora. Tente de novo em instantes.'))
    expect(screen.getByRole('button', { name: 'Ligar os dois clientes' })).toBeTruthy()
  })

  test('200 sem `ligacao` NÃO destrava: alerta e segue travado', async () => {
    const { onBloqueioChange } = await conferirFaltaLigar({ 'POST /hub-codes/EH-EEEEEE/ligar': { ok: true } })
    fireEvent.click(screen.getByRole('button', { name: 'Ligar os dois clientes' }))
    await waitFor(() => expect(alerta).toHaveBeenCalled())
    expect(onBloqueioChange).not.toHaveBeenLastCalledWith(false)
  })

  test('clique repetido durante "Ligando…" manda só um POST', async () => {
    let solta
    const espera = new Promise(r => { solta = r })
    const { chamadas } = await conferirFaltaLigar({
      'POST /hub-codes/EH-EEEEEE/ligar': async () => { await espera; return { ligacao: { estado: 'ligado', mensagem: '' } } },
    })
    const botao = screen.getByRole('button', { name: 'Ligar os dois clientes' })
    fireEvent.click(botao)
    expect(await screen.findByRole('button', { name: 'Ligando…' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ligando…' }))
    solta()
    await waitFor(() => expect(screen.queryByRole('button', { name: /Ligar|Ligando/ })).toBeNull())
    expect(chamadas.filter(c => c.chave === 'POST /hub-codes/EH-EEEEEE/ligar').length).toBe(1)
  })

  test('ligação duplicada também oferece o botão', async () => {
    mockApi({
      'GET /hub-codes/EH-FFFFFF': { nome: 'Campanha Teste', codigo: 'EH-FFFFFF', ligacao: { estado: 'ligacaoNoDuplicado', mensagem: 'Existe um cadastro duplicado no hub.' } },
    })
    montar({ inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-FFFFFF' } })
    fireEvent.blur(campoCodigo())
    expect(await screen.findByText('Existe um cadastro duplicado no hub.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Ligar os dois clientes' })).toBeTruthy()
  })

  test('em edição, enquanto a conferência ao abrir está em voo o Avançar fica travado', async () => {
    let solta
    const espera = new Promise(r => { solta = r })
    mockApi({
      'GET /hub-codes/EH-GGGGGG': async () => { await espera; return { nome: 'Campanha Teste', codigo: 'EH-GGGGGG', ligacao: { estado: 'ligado', mensagem: '' } } },
    })
    const { onBloqueioChange } = montar({
      inicial: { ...VAZIO, client_id: CLIENTE_A.id, hub_code: 'EH-GGGGGG' }, extra: { isEditMode: true },
    })
    expect(await screen.findByText('Conferindo com a Central…')).toBeTruthy()
    await waitFor(() => expect(onBloqueioChange).toHaveBeenCalledWith(true))
    solta()
    expect(await screen.findByText('Confere:')).toBeTruthy()
    await waitFor(() => expect(onBloqueioChange).toHaveBeenLastCalledWith(false))
  })
})
