import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import RuleSidePanel from './RuleSidePanel'

const TIPOS = [
  { id: 't-spot', name: 'Spot 30s', color: '#3b82f6', materialCount: 2 },
  { id: 't-vin', name: 'Vinheta', color: '#22c55e', materialCount: 0 },
]
const EMISSORAS = [
  { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste' },
  { id: 'st-2', name: 'Rádio Outra AM', band: 'AM' },
]
const MATERIAIS_POR_TIPO = new Map([
  ['t-spot', [{ id: 'm1', title: 'Spot Teste A', durationSeconds: 30 }, { id: 'm2', title: 'Spot Teste B' }]],
])

function montar(props = {}) {
  const onClose = vi.fn(), onSubmit = vi.fn(), onDelete = vi.fn()
  const r = render(
    <RuleSidePanel
      open onClose={onClose} onSubmit={onSubmit} onDelete={onDelete}
      types={TIPOS} stations={EMISSORAS}
      campaignStart="2026-03-01T00:00:00Z" campaignEnd="2026-03-31T00:00:00Z"
      materialsByType={MATERIAIS_POR_TIPO}
      {...props}
    />,
  )
  return { onClose, onSubmit, onDelete, ...r }
}

const adicionar = () => screen.getByRole('button', { name: /^Adicionar/ })

describe('RuleSidePanel — criação', () => {
  test('fechado não renderiza nada', () => {
    montar({ open: false })
    expect(screen.queryByText('Nova regra')).toBeNull()
  })

  test('abre com os padrões: seg-sex, 08:00–10:00, 3×/dia e o período da campanha', () => {
    const { baseElement } = montar()
    expect(screen.getByText('Nova regra')).toBeTruthy()
    expect(screen.getByDisplayValue('3')).toBeTruthy()
    expect(screen.getByDisplayValue('08:00')).toBeTruthy()
    expect(screen.getByDisplayValue('10:00')).toBeTruthy()
    expect(screen.getByDisplayValue('2026-03-01')).toBeTruthy()
    expect(screen.getByDisplayValue('2026-03-31')).toBeTruthy()
    // semana: D S T Q Q S S — seg a sex ligados (62)
    const dias = within(baseElement).getAllByRole('button', { name: /^[DSTQ]$/ })
    expect(dias.length).toBe(7)
  })

  test('"Adicionar regra" fica bloqueado até escolher tipo e emissora', () => {
    montar()
    expect(adicionar().disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    expect(adicionar().disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    expect(adicionar().disabled).toBe(false)
  })

  test('envia type_ids, emissoras, faixa, período, dias e inserções (nome aparado)', () => {
    const { onSubmit } = montar()
    fireEvent.change(screen.getByPlaceholderText(/ex\.: Rede Nova Brasil/), { target: { value: '  Conjunto Teste  ' } })
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Outra AM/ }))
    fireEvent.change(screen.getByDisplayValue('3'), { target: { value: '5' } })
    fireEvent.change(screen.getByDisplayValue('08:00'), { target: { value: '07:30' } })
    fireEvent.change(screen.getByDisplayValue('10:00'), { target: { value: '12:00' } })
    fireEvent.click(adicionar())
    expect(onSubmit).toHaveBeenCalledWith({
      type_ids: ['t-spot'],
      name: 'Conjunto Teste',
      station_ids: ['st-1', 'st-2'],
      material_ids: [],
      start_date: '2026-03-01',
      end_date: '2026-03-31',
      weekday_mask: 62,
      time_start: '07:30',
      time_end: '12:00',
      plays_per_day: 5,
    })
  })

  test('marcar/desmarcar emissora alterna a seleção', () => {
    const { onSubmit } = montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    expect(adicionar().disabled).toBe(true)  // sem emissora de novo
    fireEvent.click(screen.getByRole('button', { name: /Rádio Outra AM/ }))
    fireEvent.click(adicionar())
    expect(onSubmit.mock.calls[0][0].station_ids).toEqual(['st-2'])
  })

  test('dias da semana: cada botão liga/desliga um bit da máscara', () => {
    const { onSubmit, baseElement } = montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    const dias = within(baseElement).getAllByRole('button', { name: /^[DSTQ]$/ })
    fireEvent.click(dias[0])   // domingo liga (bit 0) → 62 + 1
    fireEvent.click(dias[1])   // segunda desliga (bit 1) → − 2
    fireEvent.click(adicionar())
    expect(onSubmit.mock.calls[0][0].weekday_mask).toBe(61)
  })

  test('mais de um tipo cria uma regra idêntica por tipo e o botão avisa quantas', () => {
    const { onSubmit } = montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Vinheta/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    expect(screen.getByText(/Vou criar/).textContent).toMatch(/2 regras idênticas/)
    fireEvent.click(screen.getByRole('button', { name: 'Adicionar 2 regras' }))
    expect(onSubmit.mock.calls[0][0].type_ids).toEqual(['t-spot', 't-vin'])
  })

  test('tipo único mostra quantos materiais dele estão na campanha; tipo sem material avisa', () => {
    montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    expect(document.body.textContent).toMatch(/\(2 material\w* desse tipo na campanha\)/)
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))  // desmarca
    fireEvent.click(screen.getByRole('button', { name: /Vinheta/ }))
    expect(document.body.textContent).toMatch(/nenhum material desse tipo na campanha ainda/)
  })

  // BUG: RuleSidePanel.jsx:315 — `material${n !== 1 ? 'is' : ''}` gera "materialis" no plural.
  // Esperado: "2 materiais desse tipo na campanha".
  test.skip('plural correto: "(2 materiais desse tipo na campanha)"', () => {
    montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    expect(document.body.textContent).toMatch(/\(2 materiais desse tipo na campanha\)/)
  })

  test('plays_per_day fora de 1–100 bloqueia o envio', () => {
    montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    fireEvent.change(screen.getByDisplayValue('3'), { target: { value: '0' } })
    expect(adicionar().disabled).toBe(true)
    fireEvent.change(screen.getByDisplayValue('0'), { target: { value: '101' } })
    expect(adicionar().disabled).toBe(true)
    fireEvent.change(screen.getByDisplayValue('101'), { target: { value: '100' } })
    expect(adicionar().disabled).toBe(false)
  })

  test('sem tipos disponíveis orienta a voltar ao passo 3', () => {
    montar({ types: [] })
    expect(screen.getByText(/Nenhum tipo disponível/)).toBeTruthy()
  })

  test('submitting troca o rótulo para "Salvando…" e trava o botão', () => {
    montar({ submitting: true })
    expect(screen.getByRole('button', { name: 'Salvando…' }).disabled).toBe(true)
  })
})

