/**
 * Decide se o auto-save do Step 2 do wizard deve disparar um
 * PUT /campaigns/{id}/stations, e com qual lista.
 *
 * Existe como funcao pura por causa do incidente 2026-08-31: a campanha
 * 6fa29650 perdeu as 15 emissoras porque o effect de save do StationsStep
 * rodava a partir de ESTADO DERIVADO (hidratacao, refetch do invalidate,
 * remontagem) e nao de acao do usuario. O log mostrou dois PUTs a 968ms um do
 * outro — o segundo, 781ms depois do refetch disparado pelo onSuccess do
 * primeiro — e foi o segundo que gravou a lista vazia.
 *
 * A regra que fecha a classe inteira e o `dirty`: nada que o usuario nao
 * tenha feito pode virar escrita. Sem isso, qualquer caminho que deixe
 * `selectedIds` momentaneamente fora de sincronia com `currentSelection`
 * (e sao varios) vira um PUT destrutivo, porque o endpoint substitui
 * target_stations por inteiro.
 *
 * @param {object}   p
 * @param {boolean}  p.dirty            usuario mexeu na selecao desde o ultimo save
 * @param {?string}  p.campaignId       null enquanto a campanha nao foi criada
 * @param {string[]} p.allStationIds    ids do catalogo ja carregado
 * @param {string[]} p.selectedIds      selecao visivel na tela
 * @param {string[]} p.currentSelection target_stations persistido na campanha
 * @returns {{save: boolean, ids: ?string[], reason: string}}
 */
export function planStationsSave({
  dirty,
  campaignId,
  allStationIds,
  selectedIds,
  currentSelection,
}) {
  const persisted = currentSelection ?? []

  if (!campaignId) return { save: false, ids: null, reason: 'no-campaign' }
  if (!allStationIds || allStationIds.length === 0) {
    return { save: false, ids: null, reason: 'catalog-loading' }
  }
  if (!dirty) return { save: false, ids: null, reason: 'not-user-edited' }

  // Ids persistidos que o catalogo nao resolve — emissora deletada (a coluna
  // e uuid[] sem FK) ou fora da janela do pre-fetch. Como o PUT substitui a
  // lista inteira, eles precisam viajar junto: sem isso, mexer em QUALQUER
  // emissora visivel derrubaria as invisiveis em silencio.
  const known = new Set(allStationIds)
  const unresolved = persisted.filter(id => !known.has(id))

  const visible = selectedIds ?? []
  const ids = unresolved.length === 0 ? visible : [...visible, ...unresolved]

  const unchanged = ids.length === persisted.length &&
    ids.every(id => persisted.includes(id))
  if (unchanged) return { save: false, ids: null, reason: 'unchanged' }

  return { save: true, ids, reason: 'user-edit' }
}
