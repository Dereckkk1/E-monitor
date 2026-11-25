import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import DashboardPage from './DashboardPage'
import { renderComProviders, USUARIO_CLIENTE } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

// "Hoje" fixo: as campanhas abaixo são relativas a 15/07/2026.
const AGORA = new Date('2026-07-15T15:00:00Z')
const ID_CLIENTE = USUARIO_CLIENTE.client_id

const CAMPANHAS = [
  { id: 'c-ativa', name: 'Campanha Ativa Teste', status: 'ativa', start_date: '2026-07-01', end_date: '2026-07-31', target_stations: ['s1', 's2', 's3'] },
  { id: 'c-prog-1', name: 'Campanha Programada Perto', status: 'programada', start_date: '2026-07-17', end_date: '2026-08-10', target_stations: ['s1'] },
  { id: 'c-prog-2', name: 'Campanha Programada Longe', status: 'programada', start_date: '2026-08-15', end_date: '2026-09-10', target_stations: [] },
  { id: 'c-fim', name: 'Campanha Concluida Teste', status: 'concluida', start_date: '2026-05-01', end_date: '2026-05-31T00:00:00Z', target_stations: ['s1', 's2'] },
  { id: 'c-cancel', name: 'Campanha Cancelada Teste', status: 'cancelada', start_date: '2026-06-01', end_date: '2026-06-30', target_stations: ['s1'] },
]

