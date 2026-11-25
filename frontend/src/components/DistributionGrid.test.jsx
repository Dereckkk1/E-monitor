import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import DistributionGrid from './DistributionGrid'

const ESTACAO = { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'XX', pmm: 1000 }
const LINHA = { stationId: 'st-1', materialId: 't-spot', materialTitle: 'Spot 30s', typeColor: '#3b82f6', ruleSummary: null, extraRules: 0 }

// 02, 03 e 04/03/2026
const CELULAS = new Map([
  ['st-1|t-spot|2026-03-02', { expected: 2, in_slot: 2, deficit: 0, bonus: 1 }],
  ['st-1|t-spot|2026-03-03', { expected: 2, in_slot: 1, deficit: 1, out_slot: 1 }],
  ['st-1|t-spot|2026-03-04', { expected: 2, in_slot: 0, deficit: 2, out_date: 1, hasOverride: true }],
])

function montar(props = {}) {
  return render(
    <DistributionGrid
      month={new Date(2026, 2, 1)}
      campaignStart="2026-03-02"
      campaignEnd="2026-03-04"
      stations={[ESTACAO]}
      rows={[LINHA]}
      cellData={CELULAS}
      capAtToday={false}
      {...props}
    />,
  )
}

describe('DistributionGrid — estrutura', () => {
  test('cabeçalho com o dia da semana e o número de cada dia da campanha no mês', () => {
    const { container } = montar()
    expect(screen.getByText('Emissora / Material')).toBeTruthy()
    expect(screen.getByText('Resumo')).toBeTruthy()
    // 02/03/2026 = segunda, 03 = terça, 04 = quarta
    const cabecalhos = Array.from(container.querySelectorAll('div')).filter(d => /^(DOM|SEG|TER|QUA|QUI|SEX|SÁB)\d\d$/.test(d.textContent))
    expect(cabecalhos.map(d => d.textContent)).toEqual(['SEG02', 'TER03', 'QUA04'])
  })

  test('mostra a emissora (nome, banda, dial, cidade) e a linha do material', () => {
    montar()
    expect(screen.getByText('Rádio Teste FM')).toBeTruthy()
    expect(screen.getByText(/FM 99.5 · Cidade Teste/)).toBeTruthy()
    expect(screen.getByText('Spot 30s')).toBeTruthy()
  })

  test('modo inline (usado em /detections) separa as colunas Emissora e Material', () => {
    montar({ inlineStationInfo: true })
    expect(screen.getByText('Emissora')).toBeTruthy()
    expect(screen.getByText('Material')).toBeTruthy()
    expect(screen.getByText('FM 99.5')).toBeTruthy()
    expect(screen.getByText('Cidade Teste / XX')).toBeTruthy()
  })

  test('linha "fantasma" avisa que aguarda áudio; linha fora do escopo explica por quê', () => {
    montar({
      rows: [
        { ...LINHA, ghost: true },
        { ...LINHA, materialId: 't-vin', materialTitle: 'Vinheta', outOfScope: true },
      ],
    })
    expect(screen.getByText('· aguardando áudio')).toBeTruthy()
    const fora = screen.getByText('· fora do escopo atual')
    expect(fora.getAttribute('title')).toMatch(/Nenhum material deste tipo aponta mais/)
  })

  test('emissora fora do catálogo NUNCA some: aparece o placeholder e as veiculações continuam', () => {
    montar({ stations: [], inlineStationInfo: true })
    expect(screen.getByText('Emissora não identificada')).toBeTruthy()
    expect(screen.getByText('cadastro não carregado')).toBeTruthy()
    expect(screen.getByTitle('Spot 30s · 2026-03-02')).toBeTruthy()
  })

  test('clicar na emissora chama onStationClick com o id', () => {
    const onStationClick = vi.fn()
    montar({ onStationClick })
    fireEvent.click(screen.getByText('Rádio Teste FM'))
    expect(onStationClick).toHaveBeenCalledWith('st-1')
  })

  test('intervalo visível explícito cruza meses e mostra a sigla do mês', () => {
    const { container } = montar({ visibleStart: '2026-02-27', visibleEnd: '2026-03-03', campaignStart: '2026-02-01', campaignEnd: '2026-03-31' })
    const texto = container.textContent
    expect(texto).toContain('fev')
    expect(texto).toContain('mar')
    expect(screen.getAllByTitle(/^Spot 30s · /).length).toBe(5)  // 27, 28/02 e 01, 02, 03/03
  })
})