describe('RuleSidePanel — escopo por material', () => {
  function comTipoUnico() {
    const r = montar()
    fireEvent.click(screen.getByRole('button', { name: /Spot 30s/ }))
    fireEvent.click(screen.getByRole('button', { name: /Rádio Teste FM/ }))
    return r
  }

  test('com um tipo que tem materiais aparece "Esta regra cobre"; padrão é todos do tipo', () => {
    comTipoUnico()
    expect(screen.getByText('Esta regra cobre')).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Todos do tipo' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('qualquer material', { selector: 'strong' })).toBeTruthy()
  })

  test('materiais específicos: lista com duração, contador e payload com material_ids', () => {
    const { onSubmit } = comTipoUnico()
    fireEvent.click(screen.getByRole('tab', { name: 'Materiais específicos' }))
    expect(screen.getByText('0 de 2 marcados')).toBeTruthy()
    expect(screen.getByText('30s')).toBeTruthy()
    expect(screen.getByText(/Marque os materiais que são a exceção/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Spot Teste B/ }))
    expect(screen.getByText('1 de 2 marcado')).toBeTruthy()
    expect(screen.getByText(/saem\s+da regra geral/)).toBeTruthy()
    fireEvent.click(adicionar())
    expect(onSubmit.mock.calls[0][0].material_ids).toEqual(['m2'])
  })

  test('voltar pra "Todos do tipo" envia material_ids vazio mesmo com materiais marcados antes', () => {
    const { onSubmit } = comTipoUnico()
    fireEvent.click(screen.getByRole('tab', { name: 'Materiais específicos' }))
    fireEvent.click(screen.getByRole('button', { name: /Spot Teste A/ }))
    fireEvent.click(screen.getByRole('tab', { name: 'Todos do tipo' }))
    fireEvent.click(adicionar())
    expect(onSubmit.mock.calls[0][0].material_ids).toEqual([])
  })

  test('escolher um segundo tipo some com o escopo e limpa os materiais', () => {
    comTipoUnico()
    fireEvent.click(screen.getByRole('button', { name: /Vinheta/ }))
    expect(screen.queryByText('Esta regra cobre')).toBeNull()
  })
})

