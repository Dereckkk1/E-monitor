// Testes de stationCatalog.js — resolução de emissoras da grade de /detections.
//
// Contexto (bug 2026-09-02): /detections resolvia os rótulos das emissoras com
// `GET /stations?limit=2000`, que é UMA PÁGINA de um catálogo de 7,5 mil linhas
// ordenada por (monitoring_status, pmm DESC NULLS LAST, name). Emissora
// `paused` com `pmm NULL` cai na posição ~2700 e some da página — a grade
// descartava a linha inteira em silêncio (`if (!station) return null`) enquanto
// o rodapé continuava contando 4 emissoras. Campanha 191 (UNIUBE) exibia 3 de
// 4, escondendo 258 veiculações.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { collectStationIds, chunkIds, indexStations, resolveStation } from './stationCatalog.js'

// ── collectStationIds ────────────────────────────────────────────
// O conjunto pedido ao backend tem que ser EXATAMENTE o que a grade pode
// desenhar: toda emissora com linha (escopo, plano ou histórico) mais as da
// campanha. Nada de página.

test('coleta os ids das linhas, sem repetir e na ordem de aparição', () => {
  const ids = collectStationIds({
    rows: [
      { stationId: 'a' }, { stationId: 'b' }, { stationId: 'a' }, { stationId: 'c' },
    ],
  })
  assert.deepEqual(ids, ['a', 'b', 'c'])
})

test('inclui emissoras da campanha que ainda não têm linha', () => {
  const ids = collectStationIds({ rows: [{ stationId: 'a' }], extraIds: ['b', 'a'] })
  assert.deepEqual(ids, ['a', 'b'])
})

test('ignora id vazio/nulo e entrada ausente', () => {
  assert.deepEqual(collectStationIds({ rows: [{ stationId: null }, { stationId: '' }] }), [])
  assert.deepEqual(collectStationIds({}), [])
  assert.deepEqual(collectStationIds(), [])
})

// ── chunkIds ─────────────────────────────────────────────────────
// O backend recusa `?ids=` com mais de 500 (400 "ids max=500"). Fatiar é o que
// impede a correção de reintroduzir o próprio bug que ela conserta: truncar em
// 500 seria a mesma perda silenciosa, só com outro número.

test('fatia em blocos do tamanho pedido sem perder nenhum id', () => {
  const ids = Array.from({ length: 501 }, (_, i) => `id-${i}`)
  const chunks = chunkIds(ids, 500)
  assert.equal(chunks.length, 2)
  assert.equal(chunks[0].length, 500)
  assert.equal(chunks[1].length, 1)
  assert.deepEqual(chunks.flat(), ids)
})

test('lista menor que o bloco vira um bloco só; lista vazia não vira bloco', () => {
  assert.deepEqual(chunkIds(['a', 'b'], 500), [['a', 'b']])
  assert.deepEqual(chunkIds([], 500), [])
})

// ── resolveStation ───────────────────────────────────────────────
// A regra que fecha a classe do bug: emissora fora do catálogo NUNCA some da
// tela. Vem um placeholder marcado, e os números da linha continuam visíveis.

test('emissora presente no catálogo é devolvida como está', () => {
  const idx = indexStations([{ id: 'a', name: 'Favorita', pmm: 3794 }])
  const st = resolveStation(idx, 'a')
  assert.equal(st.name, 'Favorita')
  assert.equal(st.pmm, 3794)
  assert.ok(!st.unresolved)
})

test('emissora ausente vira placeholder marcado, nunca undefined', () => {
  const idx = indexStations([{ id: 'a', name: 'Favorita' }])
  const st = resolveStation(idx, 'zzz')
  assert.equal(st.id, 'zzz')
  assert.equal(st.unresolved, true)
  assert.equal(typeof st.name, 'string')
  assert.ok(st.name.length > 0, 'placeholder precisa de rótulo legível')
})

test('catálogo vazio (ex.: fetch ainda em voo) resolve todo mundo como placeholder', () => {
  const idx = indexStations([])
  assert.equal(resolveStation(idx, 'a').unresolved, true)
  assert.equal(resolveStation(undefined, 'a').unresolved, true)
})
