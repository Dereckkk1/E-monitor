// pdfCampaignFailure.js — "Relatório de Cobrança" PDF, gerado client-side.
//
// Distingue-se do pdfReport.js (relatório genérico de campanha pra cliente):
// este aqui foca em FALHAS — emissoras que não cumpriram o programado.
// Layout pensado pra ser enviado pra emissora ou pro comercial.
//
// Entrada: payload do GET /v1/internal/admin/campaign-failures/{id}.

import { jsPDF } from 'jspdf'
import autoTable from 'jspdf-autotable'

const TOKENS = {
  action:      [232, 30, 117],   // #E81E75
  actionLight: [252, 231, 243],
  text:        [6, 5, 91],
  text2:       [75, 85, 99],
  text3:       [156, 163, 175],
  border:      [226, 232, 240],
  surface2:    [241, 245, 249],
  white:       [255, 255, 255],
  deficit:     [220, 38, 38],
  bonified:    [168, 85, 247],
}

function pad2(n) { return String(n).padStart(2, '0') }
function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = String(yyyymmdd).split('-')
  return `${d}/${m}/${y}`
}
function fmtNow() {
  const d = new Date()
  return `${pad2(d.getDate())}/${pad2(d.getMonth()+1)}/${d.getFullYear()} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}
function slugify(s) {
  return String(s || 'campanha')
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || 'campanha'
}
function stamp() {
  const d = new Date()
  return `${d.getFullYear()}${pad2(d.getMonth()+1)}${pad2(d.getDate())}`
}

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
    } catch { return null }
  })()
  return _logoPromise
}

// Day chips: render as "24, 26, 29" for compactness in PDF cell.
function fmtDaysList(days) {
  if (!days?.length) return '—'
  const monthSet = new Set(days.map(d => d.slice(0, 7)))
  if (monthSet.size === 1) {
    return days.map(d => d.slice(8, 10)).join(', ')
  }
  return days.map(d => `${d.slice(8, 10)}/${d.slice(5, 7)}`).join(', ')
}

export async function generateCampaignFailurePdf(payload) {
  const { campaign, summary, stations = [] } = payload
  const doc = new jsPDF({ unit: 'pt', format: 'a4' })
  const pageW = doc.internal.pageSize.getWidth()
  const margin = 36
  const logo = await loadLogoDataURL()

  // ─── Header band ────────────────────────────────────────────────
  doc.setFillColor(...TOKENS.action)
  doc.rect(0, 0, pageW, 8, 'F')

  if (logo) {
    doc.addImage(logo, 'PNG', margin, 22, 90, 28)
  } else {
    doc.setTextColor(...TOKENS.action)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(16)
    doc.text('E-monitor', margin, 42)
  }

  doc.setTextColor(...TOKENS.text)
  doc.setFont('helvetica', 'bold')
  doc.setFontSize(20)
  doc.text('Relatório de Cobrança', margin, 90)

  doc.setFont('helvetica', 'normal')
  doc.setFontSize(11)
  doc.setTextColor(...TOKENS.text2)
  doc.text(campaign.client_name || '—', margin, 110)

  doc.setFontSize(10)
  doc.setTextColor(...TOKENS.text3)
  doc.text(
    `${campaign.name || '—'} · ${fmtDateBR(campaign.start_date)} a ${fmtDateBR(campaign.end_date)}`,
    margin, 126,
  )

  // ─── KPI strip ─────────────────────────────────────────────────
  const kpiY = 150
  const kpiW = (pageW - margin * 2 - 16) / 3
  const kpis = [
    { label: 'Emissoras com falha', value: summary?.stations_with_failure ?? 0, color: TOKENS.text },
    { label: 'Dias com falha',      value: summary?.total_failure_days    ?? 0, color: TOKENS.text },
    { label: 'Déficit total',       value: summary?.total_deficit         ?? 0, color: TOKENS.deficit },
  ]
  kpis.forEach((kpi, i) => {
    const x = margin + i * (kpiW + 8)
    doc.setFillColor(...TOKENS.surface2)
    doc.roundedRect(x, kpiY, kpiW, 56, 8, 8, 'F')
    doc.setTextColor(...kpi.color)
    doc.setFont('helvetica', 'bold')
    doc.setFontSize(22)
    doc.text(String(kpi.value), x + 14, kpiY + 30)
    doc.setFont('helvetica', 'normal')
    doc.setFontSize(9)
    doc.setTextColor(...TOKENS.text3)
    doc.text(kpi.label.toUpperCase(), x + 14, kpiY + 48)
  })

  // ─── Stations table ────────────────────────────────────────────
  // Body rows keep numbers; we draw the coverage bar in a didDrawCell hook
  // so each row gets a visual under the percentage.
  const body = stations.map(s => {
    const programmed = s.programmed || 0
    const identified = s.identified || 0
    const extras = s.extras || 0
    const deficit = s.deficit || 0
    const coverage = programmed > 0 ? Math.round((identified / programmed) * 100) : 0
    return {
      _raw: { programmed, identified, extras, deficit, coverage, isBonified: s.is_bonified },
      cells: [
        s.station.name + (s.station.city ? `\n${s.station.city}` : ''),
        String(programmed),
        `${identified.toLocaleString('pt-BR')}  ${coverage}%`,
        fmtDaysList(s.failure_days || []),
        s.is_bonified ? `Bonificada · +${extras}` : (deficit > 0 ? `Cobrar (${deficit})` : '—'),
      ],
    }
  })

  autoTable(doc, {
    startY: kpiY + 80,
    head: [['Emissora', 'Programado', 'Veiculou', 'Dias com falha', 'Status']],
    body: body.map(b => b.cells),
    margin: { left: margin, right: margin },
    styles: { fontSize: 9, cellPadding: { top: 7, right: 6, bottom: 12, left: 6 }, valign: 'top' },
    headStyles: {
      fillColor: TOKENS.surface2, textColor: TOKENS.text3,
      fontSize: 8, fontStyle: 'bold', cellPadding: 6, halign: 'left',
    },
    columnStyles: {
      0: { cellWidth: 150 },
      1: { halign: 'right', cellWidth: 60 },
      2: { halign: 'right', cellWidth: 70 },
      3: { cellWidth: 'auto' },
      4: { cellWidth: 95, halign: 'left' },
    },
    didParseCell: (data) => {
      if (data.section !== 'body') return
      const raw = body[data.row.index]?._raw
      if (!raw) return
      // Status column color
      if (data.column.index === 4) {
        if (raw.isBonified) data.cell.styles.textColor = TOKENS.bonified
        else if (raw.deficit > 0) data.cell.styles.textColor = TOKENS.deficit
      }
      // Veiculou column: bold coverage %
      if (data.column.index === 2) {
        data.cell.styles.fontStyle = 'bold'
      }
    },
    didDrawCell: (data) => {
      // Draw a thin coverage bar UNDER the Veiculou cell.
      if (data.section !== 'body' || data.column.index !== 2) return
      const raw = body[data.row.index]?._raw
      if (!raw || raw.programmed === 0) return
      const x = data.cell.x + 4
      const y = data.cell.y + data.cell.height - 6
      const w = data.cell.width - 8
      const h = 3
      // Background track
      doc.setFillColor(...TOKENS.surface2)
      doc.roundedRect(x, y, w, h, 1, 1, 'F')
      // Filled portion: ok (green) up to coverage%, then extras (purple)
      const denom = Math.max(raw.programmed, raw.identified + raw.deficit)
      if (denom > 0) {
        const okW = (raw.identified / denom) * w
        const extrasW = (raw.extras / denom) * w
        if (okW > 0) {
          const okColor = raw.isBonified ? TOKENS.bonified : [22, 163, 74]
          doc.setFillColor(...okColor)
          doc.roundedRect(x, y, okW, h, 1, 1, 'F')
        }
        if (extrasW > 0 && !raw.isBonified) {
          doc.setFillColor(...TOKENS.bonified)
          doc.rect(x + okW, y, extrasW, h, 'F')
        }
      }
    },
  })

  // ─── Footer (every page) ───────────────────────────────────────
  const pages = doc.getNumberOfPages()
  for (let p = 1; p <= pages; p++) {
    doc.setPage(p)
    doc.setFontSize(8)
    doc.setTextColor(...TOKENS.text3)
    doc.text(`Gerado por E-monitor · ${fmtNow()}`, margin, doc.internal.pageSize.getHeight() - 20)
    doc.text(`Página ${p} de ${pages}`, pageW - margin, doc.internal.pageSize.getHeight() - 20, { align: 'right' })
  }

  doc.save(`cobranca-${slugify(campaign.name)}-${stamp()}.pdf`)
}