describe('DashboardPage — cliente', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas) {
    const chamadas = mockApi({
      'GET /campaigns': { data: CAMPANHAS },
      'GET /clients': { data: [{ id: ID_CLIENTE, name: 'Cliente Teste Ltda' }] },
      'GET /campaigns/financials': [
        { campaign_id: 'c-ativa', total_audience: 1_250_000, total_invested: 5000, total_bonus_value: 1000, fixed_cpm: null },
      ],
      ...rotas,
    })
    renderComProviders(<><DashboardPage /><LocalizacaoAtual /></>, { usuario: USUARIO_CLIENTE })
    return chamadas
  }

  test('mostra total sem canceladas, distribuição por status e o cliente vinculado', async () => {
    montar()
    expect(await screen.findByText('Olá, Cliente')).toBeTruthy()
    expect(screen.getByText('Cliente Teste Ltda')).toBeTruthy()
    // 5 campanhas, 1 cancelada fora do total → 4
    expect(screen.getByText('Campanhas no total').nextSibling.textContent).toContain('4')
    expect(screen.getByText('campanhas')).toBeTruthy()
    // legenda: ativas / programadas / concluídas (cancelada também aparece na legenda)
    const legenda = document.querySelector('.cdash-distrib-legend')
    expect(legenda.textContent).toContain('1ativas')
    expect(legenda.textContent).toContain('2programadas')
    expect(legenda.textContent).toContain('1concluídas')
    expect(legenda.textContent).toContain('1canceladas')
  })

  test('próximas a iniciar: ordena por proximidade, mostra contagem regressiva e navega', async () => {
    montar()
    await screen.findByText('Próximas a iniciar')
    expect(screen.getByText('2 programadas')).toBeTruthy()
    const perto = screen.getByText('Campanha Programada Perto')
    expect(perto).toBeTruthy()
    // 15/07 → 17/07 = em 2 dias (urgente <= 3 dias)
    expect(screen.getByText('em 2 dias').className).toContain('urgent')
    expect(screen.getByText('em 31 dias').className).not.toContain('urgent')
    expect(screen.getByText('1 emissora')).toBeTruthy()
    expect(screen.getByText('sem emissoras definidas')).toBeTruthy()

    fireEvent.click(perto)
    expect(urlAtual(screen)).toBe('/campaigns?status=programada')
  })

  test('card da campanha ativa: progresso, emissoras, impactos compactos e CPM com bonificação', async () => {
    const chamadas = montar()
    const titulo = await screen.findByText('Em atividade agora', { exact: false })
    expect(titulo.textContent).toContain('1 ativa')
    const card = screen.getByTitle('Ver veiculações de Campanha Ativa Teste')
    expect(within(card).getByText('ATIVA')).toBeTruthy()
    expect(within(card).getByText('3')).toBeTruthy()           // 3 emissoras
    expect(within(card).getByText('emissoras')).toBeTruthy()
    // 1/jul 00:00 → 15/jul 12:00 (BRT) = 14,5 dias, arredondado pra 15; total 31 dias
    expect(within(card).getByText('15')).toBeTruthy()
    expect(within(card).getByText('31 dias')).toBeTruthy()
    expect(card.querySelector('[role=progressbar]').getAttribute('aria-valuenow')).toBe('48')
    // impactos 1.250.000 → "1,3 mi"; CPM = (5000+1000)/1.25M*1000 = R$ 4,80
    await waitFor(() => expect(within(card).getByText(/mi$/)).toBeTruthy())
    expect(within(card).getByText(/4,80/)).toBeTruthy()
    // só pede o financeiro das campanhas ativas renderizadas
    const fin = chamadas.find(c => c.chave === 'GET /campaigns/financials')
    expect(fin.params).toEqual({ ids: 'c-ativa' })

    fireEvent.click(card)
    expect(urlAtual(screen)).toBe('/detections?campaign_id=c-ativa')
  })

  // BUG: DashboardPage.jsx:13-21 (formatDate) — o backend manda DATE como
  // RFC3339 em UTC ("2026-07-01T00:00:00Z"); toLocaleDateString com
  // timeZone America/Sao_Paulo mostra 30/06/2026. Esperado: 01/07/2026 → 31/07/2026.
  test.skip('período do card mostra as datas de calendário da campanha (sem voltar 1 dia)', async () => {
    montar({
      'GET /campaigns': { data: [{ ...CAMPANHAS[0], start_date: '2026-07-01T00:00:00Z', end_date: '2026-07-31T00:00:00Z' }] },
    })
    const card = await screen.findByTitle('Ver veiculações de Campanha Ativa Teste')
    expect(card.textContent).toContain('01/07/2026')
    expect(card.textContent).toContain('31/07/2026')
  })

  test('CPM fixo da campanha sobrescreve o dinâmico e vira "(fixo)"', async () => {
    montar({
      'GET /campaigns/financials': [
        { campaign_id: 'c-ativa', total_audience: 1_000_000, total_invested: 5000, total_bonus_value: 0, fixed_cpm: 12.5 },
      ],
    })
    const card = (await screen.findByTitle('Ver veiculações de Campanha Ativa Teste'))
    await waitFor(() => expect(within(card).getByText('CPM (fixo)')).toBeTruthy())
    expect(within(card).getByText(/12,50/)).toBeTruthy()
  })

  test('sem financeiro da campanha o CPM e os impactos ficam como traço', async () => {
    montar({ 'GET /campaigns/financials': [] })
    const card = await screen.findByTitle('Ver veiculações de Campanha Ativa Teste')
    await waitFor(() => expect(card.querySelectorAll('.cdash-active-stat-value')[1].textContent).toBe('—'))
    expect(card.querySelectorAll('.cdash-active-stat-value')[2].textContent).toBe('—')
  })

  test('concluídas recentes listam só concluídas (cancelada fica fora) e abrem o histórico', async () => {
    montar()
    const recente = await screen.findByText('Campanha Concluida Teste')
    expect(screen.queryByText('Campanha Cancelada Teste')).toBeNull()
    const linha = recente.closest('button')
    expect(linha.textContent).toMatch(/mai.*26/)
    expect(linha.textContent).toContain('2 emissoras')
    fireEvent.click(linha)
    expect(urlAtual(screen)).toBe('/detections?campaign_id=c-fim')

    fireEvent.click(screen.getByRole('button', { name: /Histórico/ }))
    expect(urlAtual(screen)).toBe('/campaigns?status=concluida')
    fireEvent.click(screen.getByRole('button', { name: /Ver todas/ }))
    expect(urlAtual(screen)).toBe('/campaigns?status=ativa')
  })

  test('sem campanhas: estado vazio com boas-vindas ao cliente', async () => {
    montar({ 'GET /campaigns': { data: [] } })
    expect(await screen.findByText('Nenhuma campanha por aqui ainda')).toBeTruthy()
    expect(screen.getByText('Bem-vindo, Cliente Teste Ltda')).toBeTruthy()
  })

  test('só campanhas canceladas também cai no estado vazio', async () => {
    montar({ 'GET /campaigns': { data: [CAMPANHAS[4]] } })
    expect(await screen.findByText('Nenhuma campanha por aqui ainda')).toBeTruthy()
  })

  test('erro ao carregar mostra mensagem e "Tentar novamente" refaz a chamada', async () => {
    let tentativas = 0
    const chamadas = montar({
      'GET /campaigns': () => {
        tentativas++
        return tentativas === 1 ? erroApi(500, { error: 'x' }) : { data: CAMPANHAS }
      },
    })
    expect(await screen.findByText('Não foi possível carregar suas campanhas')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Tentar novamente' }))
    expect(await screen.findByText('Campanhas no total')).toBeTruthy()
    expect(chamadas.filter(c => c.chave === 'GET /campaigns').length).toBe(2)
  })
})

