// pdfReport.js — Gerador de PDF de relatório de campanha.
//
// Segue o design system Radiocheck/E-monitor (DESIGN.md):
//   - Tinta de ação: #E81E75 (rosa "tertiary"), usada em destaques.
//   - Tipografia de cabeçalho: Space Grotesk (carregada via Google Fonts).
//   - Tipografia de corpo: helvetica (built-in do jsPDF — embed da Fira Sans
//     Condensed exigiria conversão TTF→base64, que adicionaria ~200kb sem
//     ganho visível em tabelas. Mantemos helvetica e usamos os mesmos
//     tons/cores pra preservar a identidade.)
//   - Logo E-monitor no canto superior esquerdo (vem de /public).
//
// Entrada: payload do GET /reports/campaigns/{id}/summary.
// Saída: dispara download `relatorio-{slug}-{stamp}.pdf` no navegador.

import { jsPDF } from 'jspdf'
import autoTable from 'jspdf-autotable'

// ── Design tokens ────────────────────────────────────────────────
// Mantidos em sync com src/index.css. Se mudar lá, mudar aqui — não há
// import direto de CSS variables dentro de Canvas/jsPDF.
const TOKENS = {
  action:       [232, 30, 117],   // #E81E75
  actionLight:  [252, 231, 243],  // #fce7f3
  text:         [6, 5, 91],       // #06055B
  text2:        [75, 85, 99],     // #4b5563
  text3:        [156, 163, 175],  // #9ca3af
  border:       [226, 232, 240],  // #e2e8f0
  surface2:     [241, 245, 249],  // #f1f5f9
  white:        [255, 255, 255],
}

// ── Helpers de formatação ────────────────────────────────────────
function pad2(n) { return String(n).padStart(2, '0') }
function fmtDate(d) {
  if (!d) return '—'
  const dt = (d instanceof Date) ? d : new Date(d)
  if (isNaN(dt.getTime())) return '—'
  return `${pad2(dt.getDate())}/${pad2(dt.getMonth()+1)}/${dt.getFullYear()}`
}
function fmtPeriod(from, to, campStart, campEnd) {
  const start = from || campStart
  const end   = to   || campEnd
  return `${fmtDate(start)} – ${fmtDate(end)}`
}
function fmtNumber(n) {
  return Number(n || 0).toLocaleString('pt-BR')
}
function slugify(s) {
  return String(s || 'campanha')
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || 'campanha'
}

// ── Logo loading ─────────────────────────────────────────────────
// Carrega o PNG da logo E-monitor via fetch e converte pra dataURL.
// Cached em módulo pra não rebaixar a cada PDF gerado.
let _logoPromise = null
async function loadLogoDataURL() {
  if (_logoPromise) return _logoPromise
  _logoPromise = (async () => {
    try {
      const resp = await fetch('/E-monitor%20logo.png')
      if (!resp.ok) return null
      const blob = await resp.blob()
      return await new Promise((resolve) => {
        const fr = new FileReader()
        fr.onload = () => resolve(fr.result)
        fr.onerror = () => resolve(null)
        fr.readAsDataURL(blob)
      })
    } catch {
      return null
    }
  })()
  return _logoPromise
}

// Pré-carrega a logo eagerly — o usuário sempre acaba clicando em PDF, e
// poupamos um round-trip no momento do click. Idempotente.
export function prefetchReportLogo() {
  loadLogoDataURL()
}

// ── Status badge (capa) ──────────────────────────────────────────
const STATUS_LABEL = {
  programada: 'Programada',
  ativa: 'Ativa',
  concluida: 'Concluída',
  cancelada: 'Cancelada',
}
const STATUS_COLORS = {
  programada: { bg: [219, 234, 254], fg: [37, 99, 235] },
  ativa:      { bg: [220, 252, 231], fg: [22, 163, 74]  },
  concluida:  { bg: [243, 244, 246], fg: [75, 85, 99]   },
  cancelada:  { bg: [254, 226, 226], fg: [220, 38, 38]  },
}

// ── Layout primitives ────────────────────────────────────────────

function setColor(doc, kind, rgb) {
  if (kind === 'text') doc.setTextColor(rgb[0], rgb[1], rgb[2])
  if (kind === 'fill') doc.setFillColor(rgb[0], rgb[1], rgb[2])
  if (kind === 'draw') doc.setDrawColor(rgb[0], rgb[1], rgb[2])
}

