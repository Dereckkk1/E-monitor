// exportInsights.js — exporta o dashboard /insights em PNG (html2canvas) e PDF (jsPDF
// A4 paisagem: capa com metadados + screenshot ajustado à página). html2canvas e
// jsPDF são dublês: conferimos nome do arquivo, opções da captura e a geometria.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import html2canvas from 'html2canvas'
import { estadoPdf, reiniciarPdfFalso } from '../test-utils/area-api-pdf'
import { exportInsightsPNG, exportInsightsPDF } from './exportInsights.js'

vi.mock('html2canvas', () => ({ default: vi.fn() }))
vi.mock('jspdf', async () => {
  const { JsPdfFalso } = await import('../test-utils/area-api-pdf')
  return { jsPDF: JsPdfFalso }
})

function canvasFalso({ largura = 1000, altura = 500, blob = new Blob(['png'], { type: 'image/png' }) } = {}) {
  return {
    width: largura,
    height: altura,
    toDataURL: vi.fn(() => 'data:image/png;base64,AAAA'),
    toBlob: vi.fn((cb, tipo) => cb(blob, tipo)),
  }
}

const el = { id: 'corpo-do-dashboard' }
let baixados

beforeEach(() => {
  reiniciarPdfFalso()
  baixados = []
  vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click() {
    baixados.push({ download: this.download, href: this.href, noDom: document.body.contains(this) })
  })
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:insights-teste')
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  html2canvas.mockReset()
})

describe('exportInsightsPNG', () => {
  test('captura o elemento com as opções do dashboard e baixa o PNG com o nome do cliente e do período', async () => {
    const canvas = canvasFalso()
    html2canvas.mockResolvedValue(canvas)

    await exportInsightsPNG(el, { clientName: 'Cliente Têste & Cia.', period: { from: '2026-03-01', to: '2026-03-31' } })

    expect(html2canvas).toHaveBeenCalledWith(el, { backgroundColor: '#f8fafc', scale: 2, useCORS: true, logging: false })
    expect(canvas.toBlob.mock.calls[0][1]).toBe('image/png')
    expect(baixados).toEqual([{ download: 'dashboard-cliente-teste-cia-2026-03-01-2026-03-31.png', href: 'blob:insights-teste', noDom: true }])
    // o link some do DOM depois do clique
    expect(document.querySelector('a[download]')).toBeNull()
  })

  test('a URL do blob é liberada 1s depois (o download precisa de tempo pra começar)', async () => {
    vi.useFakeTimers()
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPNG(el, { clientName: 'Cliente Teste' })
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
    vi.advanceTimersByTime(999)
    expect(URL.revokeObjectURL).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:insights-teste')
  })

  test('sem cliente nem período o nome cai nos padrões', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPNG(el)
    expect(baixados[0].download).toBe('dashboard-insights--.png')
  })

  test('canvas que não gera blob não baixa nada e a promessa resolve (não trava a tela)', async () => {
    html2canvas.mockResolvedValue(canvasFalso({ blob: null }))
    await expect(exportInsightsPNG(el, { clientName: 'Cliente Teste' })).resolves.toBeUndefined()
    expect(baixados).toEqual([])
  })

  test('erro do html2canvas propaga pro chamador mostrar a falha', async () => {
    html2canvas.mockRejectedValue(new Error('canvas tainted'))
    await expect(exportInsightsPNG(el)).rejects.toThrow('canvas tainted')
    expect(baixados).toEqual([])
  })

  test('nome do cliente enorme é cortado em 60 caracteres no slug', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPNG(el, { clientName: 'x'.repeat(100), period: { from: 'a', to: 'b' } })
    expect(baixados[0].download).toBe(`dashboard-${'x'.repeat(60)}-a-b.png`)
  })
})

describe('exportInsightsPDF', () => {
  const doc = () => estadoPdf.docs.at(-1)

  test('A4 paisagem em mm; capa com título, cliente, período, nº de campanhas e data', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPDF(el, {
      clientName: 'Cliente Teste', period: { from: '2026-03-01', to: '2026-03-31' }, campaigns: [{ id: 'k1' }, { id: 'k2' }],
    })
    expect(doc().opcoes).toEqual({ orientation: 'landscape', unit: 'mm', format: 'a4' })
    expect(doc().textos()).toEqual([
      'Dashboard de Veiculação',
      'Cliente Teste',
      'Período: 2026-03-01 → 2026-03-31',
      '2 campanha(s) selecionada(s)',
      `Gerado em ${new Date('2026-03-10T15:00:00Z').toLocaleString('pt-BR')}`,
    ])
  })

  test('capa centralizada na largura da página (297mm)', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPDF(el, { clientName: 'Cliente Teste' })
    const titulo = doc().de('text').find(c => c.args[0] === 'Dashboard de Veiculação')
    expect(titulo.args.slice(1)).toEqual([148.5, 70, { align: 'center' }])
  })

  test('sem cliente, período ou campanhas usa "—" e omite a linha de campanhas', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPDF(el)
    const t = doc().textos()
    expect(t).toContain('—')
    expect(t).toContain('Período: — → —')
    expect(t.some(x => String(x).includes('campanha(s)'))).toBe(false)
    expect(doc().arquivoSalvo).toBe('dashboard-insights--.pdf')
  })

  test('lista de campanhas vazia também omite a linha', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPDF(el, { campaigns: [] })
    expect(doc().textos().some(x => String(x).includes('campanha(s)'))).toBe(false)
  })

  test('screenshot largo: ocupa a largura útil (281mm) e centraliza na vertical', async () => {
    html2canvas.mockResolvedValue(canvasFalso({ largura: 1000, altura: 500 }))
    await exportInsightsPDF(el, { clientName: 'Cliente Teste' })
    expect(doc().de('addPage')).toHaveLength(1)
    const [img] = doc().de('addImage')
    expect(img.pagina).toBe(2)
    expect(img.args[0]).toBe('data:image/png;base64,AAAA')
    expect(img.args[1]).toBe('PNG')
    const [, , x, y, w, h] = img.args
    expect(w).toBeCloseTo(281) // 297 − 2×8
    expect(h).toBeCloseTo(140.5) // mantém a proporção 2:1
    expect(x).toBeCloseTo(8)
    expect(y).toBeCloseTo((210 - 140.5) / 2)
  })

  test('screenshot alto: ocupa a altura útil (194mm) e centraliza na horizontal', async () => {
    html2canvas.mockResolvedValue(canvasFalso({ largura: 500, altura: 2000 }))
    await exportInsightsPDF(el, { clientName: 'Cliente Teste' })
    const [, , x, y, w, h] = doc().de('addImage')[0].args
    expect(h).toBeCloseTo(194) // 210 − 2×8
    expect(w).toBeCloseTo(48.5) // 194 ÷ 4
    expect(x).toBeCloseTo((297 - 48.5) / 2)
    expect(y).toBeCloseTo(8)
  })

  test('nome do arquivo: slug do cliente + período', async () => {
    html2canvas.mockResolvedValue(canvasFalso())
    await exportInsightsPDF(el, { clientName: 'Cliente Têste Ltda.', period: { from: '2026-03-01', to: '2026-03-31' } })
    expect(doc().arquivoSalvo).toBe('dashboard-cliente-teste-ltda-2026-03-01-2026-03-31.pdf')
  })

  test('erro na captura propaga e não cria arquivo', async () => {
    html2canvas.mockRejectedValue(new Error('sem memória'))
    await expect(exportInsightsPDF(el)).rejects.toThrow('sem memória')
    expect(estadoPdf.docs).toHaveLength(0)
  })
})
