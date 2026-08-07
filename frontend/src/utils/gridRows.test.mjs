import test from 'node:test'
import assert from 'node:assert/strict'
import { buildGridRows } from './gridRows.js'

const TIPO_SPOT = 'type-spot'
const TIPO_TEST = 'type-test'
const EMISSORA_A = 'st-a'
const EMISSORA_B = 'st-b'

const typeById = {
  [TIPO_SPOT]: { id: TIPO_SPOT, name: 'Spot 30"', color: '#ff0000' },
  [TIPO_TEST]: { id: TIPO_TEST, name: 'Testemunhal', color: '#00ff00' },
}

const materialsById = {
  'mat-1': { id: 'mat-1', type_id: TIPO_SPOT, title: 'Spot A' },
  'mat-2': { id: 'mat-2', type_id: TIPO_SPOT, title: 'Spot B' },
  'mat-3': { id: 'mat-3', type_id: TIPO_TEST, title: 'Testemunhal A' },
}

// linha do daily_play_summary
function sum(station_id, type_id, counts = {}) {
  return {
    station_id, type_id, for_date: '2026-07-10',
    expected: 0, in_slot: 0, out_slot: 0, out_date: 0, bonus: 0, deficit: 0,
    ...counts,
  }
}

test('linha em escopo aparece com os dados da regra', () => {
  const rows = buildGridRows({
    campaignMaterials: [{ material_id: 'mat-1', target_stations: [EMISSORA_A] }],
    materialsById,
    summary: [],
    typeById,
    distributionRules: [{
      type_id: TIPO_SPOT, station_ids: [EMISSORA_A],
      plays_per_day: 3, time_start: '07:00', time_end: '19:00',
    }],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].stationId, EMISSORA_A)
  assert.equal(rows[0].materialId, TIPO_SPOT)
  assert.equal(rows[0].materialTitle, 'Spot 30"')
  assert.equal(rows[0].ruleSummary, '3×/dia 07:00–19:00')
  assert.equal(rows[0].outOfScope, false)
  assert.equal(rows[0].ghost, false)
})

test('par fora do escopo COM veiculação no periodo aparece marcado', () => {
  // Cenário do bug: o operador tirou EMISSORA_A do target_stations do material,
  // mas houve tocada lá no período consultado.
  const rows = buildGridRows({
    campaignMaterials: [],
    materialsById,
    summary: [sum(EMISSORA_A, TIPO_SPOT, { in_slot: 4 })],
    typeById,
    distributionRules: [],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].stationId, EMISSORA_A)
  assert.equal(rows[0].materialId, TIPO_SPOT)
  assert.equal(rows[0].outOfScope, true)
})

test('conta tocada de qualquer categoria, não só in_slot', () => {
  for (const campo of ['in_slot', 'out_slot', 'out_date', 'bonus']) {
    const rows = buildGridRows({
      campaignMaterials: [],
      materialsById,
      summary: [sum(EMISSORA_A, TIPO_SPOT, { [campo]: 2 })],
      typeById,
      distributionRules: [],
    })
    assert.equal(rows.length, 1, `${campo} deveria gerar linha`)
  }
})

test('regra sem material vira linha fantasma — o déficit NÃO pode sumir', () => {
  // O caso que fazia a campanha "melhorar" ao desvincular material: a regra
  // continua existindo, o backend continua contando a falha, mas a grade
  // escondia a linha inteira. Zero tocada, déficit cheio.
  const rows = buildGridRows({
    campaignMaterials: [],
    materialsById,
    summary: [sum(EMISSORA_A, TIPO_SPOT, { expected: 5, deficit: 5 })],
    typeById,
    distributionRules: [{
      type_id: TIPO_SPOT, station_ids: [EMISSORA_A],
      plays_per_day: 5, time_start: '06:00', time_end: '23:00',
    }],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].ghost, true)
  assert.equal(rows[0].outOfScope, false)
  assert.equal(rows[0].ruleSummary, '5×/dia 06:00–23:00')
})

