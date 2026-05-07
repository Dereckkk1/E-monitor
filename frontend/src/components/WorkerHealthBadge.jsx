import React from 'react'
import { useQuery } from '@tanstack/react-query'
import api from '../api/client'

/**
 * WorkerHealthBadge — surface CLAP verifier reachability and per-worker
 * stall risk from the GET /v1/internal/workers endpoint (fase2 hardening).
 *
 * NOTE: This component is a port of the MonitoringPage from the
 * feature/fase2-hardening branch, kept aside intentionally. The current
 * MonitoringPage is the Stream Health dashboard (decided at merge time).
 * Integrate this component manually when the operational view needs the
 * worker-level signals on top of the stream-health timeline.
 */
export default function WorkerHealthBadge() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['worker-status'],
    queryFn: () => api.get('/workers').then((r) => r.data),
    refetchInterval: 10000,
  })

  if (isLoading) return <div>Carregando status…</div>
  if (isError) return <div>Falha ao carregar status</div>

  const workers = data?.workers ?? []
  const clapOK = !!data?.clap_verifier

  return (
    <div className="worker-health-badge">
      <p>
        CLAP Verifier: <strong>{clapOK ? 'Online' : 'Offline'}</strong>
      </p>
      <table>
        <thead>
          <tr>
            <th>Emissora</th>
            <th>Status</th>
            <th>Último PCM</th>
          </tr>
        </thead>
        <tbody>
          {workers.map((w) => (
            <tr key={w.station_id}>
              <td>{w.station_id.slice(0, 8)}…</td>
              <td>{w.stall_risk ? 'Lento' : 'OK'}</td>
              <td>
                {w.last_pcm_at
                  ? new Date(w.last_pcm_at).toLocaleTimeString()
                  : '–'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
