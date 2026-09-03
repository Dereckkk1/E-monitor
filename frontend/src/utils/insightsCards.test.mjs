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

// ── Distribuição da linha de KPIs ────────────────────────────────
//
// A regra de QUANDO cada card aparece não muda; o que se calcula aqui é em
// quantas colunas eles se dividem pra nenhuma fileira ficar com buraco. Com
// auto-fit o navegador enfiava 6 numa linha e sobrava 1 sozinho na seguinte,
// com 4 células vazias ao lado.
import { kpiCardCount, kpiColumns } from './insightsCards.js'

const base = { kpis: { investido: { executado: 10 }, bonificacao: { valor: 0, count: 0 } } }
const comTarget = (d) => ({ ...d, kpis: { ...d.kpis, stations_with_target: 12 } })
const comBonus = (d) => ({ ...d, kpis: { ...d.kpis, bonificacao: { valor: 20, count: 2 } } })

test('conta os cards que a tela realmente rende', () => {
  // impactos + cpm + investido
  assert.equal(kpiCardCount({ ...base, consolidated: true }), 3)
  // + bonificação
  assert.equal(kpiCardCount(comBonus({ ...base, consolidated: true })), 4)
  // + impactos no target e cpm no target
  assert.equal(kpiCardCount(comTarget({ ...base, consolidated: true })), 5)
  assert.equal(kpiCardCount(comBonus(comTarget({ ...base, consolidated: true }))), 6)
})

test('sem investido no payload o card não conta', () => {
  assert.equal(kpiCardCount({ consolidated: true, kpis: {} }), 2)
})

test('toda contagem que a tela produz hoje (3 a 6) cabe numa fileira só', () => {
  assert.equal(kpiColumns({ ...base, consolidated: true }), 3)
  assert.equal(kpiColumns(comBonus({ ...base, consolidated: true })), 4)
  assert.equal(kpiColumns(comTarget({ ...base, consolidated: true })), 5)
  // 6 numa linha, não 3+3: duas fileiras de cards altos empurravam os
  // gráficos pra fora da primeira tela.
  assert.equal(kpiColumns(comBonus(comTarget({ ...base, consolidated: true }))), 6)
})

test('nunca devolve 0 (grid com zero colunas some da tela)', () => {
  assert.equal(kpiColumns({ kpis: {} }) >= 1, true)
  assert.equal(kpiColumns() >= 1, true)
})

test('acima do teto continua fechando fileiras equilibradas', () => {
  // Blindagem: se um card novo entrar na linha, a distribuição não estoura
  // pra 7 numa fileira nem deixa 1 sozinho com 5 buracos do lado.
  assert.equal(kpiColumns.forCount(7), 4)   // 4+3
  assert.equal(kpiColumns.forCount(8), 4)   // 4+4
  assert.equal(kpiColumns.forCount(12), 6)  // 6+6
  assert.equal(kpiColumns.forCount(13), 5)  // 5+5+3
})
