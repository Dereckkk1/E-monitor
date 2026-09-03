// Quando o card de Bonificação aparece no /insights.
//
// Até 2026-09-03 o card sumia sempre que a seleção tivesse QUALQUER emissora
// consolidada — e 14 dos 28 clientes têm ao menos uma na seleção típica, então
// metade da base nunca via a bonificação precificada. A regra abaixo é a que
// devolve o número sem inventar preço pra emissora que não tem preço unitário.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { showBonificacao } from './insightsCards.js'

test('seleção sem consolidada: card sempre aparece, mesmo zerado', () => {
  // Aqui o zero é informação verdadeira: houve preço, não houve bônus.
  assert.equal(showBonificacao({ consolidated: false, kpis: { bonificacao: { valor: 0, count: 0 } } }), true)
  assert.equal(showBonificacao({ consolidated: false, kpis: { bonificacao: { valor: 120, count: 4 } } }), true)
})

test('seleção mista: card aparece com o valor das por-inserção', () => {
  assert.equal(showBonificacao({ consolidated: true, kpis: { bonificacao: { valor: 20, count: 2 } } }), true)
})

test('seleção 100% consolidada: card some — zero ali é ausência de preço, não de bônus', () => {
  assert.equal(showBonificacao({ consolidated: true, kpis: { bonificacao: { valor: 0, count: 0 } } }), false)
})

test('contagem sem valor ainda mostra o card (bônus precificável a preço zero)', () => {
  assert.equal(showBonificacao({ consolidated: true, kpis: { bonificacao: { valor: 0, count: 3 } } }), true)
})

test('payload incompleto não quebra nem inventa card', () => {
  assert.equal(showBonificacao({ consolidated: true }), false)
  assert.equal(showBonificacao({}), true)
  assert.equal(showBonificacao(), true)
})
