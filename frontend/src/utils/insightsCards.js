/**
 * insightsCards.js — regras de exibição dos KPI cards do /insights.
 *
 * Mora fora do componente porque DUAS telas precisam da mesma resposta: o
 * KpiCards decide se renderiza o card e o InsightsPage decide o número de
 * colunas do grid a partir dela. Enquanto a regra estava duplicada, o grid
 * contava 4 colunas com 5 cards.
 */

/**
 * O card de Bonificação aparece?
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
export function showBonificacao(data) {
  if (!data?.consolidated) return true
  const b = data?.kpis?.bonificacao
  return (b?.valor ?? 0) > 0 || (b?.count ?? 0) > 0
}
