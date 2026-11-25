// recordingEditor.js / recordings.js — bordas que recordingEditor.test.mjs e
// recordings.test.mjs não exercitam: nome do .zip, buracos fora de ordem ou
// inválidos, e emissora sem frequência.
import { describe, test, expect } from 'vitest'
import { zipName, gapMarkers, audioStartMs, defaultClipName } from './recordingEditor.js'
import { stationSub } from './recordings.js'

const base = {
  station_name: 'Rádio Teste',
  starts_at: '2026-11-10T17:00:00Z', // 14:00 em São Paulo
  ends_at: '2026-11-10T18:00:00Z',
}

describe('zipName', () => {
  test('"Trechos <emissora> <dia>" com o dia em horário de São Paulo', () => {
    expect(zipName(base)).toBe('Trechos Rádio Teste 2026-11-10')
  })
  test('gravação que começa de madrugada UTC ainda é o dia anterior em Brasília', () => {
    expect(zipName({ ...base, starts_at: '2026-11-11T01:30:00Z' })).toBe('Trechos Rádio Teste 2026-11-10')
  })
})

describe('buracos', () => {
  const t0 = Date.parse(base.starts_at)

  test('buracos fora de ordem saem ordenados no arquivo', () => {
    const rec = {
      ...base,
      gaps: [
        { from: '2026-11-10T17:30:00Z', to: '2026-11-10T17:31:00Z' },
        { from: '2026-11-10T17:10:00Z', to: '2026-11-10T17:10:30Z' },
      ],
    }
    const marcadores = gapMarkers(rec, t0)
    expect(marcadores.map(m => m.seconds)).toEqual([30, 60])
    expect(marcadores[0].t).toBeLessThan(marcadores[1].t)
    // o primeiro buraco está 10 min depois do início; o segundo, 20 min + 30s de buraco antes
    expect(marcadores[0].t).toBeCloseTo(600)
    expect(marcadores[1].t).toBeCloseTo(30 * 60 - 30)
  })

  test('buraco com fim antes do início é descartado', () => {
    const rec = { ...base, gaps: [{ from: '2026-11-10T17:10:00Z', to: '2026-11-10T17:09:00Z' }] }
    expect(gapMarkers(rec, t0)).toEqual([])
  })

  test('sem lista de buracos o pré-roll é só o que sobra de áudio, limitado a 20s', () => {
    // arquivo com 1h00m50s pra uma janela de 1h: sobram 50s, mas o pré-roll máximo é 20s
    expect(audioStartMs(base, 3650)).toBe(t0 - 20_000)
    // arquivo exatamente do tamanho da janela: sem pré-roll
    expect(audioStartMs(base, 3600)).toBe(t0)
    // arquivo mais curto que a janela: nunca negativo
    expect(audioStartMs(base, 3000)).toBe(t0)
  })
})

describe('defaultClipName', () => {
  test('trechos no mesmo minuto ganham segundos pra não repetir o nome', () => {
    const de = Date.parse('2026-11-10T17:08:00Z')
    const ate = Date.parse('2026-11-10T17:08:13Z')
    expect(defaultClipName(base, de, ate)).toBe('Rádio Teste 14h08m00s-14h08m13s')
  })
})

describe('stationSub', () => {
  test('sem frequência mostra só a praça', () => {
    expect(stationSub({ city: 'Cidade Teste', state: 'SP' })).toBe('Cidade Teste/SP')
  })
  test('sem nada devolve vazio', () => {
    expect(stationSub({})).toBe('')
  })
  test('frequência com banda e praça', () => {
    expect(stationSub({ frequency_mhz: 99.9, band: 'FM', city: 'Cidade Teste', state: 'SP' })).toBe('99,9 FM · Cidade Teste/SP')
  })
})