// Card outline com borda fininha e raio. jsPDF arrendonda via roundedRect.
function drawCard(doc, x, y, w, h, { fill = TOKENS.white } = {}) {
  setColor(doc, 'fill', fill)
  setColor(doc, 'draw', TOKENS.border)
  doc.setLineWidth(0.3)
  doc.roundedRect(x, y, w, h, 3, 3, 'FD')
}

// Acha o maior fontSize (dentro de [min, max]) em que `text` cabe em `maxWidth`
// com a fonte já setada. Usado pelo drawKPI pra não estourar a caixa quando o
// número de KPIs sobe (5 boxes ficam bem mais estreitos que 3/4) ou quando o
// valor/label é mais longo que o desenho original previa (ex.: "Impactos no
// target" e totais de impactos na casa dos milhões). Sem isso, texto largo
// simplesmente transborda pra fora da caixa (jsPDF não clipa por padrão).
function fitFontSize(doc, text, maxWidth, { max, min, font }) {
  doc.setFont(font[0], font[1])
  let size = max
  doc.setFontSize(size)
  while (size > min && doc.getTextWidth(String(text)) > maxWidth) {
    size -= 0.5
    doc.setFontSize(size)
  }
  return size
}

// KPI box: número grande em rosa, label cinza em cima. Label e valor encolhem
// automaticamente se não couberem na largura da caixa (ver fitFontSize).
function drawKPI(doc, x, y, w, h, label, value) {
  drawCard(doc, x, y, w, h)
  const maxW = w - 10 // 6mm de início + ~4mm de folga à direita

  setColor(doc, 'text', TOKENS.text3)
  const labelText = String(label).toUpperCase()
  fitFontSize(doc, labelText, maxW, { max: 8, min: 5.5, font: ['helvetica', 'normal'] })
  doc.text(labelText, x + 6, y + 7)

  setColor(doc, 'text', TOKENS.action)
  const valueText = String(value)
  fitFontSize(doc, valueText, maxW, { max: 20, min: 11, font: ['helvetica', 'bold'] })
  doc.text(valueText, x + 6, y + h - 6)
}

