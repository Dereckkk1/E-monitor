import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import StationFailureCard from './StationFailureCard'
import CampaignFailureCard from './CampaignFailureCard'
import CampaignFailureRow from './CampaignFailureRow'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'

// jsPDF não roda no jsdom — o que importa aqui é que o botão chama o gerador com o payload da API
vi.mock('../utils/pdfCampaignFailure', () => ({ generateCampaignFailurePdf: vi.fn() }))

// window.alert: um spy só no arquivo (restaurar e recriar perde as chamadas)
const alerta = vi.spyOn(window, 'alert').mockImplementation(() => {})
const erroConsole = vi.spyOn(console, 'error').mockImplementation(() => {})
beforeEach(() => { alerta.mockClear(); erroConsole.mockClear(); generateCampaignFailurePdf.mockClear() })

// ───────────────────────── StationFailureCard ─────────────────────────

const ESTACAO = { id: 'st-1', name: 'Rádio Teste FM', dial: 'FM 99,5', city: 'Cidade Teste' }
const CAMPANHA = (id, extra = {}) => ({
  campaign_id: id, campaign_name: `Campanha ${id}`, client_name: 'Cliente Teste',
  expected: 10, delivered: 6, deficit: 4, affected_by: [], ...extra,
})

function cartao(extra = {}, props = {}) {
  const data = {
    station: ESTACAO, incidents: [], total_down_seconds: 600, has_silent_gap: false, campaigns: [], ...extra,
  }
  return renderComProviders(<StationFailureCard data={data} rank={1} dateIso="2026-03-10" {...props} />)
}

