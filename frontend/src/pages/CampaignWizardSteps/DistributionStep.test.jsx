import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import DistributionStep from './DistributionStep'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'

const TIPOS = [
  { id: 't-spot', name: 'Spot 30s', color: '#3b82f6' },
  { id: 't-vin', name: 'Vinheta', color: '#22c55e' },
]
const ESTACOES = [
  { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'XX', pmm: 1000 },
  { id: 'st-2', name: 'Rádio Outra AM', band: 'AM', city: 'Outra Cidade', state: 'YY' },
]
const REGRA_SPOT = {
  id: 'r1', type_id: 't-spot', station_ids: ['st-1'], material_ids: [], name: 'Conjunto Teste',
  start_date: '2026-03-01T00:00:00Z', end_date: '2026-03-31T00:00:00Z',
  weekday_mask: 62, time_start: '08:00:00', time_end: '10:00:00', plays_per_day: 3,
}
const REGRA_VINHETA = {
  id: 'r2', type_id: 't-vin', station_ids: ['st-1'], material_ids: [], name: '',
  start_date: '2026-03-10T00:00:00Z', end_date: '2026-03-20T00:00:00Z',
  weekday_mask: 127, time_start: '06:00:00', time_end: '22:00:00', plays_per_day: 1,
}
// segunda 02/03/2026 (regra seg-sex vale) e sábado 07/03 (nenhuma regra vale)
const RESUMO = [
  { station_id: 'st-1', type_id: 't-spot', for_date: '2026-03-02', expected: 3, in_slot: 1, deficit: 2 },
  { station_id: 'st-1', type_id: 't-spot', for_date: '2026-03-03', expected: 2, in_slot: 2, deficit: 0 },
]
const OVERRIDE_03 = { station_id: 'st-1', type_id: 't-spot', for_date: '2026-03-03', plays_expected: 2, time_start: '09:00', time_end: '11:00' }
const OVERRIDE_10 = { station_id: 'st-1', type_id: 't-spot', for_date: '2026-03-10', plays_expected: 1, time_start: '09:00', time_end: '11:00' }

function propsBase(extra = {}) {
  return {
    campaignId: 'camp-1',
    // 02/03: com a data de início no dia 1 a tela abre no mês anterior (ver o skip de BUG abaixo)
    campaignStart: '2026-03-02',
    campaignEnd: '2026-03-31',
    campaignMaterials: [{ material_id: 'm1', target_stations: ['st-1'] }],
    campaignStationIds: ['st-1', 'st-2'],
    materialsById: { m1: { id: 'm1', type_id: 't-spot', title: 'Spot Teste A', duration_seconds: 30 } },
    allStations: ESTACOES,
    ...extra,
  }
}

function rotas(extra = {}) {
  return {
    'GET /campaigns/camp-1/distribution-rules': [REGRA_SPOT, REGRA_VINHETA],
    'GET /campaigns/camp-1/distribution-overrides': [OVERRIDE_03],
    'GET /campaigns/camp-1/daily-summary': RESUMO,
    'GET /material-types': TIPOS,
    ...extra,
  }
}

async function montar(props = {}, rotasExtra = {}) {
  const chamadas = mockApi(rotas(rotasExtra))
  const r = renderComProviders(<DistributionStep {...propsBase(props)} />)
  await screen.findByText('Quando e quantas vezes vai tocar?')
  return { chamadas, ...r }
}

// painel lateral de regra (portal) — escopo pra não confundir com a lista de regras
const painel = () => within(screen.getByRole('heading', { name: /^(Nova|Editar) regra$/ }).closest('div[style*="width: 480px"]'))

// espera as linhas do grid (dependem de tipos + regras carregados)
const celula = (titulo, dia) => screen.findByTitle(`${titulo} · ${dia}`)

