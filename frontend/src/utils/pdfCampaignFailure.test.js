// pdfCampaignFailure.js — "Relatório de Cobrança" (falhas por emissora). jsPDF e
// autotable são dublês (test-utils/area-api-pdf.js): conferimos o que o gerador
// decide — KPIs, linhas da tabela, cobertura, déficit dividido em "não tocou" ×
// "fora do horário", legenda, rodapé, nome do arquivo.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { estadoPdf, reiniciarPdfFalso } from '../test-utils/area-api-pdf'

vi.mock('jspdf', async () => {
  const { JsPdfFalso } = await import('../test-utils/area-api-pdf')
  return { jsPDF: JsPdfFalso }
})
vi.mock('jspdf-autotable', async () => {
  const { autoTableFalso } = await import('../test-utils/area-api-pdf')
  return { default: autoTableFalso }
})

const VERMELHO = [220, 38, 38]
const AMBAR = [180, 83, 9]
const ROXO = [168, 85, 247]
const VERDE = [22, 163, 74]

async function carregar() {
  vi.resetModules() // cache da logo vive no módulo
  return await import('./pdfCampaignFailure.js')
}
const doc = () => estadoPdf.docs.at(-1)
const tabela = () => estadoPdf.tabelas[0]
const celula = (linha, coluna) => tabela().celulas.find(c => c.linha === linha && c.coluna === coluna).cell

beforeEach(() => {
  reiniciarPdfFalso()
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['png'], { type: 'image/png' }) })))
  // 10/03/2026 12:00 em São Paulo
  vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function payload(extra = {}) {
  return {
    campaign: { name: 'Campanha Teste Verão', client_name: 'Cliente Teste', start_date: '2026-03-01', end_date: '2026-03-31' },
    summary: {
      stations_with_failure: 3, total_failure_days: 7, total_deficit: 12,
      total_deficit_absent: 8, total_deficit_off_slot: 4,
    },
    stations: [
      // déficit dividido (3 não tocou + 2 fora do horário), parte do mês
      { station: { name: 'Rádio Teste FM', city: 'Cidade Teste' }, programmed: 10, identified: 8, extras: 1, deficit: 5,
        deficit_absent: 3, deficit_off_slot: 2, is_bonified: false, failure_days: ['2026-03-24', '2026-03-26', '2026-03-29'] },
      // bonificada: não cobra nada
      { station: { name: 'Rádio Teste AM' }, programmed: 4, identified: 6, extras: 2, deficit: 0,
        deficit_absent: 0, deficit_off_slot: 0, is_bonified: true, failure_days: [] },
      // só "não tocou", dias em dois meses, nada programado
      { station: { name: 'Rádio Teste Três', city: '' }, programmed: 0, identified: 0, extras: 0, deficit: 4,
        deficit_absent: 4, deficit_off_slot: 0, is_bonified: false, failure_days: ['2026-03-30', '2026-04-02'] },
    ],
    ...extra,
  }
}

