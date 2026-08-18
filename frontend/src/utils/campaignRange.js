// União das vigências de um conjunto de campanhas: menor start_date, maior
// end_date. É a base dos presets de período quando a tela aceita seleção
// múltipla — "campanha inteira" com N campanhas só faz sentido como o
// intervalo que cobre todas, e é ele que a página usa pra semear from/to.
//
// Trabalha com as strings 'YYYY-MM-DD' direto (comparação lexicográfica ==
// cronológica nesse formato), sem Date, pra não arriscar o deslocamento de
// meia-noite UTC que jogaria a data pro dia anterior em São Paulo.
//
// Devolve null quando a lista está vazia ou nenhuma campanha tem as duas datas.
export function campaignsUnionRange(campaigns = []) {
  let start = null
  let end = null
  for (const c of campaigns) {
    if (!c?.start_date || !c?.end_date) continue
    const s = String(c.start_date).slice(0, 10)
    const e = String(c.end_date).slice(0, 10)
    if (!start || s < start) start = s
    if (!end || e > end) end = e
  }
  return start && end ? { start, end } : null
}
