import { useState } from 'react'
import { useDetections } from '../api/hooks'

export default function DetectionsPage() {
  const [filters, setFilters] = useState({ limit: 50 })
  const { data: detections = [], isLoading } = useDetections(filters)

  function set(k, v) {
    setFilters(f => {
      const next = { ...f }
      if (v) next[k] = v
      else delete next[k]
      return next
    })
  }

  if (isLoading) return <p className="empty-state">Carregando...</p>

  return (
    <div>
      <div className="page-header">
        <h2>Veiculações</h2>
      </div>

      <div className="cluster" style={{ marginBottom: 16 }}>
        <div className="field">
          <label>De</label>
          <input className="input" type="date" style={{ width: 'auto' }} onChange={e => set('start_date', e.target.value)} />
        </div>
        <div className="field">
          <label>Até</label>
          <input className="input" type="date" style={{ width: 'auto' }} onChange={e => set('end_date', e.target.value)} />
        </div>
        <div className="field">
          <label>Limite</label>
          <select className="select" style={{ width: 'auto' }} onChange={e => set('limit', Number(e.target.value))}>
            <option value={50}>50</option>
            <option value={100}>100</option>
            <option value={200}>200</option>
          </select>
        </div>
      </div>

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th>Data / Hora</th>
              <th>Emissora</th>
              <th>Comercial</th>
              <th>Confiança</th>
              <th>Evidência</th>
            </tr>
          </thead>
          <tbody>
            {detections.map(d => (
              <tr key={d.id}>
                <td style={{ whiteSpace: 'nowrap' }}>{new Date(d.detected_at).toLocaleString('pt-BR')}</td>
                <td className="text-muted" style={{ fontFamily: 'monospace', fontSize: 11 }}>{d.station_id}</td>
                <td className="text-muted" style={{ fontFamily: 'monospace', fontSize: 11 }}>{d.commercial_id}</td>
                <td>
                  <span style={{ fontWeight: 600, color: d.confidence >= 0.9 ? 'var(--c-success)' : d.confidence >= 0.7 ? 'var(--c-warning)' : 'var(--c-danger)' }}>
                    {(d.confidence * 100).toFixed(1)}%
                  </span>
                </td>
                <td>
                  {d.evidence_status === 'available' ? (
                    <audio
                      controls
                      preload="metadata"
                      src={`/v1/internal/detections/${d.id}/evidence`}
                      style={{ height: 28 }}
                      onPlay={e => {
                        document.querySelectorAll('audio').forEach(a => {
                          if (a !== e.currentTarget) a.pause()
                        })
                      }}
                    />
                  ) : (
                    <span className="text-muted">{d.evidence_status}</span>
                  )}
                </td>
              </tr>
            ))}
            {detections.length === 0 && <tr><td className="table-empty" colSpan={5}>Nenhuma veiculação encontrada.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  )
}
