import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  audioStartMs, bucketPeaks, clockAt, defaultClipName, formatClock, formatOffset, formatSpan, gapMarkers, gapsInside,
  loadClips, makeClip, offsetAt, parseClockInput, rulerStep, saveClips, updateClip,
} from './recordingEditor.js'

// 14:00–15:00 em São Paulo (17:00–18:00 UTC), com um buraco de 60s às 14:10.
const rec = {
  station_name: 'Rádio Teste',
  starts_at: '2026-11-10T17:00:00Z',
  ends_at: '2026-11-10T18:00:00Z',
  gaps: [{ from: '2026-11-10T17:10:00Z', to: '2026-11-10T17:11:00Z' }],
}
const T0 = Date.parse('2026-11-10T17:00:00Z')

test('pré-roll é o que o arquivo tem a mais que a janela menos os buracos, até 20s', () => {
  // janela 3600s − buraco 60s = 3540s esperados; arquivo de 3550s → 10s de pré-roll.
  assert.equal(audioStartMs(rec, 3550), T0 - 10_000)
  assert.equal(audioStartMs(rec, 3500), T0, 'arquivo menor que o esperado: sem pré-roll')
  assert.equal(audioStartMs(rec, 9999), T0 - 20_000, 'teto de 20s')
})

test('hora do ar pula os buracos, e a volta cai no ponto do corte', () => {
  const start = T0
  assert.equal(formatClock(clockAt(rec, 0, start)), '14:00:00')
  assert.equal(formatClock(clockAt(rec, 599, start)), '14:09:59')
  // O segundo 600 do arquivo já é depois do buraco.
  assert.equal(formatClock(clockAt(rec, 600, start)), '14:11:00')
  assert.equal(offsetAt(rec, Date.parse('2026-11-10T17:11:30Z'), start), 630)
  assert.equal(offsetAt(rec, Date.parse('2026-11-10T17:10:30Z'), start), 600, 'dentro do buraco')
  assert.equal(offsetAt(rec, T0 - 60_000, start), 0)
})

test('buraco vira marcador pontual no arquivo', () => {
  const marks = gapMarkers(rec, T0)
  assert.deepEqual(marks, [{ t: 600, seconds: 60 }])
  assert.equal(gapsInside(marks, 500, 700).length, 1)
  assert.equal(gapsInside(marks, 0, 600).length, 0, 'na ponta não conta')
})

test('cancelada mede o pré-roll até o cancelamento', () => {
  const c = { ...rec, gaps: [], canceled_at: '2026-11-10T17:30:00Z' }
  assert.equal(audioStartMs(c, 1805), T0 - 5_000)
})

test('formatos', () => {
  assert.equal(formatOffset(65.25), '1:05.3')
  assert.equal(formatOffset(3725), '1:02:05.0')
  assert.equal(formatSpan(45), '45s')
  assert.equal(formatSpan(65), '1m05s')
  assert.equal(formatSpan(3720), '1h02m')
})

test('hora digitada vale no dia da gravação', () => {
  assert.equal(parseClockInput(rec, '14:30'), Date.parse('2026-11-10T17:30:00Z'))
  assert.equal(parseClockInput(rec, '14h30'), Date.parse('2026-11-10T17:30:00Z'))
  assert.equal(parseClockInput(rec, '9:05:07'), Date.parse('2026-11-10T12:05:07Z'))
  assert.equal(parseClockInput(rec, '25:00'), null)
  assert.equal(parseClockInput(rec, 'abc'), null)
})

test('nome automático acompanha as alças até a pessoa renomear', () => {
  assert.equal(defaultClipName(rec, T0, T0 + 15 * 60_000), 'Rádio Teste 14h00-14h15')
  assert.equal(defaultClipName(rec, T0 + 8 * 60_000, T0 + 8 * 60_000 + 13_000), 'Rádio Teste 14h08m00s-14h08m13s')
  let c = makeClip(rec, 120, 60, 3550, T0)
  assert.deepEqual([c.from, c.to], [60, 120], 'ordem corrigida')
  assert.equal(c.name, 'Rádio Teste 14h01-14h02')
  c = updateClip(rec, c, { to: 300 }, 3550, T0)
  assert.equal(c.name, 'Rádio Teste 14h01-14h05')
  c = updateClip(rec, c, { name: 'Entrevista' }, 3550, T0)
  c = updateClip(rec, c, { from: 0 }, 3550, T0)
  assert.equal(c.name, 'Entrevista')
  c = updateClip(rec, c, { to: 99999 }, 3550, T0)
  assert.equal(c.to, 3550, 'preso à duração')
})

test('régua escolhe um passo redondo', () => {
  assert.equal(rulerStep(3600, 900), 600)
  assert.equal(rulerStep(60, 900), 10)
})

test('picos por pixel pegam o máximo da faixa', () => {
  const peaks = Uint8Array.from([1, 9, 3, 4, 7, 2, 0, 5])
  assert.deepEqual([...bucketPeaks(peaks, 2, 0, 4, 4)], [9, 4, 7, 5])
  assert.deepEqual([...bucketPeaks(peaks, 2, 1, 2, 2)], [3, 4])
})

test('trechos guardados no navegador', () => {
  const mem = new Map()
  const storage = { getItem: k => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v), removeItem: k => mem.delete(k) }
  saveClips('x', [{ id: 'a', from: 1, to: 2, name: 'a' }], storage)
  assert.equal(loadClips('x', storage).length, 1)
  saveClips('x', [], storage)
  assert.equal(mem.size, 0)
  mem.set('rec-editor:y', '{quebrado')
  assert.deepEqual(loadClips('y', storage), [])
  const broken = { getItem: () => { throw new Error('bloqueado') } }
  assert.deepEqual(loadClips('z', broken), [])
})