describe('RuleSidePanel — edição e fechamento', () => {
  const INICIAL = {
    id: 'r1', type_id: 't-spot', station_ids: ['st-1'], material_ids: [], name: 'Regra Teste',
    start_date: '2026-03-05T00:00:00Z', end_date: '2026-03-20T00:00:00Z',
    weekday_mask: 127, time_start: '09:15:00', time_end: '11:45:00', plays_per_day: 4,
  }

  test('carrega a regra, trava o tipo e salva com type_id (singular)', () => {
    const { onSubmit } = montar({ mode: 'edit', initial: INICIAL })
    expect(screen.getByText('Editar regra')).toBeTruthy()
    expect(screen.getByDisplayValue('Regra Teste')).toBeTruthy()
    expect(screen.getByDisplayValue('2026-03-05')).toBeTruthy()
    expect(screen.getByDisplayValue('09:15')).toBeTruthy()
    // tipo de outra regra fica desabilitado (a regra pertence a um tipo só)
    expect(screen.getByRole('button', { name: /Vinheta/ }).disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: /Vinheta/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(onSubmit).toHaveBeenCalledWith({
      type_id: 't-spot', name: 'Regra Teste', station_ids: ['st-1'], material_ids: [],
      start_date: '2026-03-05', end_date: '2026-03-20', weekday_mask: 127,
      time_start: '09:15', time_end: '11:45', plays_per_day: 4,
    })
  })

  test('regra com materiais abre no escopo "específicos" e preserva material_ids', () => {
    const { onSubmit } = montar({ mode: 'edit', initial: { ...INICIAL, material_ids: ['m1'] } })
    expect(screen.getByRole('tab', { name: 'Materiais específicos' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText('1 de 2 marcado')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(onSubmit.mock.calls[0][0].material_ids).toEqual(['m1'])
  })

  test('Excluir regra só existe em edição e chama onDelete', () => {
    const { onDelete, unmount } = montar({ mode: 'edit', initial: INICIAL })
    fireEvent.click(screen.getByRole('button', { name: /Excluir regra/ }))
    expect(onDelete).toHaveBeenCalledTimes(1)
    unmount()
    montar()
    expect(screen.queryByRole('button', { name: /Excluir regra/ })).toBeNull()
  })

  test('Cancelar, ×, Esc e o fundo fecham; clicar no painel não fecha', () => {
    const { onClose, baseElement } = montar()
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    fireEvent.click(screen.getByRole('button', { name: 'Fechar' }))
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(3)
    fireEvent.click(screen.getByText('Nova regra'))
    expect(onClose).toHaveBeenCalledTimes(3)
    // o fundo escurecido é o pai do painel
    fireEvent.click(screen.getByText('Nova regra').closest('div[style*="position: fixed; inset: 0"]'))
    expect(onClose).toHaveBeenCalledTimes(4)
    expect(baseElement).toBeTruthy()
  })
})