describe('cabeçalho e KPIs', () => {
  test('A4 em pontos; título, cliente e vigência em dd/mm/aaaa', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(doc().opcoes).toEqual({ unit: 'pt', format: 'a4' })
    const t = doc().textos()
    expect(t).toContain('Relatório de Cobrança')
    expect(t).toContain('Cliente Teste')
    expect(t).toContain('Campanha Teste Verão · 01/03/2026 a 31/03/2026')
  })

  test('KPIs: emissoras, dias e déficit total, com o rótulo em caixa alta', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const t = doc().textos()
    expect(t).toEqual(expect.arrayContaining([
      '3', 'EMISSORAS COM FALHA', '7', 'DIAS COM FALHA', '12', 'DÉFICIT TOTAL',
    ]))
  })

  test('déficit total impresso dividido, com o "+" explícito entre as parcelas', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const t = doc().textos()
    expect(t).toEqual(expect.arrayContaining(['8', '  não tocou', '+ ', '4', '  fora do horário']))
  })

  test('backend antigo (sem os campos de divisão): sem parcelas, sem legenda', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({
      summary: { stations_with_failure: 1, total_failure_days: 2, total_deficit: 5 },
      stations: [{ station: { name: 'Rádio Teste FM' }, programmed: 10, identified: 5, extras: 0, deficit: 5, failure_days: ['2026-03-05'] }],
    }))
    const t = doc().textos()
    expect(t).not.toContain('  não tocou')
    expect(t).not.toContain('COMO LER ESTE RELATÓRIO')
    expect(t).toContain('5')
  })

  test('divisão informada só pelos nomes total_* do resumo também vale', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({
      summary: { total_deficit: 6, total_deficit_off_slot: 6 }, // absent ausente → 0
      stations: [],
    }))
    const t = doc().textos()
    expect(t).toEqual(expect.arrayContaining(['0', '  não tocou', '6', '  fora do horário']))
  })

  test('payload mínimo: tudo "—" e zeros, sem quebrar', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf({ campaign: {}, summary: undefined })
    const t = doc().textos()
    expect(t).toContain('— · — a —')
    expect(t).toContain('—')
    expect(t.filter(x => x === '0')).toHaveLength(3)
    expect(tabela().opcoes.body).toEqual([])
    expect(doc().arquivoSalvo).toBe('cobranca-campanha-20260310.pdf')
  })

  test('com a logo: addImage; sem ela: marca textual', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(doc().de('addImage')[0].args.slice(1)).toEqual(['PNG', 36, 22, 90, 28])
    expect(doc().textos()).not.toContain('E-monitor')

    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false })))
    const mod2 = await carregar()
    await mod2.generateCampaignFailurePdf(payload())
    expect(doc().de('addImage')).toHaveLength(0)
    expect(doc().textosComPosicao()).toContainEqual(expect.objectContaining({ texto: 'E-monitor', x: 36, y: 42 }))
  })

  test('fetch que lança vira fallback textual', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('offline') }))
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(doc().textos()).toContain('E-monitor')
  })

  test('logo baixada uma vez só entre dois PDFs', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    await generateCampaignFailurePdf(payload())
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})

describe('tabela de emissoras', () => {
  test('cabeçalho e linhas: cobertura em %, dias com falha e total a cobrar', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const t = tabela().opcoes
    expect(t.head).toEqual([['Emissora', 'Programado', 'Veiculou', 'Dias com falha', 'Déficit a cobrar']])
    expect(t.body).toEqual([
      ['Rádio Teste FM\nCidade Teste', '10', '8  80%', '24, 26, 29', 'Cobrar (5)'],
      ['Rádio Teste AM', '4', '6  150%', '—', 'Bonificada · +2'],
      ['Rádio Teste Três', '0', '0  0%', '30/03, 02/04', 'Cobrar (4)'],
    ])
  })

  test('emissora sem déficit nem bônus mostra "—" e sem campos numéricos vira zero', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({ stations: [{ station: { name: 'Rádio Teste FM' } }] }))
    expect(tabela().opcoes.body).toEqual([['Rádio Teste FM', '0', '0  0%', '—', '—']])
  })

  test('identificadas grandes usam separador de milhar pt-BR', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({
      stations: [{ station: { name: 'Rádio Teste FM' }, programmed: 2000, identified: 1500, deficit: 500, deficit_absent: 500, deficit_off_slot: 0 }],
    }))
    expect(tabela().opcoes.body[0][2]).toBe('1.500  75%')
  })

  test('coluna do déficit: cor por tipo e altura reservada só quando há divisão', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    // linha 0: cobrar com divisão → vermelho, texto zerado e 48pt de altura
    expect(celula(0, 4).styles.textColor).toEqual(VERMELHO)
    expect(celula(0, 4).text).toEqual([''])
    expect(celula(0, 4).styles.minCellHeight).toBe(48)
    // linha 1: bonificada → roxo, texto mantido
    expect(celula(1, 4).styles.textColor).toEqual(ROXO)
    expect(celula(1, 4).text).toEqual(['Bonificada · +2'])
    expect(celula(1, 4).styles.minCellHeight).toBeUndefined()
  })

  test('sem divisão no backend: coluna do déficit continua em texto simples', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({
      stations: [{ station: { name: 'Rádio Teste FM' }, programmed: 10, identified: 5, deficit: 5, failure_days: [] }],
    }))
    expect(celula(0, 4).text).toEqual(['Cobrar (5)'])
    expect(celula(0, 4).styles.textColor).toEqual(VERMELHO)
    expect(celula(0, 4).styles.minCellHeight).toBeUndefined()
  })

  test('coluna "Veiculou" em negrito; cabeçalho é ignorado pelos hooks', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(celula(0, 2).styles.fontStyle).toBe('bold')
    expect(celula(0, 1).styles.fontStyle).toBeUndefined()
  })
})

