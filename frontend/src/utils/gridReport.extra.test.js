// gridReport.js — CSV do relatório da grade, rótulos de material/dial e o download.
// Complementa gridReport.test.mjs (modelo + emissora ausente do catálogo).
import { describe, test, expect, vi, afterEach } from 'vitest'
import {
  stationDial, materialsLabel, buildGridReportModel, buildGridReportCSV, exportGridReportCsv,
} from './gridReport.js'

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

const DIA = new Date(2026, 2, 2)
const ISO = DIA.toISOString().slice(0, 10)

function modelo({ comTarget = false, nome = 'Rádio Teste FM' } = {}) {
  return buildGridReportModel({
    campaign: { name: 'Campanha Grade Teste', status: 'ativa' },
    client: { name: 'Cliente Teste' },
    filteredRows: [
      { stationId: 's1', materialId: 't1', materialTitle: 'Spot 30s' },
      { stationId: 's1', materialId: 't2', materialTitle: 'Vinheta; "curta"' },
    ],
    stations: [{ id: 's1', name: nome, city: 'Cidade Teste', state: 'SP', band: 'FM', frequency_mhz: 99.9, pmm: 1000 }],
    days: [DIA],
    cellData: new Map([
      [`s1|t1|${ISO}`, { expected: 10, in_slot: 8, deficit: 2, bonus: 1, out_slot: 1, out_date: 0 }],
      [`s1|t2|${ISO}`, { expected: 2, in_slot: 2, deficit: 0, bonus: 0, out_slot: 0, out_date: 3 }],
    ]),
    materialLookup: { 's1|t1': [{ shortId: 241, title: 'Spot Verão' }, { title: 'Spot Inverno' }] },
    pmmTargetByStation: comTarget ? { s1: 500 } : {},
  })
}

describe('stationDial', () => {
  test('banda + frequência com vírgula decimal', () => {
    expect(stationDial({ band: 'FM', frequency_mhz: 99.9 })).toBe('FM 99,9')
    expect(stationDial({ band: 'AM', frequency_mhz: '1010' })).toBe('AM 1010,0')
    expect(stationDial({ band: 'FM', frequency_mhz: 98 })).toBe('FM 98,0')
  })
  test('partes ausentes ou inválidas somem', () => {
    expect(stationDial({ band: 'FM' })).toBe('FM')
    expect(stationDial({ frequency_mhz: 99.9 })).toBe('99,9')
    expect(stationDial({ band: 'FM', frequency_mhz: '' })).toBe('FM')
    expect(stationDial({ band: 'FM', frequency_mhz: 'abc' })).toBe('FM')
    expect(stationDial(null)).toBe('')
  })
})

describe('materialsLabel', () => {
  test('#id título por material, unidos por " / "', () => {
    expect(materialsLabel([{ shortId: 241, title: 'Spot Verão' }, { title: 'Spot Inverno' }])).toBe('#241 Spot Verão / Spot Inverno')
  })
  test('id sem título mostra só o id; item sem nada sai', () => {
    expect(materialsLabel([{ shortId: 7 }, {}, { title: '' }])).toBe('#7')
  })
  test('lista vazia ou inválida é vazia', () => {
    expect(materialsLabel([])).toBe('')
    expect(materialsLabel(null)).toBe('')
    expect(materialsLabel(undefined)).toBe('')
  })
})

describe('buildGridReportCSV', () => {
  test('cabeçalho base + uma linha por emissora × tipo, com os totais da grade', () => {
    const linhas = buildGridReportCSV(modelo()).split('\r\n')
    expect(linhas[0]).toBe('Emissora;Dial;Cidade;UF;Tipo;Materiais;Programado;Tocou (faixa);Déficit;Bônus;Fora da faixa;Fora da data;Impactos')
    // Spot: esperado 10, tocou 8, déficit 2, bônus 1, fora faixa 1, fora data 0; impactos = 1000 × (8 + 1)
    expect(linhas[1]).toBe('Rádio Teste FM;FM 99,9;Cidade Teste;SP;Spot 30s;#241 Spot Verão / Spot Inverno;10;8;2;1;1;0;9000')
    expect(linhas).toHaveLength(3)
  })

  test('campo com ponto-e-vírgula e aspas é escapado', () => {
    const linhas = buildGridReportCSV(modelo()).split('\r\n')
    expect(linhas[2]).toBe('Rádio Teste FM;FM 99,9;Cidade Teste;SP;"Vinheta; ""curta""";;2;2;0;0;0;3;2000')
  })

  test('coluna de impactos no target só existe quando alguma emissora tem PMM no target', () => {
    const sem = buildGridReportCSV(modelo()).split('\r\n')
    expect(sem[0]).not.toContain('Impactos no target')

    const com = buildGridReportCSV(modelo({ comTarget: true })).split('\r\n')
    expect(com[0].endsWith(';Impactos;Impactos no target')).toBe(true)
    expect(com[1].endsWith(';9000;4500')).toBe(true) // 500 × (8 + 1)
  })

  test('emissora sem PMM mostra "—" em Impactos (não zero)', () => {
    const m = buildGridReportModel({
      campaign: { name: 'X' }, client: { name: 'Y' },
      filteredRows: [{ stationId: 's1', materialId: 't1', materialTitle: 'Spot' }],
      stations: [{ id: 's1', name: 'Rádio Teste', band: 'FM', frequency_mhz: 99.9 }],
      days: [DIA],
      cellData: new Map([[`s1|t1|${ISO}`, { expected: 1, in_slot: 1 }]]),
    })
    expect(buildGridReportCSV(m).split('\r\n')[1].endsWith(';—')).toBe(true)
  })
})

describe('exportGridReportCsv', () => {
  test('baixa o CSV com BOM UTF-8 (Excel pt-BR) e nome com slug da campanha e data', async () => {
    vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
    const baixados = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click() {
      baixados.push({ download: this.download, noDom: document.body.contains(this) })
    })
    const criar = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:grade-teste')
    const revogar = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    exportGridReportCsv(modelo())

    expect(baixados).toEqual([{ download: 'relatorio-veiculacoes-campanha-grade-teste-20260310.csv', noDom: true }])
    const blob = criar.mock.calls[0][0]
    expect(blob.type).toBe('text/csv;charset=utf-8')
    // Blob.text() descarta o BOM ao decodificar; os bytes mostram que ele foi escrito
    const bytes = new Uint8Array(await blob.arrayBuffer())
    expect(Array.from(bytes.slice(0, 3))).toEqual([0xEF, 0xBB, 0xBF])
    expect((await blob.text()).startsWith('Emissora;Dial;')).toBe(true)
    expect(revogar).toHaveBeenCalledWith('blob:grade-teste')
    expect(document.querySelector('a[download]')).toBeNull()
  })
})
