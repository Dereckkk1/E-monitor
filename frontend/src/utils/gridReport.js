// gridReport.js — Relatório WYSIWYG da grade de /detections.
//
// Espelha exatamente o que a grade mostra (mesma view daily_play_summary, mesmo
// filtro de busca, mesmo cap-at-today) e produz um MODELO estruturado a partir
// do qual saem o CSV consolidado e o PDF por-dia. Funções puras (sem React/DOM/
// jsPDF) pra serem testáveis isoladamente — só `exportGridReportCsv` toca o DOM.
//
// Entrada canônica:
//   - filteredRows: linhas (emissora × material/tipo) JÁ filtradas pela busca
//                   (todas as páginas, não só a visível). Cada linha tem
//                   { stationId, materialId (=type_id), materialTitle, ... }.
//   - stations:     catálogo de emissoras ({ id, name, city, state, band,
//                   frequency_mhz, pmm }).
//   - days:         array de Date (o MESMO que a grade renderiza — use
//                   enumerateVisibleDays de dates.js).
//   - cellData:     Map key `${stationId}|${materialId}|${dateISO}` →
//                   { expected, in_slot, deficit, bonus, out_slot, out_date }.
//   - materialLookup: (opcional) Map/objeto key `${stationId}|${typeId}` →
//                   [{ shortId, title, durationSec }] com os materiais REAIS
//                   daquele tipo naquela emissora (a grade só conhece o tipo).
//   - pmmTargetByStation: (opcional) objeto/Map station_id → PMM no target do
//                   cliente dono da campanha (mesmo mapa que alimenta a pill
//                   de impactos-no-target de DistributionGrid.jsx, construído
//                   em DetectionsPage.jsx a partir de useClientTargetPmm).
//                   Ausente/vazio = feature não cadastrada; impactosTarget
//                   fica null em todas as emissoras e a coluna some.
//
// dateISO segue a mesma construção da grade (`d.toISOString().slice(0,10)`), que
// em America/Sao_Paulo (UTC-3) coincide com o for_date da view.

import { pad2 } from './dates'

function isoDayKey(d) { return d.toISOString().slice(0, 10) }
function dayLabel(d)  { return `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}` }

function zeroTotals() {
  return { expected: 0, inSlot: 0, deficit: 0, bonus: 0, outSlot: 0, outDate: 0 }
}

// Normaliza uma célula crua (snake_case da view) pro vocabulário camelCase do
// modelo. Célula ausente vira tudo-zero.
function normalizeCell(c) {
  return {
    expected: c?.expected ?? 0,
    inSlot:   c?.in_slot  ?? 0,
    deficit:  c?.deficit  ?? 0,
    bonus:    c?.bonus    ?? 0,
    outSlot:  c?.out_slot ?? 0,
    outDate:  c?.out_date ?? 0,
  }
}

function addInto(acc, cell) {
  acc.expected += cell.expected
  acc.inSlot   += cell.inSlot
  acc.deficit  += cell.deficit
  acc.bonus    += cell.bonus
  acc.outSlot  += cell.outSlot
  acc.outDate  += cell.outDate
}

function anyNonZero(t) {
  return !!(t.expected || t.inSlot || t.deficit || t.bonus || t.outSlot || t.outDate)
}

// Dial "FM 98,5" a partir de band + frequency_mhz (vírgula decimal pt-BR).
export function stationDial(st) {
  let freq = ''
  if (st?.frequency_mhz != null && st.frequency_mhz !== '') {
    const n = Number(st.frequency_mhz)
    if (!Number.isNaN(n)) freq = n.toFixed(1).replace('.', ',')
  }
  return [st?.band, freq].filter(Boolean).join(' ')
}

function coveragePct(expected, inSlot) {
  return expected > 0 ? Math.round((inSlot / expected) * 100) : null
}

// Resolve os materiais REAIS de uma célula (emissora × tipo). A grade é
// organizada por tipo (`daily_play_summary` agrega por type_id), então o nome
// do material não vem na linha — resolvemos aqui a partir do lookup montado na
// página (materiais da campanha por estação × tipo). Aceita Map OU objeto puro.
// Ausente → [] (sem regressão: o relatório segue mostrando só o tipo).
function resolveMaterials(lookup, stationId, typeId) {
  if (!lookup) return []
  const key = `${stationId}|${typeId}`
  const arr = typeof lookup.get === 'function' ? lookup.get(key) : lookup[key]
  return Array.isArray(arr) ? arr : []
}

// Rótulo textual dos materiais reais de um bloco — usado no CSV (uma célula) e
// como fallback de subtítulo. `#<short> <título>` por material, unidos por ' / '.
export function materialsLabel(materials) {
  if (!Array.isArray(materials) || materials.length === 0) return ''
  return materials
    .map(m => (m.shortId != null ? `#${m.shortId} ${m.title ?? ''}`.trim() : (m.title ?? '')))
    .filter(Boolean)
    .join(' / ')
}

