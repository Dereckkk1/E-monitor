// pdfReport.js com jsPDF e jspdf-autotable REAIS (no jsdom). O pdfReport.test.js usa
// dublês pra conferir cada decisão do gerador; aqui o foco é o contrato com as
// bibliotecas de verdade: colSpan, columnStyles, quebra de página automática, rodapé
// por página. O download (doc.save) é a única coisa trocada — vira o registro do nome.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { buildGridReportModel } from './gridReport.js'

const registro = vi.hoisted(() => ({ docs: [], salvos: [] }))

vi.mock('jspdf', async (importOriginal) => {
  const real = await importOriginal()
  class JsPdfRegistrado extends real.jsPDF {
    constructor(...args) {
      super(...args)
      this.save = (nome) => { registro.salvos.push(nome) } // save é propriedade da instância
      registro.docs.push(this)
    }
  }
  return { ...real, jsPDF: JsPdfRegistrado }
})

async function carregar() {
  vi.resetModules()
  return await import('./pdfReport.js')
}

beforeEach(() => {
  registro.docs.length = 0
  registro.salvos.length = 0
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false }))) // sem logo: fallback textual
  vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const doc = () => registro.docs.at(-1)

function resumoGrande(nEmissoras) {
  const by_station = []
  const by_material_station = []
  for (let i = 0; i < nEmissoras; i++) {
    by_station.push({
      station_id: `s${i}`, station_name: `Rádio Teste ${i}`, station_band: 'FM', station_frequency_mhz: 90 + (i % 15) / 10,
      station_city: 'Cidade Teste', station_state: 'SP', count: 10, station_pmm: 1000 + i, station_pmm_target: 500, impact_count: 9,
    })
    by_material_station.push({
      material_id: 'm1', station_id: `s${i}`, material_title: 'Spot Teste', station_name: `Rádio Teste ${i}`,
      station_city: 'Cidade Teste', station_state: 'SP', in_slot_count: 8, out_slot_count: 1, out_date_count: 0, orphan_count: 1, count: 10,
    })
  }
  return {
    campaign: { name: 'Campanha Teste', status: 'concluida', start_date: '2026-03-01T12:00:00Z', end_date: '2026-03-31T12:00:00Z' },
    client: { name: 'Cliente Teste', target_label: 'Adultos 25-49' },
    period: {},
    totals: { detections: 10 * nEmissoras, distinct_materials: 1, distinct_stations: nEmissoras, impactos: 9000, impactos_target: 4500, stations_with_target: 1 },
    by_material: [{ material_id: 'm1', material_short_id: 1, material_title: 'Spot Teste', material_type_name: 'Spot', material_duration_sec: 30, count: 10 * nEmissoras }],
    by_station, by_material_station,
  }
}

describe('PDFs com jsPDF real', () => {
  test('relatório de campanha: 2 tabelas + detalhe, 1 arquivo salvo, PDF válido', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumoGrande(3))
    expect(registro.salvos).toEqual(['relatorio-campanha-teste-20260310.pdf'])
    expect(doc().getNumberOfPages()).toBe(2) // capa+tabelas e a página do detalhe
    const bytes = new Uint8Array(doc().output('arraybuffer'))
    expect(String.fromCharCode(...bytes.slice(0, 5))).toBe('%PDF-')
  })

  test('muitas emissoras: o autotable pagina sozinho e o documento cresce', async () => {
    const { buildCampaignReportPDF } = await carregar()
    await buildCampaignReportPDF(resumoGrande(120))
    expect(doc().getNumberOfPages()).toBeGreaterThan(4)
    // a última tabela (detalhe) tem uma linha por material × emissora
    expect(doc().lastAutoTable.body).toHaveLength(120)
  })

  test('relatório da grade: seções com linha-título em colSpan e total por material', async () => {
    const dia = new Date(2026, 2, 2)
    const modelo = buildGridReportModel({
      campaign: { name: 'Campanha Grade Teste', status: 'ativa' },
      client: { name: 'Cliente Teste' },
      filteredRows: [{ stationId: 's1', materialId: 't1', materialTitle: 'Spot 30s' }],
      stations: [{ id: 's1', name: 'Rádio Teste FM', city: 'Cidade Teste', state: 'SP', band: 'FM', frequency_mhz: 99.9, pmm: 1000 }],
      days: [dia],
      cellData: new Map([[`s1|t1|${dia.toISOString().slice(0, 10)}`, { expected: 5, in_slot: 4, deficit: 1, bonus: 0, out_slot: 0, out_date: 0 }]]),
      filterInfo: {},
    })
    const { buildGridReportPDF } = await carregar()
    await buildGridReportPDF(modelo)
    expect(registro.salvos).toEqual(['relatorio-veiculacoes-campanha-grade-teste-20260310.pdf'])
    // título (colSpan 7) + 1 dia + total = 3 linhas
    expect(doc().lastAutoTable.body).toHaveLength(3)
    expect(doc().lastAutoTable.body[0].cells[0].colSpan).toBe(7)
  })

  test('emissoras contratadas devolve o documento sem salvar', async () => {
    const { buildStationsPDF } = await carregar()
    const d = await buildStationsPDF({
      stations: [{ name: 'Rádio Teste FM', frequency_mhz: 99.9, band: 'FM', city: 'Cidade Teste', state: 'SP', pmm: 100, contract: { on_air: true, campaigns: 1 } }],
      clientName: 'Cliente Teste',
    })
    expect(d).toBe(doc())
    expect(registro.salvos).toEqual([])
    expect(d.lastAutoTable.body).toHaveLength(1)
  })
})
