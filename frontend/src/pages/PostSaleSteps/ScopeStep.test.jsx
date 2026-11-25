import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import ScopeStep from './ScopeStep'
import ContentStep from './ContentStep'
import { Controlado, escolherOpcao } from '../../test-utils/area-componentes'
import { stationDial } from '../../components/postsale/motion'

const CLIENTES = [{ id: 'c1', name: 'Cliente Teste' }, { id: 'c2', name: 'Outro Cliente' }]
const CAMPANHAS = [
  { id: 'camp-1', name: 'Campanha Teste A', status: 'ativa', start_date: '2026-03-01T00:00:00Z', end_date: '2026-03-31T00:00:00Z' },
  { id: 'camp-2', name: 'Campanha Teste B', status: 'cancelada', start_date: '2026-04-01', end_date: '2026-04-30' },
  { id: 'camp-3', name: 'Campanha Teste C', status: 'estranho', start_date: null, end_date: null },
]

function montar(props = {}) {
  const onBlocksChange = vi.fn(), onClientChange = vi.fn()
  const r = render(
    <ScopeStep
      clients={CLIENTES} client={{ id: 'c1', name: 'Cliente Teste' }} onClientChange={onClientChange}
      campaigns={CAMPANHAS} campaignsLoading={false} blocks={[]} onBlocksChange={onBlocksChange}
      {...props}
    />,
  )
  return { onBlocksChange, onClientChange, ...r }
}

const bloco = (id, extra = {}) => ({
  campaign_id: id, campaign_name: `Campanha ${id}`, period_from: '2026-03-01', period_to: '2026-03-31',
  checking_text: '', checking_rows: null, checking_edited: false, kpi_overrides: {}, ...extra,
})

describe('ScopeStep', () => {
  test('sem cliente escolhido pede pra escolher e não mostra campanhas', () => {
    montar({ client: null })
    expect(screen.getByText('Escolha o cliente para continuar')).toBeTruthy()
    expect(screen.queryByText('Campanhas e período')).toBeNull()
  })

  test('escolher outro cliente avisa o pai com o id (e limpar manda null)', () => {
    const { onClientChange } = montar()
    escolherOpcao(screen.getByRole('combobox'), 'Outro Cliente')
    expect(onClientChange).toHaveBeenCalledWith('c2')
  })

  test('mostra as campanhas do cliente com status, datas dd/mm/aaaa e traço quando faltam', () => {
    montar()
    expect(screen.getByText('Campanhas e período')).toBeTruthy()
    const a = screen.getByLabelText('Incluir a campanha Campanha Teste A').closest('label')
    expect(a.textContent).toContain('Ativa')
    expect(a.textContent).toContain('01/03/2026 — 31/03/2026')
    expect(screen.getByLabelText('Incluir a campanha Campanha Teste B').closest('label').textContent).toContain('Cancelada')
    // status desconhecido aparece como veio; sem datas, traços
    const c = screen.getByLabelText('Incluir a campanha Campanha Teste C').closest('label')
    expect(c.textContent).toContain('estranho')
    expect(c.textContent).toContain('— — —')
    // cancelada ganha estilo próprio de etiqueta
    expect(within(screen.getByLabelText('Incluir a campanha Campanha Teste B').closest('label')).getByText('Cancelada').className).toContain('pv-tag--cancelada')
  })

  test('marcar uma campanha cria o bloco com o período da própria campanha', () => {
    const { onBlocksChange } = montar()
    fireEvent.click(screen.getByLabelText('Incluir a campanha Campanha Teste A'))
    expect(onBlocksChange).toHaveBeenCalledWith([{
      campaign_id: 'camp-1', campaign_name: 'Campanha Teste A',
      period_from: '2026-03-01', period_to: '2026-03-31',
      checking_text: '', checking_rows: null, checking_edited: false, kpi_overrides: {},
    }])
  })

  test('desmarcar tira só aquele bloco', () => {
    const { onBlocksChange } = montar({ blocks: [bloco('camp-1'), bloco('camp-2')] })
    fireEvent.click(screen.getByLabelText('Incluir a campanha Campanha Teste A'))
    expect(onBlocksChange).toHaveBeenCalledWith([bloco('camp-2')])
  })

  test('campanha marcada mostra início e fim limitados ao período dela; editar muda só o bloco certo', () => {
    const onBlocksChange = vi.fn()
    const { container } = render(
      <Controlado Comp={ScopeStep} propValor="blocks" propMudou="onBlocksChange"
        inicial={[bloco('camp-1', { campaign_name: 'Campanha Teste A' }), bloco('camp-2')]}
        onMudou={onBlocksChange}
        extra={{ clients: CLIENTES, client: CLIENTES[0], onClientChange: () => {}, campaigns: CAMPANHAS, campaignsLoading: false }} />,
    )
    const [inicio, fim] = container.querySelectorAll('li:first-child input[type="date"]')
    expect(inicio.getAttribute('min')).toBe('2026-03-01')
    expect(inicio.getAttribute('max')).toBe('2026-03-31')
    fireEvent.change(inicio, { target: { value: '2026-03-10' } })
    const ultimo = onBlocksChange.mock.calls[onBlocksChange.mock.calls.length - 1][0]
    expect(ultimo[0].period_from).toBe('2026-03-10')
    expect(ultimo[1].period_from).toBe('2026-03-01')   // a outra campanha não mexe
    fireEvent.change(fim, { target: { value: '2026-03-20' } })
    expect(onBlocksChange.mock.calls[onBlocksChange.mock.calls.length - 1][0][0]).toMatchObject({ period_from: '2026-03-10', period_to: '2026-03-20' })
  })

  test('período invertido mostra o erro', () => {
    montar({ blocks: [bloco('camp-1', { period_from: '2026-03-20', period_to: '2026-03-05' })] })
    expect(screen.getByText('A data final precisa ser igual ou posterior à inicial.')).toBeTruthy()
  })

  test('com mais de um bloco dá pra subir e descer; no topo/fim não faz nada', () => {
    const { onBlocksChange } = montar({ blocks: [bloco('camp-1'), bloco('camp-2')] })
    const subir = screen.getAllByTitle('Subir no documento')
    const descer = screen.getAllByTitle('Descer no documento')
    fireEvent.click(subir[0])      // o primeiro não sobe
    expect(onBlocksChange).not.toHaveBeenCalled()
    fireEvent.click(descer[0])
    expect(onBlocksChange.mock.calls[0][0].map(b => b.campaign_id)).toEqual(['camp-2', 'camp-1'])
    fireEvent.click(descer[1])     // o último não desce
    expect(onBlocksChange).toHaveBeenCalledTimes(1)
  })

  test('com um bloco só não há botões de ordem', () => {
    montar({ blocks: [bloco('camp-1')] })
    expect(screen.queryByTitle('Subir no documento')).toBeNull()
  })

  test('carregando mostra o esqueleto; sem campanhas avisa o nome do cliente', () => {
    const { container, rerender } = montar({ campaignsLoading: true })
    expect(container.querySelectorAll('.pv-sk-line').length).toBe(3)
    rerender(
      <ScopeStep clients={CLIENTES} client={{ id: 'c1', name: 'Cliente Teste' }} onClientChange={() => {}}
        campaigns={[]} campaignsLoading={false} blocks={[]} onBlocksChange={() => {}} />,
    )
    expect(screen.getByText('Cliente Teste ainda não tem campanha cadastrada.')).toBeTruthy()
  })
})