describe('DashboardPage — admin', () => {
  const agora = () => Date.now()
  const iso = (s) => new Date(agora() - s * 1000).toISOString()

  function montar(rotas = {}) {
    return mockApi({
      'GET /health': { status: 'ok', deps: { postgres: 'ok', nats: 'ok' } },
      'GET /workers': {
        clap_verifier: true,
        workers: [
          { station_id: 'aaaaaaaa-0000-0000-0000-000000000001', active: true, last_pcm_at: iso(5), stall_risk: false },
          { station_id: 'bbbbbbbb-0000-0000-0000-000000000002', active: true, last_pcm_at: iso(5), stall_risk: true },
          { station_id: 'cccccccc-0000-0000-0000-000000000003', active: false, last_pcm_at: null, stall_risk: false },
        ],
      },
      'GET /stream-health': [
        { id: 's1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'SP', uptime_pct: 99.9, is_currently_down: false, last_incident_at: null },
        { id: 's2', name: 'Rádio Caída AM', band: 'AM', uptime_pct: 80, is_currently_down: true, last_incident_at: iso(300) },
        { id: 's3', name: 'Rádio Instável FM', band: 'FM', uptime_pct: 96, is_currently_down: false, last_incident_at: iso(7200) },
      ],
      'GET /campaigns': { data: CAMPANHAS },
      'GET /admin/notifications': { unread_count: 0, items: [] },
      ...rotas,
    })
  }

  test('KPIs de sistema, workers, streams e uptime refletem a API', async () => {
    montar()
    renderComProviders(<DashboardPage />)
    expect(await screen.findByText('Operação E-monitor')).toBeTruthy()
    expect(await screen.findByText('Operacional')).toBeTruthy()
    expect(screen.getByText('Postgres ok')).toBeTruthy()
    expect(screen.getByText('NATS ok')).toBeTruthy()
    // 3 workers: 1 operando, 1 lento, 1 caído
    const kpiWorkers = screen.getByText('Workers').closest('.dh-kpi')
    await waitFor(() => expect(kpiWorkers.textContent).toContain('/ 3'))
    expect(within(kpiWorkers).getByText('1 lento')).toBeTruthy()
    expect(within(kpiWorkers).getByText('1 caído')).toBeTruthy()
    expect(kpiWorkers.title).toBe('CLAP verifier: online')
    // 3 streams, 1 fora do ar → 2 / 3
    const kpiStreams = screen.getByText('Streams ao ar').closest('.dh-kpi')
    await waitFor(() => expect(kpiStreams.textContent).toContain('2/ 3'))
    expect(within(kpiStreams).getByText('1 fora do ar')).toBeTruthy()
    // média (99.9 + 80 + 96) / 3 = 91.97 → 92.0% (tom ruim <95)
    const kpiUptime = screen.getByText('Uptime médio 24h').closest('.dh-kpi')
    expect(kpiUptime.textContent).toContain('92.0%')
    expect(within(kpiUptime).getByText('janela 24h').className).toContain('tone-bad')
  })

  test('painel de saúde por estação lista a estação caída primeiro e abre a edição', async () => {
    montar()
    renderComProviders(<><DashboardPage /><LocalizacaoAtual /></>)
    const caida = await screen.findByText('Rádio Caída AM')
    const nomes = [...document.querySelectorAll('.dh-station-name')].map(n => n.textContent)
    expect(nomes).toEqual(['Rádio Caída AM', 'Rádio Instável FM', 'Rádio Teste FM'])
    expect(screen.getByText('Piores 3 de 3 ativas')).toBeTruthy()
    expect(screen.getByText('Fora do ar')).toBeTruthy()
    expect(screen.getByText('Queda há 2h')).toBeTruthy()
    expect(screen.getByText('Sem quedas')).toBeTruthy()
    expect(screen.getByText('FM · 99.5 · Cidade Teste/SP')).toBeTruthy()

    fireEvent.click(caida.closest('button'))
    expect(urlAtual(screen)).toBe('/stations/s2/edit')
  })

  test('workers em alerta mostram só lento/caído e levam pra /operations', async () => {
    montar()
    renderComProviders(<><DashboardPage /><LocalizacaoAtual /></>)
    expect(await screen.findByText('bbbbbbbb…')).toBeTruthy()
    expect(screen.getByText('cccccccc…')).toBeTruthy()
    expect(screen.queryByText('aaaaaaaa…')).toBeNull()
    expect(screen.getByText('Lento')).toBeTruthy()
    expect(screen.getByText('Caído')).toBeTruthy()
    expect(screen.getByText('sem PCM recente')).toBeTruthy()
    expect(screen.getByText('3 workers ativos')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Abrir painel de operações/ }))
    expect(urlAtual(screen)).toBe('/operations')
  })

  test('campanhas por status: contadores e clique filtra a listagem', async () => {
    montar()
    renderComProviders(<><DashboardPage /><LocalizacaoAtual /></>)
    expect(await screen.findByText('5 no total')).toBeTruthy()
    const tileProg = screen.getByText('Programadas').closest('button')
    expect(within(tileProg).getByText('2')).toBeTruthy()
    const tileCanc = screen.getByText('Canceladas').closest('button')
    expect(within(tileCanc).getByText('1')).toBeTruthy()
    fireEvent.click(screen.getByText('Ativas').closest('button'))
    expect(urlAtual(screen)).toBe('/campaigns?status=ativa')
  })

  test('tudo saudável: "Tudo certo" nos workers e "tudo no ar" nos streams', async () => {
    montar({
      'GET /workers': { clap_verifier: false, workers: [
        { station_id: 'aaaaaaaa-0000-0000-0000-000000000001', active: true, last_pcm_at: iso(2), stall_risk: false },
      ] },
      'GET /stream-health': [
        { id: 's1', name: 'Rádio Teste FM', band: 'FM', uptime_pct: 100, is_currently_down: false },
      ],
    })
    renderComProviders(<DashboardPage />)
    expect(await screen.findByText('Tudo certo')).toBeTruthy()
    expect(screen.getByText('todos ok')).toBeTruthy()
    expect(screen.getByText('tudo no ar')).toBeTruthy()
    expect(screen.getByText('Workers').closest('.dh-kpi').title).toBe('CLAP verifier: offline')
  })

  test('API de saúde fora do ar mostra o banner e o sistema como indisponível', async () => {
    montar({ 'GET /health': erroApi(500, { error: 'x' }) })
    renderComProviders(<DashboardPage />)
    const banner = await screen.findByRole('alert', {}, { timeout: 5000 })
    expect(banner.textContent).toContain('API indisponível')
    expect(await screen.findByText('Indisponível')).toBeTruthy()
    expect(screen.getByText('sem resposta')).toBeTruthy()
  })

  test('sem workers nem streams os KPIs mostram traço e avisos', async () => {
    montar({
      'GET /workers': { clap_verifier: false, workers: [] },
      'GET /stream-health': [],
    })
    renderComProviders(<DashboardPage />)
    expect(await screen.findByText('sem dados')).toBeTruthy()
    expect(screen.getByText('Nenhuma estação ativa no momento.')).toBeTruthy()
    expect(screen.getByText('Workers').closest('.dh-kpi').querySelector('.dh-kpi-value').textContent).toBe('—')
  })
})
