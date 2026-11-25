// stationsExport.js — slug, selo de data e o download do CSV (o resto do módulo é
// coberto por stationsExport.test.mjs).
import { describe, test, expect, vi, afterEach } from 'vitest'
import { slugifyName, stampNow, exportStationsCsv, fmtIsoDate, contractStatusLabel } from './stationsExport.js'

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('slugifyName', () => {
  test('tira acento e símbolo e usa hífen', () => {
    expect(slugifyName('Cliente Têste & Cia.')).toBe('cliente-teste-cia')
  })
  test('sem nome usa o padrão (ou o informado)', () => {
    expect(slugifyName('')).toBe('emissoras')
    expect(slugifyName(null, 'cliente')).toBe('cliente')
    expect(slugifyName('!!!')).toBe('emissoras')
  })
  test('corta em 60 caracteres', () => {
    expect(slugifyName('a'.repeat(90))).toHaveLength(60)
  })
})

describe('datas', () => {
  test('stampNow é a data ISO sem hífens', () => {
    vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
    expect(stampNow()).toBe('20260310')
  })
  test('fmtIsoDate vazio ou truncado devolve vazio', () => {
    expect(fmtIsoDate('')).toBe('')
    expect(fmtIsoDate('2026-09')).toBe('')
  })
  test('contrato programado sem data legível vira "Programada"', () => {
    expect(contractStatusLabel({ on_air: false })).toBe('Programada')
  })
})

describe('exportStationsCsv', () => {
  test('baixa o CSV com BOM, nome com cliente e data, e sem colunas de admin pro cliente', async () => {
    vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
    const baixados = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click() {
      baixados.push({ download: this.download, noDom: document.body.contains(this) })
    })
    const criar = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:emissoras-teste')
    const revogar = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    exportStationsCsv(
      [{ name: 'Rádio Teste FM', frequency_mhz: 99.9, band: 'FM', city: 'Cidade Teste', state: 'SP', pmm: 1000, meta: { commercial_email: 'comercial@example.com' } }],
      { clientName: 'Cliente Teste', isAdmin: false },
    )

    expect(baixados).toEqual([{ download: 'emissoras-cliente-teste-20260310.csv', noDom: true }])
    const blob = criar.mock.calls[0][0]
    // Blob.text() descarta o BOM ao decodificar; os bytes mostram que ele foi escrito
    const bytes = new Uint8Array(await blob.arrayBuffer())
    expect(Array.from(bytes.slice(0, 3))).toEqual([0xEF, 0xBB, 0xBF])
    const texto = await blob.text()
    expect(texto).toContain('Rádio Teste FM;99,9;FM;Cidade Teste;SP')
    expect(texto).not.toContain('comercial@example.com')
    expect(revogar).toHaveBeenCalledWith('blob:emissoras-teste')
  })

  test('admin recebe também os campos cadastrais', async () => {
    const criar = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:x')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    exportStationsCsv([{ name: 'Rádio Teste FM', meta: { commercial_email: 'comercial@example.com' } }], { isAdmin: true })
    const texto = await criar.mock.calls[0][0].text()
    expect(texto).toContain('E-mail comercial')
    expect(texto).toContain('comercial@example.com')
  })
})
