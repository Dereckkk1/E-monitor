import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import OperationsPage from './OperationsPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

const AGORA = new Date('2026-07-15T15:00:00Z')
const ha = (s) => new Date(AGORA.getTime() - s * 1000).toISOString()

const ESTACOES = [
  { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'SP', uptime_pct: 99.8, is_currently_down: false },
  { id: 'st-2', name: 'Rádio Lenta AM', band: 'AM', frequency_mhz: 780, city: 'Vila Teste', state: 'MG', uptime_pct: 96.2, is_currently_down: false },
  { id: 'st-3', name: 'Rádio Caída FM', band: 'FM', frequency_mhz: 101.1, city: 'Cidade Teste', state: 'SP', uptime_pct: 70, is_currently_down: true },
  { id: 'st-4', name: 'Rádio Reiniciando FM', band: 'FM', frequency_mhz: 88.9, uptime_pct: 99.1, is_currently_down: false },
]

const WORKERS = {
  clap_verifier: true,
  workers: [
    { station_id: 'st-1', active: true, last_pcm_at: ha(2), stall_risk: false, bytes_received: 5_242_880, reconnects: 3, stall_restarts: 1, min_hashes: 1234 },
    { station_id: 'st-2', active: true, last_pcm_at: ha(45), stall_risk: true, reconnects: 0 },
    { station_id: 'st-3', active: false, last_pcm_at: null },
    { station_id: 'st-4', status: 'restarting', last_pcm_at: ha(5), bytes_received: 900 },
  ],
}

