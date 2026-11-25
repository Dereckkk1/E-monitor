// pdfReport.js — os três PDFs do sistema (relatório de campanha, grade
// programado×tocado, emissoras contratadas). jsPDF e autotable são trocados por
// dublês (test-utils/area-api-pdf.js) que registram cada chamada: o teste confere o
// que o gerador DECIDE — textos, linhas/colunas das tabelas, totais, cores por
// status, quebra de página, rodapé e nome do arquivo — não pixels.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { estadoPdf, reiniciarPdfFalso } from '../test-utils/area-api-pdf'
import { buildGridReportModel } from './gridReport.js'

vi.mock('jspdf', async () => {
  const { JsPdfFalso } = await import('../test-utils/area-api-pdf')
  return { jsPDF: JsPdfFalso }
})
vi.mock('jspdf-autotable', async () => {
  const { autoTableFalso } = await import('../test-utils/area-api-pdf')
  return { default: autoTableFalso }
})

// cores de status (iguais às de pdfReport.js)
const VERDE = [22, 128, 61]
const AMBAR = [180, 83, 9]
const ROXO = [124, 58, 237]
const AZUL = [29, 78, 216]
const CINZA_APAGADO = [156, 163, 175]

function logoOk() {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['png'], { type: 'image/png' }) })))
}
function logoFalha() {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false })))
}

// módulo novo a cada teste: a logo fica em cache no módulo (uma promessa só)
async function carregar() {
  vi.resetModules()
  return await import('./pdfReport.js')
}

const doc = () => estadoPdf.docs.at(-1)
const tabelas = () => estadoPdf.tabelas
const celula = (tabela, linha, coluna) => tabela.celulas.find(c => c.linha === linha && c.coluna === coluna).cell
const linhaDe = (tabela, i) => tabela.opcoes.body[i]

