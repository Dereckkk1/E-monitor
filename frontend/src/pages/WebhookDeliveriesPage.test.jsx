import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import WebhookDeliveriesPage from './WebhookDeliveriesPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'

const AGORA = new Date('2026-07-15T15:00:00Z')
const ha = (s) => new Date(AGORA.getTime() - s * 1000).toISOString()
const ID = 'cli-1'

const CONFIG = {
  webhook_url: 'https://hooks.example.com/radiocheck?token=abc',
  webhook_enabled: true,
  webhook_events: ['detection.confirmed', 'webhook.test'],
  has_secret: true,
  webhook_secret_masked: 'sk_••••9f2a',
}

const ENTREGAS = [
  { id: 'd1', event_type: 'detection.confirmed', status: 'delivered', response_code: 200, attempt_count: 1,
    delivered_at: ha(300), created_at: ha(310), payload: { detection_id: 'det-1', station: 'Rádio Teste FM' }, response_body: 'ok' },
  { id: 'd2', event_type: 'detection.confirmed', status: 'delivered', response_code: 204, attempt_count: 1, delivered_at: ha(4000), created_at: ha(4010) },
  { id: 'd3', event_type: 'webhook.test', status: 'delivered', response_code: 200, attempt_count: 3, delivered_at: ha(90000), created_at: ha(90100) },
  { id: 'd4', event_type: 'detection.retracted', status: 'failed', response_code: 500, attempt_count: 2, updated_at: ha(30),
    last_error: 'x'.repeat(100), next_attempt_at: ha(-60), created_at: ha(100) },
  { id: 'd5', event_type: 'detection.confirmed', status: 'dead', response_code: null, attempt_count: 8, updated_at: ha(86400 * 10), last_error: 'connection refused', created_at: ha(86400 * 11) },
  { id: 'd6', event_type: 'detection.confirmed', status: 'pending', response_code: null, attempt_count: 0, created_at: ha(2), updated_at: ha(2) },
]

