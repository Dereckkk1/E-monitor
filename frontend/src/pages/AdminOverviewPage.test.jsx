import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import AdminOverviewPage from './AdminOverviewPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

const AGORA = new Date('2026-07-15T15:00:00Z')
const ha = (s) => new Date(AGORA.getTime() - s * 1000).toISOString()

const INFRA_OK = {
  postgres: { status: 'ok', latency_ms: 3 },
  nats: { status: 'ok', latency_ms: 150 },
  redis: { status: 'disabled' },
  minio: { status: 'ok', latency_ms: 0.4 },
  clap_verifier: { status: 'ok', latency_ms: 20 },
}

function saudavel(extra = {}) {
  return {
    overall: 'healthy',
    infrastructure: INFRA_OK,
    observability: { prometheus: { status: 'ok', latency_ms: 12 }, grafana: { status: 'disabled' }, jaeger: { status: 'disabled' } },
    workers: { running: 8, expected_active: 8, stalled: 0, missing: 0 },
    streams: { live_now: 8, expected_active: 8, down_now: 0, incidents_24h: 1 },
    data_pipeline: { detections_1h: 42, last_detection_at: ha(120), webhooks_pending: 2, webhooks_failed_24h: 0, audit_rejected_7d: 5 },
    attention: [],
    ...extra,
  }
}