beforeEach(() => {
  reiniciarPdfFalso()
  logoOk()
  // 10/03/2026 12:00 em São Paulo
  vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

// ── Relatório de campanha ─────────────────────────────────────────────────
function resumo(extra = {}) {
  return {
    campaign: { name: 'Campanha Teste Verão', status: 'ativa', start_date: '2026-03-01T12:00:00Z', end_date: '2026-03-31T12:00:00Z' },
    client: { name: 'Cliente Teste', target_label: 'Mulheres 25-49' },
    period: { from: '2026-03-05T12:00:00Z', to: '2026-03-20T12:00:00Z' },
    totals: {
      detections: 12, distinct_materials: 2, distinct_stations: 2,
      impactos: 123456, impactos_target: 7890, stations_with_target: 1,
    },
    by_material: [
      { material_id: 'm1', material_short_id: 241, material_title: 'Spot Verão 30s', material_type_name: 'Spot', material_duration_sec: 29.6, count: 8 },
      { material_id: 'm2', material_short_id: null, material_title: '', material_type_name: '', material_duration_sec: null, count: 4 },
    ],
    by_station: [
      { station_id: 's1', station_name: 'Rádio Teste FM', station_band: 'FM', station_frequency_mhz: 99.9, station_city: 'Cidade Teste', station_state: 'SP',
        count: 8, station_pmm: 1000, station_pmm_target: 500, impact_count: 6 },
      // sem impact_count: cai na soma Dentro + Bônus da própria tabela
      { station_id: 's2', station_name: 'Rádio Teste AM', station_band: null, station_frequency_mhz: null, station_city: '', station_state: '',
        count: 4, station_pmm: 2000, station_pmm_target: null },
      { station_id: 's3', station_name: '', count: 0, station_pmm: null },
    ],
    by_material_station: [
      { material_id: 'm1', station_id: 's1', material_title: 'Spot Verão 30s', station_name: 'Rádio Teste FM', station_city: 'Cidade Teste', station_state: 'SP',
        in_slot_count: 5, out_slot_count: 1, out_date_count: 0, orphan_count: 2, count: 8 },
      { material_id: 'm2', station_id: 's2', material_title: '', station_name: 'Rádio Teste AM', station_city: '', station_state: '',
        in_slot_count: 3, out_slot_count: 0, out_date_count: 4, orphan_count: 1, count: 8 },
    ],
    ...extra,
  }
}

describe('buildCampaignReportPDF: capa', () => {
  test('cria o A4 em mm com compressão e mostra campanha, cliente, período e status', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const d = doc()
    expect(d.opcoes).toEqual({ unit: 'mm', format: 'a4', compress: true })
    const textos = d.textos()
    expect(textos).toContain('RELATÓRIO DE VEICULAÇÕES')
    expect(textos).toContain('Campanha Teste Verão')
    expect(textos).toContain('Cliente Teste  ·  05/03/2026 – 20/03/2026')
    expect(textos).toContain('Público-alvo: Mulheres 25-49')
    expect(textos).toContain('Ativa')
  })

  test('sem período explícito usa a vigência da campanha', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ period: {} }))
    expect(doc().textos()).toContain('Cliente Teste  ·  01/03/2026 – 31/03/2026')
  })

  test('sem rótulo de público-alvo a linha não existe', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ client: { name: 'Cliente Teste', target_label: '   ' } }))
    expect(doc().textos().some(t => String(t).startsWith('Público-alvo'))).toBe(false)
  })

  test('status desconhecido aparece como veio; ausente vira Concluída', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ campaign: { name: 'X', status: 'pausa-estranha' } }))
    expect(doc().textos()).toContain('pausa-estranha')

    reiniciarPdfFalso()
    await buildCampaignReportPDF(resumo({ campaign: { name: 'X' } }))
    expect(doc().textos()).toContain('Concluída')
  })

  test('com a logo: addImage com o dataURL; sem fallback textual', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const img = doc().de('addImage')
    expect(img).toHaveLength(1)
    expect(img[0].args[0]).toMatch(/^data:image\/png;base64,/)
    expect(img[0].args.slice(1)).toEqual(['PNG', 15, 12, 28, 11, '', 'FAST'])
    expect(doc().textos()).not.toContain('E-monitor')
  })

  test('logo que não carrega: marca textual "E-monitor" no lugar', async () => {
    logoFalha()
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    expect(doc().de('addImage')).toHaveLength(0)
    expect(doc().textosComPosicao()).toContainEqual(expect.objectContaining({ texto: 'E-monitor', x: 15, y: 20 }))
  })

  test('fetch que lança também cai no fallback textual', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('offline') }))
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    expect(doc().textos()).toContain('E-monitor')
  })

  test('imagem corrompida não derruba o PDF (segue sem logo e sem fallback)', async () => {
    estadoPdf.addImageFalha = true
    const { buildCampaignReportPDF } = await carregar()
    await expect(buildCampaignReportPDF(resumo())).resolves.toBeUndefined()
    expect(doc().arquivoSalvo).toMatch(/^relatorio-/)
  })

  test('a logo é baixada uma vez só, mesmo com pré-carga + dois PDFs', async () => {
    const { buildCampaignReportPDF, prefetchReportLogo } = await carregar()
    prefetchReportLogo()
    prefetchReportLogo()
    await buildCampaignReportPDF(resumo())
    await buildCampaignReportPDF(resumo())
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/E-monitor%20logo.png')
  })

  // BUG (pdfReport.js:99-101 x 77-95): o comentário de prefetchReportLogo promete que,
  // se a pré-carga falhar, "o PDF tenta de novo" — mas a promessa que resolveu em null
  // fica em cache (_logoPromise) e nenhum PDF seguinte tenta baixar a logo outra vez.
  // Uma falha transitória na pré-carga tira a logo de todos os PDFs até recarregar a página.
  test.skip('BUG: falha transitória na pré-carga não deveria tirar a logo dos PDFs seguintes', async () => {
    logoFalha()
    const { buildCampaignReportPDF, prefetchReportLogo } = await carregar()
    prefetchReportLogo()
    await Promise.resolve()
    logoOk()
    await buildCampaignReportPDF(resumo())
    expect(doc().de('addImage')).toHaveLength(1)
  })

  // BUG (pdfReport.js:35-40): fmtDate faz new Date('2026-03-01T00:00:00Z'). A API manda
  // start_date/end_date (DATE do Postgres, time.Time no Go) como meia-noite UTC; no fuso de
  // São Paulo isso é 21h do dia anterior, então, quando o summary não traz `period`
  // (cai na vigência da campanha), a capa mostra "28/02/2026 – 30/03/2026" para uma
  // campanha de 01/03 a 31/03. dates.js (parseLocalDate) e stationsExport.js (fmtIsoDate)
  // já tratam essa armadilha; aqui ficou de fora.
  test.skip('BUG: vigência em meia-noite UTC aparece um dia antes no PDF', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({
      period: {},
      campaign: { name: 'X', start_date: '2026-03-01T00:00:00Z', end_date: '2026-03-31T00:00:00Z' },
    }))
    expect(doc().textos()).toContain('Cliente Teste  ·  01/03/2026 – 31/03/2026')
  })
})