describe('StationFailureCard — cabeçalho e severidade', () => {
  test('mostra posição com zero à esquerda, nome, dial e cidade', () => {
    cartao({}, { rank: 3 })
    expect(screen.getByText('03')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Rádio Teste FM' })).toBeTruthy()
    expect(screen.getByText('FM 99,5 · Cidade Teste')).toBeTruthy()
  })

  test('sem dial nem cidade mostra traço; sem rank usa 01', () => {
    cartao({ station: { id: 'st-2', name: 'Rádio Sem Dial' } }, { rank: undefined })
    expect(screen.getByText('—')).toBeTruthy()
    expect(screen.getByText('01')).toBeTruthy()
  })

  test.each([
    [30, '30s fora', 1, 'mild'],
    [120, '2min fora', 2, 'mild'],
    [600, '10min fora', 3, 'warn'],
    [2000, '33min fora', 4, 'crit'],
    [7200, '2h fora', 5, 'crit'],
    [8000, '2h 13min fora', 5, 'crit'],
  ])('%i segundos fora → "%s" (severidade %i, tom %s)', (seg, texto, nivel, tom) => {
    const { container } = cartao({ total_down_seconds: seg })
    expect(screen.getByText(texto)).toBeTruthy()
    const barra = screen.getByLabelText(`Severidade ${nivel} de 5`)
    expect(barra.className).toContain(`sfc-sev-${tom}`)
    expect(container.querySelectorAll('.sfc-sev-block.on').length).toBe(nivel)
  })
})

describe('StationFailureCard — fita de incidentes do dia', () => {
  afterEach(() => { vi.useRealTimers() })

  test('sem incidente no dia não desenha a fita', () => {
    const { container } = cartao({ incidents: [{ event_at: '2026-03-09T10:00:00Z', duration_seconds: 600 }] })
    expect(container.querySelector('.sfc-ribbon')).toBeNull()
  })

  test('um incidente vira um período fora do ar; incidentes que se sobrepõem são fundidos', () => {
    const { container, unmount } = cartao({ incidents: [{ event_at: '2026-03-10T15:00:00Z', duration_seconds: 3600 }] })
    expect(screen.getByRole('img', { name: '1 incidentes no dia, 1 períodos fora do ar' })).toBeTruthy()
    expect(container.querySelectorAll('.sfc-ribbon-rect--down').length).toBe(1)
    unmount()
    const outro = cartao({ incidents: [
      { event_at: '2026-03-10T15:00:00Z', duration_seconds: 3600 },
      { event_at: '2026-03-10T15:30:00Z', duration_seconds: 3600 },
      { event_at: '2026-03-10T20:00:00Z', duration_seconds: 600 },
    ] })
    expect(screen.getByRole('img', { name: '3 incidentes no dia, 2 períodos fora do ar' })).toBeTruthy()
    expect(outro.container.querySelectorAll('.sfc-ribbon-rect--down').length).toBe(2)
    // faixas "no ar" são os intervalos entre as quedas (3 trechos)
    expect(outro.container.querySelectorAll('.sfc-ribbon-rect--up').length).toBe(3)
    expect(outro.container.querySelectorAll('.sfc-ribbon-hour').length).toBe(9)
  })

  test('incidente que atravessa a meia-noite é cortado nos limites do dia', () => {
    const { container } = cartao({ incidents: [{ event_at: '2026-03-10T01:00:00Z', duration_seconds: 7 * 3600 }] })
    // 01:00Z = 22:00 do dia anterior em SP; só a parte de 10/03 conta
    const queda = container.querySelector('.sfc-ribbon-rect--down')
    expect(Number(queda.getAttribute('x'))).toBe(0)
  })

  test('no dia de hoje aparece o marcador de "agora" na posição do relógio', () => {
    vi.useFakeTimers({ toFake: ['Date'], now: new Date('2026-03-10T15:00:00Z') })  // 12:00 em SP
    const { container } = cartao({ incidents: [{ event_at: '2026-03-10T14:00:00Z', duration_seconds: 600 }] })
    const agora = container.querySelector('.sfc-ribbon-now')
    expect(agora).not.toBeNull()
    expect(agora.style.left).toBe('50%')
  })

  test('em outro dia não há marcador de "agora"', () => {
    vi.useFakeTimers({ toFake: ['Date'], now: new Date('2026-03-12T15:00:00Z') })
    const { container } = cartao({ incidents: [{ event_at: '2026-03-10T14:00:00Z', duration_seconds: 600 }] })
    expect(container.querySelector('.sfc-ribbon-now')).toBeNull()
  })
})

describe('StationFailureCard — campanhas afetadas', () => {
  test('resumo com incidentes, déficit detectado e campanhas; cada campanha é um link', () => {
    cartao({
      incidents: [{ event_at: '2026-03-10T15:00:00Z', duration_seconds: 60 }, { event_at: '2026-03-10T18:00:00Z', duration_seconds: 60 }],
      has_silent_gap: true, campaigns: [CAMPANHA('a'), CAMPANHA('b')],
    })
    const linha = screen.getByText('2 incidentes').closest('p')
    expect(linha.textContent).toBe('2 incidentes·déficit detectado·2 campanhas afetadas')
    const links = screen.getAllByRole('link')
    expect(links.map(l => l.getAttribute('href'))).toEqual(['/campaigns?campaign=a', '/campaigns?campaign=b'])
  })

  test('singular: 1 incidente e 1 campanha afetada', () => {
    cartao({ incidents: [{ event_at: '2026-03-10T15:00:00Z', duration_seconds: 60 }], campaigns: [CAMPANHA('a')] })
    expect(screen.getByText('1 incidente').closest('p').textContent).toBe('1 incidente·1 campanha afetada')
  })

  test('sem campanha mostra que nenhuma tinha slot na janela', () => {
    cartao({ campaigns: [] })
    expect(screen.getByText('Nenhuma campanha tinha slot nessa janela')).toBeTruthy()
  })
})

// ───────────────────────── CampaignFailureCard ─────────────────────────

function linhaEmissora(i, extra = {}) {
  return {
    station: { id: `st-${i}`, name: `Rádio Teste ${i}`, city: `Cidade ${i}` },
    programmed: 2000, identified: 1200, deficit: 800, deficit_absent: 500, deficit_off_slot: 300, is_bonified: false,
    ...extra,
  }
}
const ENTRADA = (n = 2, extra = {}) => ({
  campaign: { id: 'camp-1', name: 'Campanha Teste', client_name: 'Cliente Teste', client_logo_url: null },
  stations: Array.from({ length: n }, (_, i) => linhaEmissora(i + 1)),
  ...extra,
})

describe('CampaignFailureCard', () => {
  test('cabeçalho: cliente, campanha, contagem de emissoras e inicial do cliente quando não há logo', () => {
    const { container } = render(<CampaignFailureCard entry={ENTRADA(2)} onOpen={() => {}} />)
    expect(screen.getByText('Cliente Teste', { selector: '.cfc-client' })).toBeTruthy()
    expect(screen.getByText('Campanha Teste')).toBeTruthy()
    expect(container.querySelector('.cfc-count-num').textContent).toBe('2')
    expect(container.querySelector('.cfc-count-label').textContent).toBe('emissoras')
    expect(container.querySelector('.cfc-client-logo--fallback').textContent).toBe('C')
    expect(screen.getByLabelText('2 emissoras envolvidas')).toBeTruthy()
  })

  test('com logo mostra a imagem do cliente; se quebrar, esconde', () => {
    const entrada = ENTRADA(1)
    entrada.campaign.client_logo_url = 'https://cdn.example.com/c.png'
    render(<CampaignFailureCard entry={entrada} onOpen={() => {}} />)
    const img = screen.getByAltText('Cliente Teste')
    expect(img.getAttribute('src')).toBe('https://cdn.example.com/c.png')
    fireEvent.error(img)
    expect(img.style.display).toBe('none')
    expect(screen.getByText('emissora', { selector: '.cfc-count-label' })).toBeTruthy()
  })

  test('cada emissora mostra identificadas/programadas, % e cobertura; déficit separado em "não tocou" × "fora do horário"', () => {
    const { container } = render(<CampaignFailureCard entry={ENTRADA(1)} onOpen={() => {}} />)
    const linha = container.querySelector('.cfc-station-row')
    expect(linha.querySelector('.cfc-num-ok').textContent).toBe('1.200')
    expect(linha.querySelector('.cfc-num-total').textContent).toBe('2.000')
    expect(linha.querySelector('.cfc-num-pct').textContent).toBe(' · 60%')
    expect(linha.querySelector('.cfc-cov').getAttribute('aria-label')).toBe('Cobertura 60%')
    expect(linha.querySelectorAll('.cfc-cov-seg--on').length).toBe(6)
    expect(linha.textContent).toContain('não tocou 500')
    expect(linha.textContent).toContain('fora do horário 300')
  })

  test('emissora bonificada mostra a etiqueta em vez do déficit', () => {
    const entrada = ENTRADA(1)
    entrada.stations[0] = linhaEmissora(1, { is_bonified: true })
    render(<CampaignFailureCard entry={entrada} onOpen={() => {}} />)
    expect(screen.getByText('falhou, bonificada')).toBeTruthy()
    expect(screen.queryByText(/não tocou/)).toBeNull()
  })

  test('mais de 6 emissoras: mostra 6 e avisa quantas ficaram pro detalhe; a faixa de severidade soma o excedente', () => {
    const { container } = render(<CampaignFailureCard entry={ENTRADA(12)} onOpen={() => {}} />)
    expect(container.querySelectorAll('.cfc-station-row').length).toBe(6)
    expect(screen.getByText('+6 emissoras no detalhe')).toBeTruthy()
    expect(container.querySelector('.cfc-strip-overflow').textContent).toBe('+2')
  })

  test('no exato limite o rodapé fala "detalhe das emissoras"; com uma só, "da emissora"', () => {
    const { rerender } = render(<CampaignFailureCard entry={ENTRADA(3)} onOpen={() => {}} />)
    expect(screen.getByText('detalhe das emissoras')).toBeTruthy()
    rerender(<CampaignFailureCard entry={ENTRADA(1)} onOpen={() => {}} />)
    expect(screen.getByText('detalhe da emissora')).toBeTruthy()
  })

  test('clique, Enter e espaço abrem o detalhe', () => {
    const onOpen = vi.fn()
    render(<CampaignFailureCard entry={ENTRADA(1)} onOpen={onOpen} />)
    const cartaoEl = screen.getAllByRole('button')[0]
    fireEvent.click(cartaoEl)
    fireEvent.keyDown(cartaoEl, { key: 'Enter' })
    fireEvent.keyDown(cartaoEl, { key: ' ' })
    fireEvent.keyDown(cartaoEl, { key: 'a' })
    expect(onOpen).toHaveBeenCalledTimes(3)
  })

  test('botão de PDF busca o detalhe da campanha e gera o PDF sem abrir o cartão', async () => {
    const dado = { campaign: { id: 'camp-1' }, rows: [] }
    const chamadas = mockApi({ 'GET /admin/campaign-failures/camp-1': dado })
    const onOpen = vi.fn()
    render(<CampaignFailureCard entry={ENTRADA(1)} onOpen={onOpen} />)
    fireEvent.click(screen.getByRole('button', { name: 'Baixar PDF de cobrança' }))
    await waitFor(() => expect(generateCampaignFailurePdf).toHaveBeenCalledWith(dado))
    expect(chamadas.some(c => c.chave === 'GET /admin/campaign-failures/camp-1')).toBe(true)
    expect(onOpen).not.toHaveBeenCalled()
  })

  test('falha ao gerar o PDF avisa o usuário e libera o botão', async () => {
    mockApi({ 'GET /admin/campaign-failures/camp-1': erroApi(500, {}) })
    render(<CampaignFailureCard entry={ENTRADA(1)} onOpen={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: 'Baixar PDF de cobrança' }))
    await waitFor(() => expect(alerta).toHaveBeenCalledWith('Não foi possível gerar o PDF. Tente novamente.'))
    expect(screen.getByRole('button', { name: 'Baixar PDF de cobrança' }).disabled).toBe(false)
    expect(generateCampaignFailurePdf).not.toHaveBeenCalled()
  })
})