describe('AdminOverviewPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(payload, extras = {}) {
    chamadas = mockApi({ 'GET /admin/system-health': payload, ...extras })
    return renderComProviders(<><AdminOverviewPage /><LocalizacaoAtual /></>)
  }

  test('sistema saudável: hero "no ar", KPIs, pílulas de serviço e estado educativo', async () => {
    const { container } = montar(saudavel())
    expect(await screen.findByText('Sistema no ar')).toBeTruthy()
    expect(screen.getByText('8 de 8 emissoras ao ar')).toBeTruthy()
    expect(container.querySelector('.aov').getAttribute('data-mode')).toBe('nominal')
    expect(screen.getByText('Sinal ao ar — 8/8')).toBeTruthy()
    expect(screen.getByText('Detecções na última hora — 42')).toBeTruthy()

    const kpiWorkers = screen.getByText('Workers', { selector: '.aov-kpi-label' }).closest('a')
    expect(kpiWorkers.querySelector('.aov-kpi-value').textContent).toBe('8')
    expect(kpiWorkers.querySelector('.aov-kpi-value-suffix').textContent).toBe('/8')
    expect(within(kpiWorkers).getByText('Em execução')).toBeTruthy()

    const kpiPipeline = screen.getByText('Pipeline 1h').closest('a')
    expect(within(kpiPipeline).getByText('há 2min')).toBeTruthy()
    expect(within(kpiPipeline).getByText('Webhooks pendentes').nextSibling.textContent).toBe('2')
    expect(within(kpiPipeline).getByText('Audit rejeitou 7d').nextSibling.textContent).toBe('5')
    expect(within(kpiPipeline).getByText('Audit rejeitou 7d').closest('li').className).toContain('--warn')

    // pílulas: latência formatada, serviço desligado, tooltip com a variável de ambiente
    const nats = screen.getByLabelText('NATS: no ar')
    expect(nats.textContent).toContain('0.15s')
    expect(nats.title).toContain('Var: NATS_URL')
    expect(screen.getByLabelText('Redis: não usa')).toBeTruthy()
    expect(screen.getByLabelText('MinIO: no ar').textContent).toContain('<1ms')
    expect(screen.getByLabelText('Postgres: no ar').textContent).toContain('3ms')

    expect(screen.getByText('Nada exigindo atenção.')).toBeTruthy()
  })

  test('sem emissoras monitoradas o título explica em vez de "0 de 0"', async () => {
    montar(saudavel({ streams: { live_now: 0, expected_active: 0, down_now: 0, incidents_24h: 0 } }))
    expect(await screen.findByText('Sem emissoras monitoradas')).toBeTruthy()
  })

  test('KPI navega pra página relacionada ao clicar', async () => {
    montar(saudavel())
    await screen.findByText('Sistema no ar')
    fireEvent.click(screen.getByText('Streams ao ar', { selector: '.aov-kpi-label' }).closest('a'))
    expect(urlAtual(screen)).toBe('/monitoring')
    fireEvent.click(screen.getByText('Workers', { selector: '.aov-kpi-label' }).closest('a'))
    expect(urlAtual(screen)).toBe('/operations')
    fireEvent.click(screen.getByText('Pipeline 1h').closest('a'))
    expect(urlAtual(screen)).toBe('/detections')
  })

  test('degradado: lista os itens de atenção, críticos primeiro, e o cartão leva à ferramenta', async () => {
    const { container } = montar(saudavel({
      overall: 'degraded',
      workers: { running: 6, expected_active: 8, stalled: 2, missing: 0 },
      streams: { live_now: 7, expected_active: 8, down_now: 1, incidents_24h: 3 },
      attention: [
        { kind: 'worker', station_id: 'st-1', severity: 'warning', reason: 'stalled', title: 'Rádio Teste FM', detail: 'Sem PCM há 3min', action_label: 'Ver worker', action_url: '/operations' },
        { kind: 'stream', station_id: 'st-2', severity: 'critical', reason: 'stream_down', title: 'Rádio Caída AM', detail: 'Stream fora há 20min', action_label: 'Abrir emissora', action_url: '/stations/st-2/edit' },
      ],
    }))
    expect(await screen.findByText('Sistema degradado')).toBeTruthy()
    expect(container.querySelector('.aov').getAttribute('data-mode')).toBe('incident')
    expect(screen.getByText('2 travados · 1 fora')).toBeTruthy()
    const titulos = [...document.querySelectorAll('.aov-card-title-name')].map(n => n.textContent)
    expect(titulos).toEqual(['Rádio Caída AM', 'Rádio Teste FM'])
    expect(screen.getByText('Stream fora')).toBeTruthy()
    expect(screen.getByText('Travado')).toBeTruthy()
    expect(screen.queryByText('Nada exigindo atenção.')).toBeNull()

    fireEvent.click(screen.getByText('Rádio Caída AM').closest('button'))
    expect(urlAtual(screen)).toBe('/stations/st-2/edit')
  })

  test('silenciar um alerta o esconde; silenciar todos mostra o aviso e "Reexibir" traz de volta', async () => {
    montar(saudavel({
      overall: 'degraded',
      attention: [
        { kind: 'worker', station_id: 'st-1', severity: 'warning', reason: 'stalled', title: 'Rádio Teste FM', detail: 'd1' },
        { kind: 'stream', station_id: 'st-2', severity: 'warning', reason: 'stream_down', title: 'Rádio Caída AM', detail: 'd2' },
      ],
    }))
    await screen.findByText('Rádio Teste FM')
    expect(document.querySelector('.aov-attention-count').textContent).toBe('2')
    fireEvent.click(screen.getAllByRole('button', { name: /Silenciar 5min/ })[0])
    expect(screen.queryByText('Rádio Teste FM')).toBeNull()
    expect(screen.getByText('Rádio Caída AM')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Silenciar 5min/ }))
    expect(screen.getByText('Todos os alertas silenciados localmente.', { exact: false })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Reexibir' }))
    expect(screen.getByText('Rádio Teste FM')).toBeTruthy()
    expect(screen.getByText('Rádio Caída AM')).toBeTruthy()
  })

  test('crítico: o título nomeia a infraestrutura fora do ar e o card de workers fica em tom crítico', async () => {
    montar(saudavel({
      overall: 'critical',
      infrastructure: { ...INFRA_OK, postgres: { status: 'down', detail: 'connection refused' } },
      workers: { running: 5, expected_active: 8, stalled: 0, missing: 3 },
      attention: [],
    }))
    expect(await screen.findByText('Sistema crítico')).toBeTruthy()
    expect(screen.getByText('Postgres fora')).toBeTruthy()
    const pilula = screen.getByLabelText('Postgres: fora')
    expect(pilula.textContent).toContain('connection refused')
    const kpi = screen.getByText('Workers', { selector: '.aov-kpi-label' }).closest('a')
    expect(kpi.className).toContain('aov-kpi--crit')
    expect(within(kpi).getByText('Ausentes').nextSibling.textContent).toBe('3')
  })

  test('crítico por workers ausentes (infra ok) nomeia a falta de workers', async () => {
    montar(saudavel({ overall: 'critical', workers: { running: 5, expected_active: 8, stalled: 0, missing: 3 } }))
    expect(await screen.findByText('3 worker(s) ausente(s)')).toBeTruthy()
  })

  test('tecla R atualiza agora (e é ignorada dentro de campo de texto); botão também', async () => {
    montar(saudavel())
    await screen.findByText('Sistema no ar')
    const total = () => chamadas.filter(c => c.chave === 'GET /admin/system-health').length
    expect(total()).toBe(1)

    fireEvent.keyDown(window, { key: 'r' })
    await waitFor(() => expect(total()).toBe(2))

    const campo = document.createElement('input')
    document.body.appendChild(campo)
    fireEvent.keyDown(campo, { key: 'r' })
    fireEvent.keyDown(window, { key: 'r', ctrlKey: true })
    expect(total()).toBe(2)
    campo.remove()

    fireEvent.click(screen.getByLabelText('Atualizar agora (R)'))
    await waitFor(() => expect(total()).toBe(3))
  })

  test('falha ao ler o health mostra o erro e "Tentar novamente" recarrega', async () => {
    let n = 0
    montar(null, {
      'GET /admin/system-health': () => { n++; return n === 1 ? erroApi(500, {}) : saudavel() },
    })
    expect(await screen.findByText('Não foi possível ler o painel de health.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Tentar novamente' }))
    expect(await screen.findByText('Sistema no ar')).toBeTruthy()
  })
})