describe('desenho manual das células', () => {
  // o dublê põe a célula (linha i, coluna j) em x = 15 + 20j, y = 100 + 60i, largura 20
  const naCelula = (linha, coluna) => doc().textosComPosicao()
    .filter(x => x.x >= 15 + 20 * coluna && x.x < 35 + 20 * coluna && x.y >= 100 + 60 * linha && x.y < 160 + 60 * linha)

  test('célula com divisão imprime o total e as duas parcelas, cada uma na sua cor', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const textos = naCelula(0, 4).map(x => x.texto)
    expect(textos).toEqual(['Cobrar (5)', '3 ', 'não tocou', '2 ', 'fora do horário'])
    expect(naCelula(0, 4)[0]).toMatchObject({ x: 101, y: 117 }) // cell.x + 6, cell.y + 17
    // as cores usadas: vermelho pro "não tocou" e âmbar pro "fora do horário"
    const chamadas = doc().chamadas
    const corAntes = (texto) => {
      const i = chamadas.findIndex(c => c.nome === 'text' && c.args[0] === texto && c.args[1] >= 95 && c.args[2] < 160)
      return chamadas.slice(0, i).reverse().find(c => c.nome === 'setTextColor').args
    }
    expect(corAntes('não tocou')).toEqual(VERMELHO)
    expect(corAntes('fora do horário')).toEqual(AMBAR)
  })

  test('parcela zerada não é impressa (só "não tocou" na linha 2)', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const textos = naCelula(2, 4).map(x => x.texto)
    expect(textos).toEqual(['Cobrar (4)', '4 ', 'não tocou'])
  })

  test('linha bonificada não desenha parcelas à mão', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(naCelula(1, 4)).toEqual([])
  })

  test('barra de cobertura: trilho + trecho verde + extras roxos, proporcionais ao maior entre programado e entregue+déficit', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    // linha 0, coluna 2: x = 15 + 40 + 4 = 59, y = 100 + 26 = 126, w = 20 − 8 = 12
    // denominador = max(programado 10, entregue 8 + déficit 5) = 13
    const barras = doc().de('roundedRect').filter(c => c.args[1] === 126 && c.args[3] === 3)
    expect(barras).toHaveLength(2)
    expect(barras[0].args[0]).toBe(59)
    expect(barras[0].args[2]).toBeCloseTo(12) // trilho
    expect(barras[1].args[2]).toBeCloseTo((8 / 13) * 12) // trecho entregue
    const extras = doc().de('rect').find(c => c.args[1] === 126 && c.args[3] === 3)
    expect(extras.args[0]).toBeCloseTo(59 + (8 / 13) * 12) // começa onde o verde acaba
    expect(extras.args[2]).toBeCloseTo((1 / 13) * 12)
    const chamadas = doc().chamadas
    const iOk = chamadas.indexOf(barras[1])
    expect(chamadas.slice(0, iOk).reverse().find(c => c.nome === 'setFillColor').args).toEqual(VERDE)
    const iExtras = chamadas.indexOf(extras)
    expect(chamadas.slice(0, iExtras).reverse().find(c => c.nome === 'setFillColor').args).toEqual(ROXO)
  })

  test('bonificada: barra inteira roxa e sem trecho de extras separado', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    // linha 1: y = 160 + 26 = 186; denominador = max(4, 6 + 0) = 6 → trecho entregue cheio
    const barras = doc().de('roundedRect').filter(c => c.args[1] === 186 && c.args[3] === 3)
    expect(barras).toHaveLength(2)
    expect(barras[1].args[2]).toBeCloseTo(12)
    const i = doc().chamadas.indexOf(barras[1])
    expect(doc().chamadas.slice(0, i).reverse().find(c => c.nome === 'setFillColor').args).toEqual(ROXO)
    expect(doc().de('rect').some(c => c.args[1] === 186 && c.args[3] === 3)).toBe(false)
  })

  test('nada programado: sem barra de cobertura', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    // linha 2: y = 220 + 26 = 246
    expect(doc().de('roundedRect').some(c => c.args[1] === 246 && c.args[3] === 3)).toBe(false)
  })
})

