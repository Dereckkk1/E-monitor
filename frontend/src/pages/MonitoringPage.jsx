import { useStations } from '../api/hooks'
import { useEffect } from 'react'

export default function MonitoringPage() {
  const { data: stations = [], isLoading, refetch } = useStations()

  useEffect(() => {
    const interval = setInterval(refetch, 10_000)
    return () => clearInterval(interval)
  }, [refetch])

  const statusColor = { active: '#2ecc40', paused: '#aaa', calibrating: '#ff851b', error: '#ff4136' }
  const statusLabel = { active: 'Ativo', paused: 'Pausado', calibrating: 'Calibrando', error: 'Erro' }

  if (isLoading) return <div>Carregando...</div>

  return (
    <div>
      <h2>Status de Monitoramento</h2>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
        {stations.map(s => (
          <div key={s.id} style={{
            border: `2px solid ${statusColor[s.monitoring_status] || '#333'}`,
            borderRadius: 8,
            padding: 12,
            minWidth: 180,
          }}>
            <div style={{ fontWeight: 'bold' }}>{s.name}</div>
            <div style={{ fontSize: 12, color: '#666' }}>{s.band} {s.frequency_mhz} MHz — {s.city}/{s.state}</div>
            <div style={{ marginTop: 8, color: statusColor[s.monitoring_status] || '#333', fontWeight: 'bold' }}>
              {statusLabel[s.monitoring_status] || s.monitoring_status}
            </div>
          </div>
        ))}
        {stations.length === 0 && <div>Nenhuma emissora cadastrada.</div>}
      </div>
    </div>
  )
}
