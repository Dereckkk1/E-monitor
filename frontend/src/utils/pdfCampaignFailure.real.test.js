// pdfCampaignFailure.js com jsPDF e jspdf-autotable REAIS: garante que o desenho
// manual da coluna de déficit (didParseCell/didDrawCell) roda de verdade dentro do
// autotable, com paginação, sem exceção. O nome do arquivo vem do registro do save.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'

const registro = vi.hoisted(() => ({ docs: [], salvos: [] }))

vi.mock('jspdf', async (importOriginal) => {
  const real = await importOriginal()
  class JsPdfRegistrado extends real.jsPDF {
    constructor(...args) {
      super(...args)
      this.save = (nome) => { registro.salvos.push(nome) }
      registro.docs.push(this)
    }
  }
  return { ...real, jsPDF: JsPdfRegistrado }
})

beforeEach(() => {
  registro.docs.length = 0
  registro.salvos.length = 0
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false }))) // sem logo
  vi.setSystemTime(new Date('2026-03-10T15:00:00Z'))
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function carregar() {
  vi.resetModules()
  return await import('./pdfCampaignFailure.js')
}

function estacoes(n) {
  return Array.from({ length: n }, (_, i) => ({
    station: { name: `Rádio Teste ${i}`, city: 'Cidade Teste' },
    programmed: 10, identified: 6, extras: i % 3, deficit: 4, deficit_absent: 3, deficit_off_slot: 1,
    is_bonified: i % 7 === 0, failure_days: ['2026-03-05', '2026-03-06'],
  }))
}

describe('Relatório de Cobrança com jsPDF real', () => {
  test('gera o PDF com legenda e salva com o nome esperado', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf({
      campaign: { name: 'Campanha Teste', client_name: 'Cliente Teste', start_date: '2026-03-01', end_date: '2026-03-31' },
      summary: { stations_with_failure: 4, total_failure_days: 8, total_deficit: 16, total_deficit_absent: 12, total_deficit_off_slot: 4 },
      stations: estacoes(4),
    })
    expect(registro.salvos).toEqual(['cobranca-campanha-teste-20260310.pdf'])
    expect(registro.docs[0].lastAutoTable.body).toHaveLength(4)
  })

  test('lista longa pagina o documento', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf({
      campaign: { name: 'Campanha Teste', client_name: 'Cliente Teste' },
      summary: { stations_with_failure: 60, total_failure_days: 120, total_deficit: 240, total_deficit_absent: 180, total_deficit_off_slot: 60 },
      stations: estacoes(60),
    })
    expect(registro.docs[0].getNumberOfPages()).toBeGreaterThan(2)
    expect(registro.docs[0].lastAutoTable.body).toHaveLength(60)
  })

  test('backend antigo (sem divisão do déficit) também gera', async () => {
    const { generateCampaignFailurePdf } = await carregar()
    await generateCampaignFailurePdf({
      campaign: { name: 'Campanha Teste', client_name: 'Cliente Teste' },
      summary: { stations_with_failure: 1, total_failure_days: 1, total_deficit: 4 },
      stations: [{ station: { name: 'Rádio Teste FM' }, programmed: 10, identified: 6, extras: 0, deficit: 4, failure_days: ['2026-03-05'] }],
    })
    expect(registro.salvos).toHaveLength(1)
    expect(registro.docs[0].getNumberOfPages()).toBe(1)
  })
})