describe('legenda, rodapé e arquivo', () => {
  test('legenda dos dois tipos de déficit logo abaixo da tabela', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload()) // finalY 120 → ly = 146
    const t = doc().textosComPosicao()
    expect(t.find(x => x.texto === 'COMO LER ESTE RELATÓRIO')).toMatchObject({ x: 36, y: 146, pagina: 1 })
    const textos = t.map(x => x.texto)
    expect(textos).toEqual(expect.arrayContaining([
      'Não tocou', ' — nenhuma veiculação do material foi identificada no dia contratado.',
      'Fora do horário', 'fora do horário', '  =  déficit total de cada emissora.',
    ]))
    // linha separadora acima do título
    expect(doc().de('line')[0].args).toEqual([36, 132, 595.28 - 36, 132])
  })

  test('tabela alta demais empurra a legenda pra uma página nova', async () => {
    reiniciarPdfFalso({ finalY: 760 }) // 786 + 62 > 841,89 − 50
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(doc().de('addPage')).toHaveLength(1)
    expect(doc().textosComPosicao().find(x => x.texto === 'COMO LER ESTE RELATÓRIO')).toMatchObject({ y: 60, pagina: 2 })
  })

  test('rodapé em cada página: data e hora locais + "Página i de N"', async () => {
    reiniciarPdfFalso({ finalY: 760 })
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    const rodape = doc().textosComPosicao().filter(x => /^(Gerado por|Página)/.test(String(x.texto)))
    expect(rodape.map(x => x.texto)).toEqual([
      'Gerado por E-monitor · 10/03/2026 12:00', 'Página 1 de 2',
      'Gerado por E-monitor · 10/03/2026 12:00', 'Página 2 de 2',
    ])
    expect(rodape.map(x => x.pagina)).toEqual([1, 1, 2, 2])
  })

  test('arquivo nomeado cobranca-<slug>-<data>', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({ campaign: { name: 'Promoção Ação & Café', client_name: 'Cliente Teste' } }))
    expect(doc().arquivoSalvo).toBe('cobranca-promocao-acao-cafe-20260310.pdf')
  })

  test('a data do arquivo e do rodapé é a LOCAL (22h30 de 10/03 em São Paulo continua 10/03)', async () => {
    vi.setSystemTime(new Date('2026-03-11T01:30:00Z'))
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload())
    expect(doc().arquivoSalvo).toBe('cobranca-campanha-teste-verao-20260310.pdf')
    expect(doc().textos()).toContain('Gerado por E-monitor · 10/03/2026 22:30')
  })

  test('nome da campanha enorme é cortado em 60 caracteres', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf(payload({ campaign: { name: 'a'.repeat(100), client_name: 'Cliente Teste' } }))
    expect(doc().arquivoSalvo).toBe(`cobranca-${'a'.repeat(60)}-20260310.pdf`)
  })
})
