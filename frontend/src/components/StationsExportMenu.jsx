import { useEffect, useRef, useState } from 'react'
import { fetchStationsForExport } from '../api/hooks'
import { exportStationsCsv } from '../utils/stationsExport'
import { exportStationsPdf, prefetchReportLogo } from '../utils/pdfReport'

// Botão "Exportar" com CSV e PDF das emissoras contratadas.
//
// Só é renderizado quando a listagem está escopada por cliente — quem monta
// decide (ver StationsPage). No catálogo inteiro não existe: 7.500 emissoras
// não são um documento.
//
// Busca o conjunto COMPLETO no clique, não a página visível: o usuário está
// vendo 25 linhas de 37, e um arquivo com 25 seria um recorte silencioso.

function DownloadIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M8 2v8M4.5 7.5L8 11l3.5-3.5" /><path d="M2.5 13h11" />
    </svg>
  )
}
function CsvIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M9 1.5H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V5.5z" /><path d="M9 1.5v4h4" />
      <path d="M5.5 9h5M5.5 11.5h5" />
    </svg>
  )
}
function PdfIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M9 1.5H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V5.5z" /><path d="M9 1.5v4h4" />
      <path d="M5.5 10.5h1.2a1 1 0 0 0 0-2H5.5v4M9.5 8.5v4h.8a1.2 1.2 0 0 0 1.2-1.2v-1.6a1.2 1.2 0 0 0-1.2-1.2z" />
    </svg>
  )
}

export default function StationsExportMenu({ clientName, filterParams, filtersLabel, isAdmin }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(null) // 'csv' | 'pdf' | null
  const [error, setError] = useState(null)
  const rootRef = useRef(null)

  useEffect(() => {
    const onDoc = e => { if (!rootRef.current?.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [])

  async function run(kind) {
    setBusy(kind)
    setError(null)
    try {
      const stations = await fetchStationsForExport(filterParams)
      if (stations.length === 0) {
        setError('Nada para exportar com os filtros atuais.')
        return
      }
      if (kind === 'csv') exportStationsCsv(stations, { clientName, isAdmin })
      else await exportStationsPdf({ stations, clientName, filters: filtersLabel })
      setOpen(false)
    } catch {
      // Falha aqui é rede ou geração de PDF. Mostrar inline no menu em vez de
      // window.alert: o usuário está olhando pro menu quando acontece.
      setError('Não foi possível gerar o arquivo. Tente de novo.')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="stations-export" ref={rootRef}>
      <button
        type="button"
        className="btn stations-export-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => { setOpen(o => !o); prefetchReportLogo() }}
      >
        <DownloadIcon />
        Exportar
      </button>

      {open && (
        <div className="stations-export-menu" role="menu">
          <div className="stations-export-head">
            {clientName}
            {filtersLabel ? <span className="stations-export-filters">{filtersLabel}</span> : null}
          </div>
          <button type="button" role="menuitem" className="stations-export-item"
            disabled={busy !== null} onClick={() => run('csv')}>
            <span className="stations-export-item-icon"><CsvIcon /></span>
            <span className="stations-export-item-body">
              <span className="stations-export-item-title">
                {busy === 'csv' ? 'Gerando…' : 'CSV'}
              </span>
              <span className="stations-export-item-sub">
                Perfil completo: audiência, cobertura, categorias
              </span>
            </span>
          </button>
          <button type="button" role="menuitem" className="stations-export-item"
            disabled={busy !== null} onClick={() => run('pdf')}>
            <span className="stations-export-item-icon"><PdfIcon /></span>
            <span className="stations-export-item-body">
              <span className="stations-export-item-title">
                {busy === 'pdf' ? 'Gerando…' : 'PDF'}
              </span>
              <span className="stations-export-item-sub">
                Documento com capa, indicadores e lista
              </span>
            </span>
          </button>
          {error && <div className="stations-export-error">{error}</div>}
        </div>
      )}
    </div>
  )
}