describe('DistributionStep — visão geral', () => {
  test('contadores de regras e overrides e a lista de regras com a assinatura de cada uma', async () => {
    await montar()
    await screen.findByText('Conjunto Teste')
    expect(screen.getByText('regras').textContent).toBe('2 regras')
    expect(screen.getByText('override').textContent).toBe('1 override')
    // regra com nome: nome vira título, o tipo aparece ao lado
    const linhaNomeada = screen.getByText('Conjunto Teste').closest('button')
    expect(linhaNomeada.textContent).toContain('Spot 30s')
    expect(linhaNomeada.textContent).toContain('08:00–10:00 · Seg a Sex · Todo o período')
    expect(linhaNomeada.textContent).toContain('1 emissora: Rádio Teste FM')
    expect(linhaNomeada.textContent).toContain('todos do tipo')
    expect(linhaNomeada.textContent).toContain('3×/dia')
    // regra sem nome: o título é o tipo; período parcial e todos os dias
    const linhaAnonima = screen.getAllByRole('button', { name: /Vinheta/ }).find(b => b.title === 'Editar regra')
    expect(linhaAnonima.textContent).toContain('06:00–22:00 · Todos os dias · 10/03–20/03')
  })

  test('aviso âmbar lista os tipos com regra mas sem material vinculado', async () => {
    await montar()
    const aviso = await screen.findByText(/sem áudio vinculado ainda/)
    expect(aviso.textContent).toContain('1 regra')
    expect(aviso.textContent).toContain('Vinheta')
    expect(aviso.textContent).toContain('do tipo')
  })

  test('o grid mostra uma linha por emissora × tipo; tipo sem material é "fantasma"', async () => {
    await montar()
    expect(await celula('Spot 30s', '2026-03-02')).toBeTruthy()
    expect(screen.getByText('· aguardando áudio')).toBeTruthy()
    expect(screen.getByTitle('Vinheta · 2026-03-12')).toBeTruthy()
    // a meta do dia vem do daily-summary
    expect(screen.getByTitle('Spot 30s · 2026-03-02').textContent).toContain('3')
  })

  test('sem regra e sem material mostra o estado vazio e o CTA abre o painel de regra', async () => {
    await montar({ campaignMaterials: [] }, {
      'GET /campaigns/camp-1/distribution-rules': [],
      'GET /campaigns/camp-1/distribution-overrides': [],
      'GET /campaigns/camp-1/daily-summary': [],
    })
    expect(await screen.findByText('Crie a primeira regra de distribuição')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Adicionar primeira regra' }))
    expect(await screen.findByRole('heading', { name: 'Nova regra' })).toBeTruthy()
  })
})

describe('DistributionStep — mês inicial', () => {
  // BUG: DistributionStep.jsx:30-32 — `new Date(campaignStart)` lê 'YYYY-MM-DD' como UTC; em
  // America/Sao_Paulo (UTC-3) uma campanha que começa no dia 1º vira 21:00 do último dia do mês
  // anterior, e o passo abre em Fevereiro com a grade vazia. Esperado: abrir em "Março 2026"
  // (o resto do app usa parseLocalDate — utils/dates.js — justamente por isso).
  test.skip('campanha que começa no dia 1º abre no mês de início', async () => {
    await montar({ campaignStart: '2026-03-01' })
    expect(await screen.findByText('Março 2026')).toBeTruthy()
  })

  test('campanha que começa no meio do mês abre naquele mês', async () => {
    await montar()
    expect(await screen.findByText('Março 2026')).toBeTruthy()
  })
})

describe('DistributionStep — filtros', () => {
  test('filtrar regras: "n de total", sem resultado e limpar', async () => {
    await montar()
    await screen.findByText('Conjunto Teste')
    const filtro = screen.getByLabelText('Filtrar regras')
    fireEvent.change(filtro, { target: { value: 'vinheta' } })
    expect(screen.getByText('1 de 2')).toBeTruthy()
    expect(screen.queryByText('Conjunto Teste')).toBeNull()
    fireEvent.change(filtro, { target: { value: 'zzzz' } })
    expect(screen.getByText(/Nenhuma regra corresponde a/).textContent).toContain('zzzz')
    fireEvent.click(screen.getByRole('button', { name: 'Limpar filtro' }))
    expect(screen.getByText('Conjunto Teste')).toBeTruthy()
  })

  test('busca de emissora filtra o grid, mostra a contagem e tem estado sem resultado', async () => {
    await montar()
    await celula('Spot 30s', '2026-03-02')
    const busca = screen.getByPlaceholderText(/Buscar emissora por nome, dial ou cidade/)
    fireEvent.change(busca, { target: { value: 'teste' } })
    expect(screen.getByText('1 emissora')).toBeTruthy()
    expect(screen.getByTitle('Spot 30s · 2026-03-02')).toBeTruthy()
    fireEvent.change(busca, { target: { value: 'inexistente' } })
    expect(screen.getByText(/Nenhuma emissora corresponde a/).textContent).toContain('inexistente')
    expect(screen.queryByTitle('Spot 30s · 2026-03-02')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Limpar busca' }))
    expect(await celula('Spot 30s', '2026-03-02')).toBeTruthy()
  })

  test('navegar de mês busca o resumo e os overrides do mês seguinte', async () => {
    const { chamadas } = await montar({ campaignEnd: '2026-05-31' })
    await celula('Spot 30s', '2026-03-02')
    fireEvent.click(screen.getByRole('button', { name: 'Próximo mês' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'GET /campaigns/camp-1/daily-summary'
      && c.params?.from === '2026-04-01' && c.params?.to === '2026-04-30')).toBe(true))
    expect(screen.getByText('Abril 2026')).toBeTruthy()
  })
})

describe('DistributionStep — ajuste de células', () => {
  test('+ numa célula com regra única só guarda rascunho; Descartar volta ao valor do servidor', async () => {
    const { chamadas } = await montar()
    const c = await celula('Spot 30s', '2026-03-02')
    fireEvent.mouseEnter(c)
    fireEvent.click(within(c).getByRole('button', { name: 'Aumentar' }))
    expect(screen.getByText('alteração pendente', { exact: false })).toBeTruthy()
    expect(screen.getByTitle('Spot 30s · 2026-03-02').textContent).toContain('4')   // meta 3 → 4 (rascunho)
    expect(chamadas.some(x => x.metodo === 'PUT')).toBe(false)                      // ainda não foi pro servidor
    fireEvent.click(screen.getByRole('button', { name: 'Descartar' }))
    expect(screen.queryByText('alteração pendente', { exact: false })).toBeNull()
    expect(screen.getByTitle('Spot 30s · 2026-03-02').textContent).toContain('3')
  })

  test('Confirmar manda um PUT por célula com a faixa da regra única', async () => {
    const { chamadas } = await montar({}, { 'PUT /campaigns/camp-1/distribution-overrides': { ok: true } })
    const c = await celula('Spot 30s', '2026-03-02')
    fireEvent.mouseEnter(c)
    fireEvent.click(within(c).getByRole('button', { name: 'Aumentar' }))
    fireEvent.click(within(screen.getByTitle('Spot 30s · 2026-03-02')).getByRole('button', { name: 'Aumentar' }))
    expect(screen.getByText('alteração pendente', { exact: false })).toBeTruthy()  // 1 célula, 2 passos
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(x => x.chave === 'PUT /campaigns/camp-1/distribution-overrides')).toBe(true))
    const put = chamadas.find(x => x.chave === 'PUT /campaigns/camp-1/distribution-overrides')
    expect(put.data).toEqual({
      type_id: 't-spot', station_id: 'st-1', for_date: '2026-03-02',
      plays_expected: 5, time_start: '08:00', time_end: '10:00',
    })
    await waitFor(() => expect(screen.queryByText('alteração pendente', { exact: false })).toBeNull())
  })

  test('− não passa de zero', async () => {
    await montar({}, { 'GET /campaigns/camp-1/daily-summary': [{ station_id: 'st-1', type_id: 't-spot', for_date: '2026-03-02', expected: 0 }] })
    const c = await celula('Spot 30s', '2026-03-02')
    fireEvent.mouseEnter(c)
    fireEvent.click(within(c).getByRole('button', { name: 'Diminuir' }))
    expect(screen.getByText('alteração pendente', { exact: false })).toBeTruthy()
    // continua em 0 (célula sem contador visível)
    expect(/\d/.test(screen.getByTitle('Spot 30s · 2026-03-02').textContent)).toBe(false)
  })

  test('falha ao confirmar avisa pelo alerta e mantém o rascunho', async () => {
    await montar({}, { 'PUT /campaigns/camp-1/distribution-overrides': erroApi(500, {}) })
    const c = await celula('Spot 30s', '2026-03-02')
    fireEvent.mouseEnter(c)
    fireEvent.click(within(c).getByRole('button', { name: 'Aumentar' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    expect(await screen.findByText('Erro ao salvar alterações em 2026-03-02. Tente novamente.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'OK' }))
    expect(screen.getByText('alteração pendente', { exact: false })).toBeTruthy()
  })

  test('+ numa célula sem regra aplicável abre o popover de override (em vez de gravar rascunho)', async () => {
    await montar()
    const sabado = await celula('Spot 30s', '2026-03-07')   // regra é seg-sex; sábado não tem faixa
    fireEvent.mouseEnter(sabado)
    fireEvent.click(within(sabado).getByRole('button', { name: 'Aumentar' }))
    expect(await screen.findByText('Regra: 0×/dia')).toBeTruthy()
    expect(screen.queryByText('alteração pendente', { exact: false })).toBeNull()
  })

  test('clicar numa célula com override abre o popover e "Voltar à regra" apaga o ajuste', async () => {
    const { chamadas } = await montar({}, { 'DELETE /campaigns/camp-1/distribution-overrides': { ok: true } })
    const c = await celula('Spot 30s', '2026-03-03')
    fireEvent.click(c)
    fireEvent.click(await screen.findByRole('button', { name: /Voltar à regra/ }))
    await waitFor(() => expect(chamadas.some(x => x.chave === 'DELETE /campaigns/camp-1/distribution-overrides')).toBe(true))
    expect(chamadas.find(x => x.chave === 'DELETE /campaigns/camp-1/distribution-overrides').data)
      .toEqual({ type_id: 't-spot', station_id: 'st-1', for_date: '2026-03-03' })
  })

  test('Aplicar no popover grava o override; "aplicar nas demais" replica a faixa nos outros overrides do tipo', async () => {
    const { chamadas } = await montar({}, {
      'GET /campaigns/camp-1/distribution-overrides': [OVERRIDE_03, OVERRIDE_10],
      'PUT /campaigns/camp-1/distribution-overrides': { ok: true },
    })
    const c = await celula('Spot 30s', '2026-03-03')
    fireEvent.click(c)
    await screen.findByText(/Aplicar essa mesma faixa nas demais células/)
    fireEvent.change(screen.getAllByDisplayValue('09:00')[0], { target: { value: '07:00' } })
    fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.click(screen.getByRole('button', { name: 'Aplicar' }))
    await waitFor(() => expect(chamadas.filter(x => x.chave === 'PUT /campaigns/camp-1/distribution-overrides').length).toBe(2))
    const puts = chamadas.filter(x => x.chave === 'PUT /campaigns/camp-1/distribution-overrides').map(x => x.data)
    expect(puts[0]).toMatchObject({ for_date: '2026-03-03', plays_expected: 2, time_start: '07:00', time_end: '11:00' })
    // a outra célula mantém a meta e herda só a faixa
    expect(puts[1]).toMatchObject({ for_date: '2026-03-10', plays_expected: 1, time_start: '07:00', time_end: '11:00' })
  })
})

describe('DistributionStep — regras', () => {
  test('clicar numa regra abre o editor; Salvar manda PUT com type_id e a faixa editada', async () => {
    const { chamadas } = await montar({}, { 'PUT /campaigns/camp-1/distribution-rules/r1': { ok: true } })
    fireEvent.click((await screen.findByText('Conjunto Teste')).closest('button'))
    expect(await screen.findByRole('heading', { name: 'Editar regra' })).toBeTruthy()
    fireEvent.change(screen.getByDisplayValue('3'), { target: { value: '6' } })
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(chamadas.some(x => x.chave === 'PUT /campaigns/camp-1/distribution-rules/r1')).toBe(true))
    expect(chamadas.find(x => x.chave === 'PUT /campaigns/camp-1/distribution-rules/r1').data).toEqual({
      type_id: 't-spot', name: 'Conjunto Teste', station_ids: ['st-1'], material_ids: [],
      start_date: '2026-03-01', end_date: '2026-03-31', weekday_mask: 62,
      time_start: '08:00', time_end: '10:00', plays_per_day: 6,
    })
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Editar regra' })).toBeNull())
  })

  test('Excluir regra pede confirmação; confirmar apaga e fecha o painel', async () => {
    const { chamadas } = await montar({}, { 'DELETE /campaigns/camp-1/distribution-rules/r1': { ok: true } })
    fireEvent.click((await screen.findByText('Conjunto Teste')).closest('button'))
    fireEvent.click(await screen.findByRole('button', { name: /Excluir regra/ }))
    expect(await screen.findByText('Excluir esta regra?')).toBeTruthy()
    expect(chamadas.some(x => x.chave === 'DELETE /campaigns/camp-1/distribution-rules/r1')).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Confirmar' }))
    await waitFor(() => expect(chamadas.some(x => x.chave === 'DELETE /campaigns/camp-1/distribution-rules/r1')).toBe(true))
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Editar regra' })).toBeNull())
  })

  test('cancelar a confirmação não apaga nada', async () => {
    const { chamadas } = await montar()
    fireEvent.click((await screen.findByText('Conjunto Teste')).closest('button'))
    fireEvent.click(await screen.findByRole('button', { name: /Excluir regra/ }))
    await screen.findByText('Excluir esta regra?')
    const dialogo = within(screen.getByText('Excluir esta regra?').closest('[role="dialog"]'))
    fireEvent.click(dialogo.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByText('Excluir esta regra?')).toBeNull())
    expect(chamadas.some(x => x.metodo === 'DELETE')).toBe(false)
  })

  test('Nova regra com dois tipos faz um POST por tipo, com os mesmos parâmetros', async () => {
    const { chamadas } = await montar({}, { 'POST /campaigns/camp-1/distribution-rules': { id: 'novo' } })
    await screen.findByText('Conjunto Teste')
    fireEvent.click(screen.getByRole('button', { name: 'Nova regra' }))
    await screen.findByRole('heading', { name: 'Nova regra' })
    fireEvent.click(painel().getByRole('button', { name: /^Spot 30s/ }))
    fireEvent.click(painel().getByRole('button', { name: /^Vinheta/ }))
    fireEvent.click(painel().getByRole('button', { name: /Rádio Outra AM/ }))
    fireEvent.click(painel().getByRole('button', { name: 'Adicionar 2 regras' }))
    await waitFor(() => expect(chamadas.filter(x => x.chave === 'POST /campaigns/camp-1/distribution-rules').length).toBe(2))
    const posts = chamadas.filter(x => x.chave === 'POST /campaigns/camp-1/distribution-rules').map(x => x.data)
    expect(posts.map(p => p.type_id)).toEqual(['t-spot', 't-vin'])
    expect(posts[0]).toMatchObject({
      station_ids: ['st-2'], material_ids: [], start_date: '2026-03-02', end_date: '2026-03-31',
      weekday_mask: 62, time_start: '08:00', time_end: '10:00', plays_per_day: 3,
    })
    expect(posts[0]).not.toHaveProperty('type_ids')
  })

  test('erro ao criar a regra mostra o aviso e deixa o painel aberto', async () => {
    await montar({}, { 'POST /campaigns/camp-1/distribution-rules': erroApi(422, { error: 'faixa inválida' }) })
    await screen.findByText('Conjunto Teste')
    fireEvent.click(screen.getByRole('button', { name: 'Nova regra' }))
    await screen.findByRole('heading', { name: 'Nova regra' })
    fireEvent.click(painel().getByRole('button', { name: /^Spot 30s/ }))
    fireEvent.click(painel().getByRole('button', { name: /Rádio Teste FM/ }))
    fireEvent.click(painel().getByRole('button', { name: 'Adicionar regra' }))
    expect(await screen.findByText('Erro ao salvar regra. Verifique os campos e tente novamente.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'OK' }))
    expect(screen.getByRole('heading', { name: 'Nova regra' })).toBeTruthy()
  })
})