describe('stationDial', () => {
  test('cidade/UF e dial; só banda quando não há frequência; vazio sem nada', () => {
    expect(stationDial({ city: 'Cidade Teste', state: 'XX', frequency_mhz: 99.5, band: 'FM' })).toBe('Cidade Teste/XX · 99,5 FM')
    expect(stationDial({ city: 'Cidade Teste', band: 'AM' })).toBe('Cidade Teste · AM')
    expect(stationDial({})).toBe('')
  })
})

// ───────────────────────────── ContentStep ─────────────────────────────

const KPIS = { valor_entregue: 1000, impactos: 50000, bonificacao: 200, bonificacao_count: 4, consolidated: false }
const LINHA_ACIMA = {
  station_id: 'st-1', name: 'Rádio Teste FM', kind: 'above', delivery_pct: 120, bonus_count: 3,
  city: 'Cidade Teste', state: 'XX', frequency_mhz: 99.5, band: 'FM',
}
const LINHA_COMP = { station_id: 'st-2', name: 'Rádio Outra AM', kind: 'compensation', delivery_pct: 80, note: '', band: 'AM' }
const PREVIEW = {
  campaigns: [{
    campaign_id: 'camp-1', name: 'Campanha Teste A', period_label: '01/03 a 31/03', kpis: KPIS,
    checking_text: 'Texto sugerido do sistema', checking_rows: [LINHA_ACIMA, LINHA_COMP], conforming_count: 5,
  }],
}

function montarConteudo(props = {}) {
  const onMeta = vi.fn(), onBlockChange = vi.fn()
  const r = render(
    <ContentStep
      title="Título Teste" introMessage="Mensagem Teste" attachmentsUrl=""
      blocks={[bloco('camp-1', { campaign_name: 'Campanha Teste A' })]}
      preview={PREVIEW} onMeta={onMeta} onBlockChange={onBlockChange}
      {...props}
    />,
  )
  return { onMeta, onBlockChange, ...r }
}