describe('OperationsPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas = {}) {
    chamadas = mockApi({
      'GET /workers': WORKERS,
      'GET /stream-health': ESTACOES,
      ...rotas,
    })
    return renderComProviders(<><OperationsPage /><LocalizacaoAtual /></>)
  }

  const nomesNaTabela = () => [...document.querySelectorAll('.op-cell-station-name')].map(n => n.textContent)

  test('KPIs: workers por status, streams ao ar, reconexões e CLAP', async () => {
    montar()
    await screen.findByText('Rádio Teste FM')
    const kpi = (rotulo) => screen.getByText(rotulo).closest('.op-kpi')

    // 4 workers: só o st-1 está em execução
    expect(kpi('Workers ativos').querySelector('.op-kpi-value').textContent).toBe('1/ 4')
    expect(within(kpi('Workers ativos')).getByText('1 caído')).toBeTruthy()
    expect(kpi('Workers ativos').className).toContain('op-kpi--danger')
    // 4 emissoras cruzadas com a saúde, 1 fora do ar
    expect(kpi('Streams ao ar').querySelector('.op-kpi-value').textContent).toBe('3/ 4')
    expect(within(kpi('Streams ao ar')).getByText('1 fora do ar')).toBeTruthy()
    expect(kpi('Streams ao ar').className).toContain('op-kpi--danger') // 75% < 90%
    expect(kpi('Reconexões').querySelector('.op-kpi-value').textContent).toBe('3')
    expect(within(kpi('CLAP verifier')).getByText('Online')).toBeTruthy()
  })

  test('linhas ordenadas pelos problemas primeiro, com status, bytes e métricas formatadas', async () => {
    montar()
    await screen.findByText('Rádio Teste FM')
    // down → restarting → stalled → running
    expect(nomesNaTabela()).toEqual(['Rádio Caída FM', 'Rádio Reiniciando FM', 'Rádio Lenta AM', 'Rádio Teste FM'])

    const linhaOk = screen.getByText('Rádio Teste FM').closest('tr')
    expect(within(linhaOk).getByText('Em execução')).toBeTruthy()
    expect(within(linhaOk).getByText('há 2.0s')).toBeTruthy()
    expect(within(linhaOk).getByText('5.00 MB')).toBeTruthy()
    expect(within(linhaOk).getByText('1.234')).toBeTruthy()          // min hashes pt-BR
    expect(within(linhaOk).getByText('99.8%')).toBeTruthy()
    expect(linhaOk.textContent).toContain('FM · 99.5 MHz')
    expect(linhaOk.textContent).toContain('Cidade Teste/SP')
    expect(linhaOk.querySelector('.op-uptime-fill--ok')).toBeTruthy()

    const linhaLenta = screen.getByText('Rádio Lenta AM').closest('tr')
    expect(within(linhaLenta).getByText('Sem PCM')).toBeTruthy()
    expect(within(linhaLenta).getByText('há 45s')).toBeTruthy()
    expect(linhaLenta.querySelector('.op-cell-num--danger')).toBeTruthy()   // PCM velho vira vermelho
    expect(linhaLenta.querySelector('.op-uptime-fill--warn')).toBeTruthy()

    const linhaCaida = screen.getByText('Rádio Caída FM').closest('tr')
    expect(within(linhaCaida).getByText('Caído')).toBeTruthy()
    expect(linhaCaida.querySelector('.op-uptime-fill--bad')).toBeTruthy()
    expect(within(linhaCaida).getAllByText('—').length).toBeGreaterThan(0)  // sem PCM/bytes/min hashes

    const linhaReinicio = screen.getByText('Rádio Reiniciando FM').closest('tr')
    expect(within(linhaReinicio).getByText('Reiniciando')).toBeTruthy()
    expect(within(linhaReinicio).getByText('900 B')).toBeTruthy()
  })

  test('filtro "Só problemas" esconde os saudáveis; busca por cidade e "nenhum corresponde"', async () => {
    montar()
    await screen.findByText('Rádio Teste FM')
    expect(screen.getByRole('tab', { name: /Todos/ }).textContent).toContain('4')
    expect(screen.getByRole('tab', { name: /Só problemas/ }).textContent).toContain('3')

    fireEvent.click(screen.getByRole('tab', { name: /Só problemas/ }))
    expect(nomesNaTabela()).toEqual(['Rádio Caída FM', 'Rádio Reiniciando FM', 'Rádio Lenta AM'])

    fireEvent.click(screen.getByRole('tab', { name: /Todos/ }))
    fireEvent.change(screen.getByPlaceholderText('Buscar por emissora ou cidade…'), { target: { value: 'vila teste' } })
    expect(nomesNaTabela()).toEqual(['Rádio Lenta AM'])

    fireEvent.change(screen.getByPlaceholderText('Buscar por emissora ou cidade…'), { target: { value: 'inexistente' } })
    expect(screen.getByText('Nenhum worker corresponde ao filtro.')).toBeTruthy()
  })

  test('clicar na linha abre o cadastro da emissora', async () => {
    montar()
    fireEvent.click((await screen.findByText('Rádio Lenta AM')).closest('tr'))
    expect(urlAtual(screen)).toBe('/stations/st-2/edit')
  })

  test('tudo saudável: "todos saudáveis", "todos no ar" e CLAP offline', async () => {
    montar({
      'GET /workers': { clap_verifier: false, workers: [WORKERS.workers[0]] },
      'GET /stream-health': { data: [ESTACOES[0]] }, // aceita também o formato { data: [...] }
    })
    await screen.findByText('Rádio Teste FM')
    expect(screen.getByText('todos saudáveis')).toBeTruthy()
    expect(screen.getByText('todos no ar')).toBeTruthy()
    expect(screen.getByText('Offline')).toBeTruthy()
    expect(screen.getByText('atualizado agora')).toBeTruthy()
  })

  test('sem workers: estado vazio explica e leva às campanhas', async () => {
    montar({ 'GET /workers': { clap_verifier: false, workers: [] }, 'GET /stream-health': [] })
    expect(await screen.findByText('Nenhum worker ativo')).toBeTruthy()
    expect(screen.getByText('nenhum worker')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ver campanhas' }))
    expect(urlAtual(screen)).toBe('/campaigns')
  })

  test('falha no /workers sinaliza "falha ao atualizar"', async () => {
    montar({ 'GET /workers': erroApi(500, { error: 'x' }) })
    // a query tem 1 retry (1s de espera) antes de virar erro
    expect(await screen.findByText('falha ao atualizar', {}, { timeout: 5000 })).toBeTruthy()
  })
})
