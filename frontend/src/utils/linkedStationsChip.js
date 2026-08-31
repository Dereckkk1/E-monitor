/**
 * Descreve, pro chip do Step 4 do wizard, a relacao entre as emissoras
 * vinculadas a UM material e as emissoras da campanha.
 *
 * Existe porque as duas listas sao colunas independentes — campaigns
 * .target_stations e campaign_materials.target_stations — e nada no banco
 * garante que a segunda esteja contida na primeira. Quando divergem, o rotulo
 * antigo (`${vinculadas} de ${daCampanha} emissoras`) produzia frases
 * impossiveis: a campanha 6fa29650 ficou com 0 emissoras e o chip anunciou
 * "15 de 0 emissoras" (2026-08-31).
 *
 * @param {object}   p
 * @param {string[]} p.linkedIds           emissoras vinculadas ao material
 * @param {string[]} p.campaignStationIds  emissoras da campanha
 * @returns {{tone: 'success'|'warn'|'danger', label: string}}
 */
export function describeLinkedStations({ linkedIds, campaignStationIds }) {
  const linked = linkedIds ?? []
  const campaign = campaignStationIds ?? []

  // A campanha nao monitora nada: o material e irrelevante ate o Step 2 ser
  // preenchido. Falar do vinculo aqui so confunde — aponta a causa.
  if (campaign.length === 0) {
    return { tone: 'danger', label: 'campanha sem emissoras — veja o passo 2' }
  }
  if (linked.length === 0) {
    return { tone: 'danger', label: 'sem emissora — não será detectado' }
  }

  const inCampaign = new Set(campaign)
  const covered = linked.filter(id => inCampaign.has(id)).length
  const orphans = linked.length - covered

  if (orphans > 0) {
    return {
      tone: 'warn',
      label: `${covered} de ${campaign.length} · ${orphans} fora da campanha`,
    }
  }
  if (covered === campaign.length) {
    return { tone: 'success', label: `em todas (${campaign.length})` }
  }
  return { tone: 'warn', label: `${covered} de ${campaign.length} emissoras` }
}