describe('buildCampaignReportPDF: KPIs', () => {
  const kpiLabels = () => doc().textos().filter(t => ['VEICULAÇÕES', 'MATERIAIS', 'EMISSORAS', 'IMPACTOS', 'IMPACTOS NO TARGET'].includes(t))

  test('com PMM no target cadastrado: 5 caixas, números em pt-BR', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    expect(kpiLabels()).toEqual(['VEICULAÇÕES', 'MATERIAIS', 'EMISSORAS', 'IMPACTOS', 'IMPACTOS NO TARGET'])
    const t = doc().textos()
    expect(t).toEqual(expect.arrayContaining(['12', '2', '123.456', '7.890']))
  })

  test('sem PMM no target: 4 caixas e nenhuma coluna de target', async () => {
    const { buildCampaignReportPDF } = await carregar()
    const r = resumo()
    r.totals.stations_with_target = 0
    await buildCampaignReportPDF(r)
    expect(kpiLabels()).toEqual(['VEICULAÇÕES', 'MATERIAIS', 'EMISSORAS', 'IMPACTOS'])
    const porEmissora = tabelas()[1]
    expect(porEmissora.opcoes.head[0]).not.toContain('Impactos no target')
    expect(porEmissora.opcoes.columnStyles[10]).toBeUndefined()
    expect(porEmissora.opcoes.body.every(l => l.length === 10)).toBe(true)
  })

  test('texto largo demais encolhe a fonte até caber (piso de 5,5pt no rótulo)', async () => {
    const { buildCampaignReportPDF } = await carregar()
    const r = resumo()
    r.totals.impactos = 987654321987
    await buildCampaignReportPDF(r)
    const tamanhos = doc().de('setFontSize').map(c => c.args[0])
    // o valor começa em 20pt e desce em passos de 0,5 quando não cabe
    expect(tamanhos.some(n => n < 20 && n >= 11 && n % 1 === 0.5)).toBe(true)
    expect(Math.min(...tamanhos)).toBeGreaterThanOrEqual(5.5)
  })

  test('summary vazio não quebra: tudo zerado e "—"', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF({})
    const t = doc().textos()
    expect(t).toContain('—')
    expect(t).toContain('—  ·  — – —')
    expect(t).toContain('0')
    expect(doc().arquivoSalvo).toBe('relatorio-campanha-20260310.pdf')
  })
})

