import { createRef } from 'react'
import { describe, test, expect, beforeEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, act, within } from '@testing-library/react'
import PricingStep from './PricingStep'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'

// window.alert: spy único (recriar depois de restaurar perde as chamadas)
const alerta = vi.spyOn(window, 'alert').mockImplementation(() => {})
beforeEach(() => alerta.mockClear())

const ESTACAO_1 = { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'XX', pmm: 1234.6 }
const ESTACAO_2 = { id: 'st-2', name: 'Rádio Outra AM', band: 'AM' }
const TIPOS = [
  { id: 't-spot', name: 'Spot 30s', color: '#3b82f6' },
  { id: 't-vin', name: 'Vinheta', color: '#22c55e' },
]

function propsBase(extra = {}) {
  return {
    campaignId: 'camp-1',
    campaignStations: [ESTACAO_1, ESTACAO_2],
    // st-1 tem um material do tipo Spot; a regra de distribuição soma o tipo Vinheta
    campaignMaterials: [{ material_id: 'm1', target_stations: ['st-1'] }],
    materialsById: { m1: { id: 'm1', type_id: 't-spot' } },
    distributionRules: [{ type_id: 't-vin', station_ids: ['st-1'] }],
    ...extra,
  }
}

function rotas(extra = {}) {
  return {
    'GET /campaigns/camp-1/pricing': [],
    'GET /material-types': TIPOS,
    ...extra,
  }
}

async function montar(props = {}, rotasExtra = {}) {
  const chamadas = mockApi(rotas(rotasExtra))
  const ref = createRef()
  const r = renderComProviders(<PricingStep ref={ref} {...propsBase(props)} />)
  await screen.findByText('Rádio Teste FM')
  return { chamadas, ref, ...r }
}

const cartao = (nome) => screen.getByText(nome).closest('article')
const campos = (escopo) => within(escopo).getAllByPlaceholderText('R$ 0,00')

describe('PricingStep — cartões por emissora', () => {
  test('mostra cabeçalho, dados da emissora e começa com tudo pendente', async () => {
    await montar()
    expect(screen.getByRole('heading', { name: 'Quanto vale cada emissora?' })).toBeTruthy()
    expect(screen.getByText(/FM 99.5 · Cidade Teste\/XX/)).toBeTruthy()
    expect(screen.getByText('PMM 1235')).toBeTruthy()
    expect(screen.getAllByText('Pendente').length).toBe(2)
    expect(screen.getByText('0/2')).toBeTruthy()
    expect(screen.getByText('0%')).toBeTruthy()
  })

  test('modo padrão: por inserção quando a emissora tem tipos (união de material + regra), consolidado quando não', async () => {
    await montar()
    const c1 = cartao('Rádio Teste FM')
    // tipos ordenados por nome: Spot 30s, Vinheta
    expect(within(c1).getByText('Spot 30s')).toBeTruthy()
    expect(within(c1).getByText('Vinheta')).toBeTruthy()
    expect(within(c1).getByText('Valor unitário por tipo (2)')).toBeTruthy()
    const c2 = cartao('Rádio Outra AM')
    expect(within(c2).getByText('Valor total da campanha pra essa emissora')).toBeTruthy()
    // sem tipos, "Por inserção" fica desabilitado e explica por quê
    expect(within(c2).getByRole('button', { name: /Por inserção/ }).disabled).toBe(true)
    expect(within(c2).getByText('Sem tipos linkados nessa emissora')).toBeTruthy()
  })

  test('hidrata os valores já cadastrados no servidor e marca OK', async () => {
    await montar({}, {
      'GET /campaigns/camp-1/pricing': [
        { station_id: 'st-1', mode: 'per_insertion', per_type: [{ type_id: 't-spot', unit_value: 100 }, { type_id: 't-vin', unit_value: 25.5 }] },
        { station_id: 'st-2', mode: 'consolidated', consolidated_value: 2000 },
      ],
    })
    await waitFor(() => expect(screen.getAllByText('OK').length).toBe(2))
    expect(screen.getByText('2/2')).toBeTruthy()
    expect(screen.getByText('100%')).toBeTruthy()
    expect(within(cartao('Rádio Teste FM')).getAllByPlaceholderText('R$ 0,00').map(i => i.value)).toEqual(['100,00', '25,50'])
    expect(within(cartao('Rádio Outra AM')).getByPlaceholderText('R$ 0,00').value).toBe('2000,00')
    // valor base = 100 + 25,5 + 2000
    expect(screen.getByText(/R\$\s*2\.125,50/)).toBeTruthy()
  })

  test('digitar valor com vírgula e milhar vira número; ao sair do campo formata com 2 casas', async () => {
    await montar()
    const campo = within(cartao('Rádio Outra AM')).getByPlaceholderText('R$ 0,00')
    fireEvent.focus(campo)
    fireEvent.change(campo, { target: { value: '1.500,75' } })
    expect(screen.getByText(/R\$\s*1\.500,75/)).toBeTruthy() // Valor base
    fireEvent.blur(campo)
    expect(campo.value).toBe('1500,75')
    expect(within(cartao('Rádio Outra AM')).getByText('OK')).toBeTruthy()
    expect(screen.getByText('1/2')).toBeTruthy()
  })

  test('limpar o valor volta a Pendente com o motivo "valor consolidado obrigatório"', async () => {
    await montar({}, { 'GET /campaigns/camp-1/pricing': [{ station_id: 'st-2', mode: 'consolidated', consolidated_value: 50 }] })
    const c2 = cartao('Rádio Outra AM')
    await waitFor(() => expect(within(c2).getByText('OK')).toBeTruthy())
    fireEvent.change(within(c2).getByPlaceholderText('R$ 0,00'), { target: { value: '' } })
    expect(within(c2).getByText('Pendente').getAttribute('title')).toBe('valor consolidado obrigatório')
  })

  test('por inserção só fica OK com TODOS os tipos preenchidos; o motivo aponta o que falta', async () => {
    await montar()
    const c1 = cartao('Rádio Teste FM')
    const [spot, vinheta] = campos(c1)
    expect(within(c1).getByText('Pendente').getAttribute('title')).toBe('Spot 30s sem valor')
    fireEvent.change(spot, { target: { value: '80' } })
    expect(within(c1).getByText('Pendente').getAttribute('title')).toBe('Vinheta sem valor')
    fireEvent.change(vinheta, { target: { value: '0' } })   // zero é valor válido
    expect(within(c1).getByText('OK')).toBeTruthy()
  })

  test('trocar de modo limpa o lado oposto (consolidado → por inserção zera o consolidado)', async () => {
    await montar({}, {
      'GET /campaigns/camp-1/pricing': [{ station_id: 'st-1', mode: 'consolidated', consolidated_value: 900 }],
    })
    const c1 = cartao('Rádio Teste FM')
    await waitFor(() => expect(within(c1).getByText('OK')).toBeTruthy())
    fireEvent.click(within(c1).getByRole('button', { name: /Por inserção/ }))
    expect(within(c1).getByText('Pendente')).toBeTruthy()
    expect(within(c1).getAllByPlaceholderText('R$ 0,00').map(i => i.value)).toEqual(['', ''])
    fireEvent.click(within(c1).getByRole('button', { name: /Valor consolidado/ }))
    expect(within(c1).getByPlaceholderText('R$ 0,00').value).toBe('')  // não voltou o 900 antigo
  })

  test('sem emissoras na campanha manda voltar ao passo 2', async () => {
    mockApi(rotas())
    renderComProviders(<PricingStep {...propsBase({ campaignStations: [] })} />)
    expect(await screen.findByText(/Volte ao passo 2 e selecione pelo menos uma emissora/)).toBeTruthy()
  })
})

