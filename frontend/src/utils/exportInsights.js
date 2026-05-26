// Exportação do dashboard /insights — PNG (html2canvas) e PDF (jsPDF).
//
// O elemento alvo deve ser o container `.in-body` (passado pela página
// via ref). html2canvas captura o DOM atual e devolve um <canvas> que
// convertemos pra blob → download.
//
// PDF: A4 paisagem, 2 páginas (capa com metadados + screenshot).

import html2canvas from 'html2canvas'
import { jsPDF } from 'jspdf'

function slugify(s) {
  return String(s || 'insights')
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || 'insights'
}

function saveBlob(blob, filename) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function captureCanvas(el) {
  return await html2canvas(el, {
    backgroundColor: '#f8fafc', // --color-gray-50
    scale: 2,
    useCORS: true,
    logging: false,
  })
}

export async function exportInsightsPNG(el, { clientName, period } = {}) {
  const canvas = await captureCanvas(el)
  const filename = `dashboard-${slugify(clientName)}-${period?.from || ''}-${period?.to || ''}.png`
  await new Promise(resolve => {
    canvas.toBlob(blob => {
      if (blob) saveBlob(blob, filename)
      resolve()
    }, 'image/png')
  })
}

export async function exportInsightsPDF(el, { clientName, period, campaigns } = {}) {
  const canvas = await captureCanvas(el)
  const imgData = canvas.toDataURL('image/png')

  const doc = new jsPDF({ orientation: 'landscape', unit: 'mm', format: 'a4' })
  const W = doc.internal.pageSize.getWidth()
  const H = doc.internal.pageSize.getHeight()

  // Capa
  doc.setFillColor(248, 250, 252)
  doc.rect(0, 0, W, H, 'F')
  doc.setTextColor(6, 5, 91)
  doc.setFontSize(24)
  doc.text('Dashboard de Veiculação', W / 2, H / 3, { align: 'center' })

  doc.setFontSize(16)
  doc.text(clientName || '—', W / 2, H / 3 + 14, { align: 'center' })

  doc.setFontSize(12)
  doc.setTextColor(75, 85, 99)
  doc.text(`Período: ${period?.from || '—'} → ${period?.to || '—'}`, W / 2, H / 3 + 26, { align: 'center' })

  if (Array.isArray(campaigns) && campaigns.length) {
    doc.setFontSize(11)
    doc.text(`${campaigns.length} campanha(s) selecionada(s)`, W / 2, H / 3 + 34, { align: 'center' })
  }

  doc.setFontSize(9)
  doc.text(`Gerado em ${new Date().toLocaleString('pt-BR')}`, W / 2, H - 12, { align: 'center' })

  // Página 2 — screenshot
  doc.addPage()
  const margin = 8
  const maxW = W - margin * 2
  const maxH = H - margin * 2
  const aspectImg = canvas.height / canvas.width
  const aspectBox = maxH / maxW
  let imgW, imgH
  if (aspectImg > aspectBox) {
    imgH = maxH
    imgW = imgH / aspectImg
  } else {
    imgW = maxW
    imgH = imgW * aspectImg
  }
  doc.addImage(imgData, 'PNG', (W - imgW) / 2, (H - imgH) / 2, imgW, imgH)

  doc.save(`dashboard-${slugify(clientName)}-${period?.from || ''}-${period?.to || ''}.pdf`)
}
