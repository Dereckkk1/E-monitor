import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  coveragePercent, downloadName, errorMessage, gapLabels, isLive, statusInfo, todayInSaoPaulo, windowLabel, expiresLabel,
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
  assert.equal(coveragePercent({ ...rec, status: 'concluida', covered_seconds: 3700 }), 100, 'pre-roll não passa de 100')
})

test('cobertura arredonda para baixo: com buraco nunca mostra 100%', () => {
  // 3599s de 3600s = 99,97%.
  assert.equal(coveragePercent({ ...rec, covered_seconds: 3599 }), 99)
  assert.equal(coveragePercent({ ...rec, status: 'concluida', covered_seconds: 3600 }), 100)
})

test('parcial nunca chega a 100%, nem com pré-roll e burst somando mais que a janela', () => {
  assert.equal(coveragePercent({ ...rec, covered_seconds: 3600 }), 99)
  assert.equal(coveragePercent({ ...rec, covered_seconds: 3700 }), 99)
})

test('cancelada: a janela termina no cancelamento, não no fim programado', () => {
  const canceled = {
    ...rec, status: 'cancelada', canceled_at: '2026-11-05T23:30:00Z', covered_seconds: 1800,
  }
  assert.equal(coveragePercent(canceled), 100, 'gravou os 30 min que eram possíveis')
  assert.equal(coveragePercent({ ...canceled, covered_seconds: 900 }), 50)
})

test('cancelamento depois do fim da janela não encurta nada', () => {
  assert.equal(coveragePercent({ ...rec, canceled_at: '2026-11-06T02:00:00Z' }), 50)
})

test('parada no meio e fechada como parcial usa o cancelamento também', () => {
  // O backend considera canceled_at, seja qual for o status final.
  const stopped = { ...rec, status: 'parcial', canceled_at: '2026-11-05T23:30:00Z', covered_seconds: 1500 }
  assert.equal(coveragePercent(stopped), 83)
})

test('sem cobertura é null, não zero', () => {
  assert.equal(coveragePercent({ ...rec, covered_seconds: null }), null)
})

test('buracos em horário de São Paulo', () => {
  assert.deepEqual(gapLabels(rec), ['sem áudio das 20:10:00 às 20:40:00'])
  assert.deepEqual(gapLabels({ ...rec, gaps: [] }), [])
})

test('parcial sem nenhum buraco listado não quebra a tela', () => {
  const noList = { ...rec, gaps: [], covered_seconds: 3300 }
  assert.deepEqual(gapLabels(noList), [])
  assert.equal(coveragePercent(noList), 91)
})

test('lista longa de buracos mostra os 20 primeiros e resume o resto', () => {
  const gaps = Array.from({ length: 23 }, (_, i) => ({
    from: new Date(Date.UTC(2026, 10, 5, 23, i, 0)).toISOString(),
    to: new Date(Date.UTC(2026, 10, 5, 23, i, 10)).toISOString(),
  }))
  const labels = gapLabels({ ...rec, gaps })
  assert.equal(labels.length, 21)
  assert.equal(labels[0], 'sem áudio das 20:00:00 às 20:00:10')
  assert.equal(labels[19], 'sem áudio das 20:19:00 às 20:19:10')
  assert.equal(labels[20], 'e mais 3 trechos')
  assert.equal(gapLabels({ ...rec, gaps: gaps.slice(0, 20) }).length, 20, 'exatamente 20 não resume')
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

const GENERIC = 'Não foi possível concluir. Tente de novo.'
const httpErr = (status, data) => ({ response: { status, data } })

test('erro 4xx com texto da API aparece como veio', () => {
  assert.equal(errorMessage(httpErr(400, 'fim deve ser depois do início\n')), 'fim deve ser depois do início')
  assert.equal(errorMessage(httpErr(409, 'já existe uma gravação nesse horário')), 'já existe uma gravação nesse horário')
})

test('HTML de erro do proxy nunca aparece cru', () => {
  assert.equal(errorMessage(httpErr(502, '<html><body>Bad gateway</body></html>')), GENERIC)
  assert.equal(errorMessage(httpErr(400, '<html>x</html>')), GENERIC, 'mesmo em 4xx')
})

test('5xx usa a mensagem genérica', () => {
  assert.equal(errorMessage(httpErr(500, 'pq: connection refused')), GENERIC)
})

test('mensagens técnicas da API não vão para a tela', () => {
  for (const m of ['forbidden', 'invalid json', 'invalid id', 'internal error']) {
    assert.equal(errorMessage(httpErr(403, m)), GENERIC, m)
  }
  assert.equal(errorMessage(httpErr(400, 'x'.repeat(201))), GENERIC, 'texto longo demais')
})

test('sem resposta (erro de rede) usa a mensagem genérica', () => {
  assert.equal(errorMessage(new Error('Network Error')), GENERIC)
  assert.equal(errorMessage(undefined), GENERIC)
  assert.equal(errorMessage(httpErr(400, { error: 'x' })), GENERIC, 'corpo que não é texto')
})
