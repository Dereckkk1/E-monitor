// stationStatus.js — rótulo e classe do selo de monitoring_status.
import { describe, test, expect } from 'vitest'
import { STATUS_META, statusMetaFor } from './stationStatus.js'

describe('statusMetaFor', () => {
  test.each([
    ['active', 'Ativa', 'badge-success'],
    ['calibrating', 'Calibrando', 'badge-warning'],
    ['paused', 'Sem campanha ativa', 'badge-neutral'],
    ['error', 'Erro', 'badge-danger'],
  ])('%s -> %s (%s)', (status, label, cls) => {
    expect(statusMetaFor(status)).toEqual({ label, cls })
    expect(STATUS_META[status]).toEqual({ label, cls })
  })

  test('status novo/desconhecido aparece como veio, em selo neutro (não quebra a listagem)', () => {
    expect(statusMetaFor('migrating')).toEqual({ label: 'migrating', cls: 'badge-neutral' })
  })
})