describe('buildCampaignReportPDF: tabela "Por material"', () => {
  test('cabeçalho, linhas e totais por categoria somados do material × emissora', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const t = tabelas()[0]
    expect(t.opcoes.head).toEqual([['ID', 'Material', 'Tipo', 'Dur', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total']])
    // m1: 5 dentro, 1 fora faixa, 0 fora data, 2 bônus (orphan), total 8; 29,6s arredonda pra 30s
    expect(linhaDe(t, 0)).toEqual([241, 'Spot Verão 30s', 'Spot', '30s', '5', '1', '0', '2', '8'])
    // m2 sem id/título/tipo/duração: "—" em todos; contagens vêm da linha por emissora
    expect(linhaDe(t, 1)).toEqual(['—', '—', '—', '—', '3', '0', '4', '1', '4'])
  })

  test('material sem linha no detalhamento fica com zeros (não some)', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ by_material_station: [] }))
    expect(linhaDe(tabelas()[0], 0).slice(4, 8)).toEqual(['0', '0', '0', '0'])
  })

  test('colunas de status ganham a cor da categoria quando > 0 e ficam apagadas em zero', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const t = tabelas()[0]
    expect(celula(t, 0, 4).styles.textColor).toEqual(VERDE) // Dentro = 5
    expect(celula(t, 0, 5).styles.textColor).toEqual(AMBAR) // Fora faixa = 1
    expect(celula(t, 0, 6).styles.textColor).toEqual(CINZA_APAGADO) // Fora data = 0
    expect(celula(t, 0, 7).styles.textColor).toEqual(AZUL) // Bônus = 2
    expect(celula(t, 1, 6).styles.textColor).toEqual(ROXO) // Fora data = 4
    // colunas sem semáforo (ID, Material, Total) não são mexidas
    expect(celula(t, 0, 1).styles.textColor).toBeUndefined()
    expect(celula(t, 0, 8).styles.textColor).toBeUndefined()
  })

  test('lista ausente vira tabela vazia', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ by_material: undefined }))
    expect(tabelas()[0].opcoes.body).toEqual([])
  })
})

describe('buildCampaignReportPDF: tabela "Por emissora"', () => {
  test('Impactos = pmm × (Dentro + Bônus), preferindo o impact_count do backend', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const t = tabelas()[1]
    expect(t.opcoes.head[0]).toEqual(['Emissora', 'Dial', 'Cidade', 'UF', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total', 'Impactos', 'Impactos no target'])
    // s1: impact_count 6 × pmm 1000 (NÃO usa o total 8, que inclui fora-faixa); target 500 × 6
    expect(linhaDe(t, 0)).toEqual(['Rádio Teste FM', 'FM 99,9', 'Cidade Teste', 'SP', '5', '1', '0', '2', '8', '6.000', '3.000'])
    // s2 sem impact_count: cai no Dentro (3) + Bônus (1) = 4 × pmm 2000; sem target → "—"
    expect(linhaDe(t, 1)).toEqual(['Rádio Teste AM', '—', '—', '—', '3', '0', '4', '1', '4', '8.000', '—'])
    // s3 sem nome nem pmm: tudo "—" e zero
    expect(linhaDe(t, 2)).toEqual(['—', '—', '—', '—', '0', '0', '0', '0', '0', '—', '—'])
  })

  test('as colunas de status da tabela por emissora também levam semáforo', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const t = tabelas()[1]
    expect(celula(t, 0, 4).styles.textColor).toEqual(VERDE)
    expect(celula(t, 0, 7).styles.textColor).toEqual(AZUL)
    expect(celula(t, 2, 4).styles.textColor).toEqual(CINZA_APAGADO)
  })

  test('título "Por emissora" fica logo abaixo da tabela anterior quando há espaço', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const titulo = doc().textosComPosicao().find(x => x.texto === 'Por emissora')
    expect(titulo).toMatchObject({ x: 15, y: 128, pagina: 1 }) // finalY 120 + 8
  })

  test('tabela anterior encostada no fim da página empurra a seção pra página nova', async () => {
    reiniciarPdfFalso({ finalY: 250 }) // 250 + 8 > 297 − 60
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ by_material_station: [] }))
    const titulo = doc().textosComPosicao().find(x => x.texto === 'Por emissora')
    expect(titulo).toMatchObject({ y: 20, pagina: 2 })
    expect(doc().de('addPage')).toHaveLength(1)
  })
})

