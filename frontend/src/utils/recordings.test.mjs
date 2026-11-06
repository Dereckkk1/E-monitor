import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  coveragePercent, downloadName, gapLabels, isLive, statusInfo, todayInSaoPaulo, windowLabel, expiresLabel,
} from './recordings.js'

const rec = {
  status: 'parcial',
  starts_at: '2026-11-05T23:00:00Z',
  ends_at: '2026-11-06T00:00:00Z',
  covered_seconds: 1800,
  gaps: [{ from: '2026-11-05T23:10:00Z', to: '2026-11-05T23:40:00Z' }],
}

test('janela aparece em horário de São Paulo', () => {
  assert.equal(windowLabel(rec), '05/11/2026, 20:00 às 21:00')
})

test('cobertura em percentual da janela', () => {
  assert.equal(coveragePercent(rec), 50)
  assert.equal(coveragePercent({ ...rec, covered_seconds: 3700 }), 100, 'pre-roll não passa de 100')
})

test('sem cobertura é null, não zero', () => {
  assert.equal(coveragePercent({ ...rec, covered_seconds: null }), null)
})

test('buracos em horário de São Paulo', () => {
  assert.deepEqual(gapLabels(rec), ['sem áudio das 20:10:00 às 20:40:00'])
  assert.deepEqual(gapLabels({ ...rec, gaps: [] }), [])
})

test('isLive pede atualização automática da lista', () => {
  assert.equal(isLive([{ status: 'concluida' }]), false)
  assert.equal(isLive([{ status: 'concluida' }, { status: 'gravando' }]), true)
  assert.equal(isLive([{ status: 'programada' }]), true)
  assert.equal(isLive([{ status: 'processando' }]), true)
  assert.equal(isLive(undefined), false)
})

test('todo status tem rótulo', () => {
  for (const s of ['programada', 'gravando', 'processando', 'concluida', 'parcial', 'falhou', 'cancelada', 'expirada']) {
    assert.ok(statusInfo(s).label, s)
    assert.ok(statusInfo(s).badge, s)
  }
})

test('dia de hoje em São Paulo, não em UTC', () => {
  assert.equal(todayInSaoPaulo(new Date('2026-11-06T01:00:00Z')), '2026-11-05')
})

test('nome do arquivo sem acento e em horário de São Paulo', () => {
  assert.equal(
    downloadName({ station_name: 'Rádio Canção FM 99,9', starts_at: '2026-11-05T23:00:00Z' }),
    'gravacao-radio-cancao-fm-99-9-2026-11-05-20h00.m4a',
  )
})

test('expiração só existe com áudio', () => {
  assert.equal(expiresLabel({ expires_at: '2026-11-20T23:00:00Z', has_audio: true }), '20/11/2026')
  assert.equal(expiresLabel({ expires_at: null, has_audio: false }), '—')
  assert.equal(expiresLabel({ expires_at: '2026-11-20T23:00:00Z', has_audio: false }), '—')
})
