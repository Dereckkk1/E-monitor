// Testes de gridReport.js — modelo do relatório WYSIWYG da grade.
//
// Regressão 2026-09-02: `if (!st) continue` descartava do CSV/PDF a emissora
// que não estivesse no catálogo carregado (`GET /stations?limit=2000` devolve
// só a 1ª página de 7,5 mil). Na campanha 191 isso apagou 258 veiculações do
// relatório entregue ao cliente, sem nenhum aviso — o rodapé continuava
// dizendo "4 emissoras".
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildGridReportModel } from './gridReport.js'

const DIA = new Date(2026, 7, 3) // 03/08/2026, local — mesma construção da grade
const DIA_ISO = DIA.toISOString().slice(0, 10)

function modelo({ stations }) {
  const cellData = new Map([
    [`s1|t1|${DIA_ISO}`, { expected: 10, in_slot: 10, deficit: 0, bonus: 2, out_slot: 0, out_date: 0 }],
    [`s2|t1|${DIA_ISO}`, { expected: 10, in_slot: 9,  deficit: 1, bonus: 0, out_slot: 0, out_date: 0 }],
  ])
  return buildGridReportModel({
    campaign: { name: '191 UNIUBE', status: 'ativa' },
    client: { name: 'UNIUBE' },
    filteredRows: [
      { stationId: 's1', materialId: 't1', materialTitle: 'Spot 30"' },
      { stationId: 's2', materialId: 't1', materialTitle: 'Spot 30"' },
    ],
    stations,
    days: [DIA],
    cellData,
  })
}

test('emissora fora do catálogo continua no relatório, com os números dela', () => {
  // s2 (a de pmm NULL que caiu fora da página do catálogo) não vem em stations.
  const m = modelo({ stations: [{ id: 's1', name: 'Favorita', pmm: 100 }] })

  assert.deepEqual(m.byStation.map(s => s.stationId), ['s1', 's2'])
  const s2 = m.byStation.find(s => s.stationId === 's2')
  assert.equal(s2.totals.inSlot, 9)
  assert.equal(s2.totals.deficit, 1)
  assert.equal(m.grandTotals.inSlot, 19, 'total do relatório não pode perder a emissora')
})

test('emissora fora do catálogo não inventa PMM nem impacto', () => {
  const m = modelo({ stations: [{ id: 's1', name: 'Favorita', pmm: 100 }] })
  const s2 = m.byStation.find(s => s.stationId === 's2')
  assert.equal(s2.pmm, null)
  assert.equal(s2.impactos, null)
})

test('catálogo completo segue igual — placeholder é exceção, não caminho normal', () => {
  const m = modelo({
    stations: [
      { id: 's1', name: 'Favorita', pmm: 100 },
      { id: 's2', name: 'Cancella - FM (103.3)', city: 'Ituiutaba', state: 'MG' },
    ],
  })
  const s2 = m.byStation.find(s => s.stationId === 's2')
  assert.equal(s2.stationName, 'Cancella - FM (103.3)')
  assert.equal(s2.stationCity, 'Ituiutaba')
  assert.equal(m.grandTotals.inSlot, 19)
})
