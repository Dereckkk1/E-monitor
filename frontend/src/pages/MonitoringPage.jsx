import { useStations } from '../api/hooks'
import { useEffect } from 'react'

const STATUS_CLASS = { active: 'badge-active', paused: 'badge-paused', calibrating: 'badge-paused', error: 'badge-error' }
const STATUS_LABEL = { active: 'Ativo', paused: 'Pausado', calibrating: 'Calibrando', error: 'Erro' }
const DOT_COLOR    = { active: 'var(--c-success)', paused: 'var(--c-text-3)', calibrating: 'var(--c-warning)', error: 'var(--c-danger)' }

export default function MonitoringPage() {
  const { data: stations = [], isLoading, refetch } = useStations()

  useEffect(() => {
    const t = setInterval(refetch, 10_000)
    return () => clearInterval(t)
  }, [refetch])

  if (isLoading) return <p className="empty-state">Carregando...</p>

  return (
    <div>
      <div className="page-header">
        <h2>Monitoramento</h2>
        <span className="text-muted">Atualiza automaticamente a cada 10s</span>
      </div>

      {stations.length === 0 && <p className="empty-state">Nenhuma emissora cadastrada.</p>}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 12 }}>
        {stations.map(s => (
          <div key={s.id} className="card" style={{ padding: 14 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
              <span style={{ fontWeight: 600, fontSize: 14 }}>{s.name}</span>
              <span
                title={STATUS_LABEL[s.monitoring_status] ?? s.monitoring_status}
                style={{
                  width: 10, height: 10, borderRadius: '50%', marginTop: 3, flexShrink: 0,
                  background: DOT_COLOR[s.monitoring_status] ?? 'var(--c-text-3)',
                  boxShadow: s.monitoring_status === 'active' ? '0 0 0 3px color-mix(in srgb, var(--c-success) 25%, transparent)' : 'none',
                }}
              />
            </div>
            <div className="text-muted">{s.band}{s.frequency_mhz ? ` · ${s.frequency_mhz} MHz` : ''}</div>
            <div className="text-muted">{[s.city, s.state].filter(Boolean).join(' / ') || '—'}</div>
            <div style={{ marginTop: 10 }}>
              <span className={`badge ${STATUS_CLASS[s.monitoring_status] ?? 'badge-ended'}`}>
                {STATUS_LABEL[s.monitoring_status] ?? s.monitoring_status}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