describe('PricingStep — CPM fixo', () => {
  // Regressão: initialFixedCPM=null (campanha SEM CPM fixo) virava 0 via Number(null) e o
  // campo abria com "0,00 · ativo".
  test('campanha sem CPM fixo (null) abre vazia e "opcional"', async () => {
    await montar({ initialFixedCPM: null })
    expect(screen.getByText('· opcional')).toBeTruthy()
    expect(screen.getByPlaceholderText('usa cálc. dinâmico').value).toBe('')
  })

  test('com CPM fixo inicial: "ativo" e valor formatado em reais', async () => {
    await montar({ initialFixedCPM: 12.5 })
    expect(screen.getByText('· ativo')).toBeTruthy()
    expect(screen.getByPlaceholderText('usa cálc. dinâmico').value).toBe('12,50')
  })

  test('alterar o CPM fixo e salvar manda PUT /fixed-cpm com o número digitado', async () => {
    const { chamadas, ref } = await montar({ initialFixedCPM: 12.5 }, { 'PUT /campaigns/camp-1/fixed-cpm': { ok: true } })
    const campo = screen.getByPlaceholderText('usa cálc. dinâmico')
    fireEvent.focus(campo)
    fireEvent.change(campo, { target: { value: '20' } })
    fireEvent.blur(campo)
    expect(campo.value).toBe('20,00')
    // as emissoras estão pendentes (saveAll devolve false), mas o CPM é persistido antes
    await act(async () => { await ref.current.saveAll() })
    expect(chamadas.find(c => c.chave === 'PUT /campaigns/camp-1/fixed-cpm').data).toEqual({ fixed_cpm: 20 })
  })
})

