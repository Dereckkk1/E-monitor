/**
 * stationCatalog.js — como a grade resolve o cadastro de uma emissora.
 *
 * Existe por causa do bug de 2026-09-02 (campanha 191 UNIUBE mostrava 3 de 4
 * emissoras): `/detections` pedia `GET /stations?limit=2000`, que é UMA PÁGINA
 * de um catálogo de 7,5 mil linhas ordenado por
 * (monitoring_status, pmm DESC NULLS LAST, name). Quando a campanha termina, o
 * supervisor marca a emissora como `paused`; se ela também tiver `pmm NULL`,
 * cai para a posição ~2.700 e some da página. A grade então descartava a linha
 * inteira em silêncio (`if (!station) return null`) — junto com 258
 * veiculações — enquanto o rodapé continuava contando 4 emissoras, porque a
 * contagem é feita ANTES dessa resolução.
 *
 * As duas metades da correção moram aqui:
 *
 *   1. `collectStationIds` + `chunkIds` — pedir o CONJUNTO EXATO ao backend
 *      (`?ids=`, que ignora paginação) em vez de uma página. É a mesma saída
 *      que o seletor do /insights já usa (components/insights/FiltersBar.jsx):
 *      subir o `limit` resolveria a truncagem e criaria um payload de 10 MB.
 *
 *   2. `resolveStation` — emissora ausente do catálogo NUNCA some da tela.
 *      Vem um placeholder marcado (`unresolved: true`) e os números da linha
 *      continuam visíveis. Isso fecha a classe do bug: se o catálogo vier
 *      incompleto de novo (API antiga que ignora `?ids=`, request em voo,
 *      erro de rede), o pior caso passa a ser um rótulo feio — não um número
 *      errado sem aviso.
 *
 * Funções puras (sem React) — testadas em stationCatalog.test.mjs.
 */

// O backend recusa `?ids=` acima disso (400 "ids max=500", handlers/stations.go).
export const MAX_IDS_PER_REQUEST = 500

export const UNRESOLVED_STATION_NAME = 'Emissora não identificada'

/**
 * Ids das emissoras que a grade pode desenhar: as das linhas (escopo atual,
 * plano e histórico — ver gridRows.js) mais as extras passadas pelo chamador
 * (target_stations da campanha). Únicos, na ordem de aparição.
 */
export function collectStationIds({ rows = [], extraIds = [] } = {}) {
  const seen = new Set()
  const out = []
  const push = (id) => {
    if (!id || seen.has(id)) return
    seen.add(id)
    out.push(id)
  }
  for (const r of rows ?? []) push(r?.stationId)
  for (const id of extraIds ?? []) push(id)
  return out
}

/**
 * Fatia a lista em requests que cabem no cap do backend. Truncar em 500 seria
 * reintroduzir a perda silenciosa que esta correção conserta, só com outro
 * número — por isso fatia, não corta. (Na prática a maior campanha em prod tem
 * 37 emissoras: o segundo bloco é seguro de existir e raro de acontecer.)
 */
export function chunkIds(ids = [], size = MAX_IDS_PER_REQUEST) {
  const out = []
  for (let i = 0; i < (ids?.length ?? 0); i += size) out.push(ids.slice(i, i + size))
  return out
}

/** Índice id → emissora. Uma vez por render, em vez de um find() por linha. */
export function indexStations(list = []) {
  return new Map((list ?? []).map(s => [s.id, s]))
}

/**
 * Emissora do catálogo, ou um placeholder marcado quando ela não veio.
 * Nunca devolve undefined — é o que impede a linha de sumir.
 */
export function resolveStation(index, stationId) {
  const st = index?.get?.(stationId)
  if (st) return st
  return { id: stationId, name: UNRESOLVED_STATION_NAME, unresolved: true }
}
