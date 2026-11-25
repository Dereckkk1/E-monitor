// dial.js — dial em português: FM com vírgula decimal, AM inteiro.
import { describe, test, expect } from 'vitest'
import { formatDial } from './dial.js'

describe('formatDial', () => {
  test.each([
    [99.9, 'FM', '99,9 FM'],
    [88.5, 'FM', '88,5 FM'],
    [101.55, 'FM', '101,55 FM'], // até 2 casas
    [1010, 'AM', '1010 AM'], // inteiro: sem decimal
    ['99.9', 'FM', '99,9 FM'], // vem string da API em alguns endpoints
    [93, 'FM', '93 FM'],
  ])('%s %s -> %s', (freq, banda, esperado) => {
    expect(formatDial(freq, banda)).toBe(esperado)
  })

  test('sem banda devolve só o número', () => {
    expect(formatDial(99.9)).toBe('99,9')
    expect(formatDial(1010, '')).toBe('1010')
  })

  test('sem frequência devolve a banda (ou vazio)', () => {
    expect(formatDial(null, 'FM')).toBe('FM')
    expect(formatDial(undefined, 'AM')).toBe('AM')
    expect(formatDial(null)).toBe('')
  })
})