describe('DistributionGrid — células e resumos', () => {
  test('cada dia mostra os contadores do cellData e os dias sem dado ficam vazios', () => {
    montar()
    const c2 = screen.getByTitle('Spot 30s · 2026-03-02')
    expect(c2.textContent).toBe('22+1')       // esperado 2, tocou 2, bônus +1
    const c3 = screen.getByTitle('Spot 30s · 2026-03-03')
    expect(c3.textContent).toBe('21-1+1')     // esperado 2, tocou 1, faltou -1, fora da faixa +1
    const c4 = screen.getByTitle('Spot 30s · 2026-03-04')
    expect(c4.textContent).toBe('2-2+1')      // esperado 2 (override), faltou -2, fora da data +1
  })

  test('célula fora do período da campanha não responde ao clique', () => {
    const onCellClick = vi.fn()
    montar({ onCellClick, campaignStart: '2026-03-03', visibleStart: '2026-03-02', visibleEnd: '2026-03-04' })
    fireEvent.click(screen.getByTitle('Spot 30s · 2026-03-02'))
    expect(onCellClick).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTitle('Spot 30s · 2026-03-03'))
    expect(onCellClick).toHaveBeenCalledTimes(1)
  })

  test('clicar numa célula chama onCellClick(emissora, tipo, data, retângulo)', () => {
    const onCellClick = vi.fn()
    montar({ onCellClick })
    fireEvent.click(screen.getByTitle('Spot 30s · 2026-03-03'))
    expect(onCellClick).toHaveBeenCalledTimes(1)
    const [est, tipo, data, rect] = onCellClick.mock.calls[0]
    expect([est, tipo, data]).toEqual(['st-1', 't-spot', '2026-03-03'])
    expect(rect).toHaveProperty('top')
  })

  test('com onCellIncrement/Decrement, os botões +/- aparecem no hover e repassam a célula', () => {
    const onInc = vi.fn(), onDec = vi.fn()
    montar({ onCellIncrement: onInc, onCellDecrement: onDec })
    const celula = screen.getByTitle('Spot 30s · 2026-03-02')
    fireEvent.mouseEnter(celula)
    fireEvent.click(within(celula).getByRole('button', { name: 'Aumentar' }))
    fireEvent.click(within(celula).getByRole('button', { name: 'Diminuir' }))
    expect(onInc.mock.calls[0].slice(0, 3)).toEqual(['st-1', 't-spot', '2026-03-02'])
    expect(onDec.mock.calls[0].slice(0, 3)).toEqual(['st-1', 't-spot', '2026-03-02'])
  })

  test('resumo da linha soma o período: programada, tocou, bônus, déficit, fora da faixa, fora da data', () => {
    const { container } = montar()
    // 6 pílulas na ordem: programada / tocou / bônus / déficit / fora da faixa / fora da data
    const pilulas = Array.from(container.querySelectorAll('span[style*="min-width: 22px"]')).map(s => s.textContent)
    expect(pilulas).toEqual(['6', '3', '+1', '-3', '+1', '+1'])
  })

  test('impactos = PMM × (tocou + bônus), com sufixo K', () => {
    montar()
    // 1000 × (3 + 1) = 4.000
    const pill = screen.getByTitle('4.000 impactos = PMM 1.000 × 4 veiculações na estação (3 dentro da faixa + 1 de bonificação)')
    expect(pill.textContent).toBe('4,0K')
  })

  test('sem PMM cadastrado mostra "—" e explica', () => {
    montar({ stations: [{ ...ESTACAO, pmm: null }] })
    const pill = screen.getByTitle('PMM não cadastrado pra essa emissora')
    expect(pill.textContent).toBe('—')
  })

  test('pill do target só aparece com PMM no target cadastrado e usa a mesma base', () => {
    const { rerender } = montar()
    expect(screen.queryByText(/target$/)).toBeNull()
    rerender(
      <DistributionGrid month={new Date(2026, 2, 1)} campaignStart="2026-03-02" campaignEnd="2026-03-04"
        stations={[ESTACAO]} rows={[LINHA]} cellData={CELULAS} capAtToday={false}
        pmmTargetByStation={{ 'st-1': 500 }} targetLabel="Público Teste" />,
    )
    const pill = screen.getByText('2,0K target')   // 500 × 4
    expect(pill.getAttribute('title')).toMatch(/\(Público Teste\)/)
  })

  test('sem preço cadastrado o valor é "R$ —"', () => {
    montar()
    const pill = screen.getByTitle('Pricing não cadastrado')
    expect(pill.textContent).toBe('R$ —')
  })

  test('preço consolidado mostra o valor fechado, independente das veiculações', () => {
    montar({ pricingByStation: { 'st-1': { mode: 'consolidated', consolidated_value: 1500 } } })
    const pill = screen.getByTitle('Valor consolidado da emissora na campanha')
    expect(pill.textContent).toMatch(/R\$\s*1\.500,00/)
  })

  test('preço por inserção: valor unitário × tocadas, e bônus em reais à parte', () => {
    montar({ pricingByStation: { 'st-1': { mode: 'per_insertion', per_type: [{ type_id: 't-spot', unit_value: 100 }] } } })
    expect(screen.getByTitle('Σ (valor unitário × veiculações) por tipo · 3 inserções').textContent).toMatch(/R\$\s*300,00/)
    expect(screen.getByTitle('Bonificação total: 1 inserções × valor unitário').textContent).toMatch(/R\$\s*100,00/)
  })

  test('summary="plan" (usado em /materials) troca impactos/valor por "N programados"', () => {
    montar({ summary: 'plan' })
    expect(screen.getByText('6 programados')).toBeTruthy()
    expect(screen.queryByText('R$ —')).toBeNull()
    // só a pílula escura de "programada" no resumo da linha
    const { container } = montar({ summary: 'plan' })
    expect(container.querySelectorAll('span[style*="min-width: 22px"]').length).toBeGreaterThan(0)
  })

  test('summary="plan" no singular: "1 programado"', () => {
    montar({
      summary: 'plan',
      cellData: new Map([['st-1|t-spot|2026-03-02', { expected: 1 }]]),
    })
    expect(screen.getByText('1 programado')).toBeTruthy()
  })

  test('várias linhas da mesma emissora compartilham UM bloco de total', () => {
    montar({
      rows: [LINHA, { ...LINHA, materialId: 't-vin', materialTitle: 'Vinheta' }],
    })
    expect(screen.getAllByTitle(/impactos = PMM/).length).toBe(1)
    expect(screen.getByText('Vinheta')).toBeTruthy()
  })
})
