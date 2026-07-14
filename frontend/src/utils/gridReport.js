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
//                   frequency_mhz }).
//   - days:         array de Date (o MESMO que a grade renderiza — use
//                   enumerateVisibleDays de dates.js).
//   - cellData:     Map key `${stationId}|${materialId}|${dateISO}` →
//                   { expected, in_slot, deficit, bonus, out_slot, out_date }.
//   - materialLookup: (opcional) Map/objeto key `${stationId}|${typeId}` →
//                   [{ shortId, title, durationSec }] com os materiais REAIS
//                   daquele tipo naquela emissora (a grade só conhece o tipo).
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
 *     materials: Array<{ typeId, title,
 *       materials: Array<{ shortId, title, durationSec }>,  // materiais REAIS do tipo
 *       days: Array<{ dateISO, dateLabel, ...cell }>,   // só dias não-vazios
 *       totals }>,
 *     totals }>,
 *   grandTotals,
 *   slug,
 * }}
 */
export function buildGridReportModel({
  campaign, client, filteredRows, stations, days, cellData, filterInfo = {},
  materialLookup = null,
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
      })
      addInto(stationTotals, matTotals)
    }

    addInto(grandTotals, stationTotals)
    byStation.push({
      stationId,
      stationName:  st.name ?? '—',
      stationCity:  st.city ?? '',
      stationState: st.state ?? '',
      stationDial:  stationDial(st),
      materials,
      totals: stationTotals,
    })
  }

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

const CSV_HEADER = [
  'Emissora', 'Dial', 'Cidade', 'UF', 'Tipo', 'Materiais',
  'Programado', 'Tocou (faixa)', 'Déficit', 'Bônus',
  'Fora da faixa', 'Fora da data',
]

function csvEscape(v) {
  const s = String(v ?? '')
  return /[;"\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

export function buildGridReportCSV(model) {
  const lines = [CSV_HEADER.map(csvEscape).join(';')]
  for (const s of model.byStation) {
    for (const m of s.materials) {
      lines.push([
        s.stationName, s.stationDial, s.stationCity, s.stationState,
        m.title, materialsLabel(m.materials),
        m.totals.expected, m.totals.inSlot, m.totals.deficit,
        m.totals.bonus, m.totals.outSlot, m.totals.outDate,
      ].map(csvEscape).join(';'))
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
