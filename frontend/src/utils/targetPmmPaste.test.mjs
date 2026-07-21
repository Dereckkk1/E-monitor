import test from 'node:test'
import assert from 'node:assert/strict'
import { parseNumberBR, parsePastedTargets } from './targetPmmPaste.js'

const stations = [
  { station_id: 'a', short_id: 12, name: 'Rádio Alfa', frequency_mhz: 99.5 },
  { station_id: 'b', short_id: 34, name: 'Radio Beta', frequency_mhz: 101.1 },
  { station_id: 'c', short_id: 56, name: 'Rádio Beta', frequency_mhz: 88.3 },
]

test('numero BR: ponto e milhar, virgula e decimal', () => {
  assert.equal(parseNumberBR('12.345'), 12345)
  assert.equal(parseNumberBR('12345'), 12345)
  assert.equal(parseNumberBR('3.400,6'), 3401)
  assert.equal(parseNumberBR(' 1 200 '), 1200)
  assert.equal(parseNumberBR('abc'), null)
  assert.equal(parseNumberBR(''), null)
  assert.equal(parseNumberBR('-5'), null)
})

test('casa por short_id exato', () => {
  const r = parsePastedTargets('12\t3400', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'a')
  assert.equal(r.matched[0].pmm_target, 3400)
})

test('casa por nome normalizado (sem acento, sem caixa)', () => {
  const r = parsePastedTargets('radio alfa;1200', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'a')
})

test('nome duplicado sem dial vira ambiguo', () => {
  const r = parsePastedTargets('radio beta\t900', stations)
  assert.equal(r.matched.length, 0)
  assert.equal(r.ambiguous.length, 1)
  assert.equal(r.ambiguous[0].line, 1)
})

test('nome duplicado COM dial desempata', () => {
  const r = parsePastedTargets('radio beta 88,3\t900', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'c')
})

test('emissora inexistente vira notFound', () => {
  const r = parsePastedTargets('Radio Fantasma\t900', stations)
  assert.equal(r.notFound.length, 1)
  assert.equal(r.notFound[0].raw, 'Radio Fantasma')
})

test('linhas vazias e cabecalho sao ignorados', () => {
  const r = parsePastedTargets('Emissora\tPMM Target\n\n12\t3400\n', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.notFound.length, 0)
})

test('valor invalido vira notFound com motivo', () => {
  const r = parsePastedTargets('12\tabc', stations)
  assert.equal(r.matched.length, 0)
  assert.equal(r.invalid.length, 1)
})
