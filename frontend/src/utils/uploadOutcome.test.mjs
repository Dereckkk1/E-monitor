import test from 'node:test'
import assert from 'node:assert/strict'
import { planUploadOutcome } from './uploadOutcome.js'

const mat = { id: 'm-1', short_id: 221, title: 'UNIFIQUE - COBERTURA NACIONAL CXS - SPOT 30' }

test('201 = material novo: segue o fluxo normal de fingerprint', () => {
  const out = planUploadOutcome({ status: 201, material: mat, alreadyLinkedIds: new Set() })
  assert.equal(out.reused, false)
  assert.equal(out.shouldLink, true)
  assert.equal(out.stage, 'fingerprinting')
  assert.equal(out.notice, null)
})

test('200 = dedup por sha: nao encena fingerprint, avisa reuso', () => {
  const out = planUploadOutcome({ status: 200, material: mat, alreadyLinkedIds: new Set() })
  assert.equal(out.reused, true)
  assert.equal(out.stage, 'reused')
  assert.equal(out.shouldLink, true)
  assert.match(out.notice, /já está cadastrado/i)
  assert.match(out.notice, /COBERTURA NACIONAL/)
  assert.match(out.notice, /221/)
})

test('200 + ja vinculado: NAO relinka (o upsert sobrescreveria as emissoras)', () => {
  const out = planUploadOutcome({
    status: 200, material: mat, alreadyLinkedIds: new Set(['m-1']),
  })
  assert.equal(out.reused, true)
  assert.equal(out.shouldLink, false, 'relinkar reescreveria target_stations')
  assert.match(out.notice, /nada foi alterado/i)
})

test('material sem short_id nao quebra a mensagem', () => {
  const out = planUploadOutcome({
    status: 200, material: { id: 'm-2', title: 'SEM ID' }, alreadyLinkedIds: new Set(),
  })
  assert.match(out.notice, /SEM ID/)
  assert.doesNotMatch(out.notice, /undefined|null|NaN/)
})