/**
 * Constrói o modelo estruturado do relatório. Puro e determinístico.
 *
 * @returns {{
 *   header: { campaignName, clientName, status, startDate, endDate,
 *             periodLabel, filterLabel },
 *   kpis:   { ...totals, coveragePct },
 *   byStation: Array<{
 *     stationId, stationName, stationCity, stationState, stationDial,
 *     pmm, impactos,             // nível emissora: pmm × Σ in_slot; null sem pmm (nota do PDF)
 *     pmmTarget, impactosTarget, // null quando o cliente não tem PMM no target cadastrado
 *     materials: Array<{ typeId, title,
 *       materials: Array<{ shortId, title, durationSec }>,  // materiais REAIS do tipo
 *       days: Array<{ dateISO, dateLabel, ...cell }>,   // só dias não-vazios
 *       impactos, impactosTarget,  // nível material: pmm × in_slot DESTE material (linha do CSV)
 *       totals }>,
 *     totals }>,
 *   grandTotals,
 *   hasTarget,  // true se QUALQUER emissora do recorte tem PMM no target
 *   slug,
 * }}
 */
export function buildGridReportModel({
  campaign, client, filteredRows, stations, days, cellData, filterInfo = {},
  materialLookup = null, pmmTargetByStation = {},
}) {
  const stationById = new Map((stations ?? []).map(s => [s.id, s]))

  // Agrupa as linhas filtradas por emissora preservando a ordem de aparição
  // (idêntico ao byStation da grade).
  const order = []
  const rowsByStation = new Map()
  for (const r of (filteredRows ?? [])) {
    if (!rowsByStation.has(r.stationId)) {
      rowsByStation.set(r.stationId, [])
      order.push(r.stationId)
    }
    rowsByStation.get(r.stationId).push(r)
  }

  const grandTotals = zeroTotals()
  const byStation = []

  for (const stationId of order) {
    const st = stationById.get(stationId)
    if (!st) continue // emissora fora do catálogo → pulada, exatamente como a grade

    const stationTotals = zeroTotals()
    const materials = []

    // PMM resolvido ANTES do loop de materiais porque impactos é calculado nos
    // dois níveis: por material (linha do CSV, somável) e por emissora (nota do
    // PDF da grade / pílula da grid). Ver comentário no push do byStation.
    const pmm = Number(st.pmm) || 0
    const pmmTarget = pmmTargetByStation?.[stationId] ?? null

    for (const row of rowsByStation.get(stationId)) {
      const matTotals = zeroTotals()
      const dayRows = []
      for (const d of (days ?? [])) {
        const cell = normalizeCell(cellData?.get(`${stationId}|${row.materialId}|${isoDayKey(d)}`))
        addInto(matTotals, cell)
        // Só entra no detalhamento diário o dia com alguma atividade OU plano.
        // Dia totalmente vazio (0 programado / 0 tocado) é ruído e some — não
        // afeta os totais (é tudo zero).
        if (anyNonZero(cell)) {
          dayRows.push({ dateISO: isoDayKey(d), dateLabel: dayLabel(d), ...cell })
        }
      }
      materials.push({
        typeId: row.materialId,
        title:  row.materialTitle ?? '—',
        materials: resolveMaterials(materialLookup, stationId, row.materialId),
        days:   dayRows,
        totals: matTotals,
        // Impactos rateados POR MATERIAL (pmm × in_slot só deste material) —
        // é o que vai pra linha do CSV. Tem que ser por linha, e não o número
        // da emissora repetido: a coluna do CSV precisa ser somável no Excel
        // (arrastar e somar é a primeira coisa que se faz), e o CSV
        // consolidado do backend já usa essa mesma semântica por
        // material × emissora — os dois batem.
        impactos:       pmm > 0 ? Math.round(pmm * matTotals.inSlot) : null,
        impactosTarget: pmmTarget != null ? Math.round(pmmTarget * matTotals.inSlot) : null,
      })
      addInto(stationTotals, matTotals)
    }

    addInto(grandTotals, stationTotals)
    // Nível EMISSORA: pmm × Σ in_slot de todos os materiais — mesma conta do
    // StationTotalCell da grade (DistributionGrid.jsx), e é esse número que
    // vai pra nota da emissora no PDF da grade. NÃO vai pro CSV: lá cada linha
    // é (emissora × material) e repetir o total da emissora em N linhas faria
    // a coluna somar N× no Excel — por isso o CSV usa o rateio por material
    // (materials[].impactos, acima). Impactos no target só existe quando o
    // cliente tem PMM no target cadastrado pra essa emissora
    // (pmmTargetByStation vem do useClientTargetPmm em DetectionsPage.jsx);
    // ausente = null, e a coluna/nota correspondente vira "—", sem regressão
    // pra quem não cadastrou.
    const impactos = pmm > 0 ? Math.round(pmm * stationTotals.inSlot) : null
    const impactosTarget = pmmTarget != null ? Math.round(pmmTarget * stationTotals.inSlot) : null
    byStation.push({
      stationId,
      stationName:  st.name ?? '—',
      stationCity:  st.city ?? '',
      stationState: st.state ?? '',
      stationDial:  stationDial(st),
      pmm: pmm > 0 ? pmm : null,
      impactos,
      pmmTarget,
      impactosTarget,
      materials,
      totals: stationTotals,
    })
  }

  // Gate de exibição da coluna "Impactos no target": só entra quando pelo
  // menos uma emissora do recorte tem PMM no target cadastrado (mesmo
  // critério do `stations_with_target` que o backend expõe pro PDF de
  // campanha em pdfReport.js).
  const hasTarget = byStation.some(s => s.impactosTarget != null)

  const periodLabel = filterInfo.periodLabel ?? ''
  const filterLabel = buildFilterLabel(filterInfo, byStation.length)

  return {
    header: {
      campaignName: campaign?.name ?? '—',
      clientName:   client?.name ?? '',
      status:       campaign?.status ?? '',
      startDate:    campaign?.start_date ?? null,
      endDate:      campaign?.end_date ?? null,
      periodLabel,
      filterLabel,
    },
    kpis: { ...grandTotals, coveragePct: coveragePct(grandTotals.expected, grandTotals.inSlot) },
    byStation,
    grandTotals,
    hasTarget,
    slug: slugify(campaign?.name),
  }
}

