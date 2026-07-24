import { test } from 'node:test'
import assert from 'node:assert/strict'
import { formatPeriod } from './PeriodLabel.js'

test('mês de calendário cheio → "jul/2026"', () => {
  assert.equal(formatPeriod('2026-07-01', '2026-07-31'), 'jul/2026')
})
test('range parcial no mesmo mês → "01–24 jul 2026"', () => {
  assert.equal(formatPeriod('2026-07-01', '2026-07-24'), '01–24 jul 2026')
})
test('acumulado (from = acumuladoFrom) → "acumulado até 24 jul 2026"', () => {
  assert.equal(formatPeriod('2000-01-01', '2026-07-24', '2000-01-01'), 'acumulado até 24 jul 2026')
})
