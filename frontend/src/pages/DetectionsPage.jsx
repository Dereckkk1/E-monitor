import { useState } from 'react'
import { useDetections } from '../api/hooks'

export default function DetectionsPage() {
  const [filters, setFilters] = useState({ limit: 50 })
  const { data: detections = [], isLoading } = useDetections(filters)

  function playEvidence(id) {
    window.open(`/v1/internal/detections/${id}/evidence`, '_blank')
  }

  if (isLoading) return <div>Carregando...</div>

  return (
    <div>
      <h2>Veiculações</h2>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12, flexWrap: 'wrap' }}>
        <input type="date" placeholder="De" onChange={e => setFilters(f => ({ ...f, start_date: e.target.value || undefined }))} />
        <input type="date" placeholder="Até" onChange={e => setFilters(f => ({ ...f, end_date: e.target.value || undefined }))} />
        <select onChange={e => setFilters(f => ({ ...f, limit: Number(e.target.value) }))}>
          <option value={50}>50</option>
          <option value={100}>100</option>
          <option value={200}>200</option>
        </select>
      </div>
      <table border="1" cellPadding="6" style={{ borderCollapse: 'collapse', width: '100%' }}>
        <thead>
          <tr><th>Data/Hora</th><th>Emissora</th><th>Comercial</th><th>Confiança</th><th>Evidência</th></tr>
        </thead>
        <tbody>
          {detections.map(d => (
            <tr key={d.id}>
              <td>{new Date(d.detected_at).toLocaleString('pt-BR')}</td>
              <td>{d.station_id}</td>
              <td>{d.commercial_id}</td>
              <td>{(d.confidence * 100).toFixed(1)}%</td>
              <td>
                {d.evidence_status === 'available'
                  ? <button onClick={() => playEvidence(d.id)}>▶ Ouvir</button>
                  : <span style={{ color: '#aaa' }}>{d.evidence_status}</span>}
              </td>
            </tr>
          ))}
          {detections.length === 0 && <tr><td colSpan={5}>Nenhuma veiculação encontrada.</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