describe('buildCampaignReportPDF: detalhe Material × Emissora, rodapé e arquivo', () => {
  test('detalhamento em página nova com legenda e colunas de status', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    expect(tabelas()).toHaveLength(3)
    const t = tabelas()[2]
    expect(doc().textosComPosicao().find(x => x.texto === 'Detalhe — Material × Emissora')).toMatchObject({ y: 20, pagina: 2 })
    expect(t.opcoes.head[0]).toEqual(['Material', 'Emissora', 'Cidade/UF', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total'])
    expect(linhaDe(t, 0)).toEqual(['Spot Verão 30s', 'Rádio Teste FM', 'Cidade Teste/SP', '5', '1', '0', '2', '8'])
    expect(linhaDe(t, 1)).toEqual(['—', 'Rádio Teste AM', '—', '3', '0', '4', '1', '8'])
    expect(celula(t, 0, 3).styles.textColor).toEqual(VERDE)
    expect(celula(t, 1, 5).styles.textColor).toEqual(ROXO)
    // legenda das 5 categorias
    expect(doc().textos()).toEqual(expect.arrayContaining(['Tocou', 'Fora da faixa', 'Fora da data', 'Déficit', 'Bônus']))
  })

  test('sem material × emissora não há página de detalhe', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ by_material_station: undefined }))
    expect(tabelas()).toHaveLength(2)
    expect(doc().de('addPage')).toHaveLength(0)
    expect(doc().textos()).not.toContain('Detalhe — Material × Emissora')
  })

  test('rodapé em todas as páginas com data e "Página i de N"; arquivo nomeado por slug e data', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    const rodape = doc().textosComPosicao().filter(x => String(x.texto).startsWith('Página') || String(x.texto).startsWith('Gerado por'))
    expect(rodape.map(x => x.texto)).toEqual([
      'Gerado por E-monitor · 10/03/2026', 'Página 1 de 2',
      'Gerado por E-monitor · 10/03/2026', 'Página 2 de 2',
    ])
    expect(rodape.map(x => x.pagina)).toEqual([1, 1, 2, 2])
    expect(doc().arquivoSalvo).toBe('relatorio-campanha-teste-verao-20260310.pdf')
  })

  test('nome do arquivo tira acento e símbolo e limita o slug a 60 caracteres', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo({ campaign: { name: `Promoção Ação & Café ${'x'.repeat(80)}` } }))
    const nome = doc().arquivoSalvo
    expect(nome).toMatch(/^relatorio-promocao-acao-cafe-x+-20260310\.pdf$/)
    expect(nome.replace('relatorio-', '').replace('-20260310.pdf', '').length).toBeLessThanOrEqual(60)
  })
})

// ── Relatório da grade ────────────────────────────────────────────────────
const DIA1 = new Date(2026, 2, 2)
const DIA2 = new Date(2026, 2, 3)
const iso = d => d.toISOString().slice(0, 10)

function modeloGrade({ semMovimento = false, comTarget = true } = {}) {
  const cellData = new Map()
  if (!semMovimento) {
    cellData.set(`s1|t1|${iso(DIA1)}`, { expected: 10, in_slot: 8, deficit: 2, bonus: 1, out_slot: 1, out_date: 0 })
    cellData.set(`s1|t1|${iso(DIA2)}`, { expected: 10, in_slot: 10, deficit: 0, bonus: 0, out_slot: 0, out_date: 2 })
    cellData.set(`s1|t2|${iso(DIA1)}`, { expected: 4, in_slot: 4, deficit: 0, bonus: 0, out_slot: 0, out_date: 0 })
    // s2 só tem linha zerada: não deve gerar seção
  }
  return buildGridReportModel({
    campaign: { name: 'Campanha Grade Teste', status: 'programada' },
    client: { name: 'Cliente Teste' },
    filteredRows: [
      { stationId: 's1', materialId: 't1', materialTitle: 'Spot 30s' },
      { stationId: 's1', materialId: 't2', materialTitle: 'Vinheta' },
      { stationId: 's2', materialId: 't1', materialTitle: 'Spot 30s' },
    ],
    stations: [
      { id: 's1', name: 'Rádio Teste FM', city: 'Cidade Teste', state: 'SP', band: 'FM', frequency_mhz: 99.9, pmm: 1000 },
      { id: 's2', name: 'Rádio Teste Vazia', city: '', state: '', band: 'AM', frequency_mhz: 1010, pmm: 0 },
    ],
    days: [DIA1, DIA2],
    cellData,
    filterInfo: { periodLabel: '02/03 a 03/03', search: 'teste' },
    materialLookup: new Map([
      ['s1|t1', [{ shortId: 241, title: 'Spot Verão', durationSec: 29.6 }]],
      ['s1|t2', [{ title: 'Vinheta A' }, { title: 'Vinheta B' }]],
    ]),
    pmmTargetByStation: comTarget ? { s1: 500 } : {},
  })
}

