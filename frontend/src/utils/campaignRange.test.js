// campaignRange.js — união das vigências de várias campanhas (menor início, maior fim).
import { describe, test, expect } from 'vitest'
import { campaignsUnionRange } from './campaignRange.js'

describe('campaignsUnionRange', () => {
  test('menor start_date e maior end_date entre as campanhas', () => {
    expect(campaignsUnionRange([
      { start_date: '2026-03-10', end_date: '2026-03-20' },
      { start_date: '2026-03-01', end_date: '2026-03-15' },
      { start_date: '2026-03-12', end_date: '2026-04-05' },
    ])).toEqual({ start: '2026-03-01', end: '2026-04-05' })
  })

  test('aceita ISO completo e corta nos 10 primeiros caracteres (sem Date, sem fuso)', () => {
    expect(campaignsUnionRange([
      { start_date: '2026-03-10T00:00:00Z', end_date: '2026-03-20T00:00:00Z' },
    ])).toEqual({ start: '2026-03-10', end: '2026-03-20' })
  })

  test('campanha sem as duas datas é ignorada', () => {
    expect(campaignsUnionRange([
      { start_date: '2026-03-10' },
      null,
      { end_date: '2026-03-20' },
      { start_date: '2026-03-12', end_date: '2026-03-14' },
    ])).toEqual({ start: '2026-03-12', end: '2026-03-14' })
  })

  test('lista vazia, ausente ou sem nenhuma vigência completa devolve null', () => {
    expect(campaignsUnionRange([])).toBeNull()
    expect(campaignsUnionRange()).toBeNull()
    expect(campaignsUnionRange([{ name: 'sem datas' }])).toBeNull()
  })
})