// Nota humana do recorte aplicado — vai pro topo do CSV/PDF pra quem recebe
// saber que é um subconjunto filtrado.
function buildFilterLabel(filterInfo, stationCount) {
  const parts = []
  const n = filterInfo.stationCount ?? stationCount
  parts.push(`${n} emissora${n === 1 ? '' : 's'}`)
  if (filterInfo.search) parts.push(`busca "${filterInfo.search}"`)
  if (filterInfo.periodLabel) parts.push(filterInfo.periodLabel)
  return parts.join(' · ')
}

function slugify(s) {
  return String(s || 'campanha')
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || 'campanha'
}

// ── CSV ──────────────────────────────────────────────────────────
// 1 linha por (emissora × material), com os mesmos totais das pílulas do
// RowSummaryCell da grade. Separador ';' e (BOM adicionado no download) pra
// abrir limpo no Excel pt-BR, igual ao reports.go.
//
// As colunas de impactos são POR LINHA (pmm × in_slot daquele material naquela
// emissora), não o total da emissora repetido — o CSV existe pra ser somado no
// Excel, e repetir o número de nível-emissora em cada material multiplicaria o
// total pelo nº de materiais. Rateado assim a coluna soma certo e casa com a
// semântica do CSV consolidado do backend (que também é material × emissora).

// Base sempre presente. "Impactos" é coluna nova pra todo mundo (pedido
// explícito do dono, mesmo sem PMM no target cadastrado); "Impactos no
// target" só entra quando `model.hasTarget` — apendada dinamicamente em
// buildGridReportCSV, não fixa aqui, senão o CSV de quem não tem cadastro
// ganharia uma coluna sempre "—" à toa.
const CSV_HEADER = [
  'Emissora', 'Dial', 'Cidade', 'UF', 'Tipo', 'Materiais',
  'Programado', 'Tocou (faixa)', 'Déficit', 'Bônus',
  'Fora da faixa', 'Fora da data', 'Impactos',
]

function csvEscape(v) {
  const s = String(v ?? '')
  return /[;"\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

export function buildGridReportCSV(model) {
  const header = model.hasTarget ? [...CSV_HEADER, 'Impactos no target'] : CSV_HEADER
  const lines = [header.map(csvEscape).join(';')]
  for (const s of model.byStation) {
    for (const m of s.materials) {
      const row = [
        s.stationName, s.stationDial, s.stationCity, s.stationState,
        m.title, materialsLabel(m.materials),
        m.totals.expected, m.totals.inSlot, m.totals.deficit,
        m.totals.bonus, m.totals.outSlot, m.totals.outDate,
        m.impactos ?? '—',
      ]
      if (model.hasTarget) row.push(m.impactosTarget ?? '—')
      lines.push(row.map(csvEscape).join(';'))
    }
  }
  return lines.join('\r\n')
}

// ── Download (browser-only) ──────────────────────────────────────
function stampNow() {
  return new Date().toISOString().slice(0, 10).replace(/-/g, '')
}

export function exportGridReportCsv(model) {
  const csv = buildGridReportCSV(model)
  // BOM UTF-8 (U+FEFF) via fromCharCode pra o fonte ficar 100% ASCII e o Excel
  // pt-BR abrir com acentos corretos — mesma intenção do BOM do reports.go.
  const blob = new Blob([String.fromCharCode(0xFEFF) + csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `relatorio-veiculacoes-${model.slug}-${stampNow()}.csv`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
