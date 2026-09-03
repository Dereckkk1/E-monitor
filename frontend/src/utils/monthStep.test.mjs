// Aritmética das setas ‹ › do seletor de competência.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { shiftMonth } from './monthStep.js'

const HOJE = new Date(2026, 8, 3) // 03/09/2026 (mês 8 = setembro no Date)

test('avança e volta um mês dentro do mesmo ano', () => {
  assert.equal(shiftMonth('2026-09', 1, HOJE), '2026-10')
  assert.equal(shiftMonth('2026-09', -1, HOJE), '2026-08')
})

test('vira o ano nas duas pontas', () => {
  assert.equal(shiftMonth('2026-12', 1, HOJE), '2027-01')
  assert.equal(shiftMonth('2026-01', -1, HOJE), '2025-12')
})

test('mantém o zero à esquerda — o input type=month exige AAAA-MM', () => {
  assert.equal(shiftMonth('2026-10', -1, HOJE), '2026-09')
  assert.match(shiftMonth('2026-11', -2, HOJE), /^\d{4}-0\d$/)
})

// Campo vazio existe de verdade: a competência do /admin/pos-venda é opcional
// (vazio = todas). Clicar a seta ali parte do mês atual, que é o mesmo default
// das outras telas — nunca de 1970 nem de string vazia.
test('campo vazio parte do mês atual', () => {
  assert.equal(shiftMonth('', -1, HOJE), '2026-08')
  assert.equal(shiftMonth('', 1, HOJE), '2026-10')
  assert.equal(shiftMonth(null, -1, HOJE), '2026-08')
  assert.equal(shiftMonth(undefined, 1, HOJE), '2026-10')
})

test('valor inválido também cai no mês atual em vez de propagar NaN', () => {
  assert.equal(shiftMonth('não é mês', 1, HOJE), '2026-10')
  assert.equal(shiftMonth('2026-13', -1, HOJE), '2026-08')
  assert.equal(shiftMonth('2026', 1, HOJE), '2026-10')
})

test('delta zero devolve o próprio mês (normalizado)', () => {
  assert.equal(shiftMonth('2026-07', 0, HOJE), '2026-07')
  assert.equal(shiftMonth('', 0, HOJE), '2026-09')
})

test('salto de vários meses', () => {
  assert.equal(shiftMonth('2026-09', 12, HOJE), '2027-09')
  assert.equal(shiftMonth('2026-09', -12, HOJE), '2025-09')
})