describe('WebhookDeliveriesPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas = {}) {
    chamadas = mockApi({
      'GET /clients': { data: [{ id: ID, name: 'Cliente Teste Ltda' }] },
      [`GET /clients/${ID}/webhook`]: CONFIG,
      [`GET /clients/${ID}/webhook-deliveries`]: { data: ENTREGAS },
      ...rotas,
    })
    return renderComProviders(<WebhookDeliveriesPage />, { rota: `/clients/${ID}/webhooks`, caminho: '/clients/:id/webhooks' })
  }

  const linhas = () => [...document.querySelectorAll('.whd-row-data')]

  test('cabeçalho mostra o cliente, a URL sem query string e o formulário hidratado da config', async () => {
    montar()
    expect(await screen.findByText('Cliente Teste Ltda')).toBeTruthy()
    const sub = document.querySelector('.whd-subtitle')
    await waitFor(() => expect(sub.querySelector('code').textContent).toBe('https://hooks.example.com/radiocheck'))
    expect(sub.textContent).toContain('Ativo')
    await waitFor(() => expect(screen.getByLabelText('URL do endpoint').value).toBe(CONFIG.webhook_url))
    expect(screen.getByText('atual: sk_••••9f2a')).toBeTruthy()
    expect(screen.getByPlaceholderText('Deixe em branco para manter atual')).toBeTruthy()
    expect(screen.getByLabelText('Ativar entrega de webhooks', { exact: false }).checked).toBe(true)
    // eventos marcados: 2 de 4
    expect(screen.getByText('2 de 4')).toBeTruthy()
    expect(screen.getByText('detection.retracted', { selector: 'code' }).closest('label').className).not.toContain('checked')
    expect(screen.getByText('webhook.test', { selector: 'code' }).closest('label').className).toContain('checked')
    // sem alteração não dá pra salvar
    expect(screen.getByRole('button', { name: 'Salvar' }).disabled).toBe(true)
  })

  test('KPIs: total, % entregue, falhas+DLQ e p95 de tentativas das entregues', async () => {
    montar()
    await screen.findAllByText('detection.confirmed', { selector: '.whd-evt-chip' })
    const kpi = (r) => screen.getByText(r, { selector: '.whd-kpi-label' }).closest('.whd-kpi')
    expect(kpi('Total entregas').querySelector('.whd-kpi-value').textContent).toBe('6')
    expect(kpi('Delivered').querySelector('.whd-kpi-value').textContent).toBe('3')
    expect(kpi('Delivered').textContent).toContain('50% do total')
    expect(kpi('Failed / Dead').querySelector('.whd-kpi-value').textContent).toBe('2')
    // tentativas das entregues = [1,1,3] → p95 = 2.8
    expect(kpi('p95 tentativas').querySelector('.whd-kpi-value').textContent).toBe('2.8')
    // última entrega bem-sucedida = d1, há 5min
    expect(screen.getByText('Última entrega bem-sucedida').nextSibling.textContent).toBe('há 5min')
  })

  test('linhas mostram quando/evento/status/HTTP/tentativas e o último erro truncado', async () => {
    montar()
    await waitFor(() => expect(linhas().length).toBe(6))
    const textos = linhas().map(l => l.textContent)
    // d1: entregue há 5min, 200, 1 tentativa
    expect(textos[0]).toContain('há 5min')
    expect(textos[0]).toContain('delivered')
    expect(textos[0]).toContain('200')
    // d4: erro de 100 chars truncado em 80 + reticências, HTTP 500 como erro
    const linhaFalha = linhas()[3]
    expect(linhaFalha.querySelector('.whd-error-cell').textContent).toBe('x'.repeat(80) + '…')
    expect(linhaFalha.querySelector('.whd-error-cell').title).toBe('x'.repeat(100))
    expect(linhaFalha.querySelector('.whd-http').className).toContain('whd-http--err')
    // d5: sem código HTTP, data antiga (>7d) em formato de data; d6 pendente "agora"
    expect(linhas()[4].querySelector('.whd-http').className).toContain('whd-http--none')
    expect(linhas()[4].textContent).toContain('05/07/2026')
    expect(linhas()[5].textContent).toContain('agora')
  })

  test('filtro por status e por evento, com contagem nas abas', async () => {
    montar()
    await waitFor(() => expect(linhas().length).toBe(6))
    expect(screen.getByRole('tab', { name: /Failed/ }).textContent).toContain('1')
    fireEvent.click(screen.getByRole('tab', { name: /Delivered/ }))
    expect(linhas().length).toBe(3)
    // combinando com o evento: delivered + webhook.test = 1
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'webhook.test' } })
    expect(linhas().length).toBe(1)
    // sem resultado
    fireEvent.click(screen.getByRole('tab', { name: /Dead/ }))
    expect(screen.getByText('Nenhuma entrega corresponde aos filtros.')).toBeTruthy()
  })

  test('expandir uma entrega mostra payload formatado e resposta; clicar de novo recolhe', async () => {
    montar()
    await waitFor(() => expect(linhas().length).toBe(6))
    fireEvent.click(linhas()[0])
    const detalhe = document.querySelector('.whd-row-detail')
    expect(detalhe.querySelector('pre').textContent).toBe(JSON.stringify({ detection_id: 'det-1', station: 'Rádio Teste FM' }, null, 2))
    expect(within(detalhe).getByText('ok')).toBeTruthy()
    expect(within(detalhe).getAllByRole('button', { name: 'Copiar' }).length).toBe(2)
    fireEvent.click(within(linhas()[0]).getByLabelText('Recolher'))
    expect(document.querySelector('.whd-row-detail')).toBeNull()
    // entrega sem payload/resposta mostra os avisos vazios
    fireEvent.click(linhas()[1])
    expect(screen.getByText('— sem payload —')).toBeTruthy()
    expect(screen.getByText('— sem corpo de resposta —')).toBeTruthy()
  })

  test('copiar o payload usa a área de transferência e confirma "Copiado"', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    montar()
    await waitFor(() => expect(linhas().length).toBe(6))
    fireEvent.click(linhas()[0])
    fireEvent.click(document.querySelector('.whd-detail-label .whd-copy-btn'))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(JSON.stringify({ detection_id: 'det-1', station: 'Rádio Teste FM' }, null, 2)))
    expect(await screen.findByText('Copiado')).toBeTruthy()
  })

  test('salvar valida: URL http, secret curto e nenhum evento são barrados sem chamar a API', async () => {
    montar()
    await waitFor(() => expect(screen.getByLabelText('URL do endpoint').value).toBe(CONFIG.webhook_url))
    const form = screen.getByLabelText('URL do endpoint').closest('form')

    fireEvent.change(screen.getByLabelText('URL do endpoint'), { target: { value: 'http://exemplo.com/hook' } })
    fireEvent.change(screen.getByLabelText('Secret HMAC'), { target: { value: 'curto' } })
    fireEvent.click(screen.getByText('detection.confirmed', { selector: 'code' }))
    fireEvent.click(screen.getByText('webhook.test', { selector: 'code' }))
    fireEvent.submit(form)
    expect(screen.getByText('URL deve usar https:// (http:// só é permitido para localhost).')).toBeTruthy()
    expect(screen.getByText('Secret deve ter ao menos 16 caracteres.')).toBeTruthy()
    expect(screen.getByText('Selecione ao menos um evento.')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('URL do endpoint'), { target: { value: 'nao-e-url' } })
    fireEvent.submit(form)
    expect(screen.getByText('URL inválida.')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('URL do endpoint'), { target: { value: '' } })
    fireEvent.submit(form)
    expect(screen.getByText('Informe a URL do endpoint.')).toBeTruthy()
    expect(chamadas.some(c => c.chave.startsWith('PATCH'))).toBe(false)
  })

  test('salvar envia só o que mudou (secret apenas se digitado) e avisa o sucesso; http em localhost é aceito', async () => {
    montar({ [`PATCH /clients/${ID}/webhook`]: { ok: true } })
    await waitFor(() => expect(screen.getByLabelText('URL do endpoint').value).toBe(CONFIG.webhook_url))
    fireEvent.change(screen.getByLabelText('URL do endpoint'), { target: { value: ' http://localhost:9000/hook ' } })
    fireEvent.click(screen.getByLabelText('Ativar entrega de webhooks', { exact: false }))
    fireEvent.click(screen.getByText('detection.retracted', { selector: 'code' }))
    const salvar = screen.getByRole('button', { name: 'Salvar' })
    expect(salvar.disabled).toBe(false)
    fireEvent.click(salvar)
    await waitFor(() => expect(chamadas.some(c => c.chave === `PATCH /clients/${ID}/webhook`)).toBe(true))
    expect(chamadas.find(c => c.chave === `PATCH /clients/${ID}/webhook`).data).toEqual({
      webhook_url: 'http://localhost:9000/hook',
      webhook_enabled: false,
      webhook_events: ['detection.confirmed', 'webhook.test', 'detection.retracted'],
    })
    expect(await screen.findByText('Configuração salva.')).toBeTruthy()
  })

  test('secret novo vai no payload; erro do servidor vira toast', async () => {
    montar({ [`PATCH /clients/${ID}/webhook`]: erroApi(400, { message: 'secret recusado' }) })
    await waitFor(() => expect(screen.getByLabelText('URL do endpoint').value).toBe(CONFIG.webhook_url))
    fireEvent.change(screen.getByLabelText('Secret HMAC'), { target: { value: 'um-secret-bem-grande-1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByText('secret recusado')).toBeTruthy()
    expect(chamadas.find(c => c.chave === `PATCH /clients/${ID}/webhook`).data.webhook_secret).toBe('um-secret-bem-grande-1')
  })

  test('"Testar webhook" enfileira o evento de teste e avisa', async () => {
    montar({ [`POST /clients/${ID}/webhook-test`]: { queued: true } })
    await waitFor(() => expect(screen.getByRole('button', { name: /Testar webhook/ }).disabled).toBe(false))
    fireEvent.click(screen.getByRole('button', { name: /Testar webhook/ }))
    expect(await screen.findByText('Evento de teste enfileirado')).toBeTruthy()
    expect(chamadas.some(c => c.chave === `POST /clients/${ID}/webhook-test`)).toBe(true)
  })

  test('falha no teste mostra o erro do servidor', async () => {
    montar({ [`POST /clients/${ID}/webhook-test`]: erroApi(502, { message: 'endpoint inalcançável' }) })
    await waitFor(() => expect(screen.getByRole('button', { name: /Testar webhook/ }).disabled).toBe(false))
    fireEvent.click(screen.getByRole('button', { name: /Testar webhook/ }))
    expect(await screen.findByText('endpoint inalcançável')).toBeTruthy()
  })

  test('sem URL configurada: estado "não configurado", teste desabilitado e o CTA foca o campo da URL', async () => {
    montar({ [`GET /clients/${ID}/webhook`]: { webhook_url: null, webhook_enabled: false, webhook_events: [], has_secret: false } })
    expect(await screen.findByText('Webhook não configurado')).toBeTruthy()
    expect(screen.getByText('Webhook ainda não configurado para este cliente.')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Testar webhook/ }).disabled).toBe(true)
    expect(screen.getByText('não configurado')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Configurar agora' }))
    expect(document.activeElement.id).toBe('whd-url-input')
    // sem config, o formulário parte do evento padrão
    expect(screen.getByText('1 de 4')).toBeTruthy()
  })

  test('URL configurada mas sem entregas: "Nenhuma entrega ainda" dispara o teste', async () => {
    montar({
      [`GET /clients/${ID}/webhook-deliveries`]: { data: [] },
      [`POST /clients/${ID}/webhook-test`]: { queued: true },
    })
    expect(await screen.findByText('Nenhuma entrega ainda')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Disparar webhook de teste' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === `POST /clients/${ID}/webhook-test`)).toBe(true))
    expect(screen.getByText('Última entrega bem-sucedida').nextSibling.textContent).toBe('—')
  })

  test('"Recarregar" busca as entregas de novo', async () => {
    montar()
    await waitFor(() => expect(linhas().length).toBe(6))
    const antes = chamadas.filter(c => c.chave === `GET /clients/${ID}/webhook-deliveries`).length
    fireEvent.click(screen.getByRole('button', { name: /Recarregar/ }))
    await waitFor(() => expect(chamadas.filter(c => c.chave === `GET /clients/${ID}/webhook-deliveries`).length).toBe(antes + 1))
    expect(chamadas.find(c => c.chave === `GET /clients/${ID}/webhook-deliveries`).params).toEqual({ limit: 200 })
  })

  test('cliente fora da lista cai no título genérico com o id', async () => {
    montar({ 'GET /clients': { data: [] } })
    expect(await screen.findByText(`Cliente #${ID}`)).toBeTruthy()
  })
})