describe('ContentStep — abertura', () => {
  test('título, mensagem e link mandam só o campo alterado pro onMeta', () => {
    const { onMeta } = montarConteudo()
    fireEvent.change(screen.getByDisplayValue('Título Teste'), { target: { value: 'Novo título' } })
    expect(onMeta).toHaveBeenLastCalledWith({ title: 'Novo título' })
    fireEvent.change(screen.getByDisplayValue('Mensagem Teste'), { target: { value: 'Nova mensagem' } })
    expect(onMeta).toHaveBeenLastCalledWith({ intro_message: 'Nova mensagem' })
    fireEvent.change(screen.getByPlaceholderText(/drive.google.com/), { target: { value: 'https://example.com/pasta' } })
    expect(onMeta).toHaveBeenLastCalledWith({ attachments_url: 'https://example.com/pasta' })
  })

  test('link inválido avisa que o botão não vai aparecer; link http(s) explica o que acontece', () => {
    const { rerender } = montarConteudo({ attachmentsUrl: 'meu drive' })
    expect(screen.getByText(/Isso não é um endereço navegável/)).toBeTruthy()
    rerender(
      <ContentStep title="t" introMessage="m" attachmentsUrl="https://example.com/pasta"
        blocks={[]} preview={PREVIEW} onMeta={() => {}} onBlockChange={() => {}} />,
    )
    expect(screen.queryByText(/Isso não é um endereço navegável/)).toBeNull()
    expect(screen.getByText(/abrindo em aba nova. Confira se a pasta está compartilhada/)).toBeTruthy()
    rerender(
      <ContentStep title="t" introMessage="m" attachmentsUrl="ftp://example.com"
        blocks={[]} preview={PREVIEW} onMeta={() => {}} onBlockChange={() => {}} />,
    )
    expect(screen.getByText(/Isso não é um endereço navegável/)).toBeTruthy()
  })
})

