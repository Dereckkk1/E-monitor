import test from 'node:test'
import assert from 'node:assert/strict'
import { describeLinkedStations } from './linkedStationsChip.js'

test('vinculado a todas as emissoras da campanha', () => {
  const out = describeLinkedStations({ linkedIds: ['a', 'b'], campaignStationIds: ['a', 'b'] })
  assert.equal(out.tone, 'success')
  assert.equal(out.label, 'em todas (2)')
})

test('vinculado a parte delas', () => {
  const out = describeLinkedStations({ linkedIds: ['a'], campaignStationIds: ['a', 'b', 'c'] })
  assert.equal(out.tone, 'warn')
  assert.equal(out.label, '1 de 3 emissoras')
})

test('material sem emissora nenhuma: nao sera detectado', () => {
  const out = describeLinkedStations({ linkedIds: [], campaignStationIds: ['a', 'b'] })
  assert.equal(out.tone, 'danger')
  assert.match(out.label, /não será detectado/)
})

// O sintoma reportado em 2026-08-31: a campanha ficou com 0 emissoras e o chip
// exibia "15 de 0 emissoras". Numero impossivel; tem que apontar o passo 2.
test('campanha sem emissoras: nao diz "N de 0", manda pro passo 2', () => {
  const out = describeLinkedStations({
    linkedIds: ['a', 'b', 'c'],
    campaignStationIds: [],
  })
  assert.equal(out.tone, 'danger')
  assert.doesNotMatch(out.label, /de 0/)
  assert.match(out.label, /passo 2/)
})

test('link apontando pra emissora fora da campanha: conta as orfas', () => {
  const out = describeLinkedStations({
    linkedIds: ['a', 'b', 'x'],
    campaignStationIds: ['a', 'b', 'c'],
  })
  assert.equal(out.tone, 'warn')
  assert.equal(out.label, '2 de 3 · 1 fora da campanha')
})

test('todas as da campanha + orfa: continua sinalizando a orfa', () => {
  const out = describeLinkedStations({
    linkedIds: ['a', 'b', 'x'],
    campaignStationIds: ['a', 'b'],
  })
  assert.equal(out.tone, 'warn')
  assert.equal(out.label, '2 de 2 · 1 fora da campanha')
})
