/**
 * monthStep.js — aritmética das setas ‹ › do seletor de competência.
 *
 * Separado do componente porque virar o ano e manter o zero à esquerda são as
 * duas coisas que quebram em silêncio: `AAAA-MM` sem pad vira um valor que o
 * `<input type="month">` recusa, e o campo aparece vazio sem erro nenhum.
 */

/** 'AAAA-MM' a partir de um Date, com zero à esquerda. */
function toMonthValue(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

/**
 * Desloca uma competência em N meses.
 *
 * @param {string} value  'AAAA-MM' (aceita vazio/nulo/inválido)
 * @param {number} delta  meses a somar (negativo volta)
 * @param {Date}   today  base pra quando `value` não é utilizável
 * @returns {string} 'AAAA-MM'
 *
 * Valor vazio ou inválido parte do mês ATUAL: a competência do
 * /admin/pos-venda é opcional (vazio = todas as competências), e clicar a seta
 * ali tem que cair no mesmo mês que as outras telas já trazem por padrão — não
 * em 1970, que é onde `new Date('')` leva.
 */
export function shiftMonth(value, delta, today = new Date()) {
  const m = /^(\d{4})-(\d{2})$/.exec(String(value ?? ''))
  const mes = m ? Number(m[2]) : 0
  const base = (m && mes >= 1 && mes <= 12)
    ? new Date(Number(m[1]), mes - 1, 1)
    : new Date(today.getFullYear(), today.getMonth(), 1)

  // Date normaliza mês fora de [0,11] virando o ano sozinho — dezembro + 1 vai
  // pra janeiro do ano seguinte sem aritmética manual.
  return toMonthValue(new Date(base.getFullYear(), base.getMonth() + delta, 1))
}
