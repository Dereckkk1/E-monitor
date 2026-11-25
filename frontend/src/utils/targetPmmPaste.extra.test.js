// targetPmmPaste.js — parser da colagem de planilha do PMM no target. Complementa
// targetPmmPaste.test.mjs (casamento por id/nome/dial) com número BR e separadores.
import { describe, test, expect } from 'vitest'
import { normalize, parseNumberBR, parsePastedTargets } from './targetPmmPaste.js'

const EMISSORAS = [
  { station_id: 'st-1', short_id: 101, name: 'Rádio Teste FM', frequency_mhz: 99.9 },
  { station_id: 'st-2', short_id: 102, name: 'Rádio Exemplo', frequency_mhz: 88.3 },
  { station_id: 'st-3', short_id: 103, name: 'Rádio Dupla', frequency_mhz: 90.1 },
  { station_id: 'st-4', short_id: 104, name: 'Rádio Dupla', frequency_mhz: 104.5 },
]

describe('normalize', () => {
  test('tira acento, colapsa espaço, baixa a caixa e apara', () => {
    expect(normalize('  RÁDIO   São  João ')).toBe('radio sao joao')
  })
  test('nulo vira vazio', () => {
    expect(normalize(null)).toBe('')
    expect(normalize(undefined)).toBe('')
  })
})

describe('parseNumberBR', () => {
  test.each([
    ['12.345', 12345], // ponto é milhar
    ['1.234.567', 1234567],
    ['12345', 12345],
    ['1.234,56', 1235], // vírgula é decimal e arredonda
    ['12,4', 12],
    ['0', 0],
    [' 1 200 ', 1200], // espaço some
  ])('%s -> %s', (entrada, esperado) => {
    expect(parseNumberBR(entrada)).toBe(esperado)
  })

  test.each(['', '   ', 'abc', '-5', '12.34', '1,2,3', '12a', null, undefined])('inválido %j -> null', (entrada) => {
    expect(parseNumberBR(entrada)).toBeNull()
  })
})

describe('parsePastedTargets: separadores e casos de borda', () => {
  test('TAB (colagem do Excel)', () => {
    const r = parsePastedTargets('Rádio Teste FM\t120.000', EMISSORAS)
    expect(r.matched).toEqual([{ station_id: 'st-1', name: 'Rádio Teste FM', pmm_target: 120000, line: 1 }])
  })

  test('ponto-e-vírgula (CSV brasileiro)', () => {
    const r = parsePastedTargets('Rádio Exemplo;85.500', EMISSORAS)
    expect(r.matched[0]).toMatchObject({ station_id: 'st-2', pmm_target: 85500 })
  })

  test('nome e valor separados por espaço, com o dial dentro do nome desempatando duplicados', () => {
    const r = parsePastedTargets('Rádio Dupla 104,5 7.000', EMISSORAS)
    expect(r.matched).toEqual([{ station_id: 'st-4', name: 'Rádio Dupla', pmm_target: 7000, line: 1 }])
  })

  test('short_id como chave', () => {
    const r = parsePastedTargets('103;500', EMISSORAS)
    expect(r.matched[0]).toMatchObject({ station_id: 'st-3', pmm_target: 500 })
  })

  test('linha sem valor vai pra notFound; vazias são ignoradas; CRLF funciona', () => {
    const r = parsePastedTargets('Rádio Teste FM\r\n\r\nRádio Exemplo\t10', EMISSORAS)
    expect(r.notFound).toEqual([{ line: 1, raw: 'Rádio Teste FM' }])
    expect(r.matched).toHaveLength(1)
    expect(r.matched[0].line).toBe(3)
  })

  test('texto vazio ou ausente não gera nada', () => {
    expect(parsePastedTargets('', EMISSORAS)).toEqual({ matched: [], ambiguous: [], notFound: [], invalid: [] })
    expect(parsePastedTargets(undefined, EMISSORAS)).toEqual({ matched: [], ambiguous: [], notFound: [], invalid: [] })
  })

  test('valor inválido numa emissora conhecida (linha 1) NÃO é engolido como cabeçalho', () => {
    const r = parsePastedTargets('Rádio Teste FM\tabc', EMISSORAS)
    expect(r.invalid).toEqual([{ line: 1, raw: 'Rádio Teste FM', value: 'abc' }])
    expect(r.matched).toEqual([])
  })

  test('cabeçalho de planilha na linha 1 é ignorado', () => {
    const r = parsePastedTargets('Emissora\tPMM\nRádio Teste FM\t10', EMISSORAS)
    expect(r.invalid).toEqual([])
    expect(r.matched).toHaveLength(1)
  })

  test('mesmo nome sem dial é ambíguo', () => {
    const r = parsePastedTargets('Rádio Dupla\t10', EMISSORAS)
    expect(r.ambiguous).toHaveLength(1)
    expect(r.matched).toEqual([])
  })
})
