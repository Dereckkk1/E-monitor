import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useDetections } from '../api/hooks'
import BadgePill from './BadgePill'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'

const CATEGORY_LABEL = {
  in_slot:  { label: 'Dentro da faixa', variant: 'green' },
  out_slot: { label: 'Fora da faixa',   variant: 'yellow' },
  out_date: { label: 'Fora da data',    variant: 'purple' },
  orphan:   { label: 'Bônus (sem regra)', variant: 'blue' },
}

function fmtTime(iso) {
  const d = new Date(iso)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

function fmtDate(iso) {
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

/**
 * Day-detail modal for the /detections grid.
 *
 * Props:
 *  - campaignId: uuid
 *  - stationId: uuid
 *  - materialId: uuid (same UUID as commercial_id in detections)
 *  - dateISO: 'YYYY-MM-DD'
 *  - station: station object (for displaying name)
 *  - material: material object (for displaying title)
 *  - cellSummary: row from daily_play_summary view ({expected, in_slot, deficit, bonus, out_slot, out_date})
 *  - onClose: () => void
 */
export default function DayDetailModal({
  campaignId, stationId, typeId, dateISO,
  station, materialType, cellSummary,
  onClose,
}) {
  const [activePlayerId, setActivePlayerId] = useState(null)
  // Blob URLs keyed by detection id. Using a ref for synchronous cache
  // lookups and state for triggering re-renders.
  const [evidenceBlobUrls, setEvidenceBlobUrls] = useState({})
  const blobUrlsRef = useRef({})
  const [loadingId, setLoadingId] = useState(null)

  // Esc to close
  useEffect(() => {
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // Revoke all blob URLs when the modal unmounts to free memory.
  useEffect(() => {
    return () => {
      Object.values(blobUrlsRef.current).forEach(u => URL.revokeObjectURL(u))
    }
  }, [])

  // ensureEvidenceUrl fetches the audio blob from the API proxy endpoint
  // on first call, caches the resulting blob URL, and returns it on
  // subsequent calls. Using the proxy avoids presigned MinIO URLs (which
  // break in prod due to mixed-content / localhost addressing).
  const ensureEvidenceUrl = useCallback(async (id) => {
    if (blobUrlsRef.current[id]) return blobUrlsRef.current[id]
    const resp = await api.get(`/detections/${id}/evidence`, { responseType: 'blob' })
    const blobUrl = URL.createObjectURL(resp.data)
    blobUrlsRef.current[id] = blobUrl
    setEvidenceBlobUrls(prev => ({ ...prev, [id]: blobUrl }))
    return blobUrl
  }, [])

  async function handlePlay(id) {
    if (loadingId) return
    if (evidenceBlobUrls[id]) {
      setActivePlayerId(id)
      return
    }
    setLoadingId(id)
    try {
      await ensureEvidenceUrl(id)
      setActivePlayerId(id)
    } catch {
      // Swallow: next click retries. AudioPlayer with no src renders idle.
    } finally {
      setLoadingId(null)
    }
  }

  async function handleDownload(id) {
    try {
      const url = await ensureEvidenceUrl(id)
      const a = document.createElement('a')
      a.href = url
      a.download = `veiculacao-${id}.m4a`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      // Same rationale as play: silent failure, user can retry.
    }
  }

  // Fetch detections for this (campaign, station, day) using São Paulo local-day
  // range (UTC-3). The daily_play_summary view buckets by SP day; matching that
  // here ensures we get the same detections the view counted.
  const startISO = new Date(`${dateISO}T00:00:00.000-03:00`).toISOString()
  const endISO   = new Date(`${dateISO}T23:59:59.999-03:00`).toISOString()
  const { data: detectionsResp, isLoading } = useDetections({
    campaign_id: campaignId,
    station_id: stationId,
    start_date: startISO,
    end_date: endISO,
    limit: 200,
  })

  // useDetections may return either an array directly or {data: [...]}
  const detections = Array.isArray(detectionsResp)
    ? detectionsResp
    : (detectionsResp?.data ?? [])

  // Migration 0019: rows are by TYPE, so the modal filters detections to those
  // whose material's type matches. The backend enriches each detection with
  // type_id via JOIN materials (catalog/detections.go).
  const filtered = detections.filter(d => d.type_id === typeId)
  const grouped = {
    in_slot:  filtered.filter(d => d.category === 'in_slot'),
    out_slot: filtered.filter(d => d.category === 'out_slot'),
    out_date: filtered.filter(d => d.category === 'out_date'),
    orphan:   filtered.filter(d => d.category === 'orphan'),
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div
        className="modal"
        style={{
          maxWidth: 700,
          maxHeight: 'calc(100vh - 48px)',
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}
        onClick={e => e.stopPropagation()}
      >
        <div className="modal-header" style={{ flexShrink: 0 }}>
          <div>
            <h3 style={{ margin: 0, display: 'flex', alignItems: 'center', gap: 8 }}>
              {materialType?.color && (
                <span style={{
                  display: 'inline-block', width: 10, height: 10, borderRadius: 3,
                  background: materialType.color, flexShrink: 0,
                }} />
              )}
              {materialType?.name ?? 'Tipo'} · {station?.name ?? 'Emissora'}
            </h3>
            <p style={{ margin: '4px 0 0', color: '#64748b', fontSize: 13 }}>
              {fmtDate(dateISO)} — materiais individuais detectados abaixo
            </p>
          </div>
          <button className="modal-close" onClick={onClose} type="button">×</button>
        </div>

        <div className="modal-body" style={{ padding: 20, flex: 1, overflowY: 'auto', minHeight: 0 }}>

          {/* Cell summary badges */}
          {cellSummary && (
            <div style={{
              display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 20,
              padding: 14, background: '#fafbfc', borderRadius: 8, border: '1px solid #e2e8f0',
            }}>
              <SummaryStat label="Esperado" value={cellSummary.expected} variant="gray" />
              <SummaryStat label="Tocou (faixa)" value={cellSummary.in_slot} variant="green" />
              {cellSummary.deficit > 0 && (
                <SummaryStat label="Faltou" value={cellSummary.deficit} variant="red" />
              )}
              {cellSummary.bonus > 0 && (
                <SummaryStat label="Bônus" value={cellSummary.bonus} variant="blue" prefix="+" />
              )}
              {cellSummary.out_slot > 0 && (
                <SummaryStat label="Fora faixa" value={cellSummary.out_slot} variant="yellow" prefix="+" />
              )}
              {cellSummary.out_date > 0 && (
                <SummaryStat label="Fora data" value={cellSummary.out_date} variant="purple" prefix="+" />
              )}
            </div>
          )}

          {isLoading ? (
            <p style={{ color: '#64748b' }}>Carregando detecções…</p>
          ) : filtered.length === 0 ? (
            <p style={{ color: '#64748b' }}>Nenhuma detection registrada nesse dia.</p>
          ) : (
            <DetectionsList
              grouped={grouped}
              materialType={materialType}
              activePlayerId={activePlayerId}
              evidenceBlobUrls={evidenceBlobUrls}
              loadingId={loadingId}
              onPlay={handlePlay}
              onPause={() => setActivePlayerId(null)}
              onDownload={handleDownload}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function SummaryStat({ label, value, variant, prefix }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
      <span style={{ fontSize: 10, color: '#64748b', textTransform: 'uppercase', fontWeight: 600 }}>{label}</span>
      <BadgePill variant={variant} value={value} prefix={prefix ?? ''} />
    </div>
  )
}

function DetectionsList({ grouped, materialType, activePlayerId, evidenceBlobUrls, loadingId, onPlay, onPause, onDownload }) {
  // Material type drives the colored left border + chip on every row. The row
  // title is the *individual* material name (commercial_name from the
  // detection record) so the user sees exactly which cut played even though
  // the cell is keyed by type.
  const typeColor = materialType?.color ?? '#94a3b8'
  const typeName  = materialType?.name  ?? 'Sem tipo'

  return (
    <div>
      {Object.entries(grouped).map(([cat, list]) => {
        if (list.length === 0) return null
        const { label, variant } = CATEGORY_LABEL[cat]
        return (
          <div key={cat} style={{ marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <BadgePill variant={variant} value={list.length} />
              <strong style={{ fontSize: 13 }}>{label}</strong>
            </div>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0, fontSize: 12 }}>
              {list.map(d => {
                const isPlaying = activePlayerId === d.id
                const isLoadingThis = loadingId === d.id
                // Show audio controls for any status that has a chance of having audio.
                // Backend returns 404 if evidence isn't actually available — handled in handlePlay's catch.
                const hasEvidence = d.evidence_status === 'available' ||
                                    d.evidence_status === 'pending' ||
                                    d.evidence_status === 'generating' ||
                                    !d.evidence_status  // legacy detections may have no status
                const evidenceLabel = d.evidence_status === 'pending' ? 'processando…'
                  : d.evidence_status === 'generating' ? 'gerando…'
                  : d.evidence_status === 'missing' ? 'sem áudio'
                  : d.evidence_status === 'failed' ? 'falhou'
                  : null
                return (
                  <li key={d.id} style={{
                    padding: '8px 10px 8px 12px', background: '#fafbfc',
                    borderRadius: 6, marginBottom: 4,
                    borderLeft: `3px solid ${typeColor}`,
                    display: 'flex', flexDirection: 'column', gap: 6,
                  }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                      <span style={{ color: typeColor, fontWeight: 600, fontSize: 12 }}>
                        {d.commercial_name || 'Material sem título'}
                      </span>
                      <span style={{
                        fontSize: 10, fontWeight: 600, textTransform: 'uppercase',
                        letterSpacing: '0.04em',
                        color: typeColor,
                        background: `color-mix(in srgb, ${typeColor} 12%, transparent)`,
                        padding: '2px 7px', borderRadius: 10,
                      }}>
                        {typeName}
                      </span>
                      <Link
                        to={`/detections/${d.id}`}
                        style={{
                          marginLeft: 'auto', fontSize: 11, color: '#E81E75',
                          fontWeight: 600, textDecoration: 'none',
                        }}
                        title="Abrir detalhes da veiculação"
                      >
                        Detalhes →
                      </Link>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, justifyContent: 'space-between' }}>
                      <span style={{ fontFamily: 'monospace', fontWeight: 600 }}>{fmtTime(d.detected_at)}</span>
                      <span style={{ color: '#64748b', flex: 1, marginLeft: 12 }}>
                        conf {(d.confidence * 100).toFixed(0)}% · hash {d.hash_count}
                      </span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        {hasEvidence && (
                          <>
                            <AudioPlayer
                              src={evidenceBlobUrls[d.id] || ''}
                              isPlaying={isPlaying}
                              onPlay={() => onPlay(d.id)}
                              onPause={onPause}
                            />
                            <button
                              type="button"
                              className="day-detail-download"
                              onClick={() => onDownload(d.id)}
                              title="Baixar áudio"
                              aria-label="Baixar evidência de áudio"
                              disabled={isLoadingThis}
                            >
                              <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
                                <path d="M8 2v8M5 7l3 3 3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                                <path d="M2 12h12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                              </svg>
                            </button>
                          </>
                        )}
                        {!hasEvidence && (
                          <span style={{ fontSize: 11, color: '#94a3b8' }}>
                            {evidenceLabel || 'indisponível'}
                          </span>
                        )}
                        {hasEvidence && evidenceLabel && (
                          <span style={{ fontSize: 10, color: '#94a3b8', marginLeft: 6 }}>
                            {evidenceLabel}
                          </span>
                        )}
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
          </div>
        )
      })}
    </div>
  )
}