describe('ContentStep — painel da campanha', () => {
  test('o primeiro bloco abre sozinho com os números do sistema e o CPM derivado', () => {
    montarConteudo()
    expect(screen.getByRole('button', { name: /Campanha Teste A/ }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByDisplayValue('1000')).toBeTruthy()
    expect(screen.getByDisplayValue('50000')).toBeTruthy()
    expect(screen.getByDisplayValue('200')).toBeTruthy()
    // (1000 + 200) / 50000 × 1000 = R$ 24,00
    expect(screen.getByText(/R\$\s*24,00/)).toBeTruthy()
    expect(screen.getAllByText('do sistema').length).toBe(3)
    expect(screen.getByText('01/03 a 31/03', { exact: false })).toBeTruthy()
  })

  test('clicar no cabeçalho recolhe e reabre', () => {
    montarConteudo()
    const cab = screen.getByRole('button', { name: /Campanha Teste A/ })
    fireEvent.click(cab)
    expect(cab.getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByText('Valor entregue')).toBeNull()
    fireEvent.click(cab)
    expect(screen.getByText('Valor entregue')).toBeTruthy()
  })

  test('sem o preview da campanha mostra "Calculando…"', () => {
    montarConteudo({ preview: { campaigns: [] } })
    expect(screen.getByText(/Calculando os números desta campanha/)).toBeTruthy()
  })

  test('editar o valor entregue grava o override', () => {
    const { onBlockChange } = montarConteudo()
    fireEvent.change(screen.getByDisplayValue('1000'), { target: { value: '1500' } })
    expect(onBlockChange).toHaveBeenLastCalledWith(expect.objectContaining({
      campaign_id: 'camp-1', kpi_overrides: { valor_entregue: 1500 },
    }))
  })

  test('voltar ao valor do sistema apaga o override em vez de gravar um igual', () => {
    const { onBlockChange } = montarConteudo({
      blocks: [bloco('camp-1', { campaign_name: 'Campanha Teste A', kpi_overrides: { valor_entregue: 1500 } })],
    })
    fireEvent.change(screen.getByDisplayValue('1500'), { target: { value: '1000' } })
    expect(onBlockChange).toHaveBeenLastCalledWith(expect.objectContaining({ kpi_overrides: {} }))
  })

  test('campo com override mostra o valor do sistema e "usar do sistema" restaura', () => {
    const { onBlockChange } = montarConteudo({
      blocks: [bloco('camp-1', { campaign_name: 'Campanha Teste A', kpi_overrides: { impactos: 60000 } })],
    })
    expect(screen.getByText('ajustado à mão')).toBeTruthy()
    expect(screen.getByText('sistema: 50.000')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'usar do sistema' }))
    expect(onBlockChange).toHaveBeenLastCalledWith(expect.objectContaining({ kpi_overrides: {} }))
  })

  test('o campo de bonificação só existe quando o card existe no documento', () => {
    const { unmount } = montarConteudo()
    expect(screen.getByText('Valor bonificado')).toBeTruthy()
    unmount()
    montarConteudo({ preview: { campaigns: [{ ...PREVIEW.campaigns[0], kpis: { ...KPIS, consolidated: true, bonificacao: 0, bonificacao_count: 0 } }] } })
    expect(screen.queryByText('Valor bonificado')).toBeNull()
  })

  test('campanha sem veiculação no período avisa e mostra a etiqueta', () => {
    montarConteudo({ preview: { campaigns: [{ ...PREVIEW.campaigns[0], kpis: { ...KPIS, impactos: 0, valor_entregue: 0 } }] } })
    expect(screen.getByText('sem veiculação no período')).toBeTruthy()
    expect(screen.getByText(/não tem veiculação registrada no período escolhido/)).toBeTruthy()
  })

  test('texto do Checking: placeholder com o sugerido e edição que vai pro bloco', () => {
    const { onBlockChange } = montarConteudo()
    const campo = screen.getByPlaceholderText('Texto sugerido do sistema')
    fireEvent.change(campo, { target: { value: 'Meu texto' } })
    expect(onBlockChange).toHaveBeenLastCalledWith(expect.objectContaining({ checking_text: 'Meu texto' }))
  })

  test('linhas do checking: grupos "Acima" e "Compensações", resto conforme o planejado', () => {
    montarConteudo()
    expect(screen.getByText('Acima do contratado').querySelector('.pv-group-count').textContent).toBe('1')
    expect(screen.getByText('Compensações').querySelector('.pv-group-count').textContent).toBe('1')
    expect(screen.getByText('Rádio Teste FM')).toBeTruthy()
    expect(screen.getByText('Cidade Teste/XX · 99,5 FM')).toBeTruthy()
    expect(screen.getByText(/As outras 5 emissoras entregaram conforme o planejado/)).toBeTruthy()
  })

  test('editar a entrega % de uma linha marca o checking como editado e preserva as outras', () => {
    const { onBlockChange } = montarConteudo()
    fireEvent.change(screen.getByDisplayValue('120'), { target: { value: '130' } })
    const bloco1 = onBlockChange.mock.calls[0][0]
    expect(bloco1.checking_edited).toBe(true)
    expect(bloco1.checking_rows.map(r => [r.station_id, r.delivery_pct])).toEqual([['st-1', 130], ['st-2', 80]])
  })

  test('linha "acima" edita inserções bônus; linha de compensação edita a observação', () => {
    const { onBlockChange } = montarConteudo()
    fireEvent.change(screen.getByDisplayValue('3'), { target: { value: '7' } })
    expect(onBlockChange.mock.calls[0][0].checking_rows[0].bonus_count).toBe(7)
    fireEvent.change(screen.getByPlaceholderText('ex.: compensação em 05/08'), { target: { value: 'compensa em 05/08' } })
    expect(onBlockChange.mock.calls[1][0].checking_rows[1].note).toBe('compensa em 05/08')
  })

  test('remover uma linha tira da lista e aumenta o "entregaram conforme" (visto no pai)', () => {
    const { onBlockChange } = montarConteudo()
    fireEvent.click(screen.getByTitle('Remover Rádio Teste FM da lista'))
    const novo = onBlockChange.mock.calls[0][0]
    expect(novo.checking_edited).toBe(true)
    expect(novo.checking_rows.map(r => r.station_id)).toEqual(['st-2'])
  })

  test('com checking já editado usa as linhas do bloco e o contador de "conforme" soma o que foi removido', () => {
    montarConteudo({
      blocks: [bloco('camp-1', { campaign_name: 'Campanha Teste A', checking_edited: true, checking_rows: [LINHA_COMP] })],
    })
    expect(screen.queryByText('Rádio Teste FM')).toBeNull()
    expect(screen.getByText(/As outras 6 emissoras entregaram/)).toBeTruthy()   // 2 + 5 − 1 linha restante
  })

  test('todas as emissoras listadas: mensagem própria', () => {
    montarConteudo({ preview: { campaigns: [{ ...PREVIEW.campaigns[0], conforming_count: 0 }] } })
    expect(screen.getByText(/Todas as emissoras do período estão listadas acima/)).toBeTruthy()
  })
})
