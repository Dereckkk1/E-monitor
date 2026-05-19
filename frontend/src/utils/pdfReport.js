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

// KPI box: número grande em rosa, label cinza em cima.
function drawKPI(doc, x, y, w, h, label, value) {
  drawCard(doc, x, y, w, h)
  setColor(doc, 'text', TOKENS.text3)
  doc.setFont('helvetica', 'normal')
  doc.setFontSize(8)
  doc.text(String(label).toUpperCase(), x + 6, y + 7)

  setColor(doc, 'text', TOKENS.action)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(20)
  doc.text(String(value), x + 6, y + h - 6)
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

  // 3) KPIs (3 boxes lado a lado).
  const kpiY = 76
  const kpiH = 22
  const kpiGap = 4
  const pageW = doc.internal.pageSize.getWidth()
  const kpiW = (pageW - marginX * 2 - kpiGap * 2) / 3
  drawKPI(doc, marginX, kpiY, kpiW, kpiH,
    'Veiculações', fmtNumber(summary.totals?.detections))
  drawKPI(doc, marginX + kpiW + kpiGap, kpiY, kpiW, kpiH,
    'Materiais', fmtNumber(summary.totals?.distinct_materials))
  drawKPI(doc, marginX + (kpiW + kpiGap) * 2, kpiY, kpiW, kpiH,
    'Emissoras', fmtNumber(summary.totals?.distinct_stations))

  // 4) Seção "Por material" — tabela.
  const sectionY = kpiY + kpiH + 12
  setColor(doc, 'text', TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(12)
  doc.text('Por material', marginX, sectionY)

  const byMaterial = Array.isArray(summary.by_material) ? summary.by_material : []
  autoTable(doc, {
    startY: sectionY + 3,
    head: [['ID', 'Material', 'Tipo', 'Duração', 'Total']],
    body: byMaterial.map(m => [
      m.material_short_id ?? '—',
      m.material_title || '—',
      m.material_type_name || '—',
      m.material_duration_sec != null ? `${Math.round(m.material_duration_sec)}s` : '—',
      fmtNumber(m.count),
    ]),
    margin: { left: marginX, right: marginX },
    styles: {
      fontSize: 9,
      cellPadding: { top: 2.5, right: 3, bottom: 2.5, left: 3 },
      textColor: TOKENS.text2,
      lineColor: TOKENS.border,
      lineWidth: 0.1,
    },
    headStyles: {
      fillColor: TOKENS.surface2,
      textColor: TOKENS.text,
      fontStyle: 'bold',
      fontSize: 8.5,
      lineColor: TOKENS.border,
    },
    alternateRowStyles: { fillColor: [250, 250, 252] },
    columnStyles: {
      0: { cellWidth: 14, halign: 'center' },
      3: { halign: 'right', cellWidth: 20 },
      4: { halign: 'right', cellWidth: 20, fontStyle: 'bold', textColor: TOKENS.action },
    },
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
  autoTable(doc, {
    startY: nextY + 3,
    head: [['Emissora', 'Dial', 'Cidade', 'UF', 'Total']],
    body: byStation.map(s => [
      s.station_name || '—',
      [s.station_band, s.station_frequency_mhz != null ? `${s.station_frequency_mhz.toFixed(1).replace('.', ',')}` : null]
        .filter(Boolean).join(' ') || '—',
      s.station_city || '—',
      s.station_state || '—',
      fmtNumber(s.count),
    ]),
    margin: { left: marginX, right: marginX },
    styles: {
      fontSize: 9,
      cellPadding: { top: 2.5, right: 3, bottom: 2.5, left: 3 },
      textColor: TOKENS.text2,
      lineColor: TOKENS.border,
      lineWidth: 0.1,
    },
    headStyles: {
      fillColor: TOKENS.surface2,
      textColor: TOKENS.text,
      fontStyle: 'bold',
      fontSize: 8.5,
      lineColor: TOKENS.border,
    },
    alternateRowStyles: { fillColor: [250, 250, 252] },
    columnStyles: {
      1: { cellWidth: 24 },
      3: { halign: 'center', cellWidth: 12 },
      4: { halign: 'right', cellWidth: 20, fontStyle: 'bold', textColor: TOKENS.action },
    },
  })

  // 6) Seção "Material × Emissora" (detalhe). Sempre em nova página, pra
  //    legibilidade da tabela quando a campanha é grande.
  const byMatSta = Array.isArray(summary.by_material_station) ? summary.by_material_station : []
  if (byMatSta.length > 0) {
    doc.addPage()
    setColor(doc, 'text', TOKENS.text)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(12)
    doc.text('Detalhe — Material × Emissora', marginX, 20)

    autoTable(doc, {
      startY: 24,
      head: [['Material', 'Emissora', 'Cidade/UF', 'Primeira', 'Última', 'Total']],
      body: byMatSta.map(r => [
        r.material_title || '—',
        r.station_name || '—',
        [r.station_city, r.station_state].filter(Boolean).join('/') || '—',
        r.first_detected_at ? fmtDate(r.first_detected_at) : '—',
        r.last_detected_at  ? fmtDate(r.last_detected_at)  : '—',
        fmtNumber(r.count),
      ]),
      margin: { left: marginX, right: marginX },
      styles: {
        fontSize: 8.5,
        cellPadding: { top: 2.2, right: 3, bottom: 2.2, left: 3 },
        textColor: TOKENS.text2,
        lineColor: TOKENS.border,
        lineWidth: 0.1,
      },
      headStyles: {
        fillColor: TOKENS.surface2,
        textColor: TOKENS.text,
        fontStyle: 'bold',
        fontSize: 8.5,
        lineColor: TOKENS.border,
      },
      alternateRowStyles: { fillColor: [250, 250, 252] },
      columnStyles: {
        3: { halign: 'right', cellWidth: 22 },
        4: { halign: 'right', cellWidth: 22 },
        5: { halign: 'right', cellWidth: 16, fontStyle: 'bold', textColor: TOKENS.action },
      },
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
