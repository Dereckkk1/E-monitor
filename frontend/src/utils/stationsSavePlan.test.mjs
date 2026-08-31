import test from 'node:test'
import assert from 'node:assert/strict'
import { planStationsSave } from './stationsSavePlan.js'

const CAMPAIGN = 'camp-1'
const CATALOG = ['a', 'b', 'c', 'd']

// Atalho: o caso saudavel (usuario mexeu, catalogo carregado).
function plan(over = {}) {
  return planStationsSave({
    dirty: true,
    campaignId: CAMPAIGN,
    allStationIds: CATALOG,
    selectedIds: ['a', 'b'],
    currentSelection: ['a', 'b'],
    ...over,
  })
}

test('sem campanha ainda criada: nao salva', () => {
  const out = plan({ campaignId: null })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'no-campaign')
})

test('catalogo ainda carregando: nao salva', () => {
  const out = plan({ allStationIds: [] })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'catalog-loading')
})

// A regressao que zerou a campanha 6fa29650 em 2026-08-31: selectedIds vazio
// (estado derivado ainda nao hidratado) contra uma campanha com 15 emissoras.
// Sem acao do usuario, isso NUNCA pode virar um PUT.
test('sem acao do usuario: nao salva, mesmo com a selecao divergindo do persistido', () => {
  const out = plan({ dirty: false, selectedIds: [], currentSelection: ['a', 'b', 'c'] })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'not-user-edited')
})

test('sem acao do usuario: nao salva nem quando a selecao GANHOU emissora', () => {
  const out = plan({ dirty: false, selectedIds: ['a', 'b', 'c'], currentSelection: ['a'] })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'not-user-edited')
})

test('usuario removeu uma emissora: salva a lista nova', () => {
  const out = plan({ selectedIds: ['a'], currentSelection: ['a', 'b'] })
  assert.equal(out.save, true)
  assert.deepEqual(out.ids, ['a'])
})

test('usuario limpou tudo: acao explicita continua salvando lista vazia', () => {
  const out = plan({ selectedIds: [], currentSelection: ['a', 'b'] })
  assert.equal(out.save, true)
  assert.deepEqual(out.ids, [])
})

test('selecao igual ao persistido: nao salva (corta o loop do refetch)', () => {
  const out = plan({ selectedIds: ['b', 'a'], currentSelection: ['a', 'b'] })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'unchanged')
})

// target_stations e uuid[] sem FK: pode referenciar emissora deletada ou fora
// da janela do catalogo. O PUT substitui a lista inteira, entao esses ids
// precisam viajar junto ou somem em silencio.
test('preserva id que o catalogo nao resolve', () => {
  const out = plan({
    selectedIds: ['a'],
    currentSelection: ['a', 'b', 'orfa'],
  })
  assert.equal(out.save, true)
  assert.deepEqual(out.ids.slice().sort(), ['a', 'orfa'])
})

test('id nao resolvido nao conta como mudanca', () => {
  const out = plan({
    selectedIds: ['a', 'b'],
    currentSelection: ['a', 'b', 'orfa'],
  })
  assert.equal(out.save, false)
  assert.equal(out.reason, 'unchanged')
})
