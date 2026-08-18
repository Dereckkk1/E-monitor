import test from 'node:test'
import assert from 'node:assert/strict'
import { tokenize, normalizeDial, dialMatchesToken, highlightSegments } from './search.js'

test('tokenize quebra por espaço, normaliza acento e descarta token curto', () => {
  assert.deepEqual(tokenize('Jb 99.9 RJ'), ['jb', '99.9', 'rj'])
  assert.deepEqual(tokenize('São Paulo'), ['sao', 'paulo'])
  // Token de 1 char cai fora, a menos que tenha dígito.
  assert.deepEqual(tokenize('a 5 fm'), ['5', 'fm'])
})

test('normalizeDial espelha o backend', () => {
  // Vírgula é como se escreve dial no Brasil.
  assert.equal(normalizeDial('99,9'), '99.9')
  assert.equal(normalizeDial('99.9'), '99.9')
  // NUMERIC(6,2) do banco: o zero morto do decimal não conta.
  assert.equal(normalizeDial('99.90'), '99.9')
  assert.equal(normalizeDial('100,00'), '100')
  // Zero significativo do inteiro não pode ser comido.
  assert.equal(normalizeDial('1080'), '1080')
  assert.equal(normalizeDial('990'), '990')
  // Não-numéricos não viram dial.
  assert.equal(normalizeDial('jb'), '')
  assert.equal(normalizeDial('rj'), '')
  assert.equal(normalizeDial('99.9.9'), '')
  assert.equal(normalizeDial(''), '')
  assert.equal(normalizeDial(null), '')
})

test('dialMatchesToken casa exato e por prefixo', () => {
  assert.equal(dialMatchesToken(99.9, '99,9'), true)
  assert.equal(dialMatchesToken(99.9, '99.90'), true)
  assert.equal(dialMatchesToken(99.9, '99'), true)   // prefixo
  assert.equal(dialMatchesToken(99.9, '98'), false)
  assert.equal(dialMatchesToken(1080, '1080'), true)
  assert.equal(dialMatchesToken(null, '99.9'), false)
  assert.equal(dialMatchesToken(99.9, 'jb'), false)
})

test('highlightSegments marca cada token separadamente', () => {
  const segs = highlightSegments('JB FM Brasília', tokenize('jb bras'))
  assert.deepEqual(segs, [
    { text: 'JB', hit: true },
    { text: ' FM ', hit: false },
    { text: 'Bras', hit: true },
    { text: 'ília', hit: false },
  ])
})

test('highlightSegments recorta o texto ACENTUADO nas posições certas', () => {
  // O match acontece no texto sem acento ("sertao"), mas o corte tem que sair
  // no original — senão o índice escorrega e o <mark> pega a letra errada.
  const segs = highlightSegments('Rádio Sertão FM', tokenize('sertao'))
  assert.deepEqual(segs, [
    { text: 'Rádio ', hit: false },
    { text: 'Sertão', hit: true },
    { text: ' FM', hit: false },
  ])
})

test('highlightSegments funde trechos sobrepostos de tokens diferentes', () => {
  const segs = highlightSegments('Joinville', ['join', 'inville'])
  assert.deepEqual(segs, [{ text: 'Joinville', hit: true }])
})

test('highlightSegments sem token devolve o texto inteiro sem hit', () => {
  assert.deepEqual(highlightSegments('Antena 1', []), [{ text: 'Antena 1', hit: false }])
  assert.deepEqual(highlightSegments('', ['x']), [])
  assert.deepEqual(highlightSegments(null, ['x']), [])
})

test('highlightSegments sem match devolve o texto inteiro sem hit', () => {
  assert.deepEqual(highlightSegments('Antena 1', ['zzz']), [{ text: 'Antena 1', hit: false }])
})