// ───────────────────────── CampaignFailureRow ─────────────────────────

const ENTRADA_LINHA = (extra = {}) => ({
  campaign: {
    id: 'camp-1', name: 'Campanha Teste', client_name: 'Cliente Teste', status: 'ativa',
    start_date: '2026-03-01', end_date: '2026-03-31',
  },
  stations_with_failure: 4, total_failure_days: 9, total_deficit: 40,
  total_deficit_absent: 25, total_deficit_off_slot: 15, is_fully_bonified: false,
  ...extra,
})

function linha(entry, onOpen = () => {}) {
  return render(<table><tbody><CampaignFailureRow entry={entry} onOpen={onOpen} /></tbody></table>)
}

describe('CampaignFailureRow', () => {
  test('mostra cliente, campanha, período, status e os números', () => {
    const { container } = linha(ENTRADA_LINHA())
    expect(screen.getByText('Cliente Teste')).toBeTruthy()
    expect(screen.getByText('Campanha Teste')).toBeTruthy()
    expect(screen.getByText('01/03/2026 – 31/03/2026')).toBeTruthy()
    expect(screen.getByText('Ativa').className).toContain('cfr-status-ativa')
    const nums = Array.from(container.querySelectorAll('.cfr-num')).map(td => td.firstChild.textContent)
    expect(nums).toEqual(['4', '9', '40'])
    expect(container.querySelector('.cfr-num-deficit').textContent).toContain('não tocou 25')
    expect(container.querySelector('.cfr-num-deficit').textContent).toContain('fora do horário 15')
  })

  test('status desconhecido aparece como veio; datas ausentes viram traço', () => {
    linha(ENTRADA_LINHA({ campaign: { id: 'c', name: 'N', client_name: 'C', status: 'pausada', start_date: null, end_date: null } }))
    expect(screen.getByText('pausada')).toBeTruthy()
    expect(screen.getByText('— – —')).toBeTruthy()
  })

  test('100% bonificada ganha a etiqueta; senão a célula fica vazia', () => {
    const { unmount } = linha(ENTRADA_LINHA({ is_fully_bonified: true }))
    expect(screen.getByText('100% bonificada')).toBeTruthy()
    unmount()
    linha(ENTRADA_LINHA())
    expect(screen.queryByText('100% bonificada')).toBeNull()
  })

  test('clique e teclado abrem; o botão de PDF não dispara a abertura', async () => {
    const dado = { ok: true }
    mockApi({ 'GET /admin/campaign-failures/camp-1': dado })
    const onOpen = vi.fn()
    linha(ENTRADA_LINHA(), onOpen)
    const tr = screen.getAllByRole('button')[0]
    fireEvent.click(tr)
    fireEvent.keyDown(tr, { key: 'Enter' })
    fireEvent.keyDown(tr, { key: ' ' })
    expect(onOpen).toHaveBeenCalledTimes(3)
    fireEvent.click(screen.getByRole('button', { name: 'Baixar PDF de cobrança' }))
    await waitFor(() => expect(generateCampaignFailurePdf).toHaveBeenCalledWith(dado))
    expect(onOpen).toHaveBeenCalledTimes(3)
  })

  test('erro no PDF avisa o usuário', async () => {
    mockApi({ 'GET /admin/campaign-failures/camp-1': erroApi(500, {}) })
    linha(ENTRADA_LINHA())
    fireEvent.click(screen.getByRole('button', { name: 'Baixar PDF de cobrança' }))
    await waitFor(() => expect(alerta).toHaveBeenCalledWith('Não foi possível gerar o PDF. Tente novamente.'))
  })
})
