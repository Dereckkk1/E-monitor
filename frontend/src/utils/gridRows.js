/**
 * buildGridRows — monta as linhas (emissora × tipo) da grade de /detections.
 *
 * A grade agrega por TIPO, não por material: `daily_play_summary` devolve
 * (campaign, type_id, station_id, for_date), então uma linha representa
 * "Spot 30\" na Rádio X", podendo ter N materiais por trás.
 *
 * As linhas saem da UNIÃO de três fontes, nesta precedência:
 *
 *   1. ESCOPO ATUAL — `campaign_materials` × `target_stations`. É o que a
 *      campanha monitora HOJE. Linha normal.
 *   2. PLANO — pares (emissora, tipo) cobertos por uma `distribution_rule`,
 *      mesmo sem material vinculado. Linha "fantasma" (`ghost: true`), igual
 *      ao step 4 do wizard (spec 2026-05-25-distribution-without-materials §4.4).
 *   3. HISTÓRICO — pares com veiculação no período, lidos do próprio summary.
 *      Linha `outOfScope: true`.
 *
 * Por que a união existe: o escopo é mutável e não versionado no tempo. Tirar
 * a emissora do `target_stations` de um material (em /campaigns → materiais)
 * fazia a linha inteira sumir da grade levando junto TODO o histórico dela E
 * TODO o déficit dela — as células já vinham do backend
 * (`daily_play_summary_for` não olha `campaign_materials`) e eram descartadas
 * por não existir linha onde pendurá-las. O efeito prático era perverso: a
 * campanha "melhorava" na tela ao desvincular material, porque as falhas
 * sumiam junto. O backend nunca deixou de contá-las (/admin/station-failures,
 * sininho e emails leem a mesma função e não olham `campaign_materials`), então
 * a grade divergia das outras telas.
 *
 * O escopo governa o que o worker monitora daqui pra frente; ele não pode
 * reescrever nem o que já tocou nem o que estava planejado.
 *
 * Linha só-histórica tem `expected = 0`, então as tocadas aparecem como
 * bônus/órfã — a leitura honesta: tocou, mas hoje não há plano ali.
 *
 * Função pura para poder ser testada sem React (gridRows.test.mjs).
 */

// Total de veiculações registradas numa linha do summary, em qualquer
// categoria. `bonus` já embute GREATEST(0, in_slot - expected) + órfãs, mas
// aqui só interessa "houve tocada?", então somar é seguro para o teste > 0.
function playCount(s) {
  return (s.in_slot ?? 0) + (s.out_slot ?? 0) + (s.out_date ?? 0) + (s.bonus ?? 0)
}

function makeRow(stationId, typeId, typeById, distributionRules, origin) {
  const type = typeById[typeId]
  if (!type) return null
  const matching = distributionRules.filter(rule =>
    rule.type_id === typeId && (rule.station_ids ?? []).includes(stationId))
  const first = matching[0]
  return {
    stationId,
    materialId: typeId,
    materialTitle: type.name,
    typeColor: type.color ?? '#94a3b8',
    ruleSummary: first
      ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
      : null,
    extraRules: Math.max(0, matching.length - 1),
    // Regra sem material vinculado — mesmo rótulo do wizard ("aguardando áudio").
    ghost: origin === 'rule',
    // Só existe porque tocou: sem material no escopo e sem plano.
    outOfScope: origin === 'history',
  }
}

export function buildGridRows({
  campaignMaterials = [],
  materialsById = {},
  summary = [],
  typeById = {},
  distributionRules = [],
} = {}) {
  // stationId → Map<typeId, origin>, com precedência material > rule > history:
  // um par que tem material é linha normal mesmo que também tenha regra e
  // histórico. Espelha o "material sobrescreve fantasma" do DistributionStep.
  const byStation = new Map()
  const RANK = { material: 3, rule: 2, history: 1 }
  const add = (sid, tid, origin) => {
    if (!sid || !tid) return
    if (!byStation.has(sid)) byStation.set(sid, new Map())
    const types = byStation.get(sid)
    const cur = types.get(tid)
    if (!cur || RANK[origin] > RANK[cur]) types.set(tid, origin)
  }

  // 1. Escopo atual.
  for (const cm of campaignMaterials) {
    const mat = materialsById[cm.material_id]
    if (!mat?.type_id) continue
    for (const sid of cm.target_stations ?? []) add(sid, mat.type_id, 'material')
  }

  // 2. Plano: toda regra materializa a linha, com ou sem material vinculado.
  //    Sem isso o déficit some da grade quando o material sai do escopo — e a
  //    campanha parece melhorar sozinha.
  for (const r of distributionRules) {
    for (const sid of r.station_ids ?? []) add(sid, r.type_id, 'rule')
  }

  // 3. Histórico: pares que tocaram no período.
  for (const s of summary) {
    if (playCount(s) <= 0) continue
    add(s.station_id, s.type_id, 'history')
  }

  // As linhas de uma emissora precisam sair contíguas: os consumidores agrupam
  // por ordem de aparição (uniqueStationIds/paginação em /detections,
  // buildGridReportModel). A ordem de inserção do Map já garante isso, e mantém
  // as emissoras do escopo antes das que entraram por plano/histórico.
  const rows = []
  for (const [sid, types] of byStation.entries()) {
    for (const [tid, origin] of types.entries()) {
      const row = makeRow(sid, tid, typeById, distributionRules, origin)
      if (row) rows.push(row)
    }
  }
  return rows
}
