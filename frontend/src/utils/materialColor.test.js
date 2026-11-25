// materialColor.js — cor estável por material (mesma identidade visual no relatório).
import { describe, test, expect } from 'vitest'
import { materialColor, MATERIAL_PALETTE } from './materialColor.js'

describe('materialColor', () => {
  test('é determinística: a mesma chave dá sempre a mesma cor', () => {
    const id = '00000000-0000-0000-0000-0000000000a1'
    expect(materialColor(id)).toBe(materialColor(id))
  })

  test('sempre devolve uma cor da paleta', () => {
    for (const k of ['a', 'b', 'material-1', 'material-2', '00000000-0000-0000-0000-000000000001', 'ç', '🎵']) {
      expect(MATERIAL_PALETTE).toContain(materialColor(k))
    }
  })

  test('sem chave cai na primeira cor da paleta', () => {
    expect(materialColor('')).toBe(MATERIAL_PALETTE[0])
    expect(materialColor(null)).toBe(MATERIAL_PALETTE[0])
    expect(materialColor(undefined)).toBe(MATERIAL_PALETTE[0])
  })

  test('espalha chaves diferentes por várias cores da paleta', () => {
    const usadas = new Set(Array.from({ length: 200 }, (_, i) => materialColor(`material-${i}`)))
    expect(usadas.size).toBeGreaterThanOrEqual(10)
  })

  test('a paleta tem 16 cores hexadecimais distintas', () => {
    expect(MATERIAL_PALETTE).toHaveLength(16)
    expect(new Set(MATERIAL_PALETTE).size).toBe(16)
    expect(MATERIAL_PALETTE.every(c => /^#[0-9A-F]{6}$/i.test(c))).toBe(true)
  })
})
