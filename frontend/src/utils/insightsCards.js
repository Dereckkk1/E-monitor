/**
 * insightsCards.js — regras de exibição dos KPI cards do /insights.
 *
 * Mora fora do componente porque DUAS telas precisam da mesma resposta: o
 * KpiCards decide se renderiza o card e o InsightsPage decide o número de
 * colunas do grid a partir dela. Enquanto a regra estava duplicada, o grid
 * contava 4 colunas com 5 cards.
 *
 * O pós-venda também consome daqui (showBonificacaoDe): os números do
 * documento vêm do MESMO insights.Compute, então a regra de exibição tem que
 * ser a mesma — foi ela ficar duplicada que deixou o pós-venda escondendo a
 * bonificação depois que o /insights parou de escondê-la.
 */

/**
 * O card de Bonificação aparece? Núcleo da regra, sobre os três valores que
 * decidem — sem depender do formato do payload de quem pergunta.
 *
 * Fora do modo fornecedor: sempre. O valor pode ser zero, e zero ali é
 * informação verdadeira — existe preço por inserção, não houve bônus.
 *
 * Em seleção com emissora consolidada (modo fornecedor): só quando existe
 * parcela por-inserção precificável. Numa seleção 100% consolidada o valor é
 * zero por AUSÊNCIA DE PREÇO (o pacote é pela emissora, não por inserção),
 * não por ausência de bonificação — exibir "R$ 0,00" ali afirmaria que não
 * houve bônus, que é outra coisa. As tocadas continuam no breakdown de
 * veiculações e em impactos.
 *
 * Antes de 2026-09-03 o card sumia em TODA seleção com consolidada, o que
 * escondia a bonificação das emissoras por-inserção de metade da base.
 */
export function showBonificacaoDe({ consolidated, valor, count } = {}) {
  if (!consolidated) return true
  return (valor ?? 0) > 0 || (count ?? 0) > 0
}

/** Adaptador pro payload do /insights (bonificação aninhada em kpis). */
export function showBonificacao(data) {
  return showBonificacaoDe({
    consolidated: data?.consolidated,
    valor: data?.kpis?.bonificacao?.valor,
    count: data?.kpis?.bonificacao?.count,
  })
}

/**
 * Teto de cards por fileira na linha de KPIs.
 *
 * 6 é a contagem máxima que a tela produz hoje (target + bonificação), e a
 * escolha é deliberada: com teto 5 os 6 cards viravam 3+3, cada card ficava
 * largo e ALTO, e as duas fileiras somadas à faixa do gênero empurravam os
 * gráficos pra fora da primeira tela. Numa fileira só, cada card fica com
 * ~245px em 1600px de viewport — largura em que os valores já couberam.
 */
export const MAX_KPI_CARDS_PER_ROW = 6

/**
 * Quantos cards a linha de KPIs vai renderizar, com as MESMAS condições dos
 * componentes (KpiCards + InvestmentToggleCard). O card de Gênero não entra:
 * ele ocupa a linha inteira sozinho.
 *
 * Impactos e CPM são fixos; target acrescenta os dois "no target";
 * bonificação segue showBonificacao; Investido some se o payload não trouxer
 * (InvestmentToggleCard devolve null).
 */
export function kpiCardCount(data) {
  const k = data?.kpis ?? {}
  let n = 2
  if ((k.stations_with_target ?? 0) > 0) n += 2
  if (showBonificacao(data)) n += 1
  if (k.investido) n += 1
  return n
}

/**
 * Em quantas colunas dividir a linha pra NENHUMA fileira ficar com buraco.
 *
 * Divide em fileiras iguais respeitando o teto de MAX_KPI_CARDS_PER_ROW. Hoje
 * isso significa uma fileira única (3, 4, 5 ou 6 cards, todos na mesma linha),
 * e o cálculo continua existindo pra contagem que estoure o teto: 7 vira 4+3,
 * não 6+1 com cinco células vazias ao lado. Quando a divisão não é exata, o
 * flex-grow da última fileira fecha o resto (.in-row--cards em
 * InsightsPage.css).
 */
function columnsForCount(n) {
  const total = Math.max(1, n)
  const rows = Math.ceil(total / MAX_KPI_CARDS_PER_ROW)
  return Math.ceil(total / rows)
}

export function kpiColumns(data) {
  return columnsForCount(kpiCardCount(data))
}

// Exposto pra teste: a regra de distribuição vale pra qualquer contagem, não
// só pras 3..6 que a tela produz hoje.
kpiColumns.forCount = columnsForCount