describe('buildGridReportPDF', () => {
  test('hero, recorte aplicado, KPIs e legenda', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    const t = doc().textos()
    expect(t).toContain('RELATÓRIO DE VEICULAÇÕES · PROGRAMADO × TOCADO')
    expect(t).toContain('Campanha Grade Teste')
    expect(t).toContain('Cliente Teste  ·  02/03 a 03/03')
    expect(t).toContain('Recorte: 2 emissoras · busca "teste" · 02/03 a 03/03')
    expect(t).toContain('Programada')
    // esperado 24, tocou 22 → cobertura 92% (arredondado)
    expect(t).toEqual(expect.arrayContaining(['COBERTURA', '92%', 'ESPERADO', '24', 'TOCOU', '22', 'DÉFICIT', '2', 'BÔNUS', '1']))
  })

  test('KPI de déficit e bônus ficam apagados quando zerados e vermelho/azul quando > 0', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    // valores dos KPIs: setTextColor logo antes do `text` do número
    const chamadas = doc().chamadas
    const corAntes = (valor) => {
      const i = chamadas.findIndex(c => c.nome === 'text' && c.args[0] === valor && c.args[2] > 95 && c.args[2] < 110)
      return chamadas.slice(0, i).reverse().find(c => c.nome === 'setTextColor').args
    }
    expect(corAntes('2')).toEqual([185, 28, 28]) // déficit 2 → vermelho
    expect(corAntes('1')).toEqual([29, 78, 216]) // bônus 1 → azul
  })

  test('uma seção por emissora COM movimento; a zerada fica de fora', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    expect(tabelas()).toHaveLength(1)
    const t = doc().textos()
    expect(t).toContain('Rádio Teste FM')
    expect(t).not.toContain('Rádio Teste Vazia')
    expect(t).toContain('FM 99,9  ·  Cidade Teste/SP')
  })

  test('nota da emissora: fora da faixa/data e impactos (base in_slot + bônus) e no target', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    // in_slot 22 + bônus 1 = 23 × pmm 1000 = 23.000; target 500 × 23 = 11.500
    expect(doc().textos()).toContain('+ 1 fora da faixa · 2 fora da data · 23.000 impactos · 11.500 no target')
  })

  test('sem PMM nem fora-de-faixa a nota some', async () => {
    const { buildGridReportPDF } = await carregar()
    const modelo = modeloGrade({ comTarget: false })
    const s = modelo.byStation[0]
    s.impactos = null
    s.totals.outSlot = 0
    s.totals.outDate = 0
    await buildGridReportPDF(modelo)
    expect(doc().textos().some(x => String(x).startsWith('+ '))).toBe(false)
  })

  test('corpo da tabela: linha-título por material, um dia por linha, Total por material', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    const t = tabelas()[0]
    expect(t.opcoes.head).toEqual([['Data', 'Prog', 'Tocou', 'Fora faixa', 'Fora data', 'Déf', 'Bônus']])
    const body = t.opcoes.body
    // material 1 (1 material real): tipo · #id título duração
    expect(body[0]).toEqual([{ content: 'Spot 30s  ·  #241 Spot Verão 30s', colSpan: 7 }])
    expect(body[1]).toEqual(['02/03', '10', '8', '1', '0', '2', '+1'])
    expect(body[2]).toEqual(['03/03', '10', '10', '0', '2', '0', '0'])
    expect(body[3]).toEqual(['Total', '20', '18', '1', '2', '2', '+1'])
    // material 2 (2 materiais reais): lista os títulos
    expect(body[4]).toEqual([{ content: 'Vinheta  ·  2 materiais: Vinheta A, Vinheta B', colSpan: 7 }])
    expect(body[5]).toEqual(['02/03', '4', '4', '0', '0', '0', '0'])
    expect(body[6]).toEqual(['Total', '4', '4', '0', '0', '0', '0'])
  })

  test('estilo por linha: título em destaque, total em negrito e semáforo só quando > 0', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    const t = tabelas()[0]
    const titulo = celula(t, 0, 0)
    expect(titulo.styles).toMatchObject({ fontStyle: 'bold', fillColor: [252, 231, 243], textColor: [6, 5, 91], halign: 'left' })
    // dia 02/03: Tocou 8 verde, Fora faixa 1 âmbar, Fora data 0 apagado, Déficit 2 vermelho, Bônus +1 azul
    expect(celula(t, 1, 2).styles.textColor).toEqual(VERDE)
    expect(celula(t, 1, 3).styles.textColor).toEqual(AMBAR)
    expect(celula(t, 1, 4).styles.textColor).toEqual(CINZA_APAGADO)
    expect(celula(t, 1, 5).styles.textColor).toEqual([185, 28, 28])
    expect(celula(t, 1, 6).styles.textColor).toEqual(AZUL)
    // coluna "Prog" e a data não têm semáforo
    expect(celula(t, 1, 1).styles.textColor).toBeUndefined()
    // linha Total: negrito + fundo; zero na coluna de status fica navy (não apagado)
    const totalCol4 = celula(t, 6, 4)
    expect(totalCol4.styles).toMatchObject({ fontStyle: 'bold', fillColor: [241, 245, 249], textColor: [6, 5, 91] })
  })

  test('sem nenhuma emissora com movimento mostra a mensagem de recorte vazio', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade({ semMovimento: true }))
    expect(tabelas()).toHaveLength(0)
    expect(doc().textos()).toContain('Nenhuma veiculação ou programação no recorte selecionado.')
  })

  test('emissora que não cabe no fim da página abre página nova', async () => {
    reiniciarPdfFalso({ finalY: 260 }) // nextY = 272 > 297 − 45
    const { buildGridReportPDF } = await carregar()
    const modelo = modeloGrade()
    // duplica a emissora pra ter uma segunda seção
    modelo.byStation.push({ ...modelo.byStation[0], stationName: 'Rádio Teste Dois' })
    await buildGridReportPDF(modelo)
    expect(doc().de('addPage')).toHaveLength(1)
    const dois = doc().textosComPosicao().find(x => x.texto === 'Rádio Teste Dois')
    expect(dois).toMatchObject({ pagina: 2, y: 20 })
  })

  test('rodapé paginado e nome do arquivo com slug da campanha e data', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    expect(doc().textos()).toContain('Página 1 de 1')
    expect(doc().arquivoSalvo).toBe('relatorio-veiculacoes-campanha-grade-teste-20260310.pdf')
  })

  test('logo ausente: marca textual no topo', async () => {
    logoFalha()
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade())
    expect(doc().textos()).toContain('E-monitor')
    expect(doc().de('addImage')).toHaveLength(0)
  })

  test('cobertura indefinida (nada programado) vira "—"', async () => {
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modeloGrade({ semMovimento: true }))
    expect(doc().textos()).toContain('—')
  })
})