// Hero da capa: barra rosa fininha no topo + título + meta da campanha.
function drawHero(doc, summary, marginX, y) {
  const pageW = doc.internal.pageSize.getWidth()
  const w = pageW - marginX * 2

  // Faixa de identidade no topo do hero (acento rosa de 2pt).
  setColor(doc, 'fill', TOKENS.action)
  doc.rect(marginX, y, w, 2.5, 'F')

  // Card branco em baixo.
  drawCard(doc, marginX, y + 2.5, w, 36)

  // Título "Relatório de Veiculações" em cinza menor.
  setColor(doc, 'text', TOKENS.text3)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(9)
  doc.text('RELATÓRIO DE VEICULAÇÕES', marginX + 8, y + 11)

  // Nome da campanha (grande, navy).
  setColor(doc, 'text', TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(20)
  const name = doc.splitTextToSize(summary.campaign?.name || '—', w - 60)
  doc.text(name[0], marginX + 8, y + 21)

  // Linha de meta: cliente · período.
  setColor(doc, 'text', TOKENS.text2)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(10)
  const period = fmtPeriod(
    summary.period?.from, summary.period?.to,
    summary.campaign?.start_date, summary.campaign?.end_date,
  )
  const clientName = summary.client?.name || '—'
  doc.text(`${clientName}  ·  ${period}`, marginX + 8, y + 31)

  // Status badge no canto direito do hero.
  const status = summary.campaign?.status || 'concluida'
  const sLabel = STATUS_LABEL[status] || status
  const sColor = STATUS_COLORS[status] || STATUS_COLORS.concluida
  const sW = doc.getTextWidth(sLabel) + 10
  const sX = marginX + w - sW - 8
  const sY = y + 9
  setColor(doc, 'fill', sColor.bg)
  doc.roundedRect(sX, sY, sW, 7, 3.5, 3.5, 'F')
  setColor(doc, 'text', sColor.fg)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(8)
  doc.text(sLabel, sX + 5, sY + 4.8)
}

// Linha rodapé padrão em cada página.
function drawFooter(doc, totalPages) {
  const pageW = doc.internal.pageSize.getWidth()
  const pageH = doc.internal.pageSize.getHeight()
  const pageCur = doc.getCurrentPageInfo().pageNumber
  // Linha cinza claro acima do rodapé pra ancorar visualmente.
  setColor(doc, 'draw', TOKENS.border)
  doc.setLineWidth(0.2)
  doc.line(15, pageH - 12, pageW - 15, pageH - 12)
  // Texto esquerda: gerado por E-monitor + timestamp.
  setColor(doc, 'text', TOKENS.text3)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(8)
  doc.text(`Gerado por E-monitor · ${fmtDate(new Date())}`, 15, pageH - 6)
  // Texto direita: paginação.
  const pg = `Página ${pageCur} de ${totalPages}`
  const pgW = doc.getTextWidth(pg)
  doc.text(pg, pageW - 15 - pgW, pageH - 6)
}

// ── Cores de categoria + legenda (compartilhadas pelos dois PDFs) ─
// Espelham as pílulas de status da grade (DayDetailModal.jsx:1594-1598) pra o
// relatório usar o mesmo semáforo que a tela. Tons levemente escurecidos pra
// legibilidade em impressão.
const CAT = {
  prog:    [100, 116, 139],  // #64748b  Programado (neutro)
  tocou:   [22, 128, 61],    // #15803d  Dentro da faixa (verde)
  outSlot: [180, 83, 9],     // #b45309  Fora da faixa (âmbar)
  outDate: [124, 58, 237],   // #7c3aed  Fora da data (roxo)
  deficit: [185, 28, 28],    // #b91c1c  Déficit (vermelho)
  bonus:   [29, 78, 216],    // #1d4ed8  Bônus (azul)
}

const LEGEND_ITEMS = [
  ['Tocou', CAT.tocou],
  ['Fora da faixa', CAT.outSlot],
  ['Fora da data', CAT.outDate],
  ['Déficit', CAT.deficit],
  ['Bônus', CAT.bonus],
]

// Desenha a legenda de cores numa linha (bolinha + rótulo). Devolve o y após.
function drawLegend(doc, x, y, items = LEGEND_ITEMS) {
  const r = 1.3
  let cx = x
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(7.5)
  for (const [label, color] of items) {
    setColor(doc, 'fill', color)
    doc.circle(cx + r, y - 1, r, 'F')
    setColor(doc, 'text', TOKENS.text2)
    doc.text(label, cx + r * 2 + 1.6, y)
    cx += r * 2 + 1.6 + doc.getTextWidth(label) + 7
  }
  return y + 4.5
}

// Cor do número numa coluna de status: colorida quando > 0, apagada quando 0
// (a não ser em linha de total, que fica navy). Reduz ruído visual.
function statusTextColor(val, color, isTotal) {
  if (val > 0) return color
  return isTotal ? TOKENS.text : TOKENS.text3
}

// Subtítulo do bloco de material no PDF WYSIWYG: tipo + material(is) REAIS
// resolvidos (a grade só conhece o tipo). 1 material → "Spot 30\" · #241
// VERISURE Alarme 30s"; N materiais → "Spot 30\" · 2 materiais: A, B".
function materialSubtitle(m) {
  const mats = Array.isArray(m.materials) ? m.materials : []
  const type = m.title || '—'
  if (mats.length === 0) return type
  if (mats.length === 1) {
    const x = mats[0]
    const bits = []
    if (x.shortId != null) bits.push(`#${x.shortId}`)
    if (x.title) bits.push(x.title)
    if (x.durationSec != null) bits.push(`${Math.round(x.durationSec)}s`)
    return `${type}  ·  ${bits.join(' ')}`
  }
  const names = mats.map(x => x.title).filter(Boolean).join(', ')
  return `${type}  ·  ${mats.length} materiais: ${names}`
}

// ── Builder principal ───────────────────────────────────────────

export async function buildCampaignReportPDF(summary) {
  const doc = new jsPDF({ unit: 'mm', format: 'a4', compress: true })
  const marginX = 15

  // 1) Logo E-monitor no topo esquerdo (15mm x 15mm aproximado, mantendo
  //    proporção. Se a logo não carregar, segue sem ela.)
  const logoData = await loadLogoDataURL()
  if (logoData) {
    try {
      doc.addImage(logoData, 'PNG', marginX, 12, 28, 11, '', 'FAST')
    } catch { /* ignore — segue sem logo */ }
  } else {
    // Fallback textual: marca "E-monitor" em rosa no topo.
    setColor(doc, 'text', TOKENS.action)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(14)
    doc.text('E-monitor', marginX, 20)
  }

  // 2) Hero (campanha + cliente + período + status badge).
  drawHero(doc, summary, marginX, 30)

  // 3) KPIs. Sem cadastro de PMM no target: 4 boxes (Impactos entra pra todos).
  //    Com cadastro: 5 boxes (Impactos no target entra). `hasTarget` também é
  //    reusado mais abaixo, na tabela "Por emissora" (§5).
  const kpiY = 76
  const kpiH = 22
  const kpiGap = 4
  const pageW = doc.internal.pageSize.getWidth()
  const hasTarget = (summary.totals?.stations_with_target ?? 0) > 0
  const kpis = [
    ['Veiculações', fmtNumber(summary.totals?.detections)],
    ['Materiais', fmtNumber(summary.totals?.distinct_materials)],
    ['Emissoras', fmtNumber(summary.totals?.distinct_stations)],
    ['Impactos', fmtNumber(summary.totals?.impactos)],
  ]
  if (hasTarget) kpis.push(['Impactos no target', fmtNumber(summary.totals?.impactos_target)])
  const kpiW = (pageW - marginX * 2 - kpiGap * (kpis.length - 1)) / kpis.length
  kpis.forEach(([label, value], i) => {
    drawKPI(doc, marginX + (kpiW + kpiGap) * i, kpiY, kpiW, kpiH, label, value)
  })

  // Breakdown por status (Dentro/Fora faixa/Fora data/Bônus) — derivado do
  // by_material_station (que já traz a contagem por categoria), somado por
  // material e por emissora. Zero mudança de backend/SQL.
  const byMatSta = Array.isArray(summary.by_material_station) ? summary.by_material_station : []
  const bmByMaterial = new Map()
  const bmByStation = new Map()
  for (const r of byMatSta) {
    for (const [map, key] of [[bmByMaterial, r.material_id], [bmByStation, r.station_id]]) {
      const a = map.get(key) ?? { inSlot: 0, outSlot: 0, outDate: 0, orphan: 0 }
      a.inSlot  += r.in_slot_count  ?? 0
      a.outSlot += r.out_slot_count ?? 0
      a.outDate += r.out_date_count ?? 0
      a.orphan  += r.orphan_count   ?? 0
      map.set(key, a)
    }
  }
  const ZERO_BD = { inSlot: 0, outSlot: 0, outDate: 0, orphan: 0 }

  // didParseCell reusável: pinta as colunas de status (índices → cor) quando > 0.
  const statusColorizer = (colorByCol) => (data) => {
    if (data.section !== 'body') return
    const color = colorByCol[data.column.index]
    if (!color) return
    const val = Number(String(data.cell.raw).replace(/[^0-9-]/g, '')) || 0
    data.cell.styles.textColor = statusTextColor(val, color, false)
  }

  // Legenda de cores (mesma da grade) abaixo dos KPIs.
  const legendY = drawLegend(doc, marginX, kpiY + kpiH + 9)

  // 4) Seção "Por material" — tabela.
  const sectionY = legendY + 6
  setColor(doc, 'text', TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(12)
  doc.text('Por material', marginX, sectionY)

  const byMaterial = Array.isArray(summary.by_material) ? summary.by_material : []
  autoTable(doc, {
    startY: sectionY + 3,
    head: [['ID', 'Material', 'Tipo', 'Dur', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total']],
    body: byMaterial.map(m => {
      const bd = bmByMaterial.get(m.material_id) ?? ZERO_BD
      return [
        m.material_short_id ?? '—',
        m.material_title || '—',
        m.material_type_name || '—',
        m.material_duration_sec != null ? `${Math.round(m.material_duration_sec)}s` : '—',
        fmtNumber(bd.inSlot),
        fmtNumber(bd.outSlot),
        fmtNumber(bd.outDate),
        fmtNumber(bd.orphan),
        fmtNumber(m.count),
      ]
    }),
    margin: { left: marginX, right: marginX },
    styles: {
      fontSize: 8.5,
      cellPadding: { top: 2.5, right: 2.5, bottom: 2.5, left: 2.5 },
      textColor: TOKENS.text2,
      lineColor: TOKENS.border,
      lineWidth: 0.1,
    },
    headStyles: {
      fillColor: TOKENS.surface2,
      textColor: TOKENS.text,
      fontStyle: 'bold',
      fontSize: 8,
      lineColor: TOKENS.border,
    },
    alternateRowStyles: { fillColor: [250, 250, 252] },
    columnStyles: {
      0: { cellWidth: 12, halign: 'center' },
      2: { cellWidth: 24 },
      3: { halign: 'right', cellWidth: 12 },
      4: { halign: 'right', cellWidth: 16 },
      5: { halign: 'right', cellWidth: 20 },
      6: { halign: 'right', cellWidth: 20 },
      7: { halign: 'right', cellWidth: 14 },
      8: { halign: 'right', cellWidth: 16, fontStyle: 'bold', textColor: TOKENS.action },
    },
    didParseCell: statusColorizer({ 4: CAT.tocou, 5: CAT.outSlot, 6: CAT.outDate, 7: CAT.bonus }),
  })

  // 5) Seção "Por emissora" — tabela.
  let nextY = doc.lastAutoTable.finalY + 8
  // Quebra de página se sobrar pouco.
  const pageH = doc.internal.pageSize.getHeight()
  if (nextY > pageH - 60) {
    doc.addPage()
    nextY = 20
  }
  setColor(doc, 'text', TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(12)
  doc.text('Por emissora', marginX, nextY)

  const byStation = Array.isArray(summary.by_station) ? summary.by_station : []
  // Cabeçalho + larguras de coluna: "Impactos" entra sempre; "Impactos no
  // target" só quando `hasTarget` (declarado no bloco de KPIs acima). As
  // larguras das colunas existentes foram reduzidas o suficiente pra abrir
  // espaço pras 1-2 colunas novas sem estourar a área útil da página (180mm =
  // A4 210mm − 2×15mm de margem); "Emissora" (coluna 0, sem largura fixa)
  // absorve a sobra. Texto que não couber quebra linha (autoTable overflow
  // default = 'linebreak') em vez de ser cortado.
  const stationHead = ['Emissora', 'Dial', 'Cidade', 'UF', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total', 'Impactos']
  const stationColumnStyles = {
    1: { cellWidth: 16 },
    2: { cellWidth: 22 },
    3: { halign: 'center', cellWidth: 9 },
    4: { halign: 'right', cellWidth: 14 },
    5: { halign: 'right', cellWidth: 16 },
    6: { halign: 'right', cellWidth: 16 },
    7: { halign: 'right', cellWidth: 12 },
    8: { halign: 'right', cellWidth: 14, fontStyle: 'bold', textColor: TOKENS.action },
    9: { halign: 'right', cellWidth: 16 },
  }
  if (hasTarget) {
    stationHead.push('Impactos no target')
    stationColumnStyles[10] = { halign: 'right', cellWidth: 18 }
  }
  autoTable(doc, {
    startY: nextY + 3,
    head: [stationHead],
    body: byStation.map(s => {
      const bd = bmByStation.get(s.station_id) ?? ZERO_BD
      const row = [
        s.station_name || '—',
        [s.station_band, s.station_frequency_mhz != null ? `${s.station_frequency_mhz.toFixed(1).replace('.', ',')}` : null]
          .filter(Boolean).join(' ') || '—',
        s.station_city || '—',
        s.station_state || '—',
        fmtNumber(bd.inSlot),
        fmtNumber(bd.outSlot),
        fmtNumber(bd.outDate),
        fmtNumber(bd.orphan),
        fmtNumber(s.count),
        s.station_pmm != null ? fmtNumber(Math.round(s.station_pmm * s.count)) : '—',
      ]
      if (hasTarget) {
        row.push(s.station_pmm_target != null ? fmtNumber(s.station_pmm_target * s.count) : '—')
      }
      return row
    }),
    margin: { left: marginX, right: marginX },
    styles: {
      fontSize: 8.5,
      cellPadding: { top: 2.5, right: 2.5, bottom: 2.5, left: 2.5 },
      textColor: TOKENS.text2,
      lineColor: TOKENS.border,
      lineWidth: 0.1,
    },
    headStyles: {
      fillColor: TOKENS.surface2,
      textColor: TOKENS.text,
      fontStyle: 'bold',
      fontSize: 8,
      lineColor: TOKENS.border,
    },
    alternateRowStyles: { fillColor: [250, 250, 252] },
    columnStyles: stationColumnStyles,
    didParseCell: statusColorizer({ 4: CAT.tocou, 5: CAT.outSlot, 6: CAT.outDate, 7: CAT.bonus }),
  })

  // 6) Seção "Material × Emissora" (detalhe). Sempre em nova página, pra
  //    legibilidade da tabela quando a campanha é grande. Troca Primeira/Última
  //    pelas colunas de status (mais úteis pro fechamento comercial); as datas
  //    de primeira/última seguem no CSV consolidado.
  if (byMatSta.length > 0) {
    doc.addPage()
    setColor(doc, 'text', TOKENS.text)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(12)
    doc.text('Detalhe — Material × Emissora', marginX, 20)
    drawLegend(doc, marginX, 26)

    autoTable(doc, {
      startY: 30,
      head: [['Material', 'Emissora', 'Cidade/UF', 'Dentro', 'Fora faixa', 'Fora data', 'Bônus', 'Total']],
      body: byMatSta.map(r => [
        r.material_title || '—',
        r.station_name || '—',
        [r.station_city, r.station_state].filter(Boolean).join('/') || '—',
        fmtNumber(r.in_slot_count),
        fmtNumber(r.out_slot_count),
        fmtNumber(r.out_date_count),
        fmtNumber(r.orphan_count),
        fmtNumber(r.count),
      ]),
      margin: { left: marginX, right: marginX },
      styles: {
        fontSize: 8.5,
        cellPadding: { top: 2.2, right: 2.5, bottom: 2.2, left: 2.5 },
        textColor: TOKENS.text2,
        lineColor: TOKENS.border,
        lineWidth: 0.1,
      },
      headStyles: {
        fillColor: TOKENS.surface2,
        textColor: TOKENS.text,
        fontStyle: 'bold',
        fontSize: 8,
        lineColor: TOKENS.border,
      },
      alternateRowStyles: { fillColor: [250, 250, 252] },
      columnStyles: {
        1: { cellWidth: 30 },
        2: { cellWidth: 22 },
        3: { halign: 'right', cellWidth: 16 },
        4: { halign: 'right', cellWidth: 20 },
        5: { halign: 'right', cellWidth: 20 },
        6: { halign: 'right', cellWidth: 14 },
        7: { halign: 'right', cellWidth: 16, fontStyle: 'bold', textColor: TOKENS.action },
      },
      didParseCell: statusColorizer({ 3: CAT.tocou, 4: CAT.outSlot, 5: CAT.outDate, 6: CAT.bonus }),
    })
  }

  // 7) Footer em todas as páginas.
  const total = doc.getNumberOfPages()
  for (let i = 1; i <= total; i++) {
    doc.setPage(i)
    drawFooter(doc, total)
  }

  // 8) Trigger download.
  const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '')
  const filename = `relatorio-${slugify(summary.campaign?.name)}-${stamp}.pdf`
  doc.save(filename)
}

// ════════════════════════════════════════════════════════════════
//  PDF WYSIWYG da grade de /detections (programado × tocado, por dia)
// ════════════════════════════════════════════════════════════════
//
// Entrada: o MODELO de utils/gridReport.js (buildGridReportModel). Espelha a
// grade — as mesmas emissoras/materiais filtrados, os mesmos números da view
// daily_play_summary — e detalha DIA A DIA por emissora. Reusa logo/tokens/
// footer do builder acima.

function drawGridHero(doc, model, marginX, y) {
  const pageW = doc.internal.pageSize.getWidth()
  const w = pageW - marginX * 2
  const h = 44

  setColor(doc, 'fill', TOKENS.action)
  doc.rect(marginX, y, w, 2.5, 'F')
  drawCard(doc, marginX, y + 2.5, w, h)

  setColor(doc, 'text', TOKENS.text3)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(9)
  doc.text('RELATÓRIO DE VEICULAÇÕES · PROGRAMADO × TOCADO', marginX + 8, y + 11)

  setColor(doc, 'text', TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(18)
  const name = doc.splitTextToSize(model.header.campaignName || '—', w - 60)
  doc.text(name[0], marginX + 8, y + 21)

  setColor(doc, 'text', TOKENS.text2)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(10)
  const meta = [model.header.clientName, model.header.periodLabel].filter(Boolean).join('  ·  ')
  doc.text(meta || '—', marginX + 8, y + 30)

  // Nota do recorte aplicado (filtro de busca + período).
  if (model.header.filterLabel) {
    setColor(doc, 'text', TOKENS.text3)
    doc.setFontSize(8.5)
    doc.text(`Recorte: ${model.header.filterLabel}`, marginX + 8, y + 38)
  }

  // Status badge.
  const status = model.header.status || 'concluida'
  const sLabel = STATUS_LABEL[status] || status
  const sColor = STATUS_COLORS[status] || STATUS_COLORS.concluida
  const sW = doc.getTextWidth(sLabel) + 10
  const sX = marginX + w - sW - 8
  const sY = y + 9
  setColor(doc, 'fill', sColor.bg)
  doc.roundedRect(sX, sY, sW, 7, 3.5, 3.5, 'F')
  setColor(doc, 'text', sColor.fg)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(8)
  doc.text(sLabel, sX + 5, sY + 4.8)

  return y + 2.5 + h
}

// Uma pílula-KPI compacta (label em cima, número embaixo) — versão estreita do
// drawKPI pra caber 5 numa linha.
function drawMiniKPI(doc, x, y, w, h, label, value, valueColor) {
  drawCard(doc, x, y, w, h)
  setColor(doc, 'text', TOKENS.text3)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(7)
  doc.text(String(label).toUpperCase(), x + 5, y + 6)
  setColor(doc, 'text', valueColor || TOKENS.action)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(16)
  doc.text(String(value), x + 5, y + h - 5)
}

export async function buildGridReportPDF(model) {
  const doc = new jsPDF({ unit: 'mm', format: 'a4', compress: true })
  const marginX = 15
  const pageW = doc.internal.pageSize.getWidth()
  const pageH = doc.internal.pageSize.getHeight()

  // 1) Logo (ou fallback textual).
  const logoData = await loadLogoDataURL()
  if (logoData) {
    try { doc.addImage(logoData, 'PNG', marginX, 12, 28, 11, '', 'FAST') } catch { /* segue sem logo */ }
  } else {
    setColor(doc, 'text', TOKENS.action)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(14)
    doc.text('E-monitor', marginX, 20)
  }

  // 2) Hero.
  const heroBottom = drawGridHero(doc, model, marginX, 30)

  // 3) KPIs (Cobertura, Esperado, Tocou, Déficit, Bônus).
  const k = model.kpis
  const kpiY = heroBottom + 10
  const kpiH = 20
  const kpiGap = 3
  const kpiW = (pageW - marginX * 2 - kpiGap * 4) / 5
  const kpis = [
    ['Cobertura', k.coveragePct != null ? `${k.coveragePct}%` : '—', TOKENS.action],
    ['Esperado', fmtNumber(k.expected), TOKENS.text],
    ['Tocou', fmtNumber(k.inSlot), CAT.tocou],
    ['Déficit', fmtNumber(k.deficit), k.deficit > 0 ? CAT.deficit : TOKENS.text3],
    ['Bônus', fmtNumber(k.bonus), k.bonus > 0 ? CAT.bonus : TOKENS.text3],
  ]
  kpis.forEach(([label, value, color], i) => {
    drawMiniKPI(doc, marginX + (kpiW + kpiGap) * i, kpiY, kpiW, kpiH, label, value, color)
  })

  // 3b) Legenda de cores (mesma da grade) logo abaixo dos KPIs.
  const legendY = drawLegend(doc, marginX, kpiY + kpiH + 7)

  // 4) Seções por emissora (dia a dia).
  let nextY = legendY + 6

  const anyNonZero = t => !!(t.expected || t.inSlot || t.deficit || t.bonus || t.outSlot || t.outDate)
  const plus = n => (n > 0 ? `+${fmtNumber(n)}` : '0')

  const stations = model.byStation.filter(s => anyNonZero(s.totals))

  if (stations.length === 0) {
    setColor(doc, 'text', TOKENS.text3)
    doc.setFont('helvetica', 'normal')
    doc.setFontSize(11)
    doc.text('Nenhuma veiculação ou programação no recorte selecionado.', marginX, nextY + 6)
  }

  for (const s of stations) {
    // Espaço mínimo pro cabeçalho da emissora + o header da 1ª tabela.
    if (nextY > pageH - 45) { doc.addPage(); nextY = 20 }

    // Cabeçalho da emissora.
    setColor(doc, 'text', TOKENS.text)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(12)
    doc.text(s.stationName, marginX, nextY)
    const sub = [s.stationDial, [s.stationCity, s.stationState].filter(Boolean).join('/')]
      .filter(Boolean).join('  ·  ')
    if (sub) {
      setColor(doc, 'text', TOKENS.text3)
      doc.setFont('helvetica', 'normal')
      doc.setFontSize(9)
      doc.text(sub, marginX, nextY + 4.5)
    }
    // Nota de fora-faixa/fora-data/impactos quando houver (não some nada da
    // grade). Impactos = pmm × Σ in_slot da emissora (calculado em
    // buildGridReportModel, gridReport.js); "no target" só quando o cliente
    // tem PMM no target cadastrado pra essa emissora especificamente.
    const extras = []
    if (s.totals.outSlot > 0) extras.push(`${fmtNumber(s.totals.outSlot)} fora da faixa`)
    if (s.totals.outDate > 0) extras.push(`${fmtNumber(s.totals.outDate)} fora da data`)
    if (s.impactos != null) extras.push(`${fmtNumber(s.impactos)} impactos`)
    if (s.impactosTarget != null) extras.push(`${fmtNumber(s.impactosTarget)} no target`)
    if (extras.length) {
      setColor(doc, 'text', TOKENS.text3)
      doc.setFont('helvetica', 'italic')
      doc.setFontSize(8.5)
      doc.text(`+ ${extras.join(' · ')}`, marginX, nextY + 9)
    }

    // Corpo da tabela: por material → linha-título (subtítulo com o material
    // REAL) + dias + linha Total. O nome do material vira subtítulo porque a
    // grade é por tipo; as contagens diárias seguem por tipo.
    const body = []
    const headerRows = new Set()  // linhas-título (material) — colSpan
    const totalRows = new Set()   // linhas de total por material — bold
    // Cor por índice de coluna de status na tabela por-dia.
    const colColor = { 2: CAT.tocou, 3: CAT.outSlot, 4: CAT.outDate, 5: CAT.deficit, 6: CAT.bonus }
    for (const m of s.materials) {
      if (!anyNonZero(m.totals)) continue
      headerRows.add(body.length)
      body.push([{ content: materialSubtitle(m), colSpan: 7 }])
      for (const d of m.days) {
        body.push([d.dateLabel, fmtNumber(d.expected), fmtNumber(d.inSlot),
          fmtNumber(d.outSlot), fmtNumber(d.outDate), fmtNumber(d.deficit), plus(d.bonus)])
      }
      totalRows.add(body.length)
      body.push(['Total', fmtNumber(m.totals.expected), fmtNumber(m.totals.inSlot),
        fmtNumber(m.totals.outSlot), fmtNumber(m.totals.outDate), fmtNumber(m.totals.deficit), plus(m.totals.bonus)])
    }

    autoTable(doc, {
      startY: nextY + (extras.length ? 12 : 8),
      head: [['Data', 'Prog', 'Tocou', 'Fora faixa', 'Fora data', 'Déf', 'Bônus']],
      body,
      margin: { left: marginX, right: marginX },
      styles: {
        fontSize: 8.5,
        cellPadding: { top: 2, right: 3, bottom: 2, left: 3 },
        textColor: TOKENS.text2,
        lineColor: TOKENS.border,
        lineWidth: 0.1,
      },
      headStyles: {
        fillColor: TOKENS.surface2,
        textColor: TOKENS.text,
        fontStyle: 'bold',
        fontSize: 8,
        lineColor: TOKENS.border,
      },
      alternateRowStyles: { fillColor: [250, 250, 252] },
      // Larguras somam exatamente a área útil (A4 210 − 2×15 = 180mm). Fixar as
      // 7 sem folga evita o aviso "units could not fit" do autotable (que ocorre
      // quando todas são fixas e NÃO preenchem a página).
      columnStyles: {
        0: { cellWidth: 22 },
        1: { halign: 'right', cellWidth: 24 },
        2: { halign: 'right', cellWidth: 26 },
        3: { halign: 'right', cellWidth: 30 },
        4: { halign: 'right', cellWidth: 30 },
        5: { halign: 'right', cellWidth: 22 },
        6: { halign: 'right', cellWidth: 26 },
      },
      didParseCell: (data) => {
        if (data.section !== 'body') return
        const ri = data.row.index
        // Linha-título do material (colSpan) — destaque, sem semáforo.
        if (headerRows.has(ri)) {
          data.cell.styles.fontStyle = 'bold'
          data.cell.styles.fillColor = TOKENS.actionLight
          data.cell.styles.textColor = TOKENS.text
          data.cell.styles.halign = 'left'
          return
        }
        const isTotal = totalRows.has(ri)
        if (isTotal) {
          data.cell.styles.fontStyle = 'bold'
          data.cell.styles.fillColor = TOKENS.surface2
          data.cell.styles.textColor = TOKENS.text
        }
        // Semáforo por coluna de status (só quando > 0; zero fica apagado).
        const color = colColor[data.column.index]
        if (color) {
          const val = Number(String(data.cell.raw).replace(/[^0-9-]/g, '')) || 0
          data.cell.styles.textColor = statusTextColor(val, color, isTotal)
        }
      },
    })

    nextY = doc.lastAutoTable.finalY + 12
  }

  // 5) Footer em todas as páginas.
  const total = doc.getNumberOfPages()
  for (let i = 1; i <= total; i++) {
    doc.setPage(i)
    drawFooter(doc, total)
  }

  // 6) Download.
  const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '')
  doc.save(`relatorio-veiculacoes-${model.slug}-${stamp}.pdf`)
}