test('sem material, sem regra e sem tocada não vira linha', () => {
  const rows = buildGridRows({
    campaignMaterials: [],
    materialsById,
    summary: [sum(EMISSORA_A, TIPO_SPOT, { expected: 0 })],
    typeById,
    distributionRules: [],
  })
  assert.equal(rows.length, 0)
})

test('material vence regra: par com os dois é linha normal, não fantasma', () => {
  const rows = buildGridRows({
    campaignMaterials: [{ material_id: 'mat-1', target_stations: [EMISSORA_A] }],
    materialsById,
    summary: [],
    typeById,
    distributionRules: [{
      type_id: TIPO_SPOT, station_ids: [EMISSORA_A],
      plays_per_day: 2, time_start: '08:00', time_end: '12:00',
    }],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].ghost, false)
})

test('regra vence histórico: par com os dois é fantasma, não fora-de-escopo', () => {
  const rows = buildGridRows({
    campaignMaterials: [],
    materialsById,
    summary: [sum(EMISSORA_A, TIPO_SPOT, { in_slot: 3, expected: 5, deficit: 2 })],
    typeById,
    distributionRules: [{
      type_id: TIPO_SPOT, station_ids: [EMISSORA_A],
      plays_per_day: 5, time_start: '06:00', time_end: '23:00',
    }],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].ghost, true)
  assert.equal(rows[0].outOfScope, false)
})

test('tipo ainda em escopo por outro material não duplica linha', () => {
  // mat-1 saiu de EMISSORA_A, mat-2 (mesmo tipo) continua lá.
  const rows = buildGridRows({
    campaignMaterials: [{ material_id: 'mat-2', target_stations: [EMISSORA_A] }],
    materialsById,
    summary: [sum(EMISSORA_A, TIPO_SPOT, { in_slot: 4 })],
    typeById,
    distributionRules: [],
  })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].outOfScope, false)
})

test('linhas da mesma emissora saem contíguas, escopo antes do histórico', () => {
  const rows = buildGridRows({
    campaignMaterials: [
      { material_id: 'mat-1', target_stations: [EMISSORA_A] },
      { material_id: 'mat-3', target_stations: [EMISSORA_B] },
    ],
    materialsById,
    summary: [
      sum(EMISSORA_A, TIPO_TEST, { in_slot: 1 }), // histórico só em A
      sum(EMISSORA_B, TIPO_SPOT, { in_slot: 1 }), // histórico só em B
    ],
    typeById,
    distributionRules: [],
  })
  assert.deepEqual(
    rows.map(r => [r.stationId, r.materialId, r.outOfScope]),
    [
      [EMISSORA_A, TIPO_SPOT, false],
      [EMISSORA_A, TIPO_TEST, true],
      [EMISSORA_B, TIPO_TEST, false],
      [EMISSORA_B, TIPO_SPOT, true],
    ],
  )
})

test('tipo desconhecido no catálogo é ignorado, não quebra', () => {
  const rows = buildGridRows({
    campaignMaterials: [],
    materialsById,
    summary: [sum(EMISSORA_A, 'type-fantasma', { in_slot: 9 })],
    typeById,
    distributionRules: [],
  })
  assert.equal(rows.length, 0)
})

test('material sem tipo não entra no escopo (grade agrega por tipo)', () => {
  const rows = buildGridRows({
    campaignMaterials: [{ material_id: 'mat-sem-tipo', target_stations: [EMISSORA_A] }],
    materialsById: { 'mat-sem-tipo': { id: 'mat-sem-tipo', type_id: null } },
    summary: [],
    typeById,
    distributionRules: [],
  })
  assert.equal(rows.length, 0)
})

test('entradas vazias/ausentes não quebram', () => {
  assert.deepEqual(buildGridRows({}), [])
  assert.deepEqual(
    buildGridRows({ campaignMaterials: [{ material_id: 'mat-1' }], materialsById, typeById }),
    [],
  )
})