describe('PricingStep — saveAll e isValid pelo ref', () => {
  async function montarPreenchido(rotasExtra = {}, props = {}) {
    return montar(props, {
      'GET /campaigns/camp-1/pricing': [
        { station_id: 'st-1', mode: 'per_insertion', per_type: [{ type_id: 't-spot', unit_value: 100 }, { type_id: 't-vin', unit_value: 25.5 }] },
        { station_id: 'st-2', mode: 'consolidated', consolidated_value: 2000 },
      ],
      'PUT /campaigns/camp-1/pricing/st-1': { ok: true },
      'PUT /campaigns/camp-1/pricing/st-2': { ok: true },
      'PUT /campaigns/camp-1/fixed-cpm': { ok: true },
      ...rotasExtra,
    })
  }

  test('isValid reflete se todas as emissoras estão completas', async () => {
    const { ref } = await montar()
    expect(ref.current.isValid()).toBe(false)
    fireEvent.change(within(cartao('Rádio Outra AM')).getByPlaceholderText('R$ 0,00'), { target: { value: '10' } })
    const [spot, vinheta] = campos(cartao('Rádio Teste FM'))
    fireEvent.change(spot, { target: { value: '1' } })
    fireEvent.change(vinheta, { target: { value: '2' } })
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
  })

  test('saveAll envia um PUT por emissora no formato de cada modo e devolve true', async () => {
    const { chamadas, ref } = await montarPreenchido()
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
    let ok
    await act(async () => { ok = await ref.current.saveAll() })
    expect(ok).toBe(true)
    const put1 = chamadas.find(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-1')
    expect(put1.data).toEqual({
      mode: 'per_insertion', consolidated_value: null,
      per_type: [{ type_id: 't-spot', unit_value: 100 }, { type_id: 't-vin', unit_value: 25.5 }],
    })
    const put2 = chamadas.find(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-2')
    expect(put2.data).toEqual({ mode: 'consolidated', consolidated_value: 2000, per_type: null })
    expect(alerta).not.toHaveBeenCalled()
    // CPM fixo não mudou → nada de PUT /fixed-cpm
    expect(chamadas.some(c => c.chave === 'PUT /campaigns/camp-1/fixed-cpm')).toBe(false)
  })

  test('com CPM fixo alterado, saveAll persiste o CPM primeiro', async () => {
    const { chamadas, ref } = await montarPreenchido({}, { initialFixedCPM: 10 })
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
    fireEvent.change(screen.getByPlaceholderText('usa cálc. dinâmico'), { target: { value: '15,5' } })
    await act(async () => { await ref.current.saveAll() })
    const idxCpm = chamadas.findIndex(c => c.chave === 'PUT /campaigns/camp-1/fixed-cpm')
    const idxPreco = chamadas.findIndex(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-1')
    expect(chamadas[idxCpm].data).toEqual({ fixed_cpm: 15.5 })
    expect(idxCpm).toBeGreaterThanOrEqual(0)
    expect(idxCpm).toBeLessThan(idxPreco)
  })

  test('limpar o CPM fixo envia null', async () => {
    const { chamadas, ref } = await montarPreenchido({}, { initialFixedCPM: 10 })
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
    fireEvent.change(screen.getByPlaceholderText('usa cálc. dinâmico'), { target: { value: '' } })
    await act(async () => { await ref.current.saveAll() })
    expect(chamadas.find(c => c.chave === 'PUT /campaigns/camp-1/fixed-cpm').data).toEqual({ fixed_cpm: null })
  })

  test('emissora incompleta: alerta lista o motivo, devolve false e NÃO grava a incompleta', async () => {
    const { chamadas, ref } = await montar({}, {
      'GET /campaigns/camp-1/pricing': [{ station_id: 'st-2', mode: 'consolidated', consolidated_value: 300 }],
      'PUT /campaigns/camp-1/pricing/st-2': { ok: true },
    })
    await waitFor(() => expect(within(cartao('Rádio Outra AM')).getByText('OK')).toBeTruthy())
    let ok
    await act(async () => { ok = await ref.current.saveAll() })
    expect(ok).toBe(false)
    expect(alerta).toHaveBeenCalledTimes(1)
    expect(alerta.mock.calls[0][0]).toContain('Rádio Teste FM: Spot 30s sem valor')
    expect(chamadas.some(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-1')).toBe(false)
    // a emissora válida foi salva mesmo assim
    expect(chamadas.some(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-2')).toBe(true)
  })

  test('erro do servidor em uma emissora entra no alerta e saveAll devolve false', async () => {
    const { ref } = await montarPreenchido({ 'PUT /campaigns/camp-1/pricing/st-2': erroApi(500, 'banco indisponível') })
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
    let ok
    await act(async () => { ok = await ref.current.saveAll() })
    expect(ok).toBe(false)
    expect(alerta.mock.calls[0][0]).toContain('Rádio Outra AM: banco indisponível')
  })

  test('erro ao salvar o CPM fixo é coletado e não impede salvar os preços', async () => {
    const { chamadas, ref } = await montarPreenchido({ 'PUT /campaigns/camp-1/fixed-cpm': erroApi(400, 'cpm inválido') }, { initialFixedCPM: 10 })
    await waitFor(() => expect(ref.current.isValid()).toBe(true))
    fireEvent.change(screen.getByPlaceholderText('usa cálc. dinâmico'), { target: { value: '30' } })
    let ok
    await act(async () => { ok = await ref.current.saveAll() })
    expect(ok).toBe(false)
    expect(alerta.mock.calls[0][0]).toContain('CPM fixo: cpm inválido')
    expect(chamadas.some(c => c.chave === 'PUT /campaigns/camp-1/pricing/st-1')).toBe(true)
  })
})