// ── PDF de emissoras contratadas ─────────────────────────────────────────
const EMISSORAS = [
  { name: 'Rádio Teste FM', frequency_mhz: 99.9, band: 'FM', city: 'Cidade Teste', state: 'SP', pmm: 12000,
    contract: { on_air: true, campaigns: 3 }, meta: { categories: ['Pop', 'Notícias', 'Esporte'] } },
  { name: 'Rádio Teste AM', frequency_mhz: 1010, band: 'AM', city: 'Outra Cidade', state: 'MG', pmm: null,
    contract: { on_air: false, starts_at: '2026-09-01T00:00:00Z', campaigns: 1 }, meta: {} },
  { name: 'Rádio Sem Dados', frequency_mhz: null, band: null, city: '', state: '', contract: null },
]

describe('buildStationsPDF / exportStationsPdf', () => {
  test('KPIs resumem a lista e a tabela mostra uma emissora por linha', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({ stations: EMISSORAS, clientName: 'Cliente Teste', filters: '' })
    expect(d).toBe(doc())
    const t = tabelas()[0]
    expect(t.opcoes.head).toEqual([['Emissora', 'Dial', 'Praça', 'PMM', 'Situação', 'Camp.', 'Categorias']])
    expect(t.opcoes.body).toEqual([
      ['Rádio Teste FM', '99,9 FM', 'Cidade Teste/SP', '12.000', 'Veiculando', '3', 'Pop, Notícias'], // só 2 categorias
      ['Rádio Teste AM', '1010 AM', 'Outra Cidade/MG', '—', 'A partir de 01/09/2026', '1', '—'],
      ['Rádio Sem Dados', '', '—', '—', '—', '—', '—'],
    ])
    const texto = d.textos()
    // 3 emissoras, 2 praças, 2 estados, 1 no ar, PMM somado 12.000
    expect(texto).toEqual(expect.arrayContaining(['EMISSORAS', '3', 'PRAÇAS', '2', 'ESTADOS', 'NO AR', '1', 'PMM SOMADO', '12.000']))
    expect(texto).toContain('EMISSORAS CONTRATADAS')
    expect(texto).toContain('Cliente Teste')
    expect(texto).toContain('Posição em 10/03/2026')
  })

  test('filtros ativos aparecem no cabeçalho (o documento diz que é um recorte)', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({ stations: EMISSORAS, clientName: 'Cliente Teste', filters: 'busca "teste" · FM' })
    expect(d.textos()).toContain('Filtros: busca "teste" · FM')
  })

  test('sem filtros a linha não existe', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({ stations: EMISSORAS, clientName: 'Cliente Teste' })
    expect(d.textos().some(x => String(x).startsWith('Filtros:'))).toBe(false)
  })

  test('cliente sem nome e lista ausente não quebram', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({ stations: undefined })
    expect(d.textos()).toContain('—')
    expect(tabelas()[0].opcoes.body).toEqual([])
  })

  test('tabela deixa folga pro rodapé e o rodapé numera as páginas', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({ stations: EMISSORAS, clientName: 'Cliente Teste' })
    expect(tabelas()[0].opcoes.margin).toEqual({ left: 15, right: 15, bottom: 18 })
    expect(d.textos()).toContain('Página 1 de 1')
  })

  test('exportStationsPdf salva com o nome do cliente e a data', async () => {
    const { exportStationsPdf } = await carregar()
    await exportStationsPdf({ stations: EMISSORAS, clientName: 'Cliente Têste & Cia' })
    expect(doc().arquivoSalvo).toBe('emissoras-cliente-teste-cia-20260310.pdf')
  })

  test('exportStationsPdf sem nome de cliente usa "emissoras"', async () => {
    const { exportStationsPdf } = await carregar()
    await exportStationsPdf({ stations: EMISSORAS })
    expect(doc().arquivoSalvo).toBe('emissoras-emissoras-20260310.pdf')
  })
})

// ── Data no nome do arquivo ──────────────────────────────────────────────
describe('data do nome do arquivo', () => {
  // BUG (pdfReport.js:297 e :884; mesmo padrão em stationsExport.stampNow, gridReport e
  // hooks.exportDetectionsCsv): o carimbo usa new Date().toISOString().slice(0, 10), ou
  // seja, a data em UTC. Entre 21h e 24h de Brasília o arquivo sai com a data de amanhã
  // ("relatorio-...-20260311.pdf" gerado às 22h30 de 10/03). O PDF de cobrança
  // (pdfCampaignFailure) já usa a data local e está certo.
  test.skip('BUG: às 22h30 de 10/03 em São Paulo o arquivo deveria levar 20260310', async () => {
    vi.setSystemTime(new Date('2026-03-11T01:30:00Z'))
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumo())
    expect(doc().arquivoSalvo).toBe('relatorio-campanha-teste-verao-20260310.pdf')
  })
})
