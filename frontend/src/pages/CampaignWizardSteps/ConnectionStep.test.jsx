import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import ConnectionStep from './ConnectionStep'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'

const E1 = { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', city: 'Cidade Teste', state: 'MG', stream_url: 'http://stream.example.com/a' }
const E2 = { id: 'st-2', name: 'Rádio Outra AM', band: 'AM', stream_url: 'http://stream.example.com/b' }
const SEM_URL = { id: 'st-3', name: 'Rádio Sem URL', band: 'FM', stream_url: null }

const testes = (chamadas) => chamadas.filter(c => c.metodo === 'POST' && c.url.endsWith('/connection-test'))
// Testes PEDIDOS pela pessoa (ping + stream + worker). O ping automático da
// abertura manda só ['ping'] e fica de fora.
const pedidos = (chamadas) => testes(chamadas).filter(c => c.data.tests.length > 1)
// A abertura já pinga toda emissora com URL, e enquanto isso os botões ficam
// travados: espera esse ping terminar antes de clicar.
const aposPingInicial = () =>
  waitFor(() => expect(screen.getByRole('button', { name: 'Testar todas' }).disabled).toBe(false))
const cartao = (nome) => screen.getByText(nome).closest('div[style*="grid-template-columns"]')

function montar(estacoes, rotas = {}) {
  const chamadas = mockApi({
    'POST /stations/st-1/connection-test': (config) => {
      const { tests } = JSON.parse(config.data)
      const todos = {
        ping: { status: 'ok', latency_ms: 42 },
        stream: { status: 'ok', codec: 'aac' },
        worker: { status: 'ok', source: 'live' },
      }
      return { results: Object.fromEntries(tests.map(t => [t, todos[t]])) }
    },
    'POST /stations/st-2/connection-test': (config) => {
      const { tests } = JSON.parse(config.data)
      return { results: Object.fromEntries(tests.map(t => [t, { status: 'fail', detail: 'timeout' }])) }
    },
    ...rotas,
  })
  const r = renderComProviders(<ConnectionStep campaignStations={estacoes} />)
  return { chamadas, ...r }
}

describe('ConnectionStep — abertura', () => {
  test('sem emissoras mostra o vazio e não chama a API', () => {
    const { chamadas } = montar([])
    expect(screen.getByText('Nenhuma emissora na campanha ainda')).toBeTruthy()
    expect(chamadas.length).toBe(0)
  })

  // Regressão: o ping automático rodava no MESMO commit em que `rows` ainda estava vazio e
  // marcava toda emissora como "Ping: sem URL" sem ir ao servidor.
  test('ao abrir testa só o ping de cada emissora com a URL cadastrada', async () => {
    const { chamadas } = montar([E1, E2])
    await waitFor(() => expect(testes(chamadas).length).toBe(2))
    expect(testes(chamadas).map(c => c.data)).toEqual([
      { tests: ['ping'], url: 'http://stream.example.com/a' },
      { tests: ['ping'], url: 'http://stream.example.com/b' },
    ])
  })

  test('emissora sem URL cadastrada fica "sem URL" sem ir ao servidor', async () => {
    const { chamadas } = montar([SEM_URL])
    await waitFor(() => expect(within(cartao('Rádio Sem URL')).getByTitle('Ping: sem URL')).toBeTruthy())
    expect(testes(chamadas).length).toBe(0)
  })

  test('cada linha mostra o nome, a banda e a cidade/UF', () => {
    montar([E1])
    const c = cartao('Rádio Teste FM')
    expect(c.textContent).toContain('FM · Cidade Teste / MG')
    expect(within(c).getByPlaceholderText('http://stream…').value).toBe('http://stream.example.com/a')
  })
})

describe('ConnectionStep — testes completos', () => {
  test('"Testar todas" roda ping, stream e worker em todas, mostra o resultado e fecha o resumo', async () => {
    const { chamadas } = montar([E1, E2])
    await aposPingInicial()
    fireEvent.click(screen.getByRole('button', { name: 'Testar todas' }))
    await waitFor(() => expect(pedidos(chamadas).length).toBe(2))
    expect(pedidos(chamadas)[0].data).toEqual({ tests: ['ping', 'stream', 'worker'], url: 'http://stream.example.com/a' })
    const c1 = cartao('Rádio Teste FM')
    await waitFor(() => expect(within(c1).getByText('AAC')).toBeTruthy())
    expect(within(c1).getByText('Ao vivo')).toBeTruthy()
    expect(within(c1).getByText('Ping 42ms')).toBeTruthy()
    expect(within(c1).getByTitle('Tudo de pé')).toBeTruthy()
    // a outra falhou nos três: o motivo fica no title da pílula
    await waitFor(() => expect(within(cartao('Rádio Outra AM')).getByTitle('Ping: timeout')).toBeTruthy())
    expect(within(cartao('Rádio Outra AM')).getByTitle('Com problema')).toBeTruthy()
    expect(screen.getByText('de pé').previousSibling.textContent).toBe('1')
    expect(screen.getByText('com problema').previousSibling.textContent).toBe('1')
    expect(screen.getByText('a testar').previousSibling.textContent).toBe('0')
  })

  test('enquanto testa o botão vira "Testando…" e fica travado', async () => {
    let solta
    const espera = new Promise(r => { solta = r })
    montar([E1], {
      'POST /stations/st-1/connection-test': async (config) => {
        if (JSON.parse(config.data).tests.length > 1) await espera   // o ping da abertura passa direto
        return { results: {} }
      },
    })
    await aposPingInicial()
    fireEvent.click(screen.getByRole('button', { name: 'Testar todas' }))
    const botao = await screen.findByRole('button', { name: 'Testando…' })
    expect(botao.disabled).toBe(true)
    expect(within(cartao('Rádio Teste FM')).getByTitle('Testando…')).toBeTruthy()
    solta()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Testar todas' }).disabled).toBe(false))
  })

  test('o botão "Testar" da linha refaz os três testes só daquela emissora', async () => {
    const { chamadas } = montar([E1, E2])
    await aposPingInicial()
    fireEvent.click(within(cartao('Rádio Outra AM')).getByRole('button', { name: 'Testar' }))
    await waitFor(() => expect(pedidos(chamadas).length).toBe(1))
    expect(pedidos(chamadas)[0].url).toBe('/stations/st-2/connection-test')
    expect(pedidos(chamadas)[0].data.tests).toEqual(['ping', 'stream', 'worker'])
  })

  test('erro de rede no teste vira "erro de rede" nas pílulas', async () => {
    montar([E1], { 'POST /stations/st-1/connection-test': erroApi(500, {}) })
    await aposPingInicial()
    fireEvent.click(within(cartao('Rádio Teste FM')).getByRole('button', { name: 'Testar' }))
    await waitFor(() => expect(within(cartao('Rádio Teste FM')).getByTitle('Ping: erro de rede')).toBeTruthy())
    expect(within(cartao('Rádio Teste FM')).getByTitle('Worker: erro de rede')).toBeTruthy()
  })

  test('resultado "skipped" não vira pílula; worker efêmero mostra "Worker ok"', async () => {
    montar([E1], {
      'POST /stations/st-1/connection-test': {
        results: { ping: { status: 'ok', latency_ms: 10 }, stream: { status: 'skipped' }, worker: { status: 'ok', source: 'ephemeral' } },
      },
    })
    await aposPingInicial()
    fireEvent.click(screen.getByRole('button', { name: 'Testar todas' }))
    const c = cartao('Rádio Teste FM')
    await waitFor(() => expect(within(c).getByText('Worker ok')).toBeTruthy())
    expect(within(c).getByTitle('Stream: a testar')).toBeTruthy()   // skipped = continua sem resultado
  })
})

describe('ConnectionStep — trocar a URL', () => {
  const salvar = (c) => within(c).getByRole('button', { name: 'Salvar' })

  test('Salvar só habilita depois de editar a URL; voltar ao original desabilita de novo', () => {
    montar([E1])
    const c = cartao('Rádio Teste FM')
    expect(salvar(c).disabled).toBe(true)
    expect(salvar(c).getAttribute('title')).toBe('Edite a URL para habilitar')
    const campo = within(c).getByPlaceholderText('http://stream…')
    fireEvent.change(campo, { target: { value: 'http://stream.example.com/nova' } })
    expect(salvar(c).disabled).toBe(false)
    fireEvent.change(campo, { target: { value: 'http://stream.example.com/a' } })
    expect(salvar(c).disabled).toBe(true)
  })

  test('testar usa a URL digitada (ainda não salva), sem espaços nas pontas', async () => {
    const { chamadas } = montar([E1])
    await aposPingInicial()
    const c = cartao('Rádio Teste FM')
    fireEvent.change(within(c).getByPlaceholderText('http://stream…'), { target: { value: '  http://stream.example.com/nova  ' } })
    fireEvent.click(within(c).getByRole('button', { name: 'Testar' }))
    await waitFor(() => expect(pedidos(chamadas).length).toBe(1))
    expect(pedidos(chamadas)[0].data.url).toBe('http://stream.example.com/nova')
  })

  test('Salvar pede confirmação, grava por PATCH e re-testa na URL nova', async () => {
    const { chamadas } = montar([E1], { 'PATCH /stations/st-1/stream-url': { ok: true } })
    await aposPingInicial()
    const c = cartao('Rádio Teste FM')
    fireEvent.change(within(c).getByPlaceholderText('http://stream…'), { target: { value: 'http://stream.example.com/nova' } })
    fireEvent.click(salvar(c))
    expect(await screen.findByText(/Trocar a URL de streaming de "Rádio Teste FM"\?/)).toBeTruthy()
    expect(chamadas.some(x => x.metodo === 'PATCH')).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(x => x.chave === 'PATCH /stations/st-1/stream-url')).toBe(true))
    expect(chamadas.find(x => x.metodo === 'PATCH').data).toEqual({ url: 'http://stream.example.com/nova' })
    await waitFor(() => expect(pedidos(chamadas).length).toBe(1))
    expect(pedidos(chamadas)[0].data).toEqual({ tests: ['ping', 'stream', 'worker'], url: 'http://stream.example.com/nova' })
    await waitFor(() => expect(salvar(cartao('Rádio Teste FM')).disabled).toBe(true))
  })

  test('cancelar a confirmação não grava nada', async () => {
    const { chamadas } = montar([E1])
    const c = cartao('Rádio Teste FM')
    fireEvent.change(within(c).getByPlaceholderText('http://stream…'), { target: { value: 'http://stream.example.com/nova' } })
    fireEvent.click(salvar(c))
    await screen.findByText(/Trocar a URL de streaming/)
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText(/Trocar a URL de streaming/)).toBeNull())
    expect(chamadas.some(x => x.metodo === 'PATCH')).toBe(false)
  })

  test('URL apagada não pede confirmação nem grava', async () => {
    const { chamadas } = montar([E1])
    const c = cartao('Rádio Teste FM')
    fireEvent.change(within(c).getByPlaceholderText('http://stream…'), { target: { value: '   ' } })
    fireEvent.click(salvar(c))
    expect(screen.queryByText(/Trocar a URL de streaming/)).toBeNull()
    expect(chamadas.some(x => x.metodo === 'PATCH')).toBe(false)
  })

  test('falha ao salvar avisa o usuário e mantém o rascunho pra tentar de novo', async () => {
    montar([E1], { 'PATCH /stations/st-1/stream-url': erroApi(500, {}) })
    const c = cartao('Rádio Teste FM')
    fireEvent.change(within(c).getByPlaceholderText('http://stream…'), { target: { value: 'http://stream.example.com/nova' } })
    fireEvent.click(salvar(c))
    fireEvent.click(await screen.findByRole('button', { name: 'Confirmar' }))
    expect(await screen.findByText('Não foi possível salvar a URL. Tente novamente.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'OK' }))
    expect(salvar(cartao('Rádio Teste FM')).disabled).toBe(false)
  })
})
