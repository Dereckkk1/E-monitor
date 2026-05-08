import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'
import {
  detectionsFor,
  formatLongDay,
  formatTimeOnly,
  stationLabel,
} from '../pages/detections/utils'

const COMMERCIAL_COLORS = [
  '#16a34a', // verde
  '#7c3aed', // roxo
  '#2563eb', // azul
  '#ea580c', // laranja
  '#db2777', // rosa
  '#0891b2', // turquesa
  '#ca8a04', // âmbar
  '#64748b', // cinza-azul
]

// Compact dd/mm/yyyy hh:mm display used in the retracted tooltip (§18.2.2).
function formatRetractedAt(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  const pad = n => String(n).padStart(2, '0')
  return `${pad(d.getDate())}/${pad(d.getMonth() + 1)}/${d.getFullYear()} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export default function DayDetailModal({ station, dayKey, buckets, onClose }) {
  const [activePlayerId, setActivePlayerId] = useState(null)
  // Cache of presigned evidence URLs keyed by detection id. Each entry is
  // { url, expiresAt } where expiresAt is the absolute deadline returned by
  // the API. We refresh when within 30s of expiry so playback / download
  // never hits a stale signature mid-stream.
  const [evidenceUrls, setEvidenceUrls] = useState({})
  const [loadingId, setLoadingId] = useState(null)

  useEffect(() => {
    function onKey(e) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // ensureEvidenceUrl returns a usable presigned URL for the detection,
  // fetching one on the first call and re-using the cached one until it's
  // close to expiry. Throws on 4xx/5xx so callers can surface the failure.
  const ensureEvidenceUrl = useCallback(async (id) => {
    const cached = evidenceUrls[id]
    if (cached && new Date(cached.expiresAt).getTime() > Date.now() + 30_000) {
      return cached.url
    }
    const { data } = await api.get(`/detections/${id}/evidence/url`)
    setEvidenceUrls(prev => ({ ...prev, [id]: data }))
    return data.url
  }, [evidenceUrls])

  async function handlePlay(id) {
    if (loadingId) return
    if (evidenceUrls[id]?.url) {
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
      a.download = `veiculacao-${id}.mp3`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      // Same rationale as play: silent failure, user can retry.
    }
  }

  if (!station || !dayKey) return null

  const list = detectionsFor(buckets, station.id, dayKey)
  const { primary, secondary } = stationLabel(station)

  // Assign a color to each unique commercial in the order they first appear
  const commercialColorMap = new Map()
  let colorIdx = 0
  for (const d of list) {
    if (!commercialColorMap.has(d.commercial_id)) {
      commercialColorMap.set(d.commercial_id, COMMERCIAL_COLORS[colorIdx % COMMERCIAL_COLORS.length])
      colorIdx++
    }
  }

  return createPortal(
    <div className="day-detail-backdrop" onClick={onClose} role="dialog" aria-modal="true">
      <div className="day-detail-card" onClick={e => e.stopPropagation()}>
        <button className="day-detail-close" onClick={onClose} aria-label="Fechar">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
        </button>

        <div className="day-detail-header">
          <StationAvatar station={station} size={40} />
          <div className="day-detail-station-text">
            <div className="day-detail-station-name">{primary}</div>
            <div className="day-detail-station-place">{secondary}</div>
          </div>
          <div className="day-detail-date">{formatLongDay(dayKey)}</div>
        </div>

        <div className="day-detail-subtitle">
          {list.length} {list.length === 1 ? 'veiculação' : 'veiculações'}
        </div>

        <div className="day-detail-list">
          {list.map(d => {
            const retracted = !!d.retracted_at
            const retractedTooltip = retracted
              ? `Retratada em ${formatRetractedAt(d.retracted_at)} — versão maior detectada`
              : undefined
            const accentColor = commercialColorMap.get(d.commercial_id)
            const itemStyle = {
              borderLeft: `3px solid ${accentColor}`,
              ...(retracted ? { textDecoration: 'line-through', opacity: 0.55 } : {}),
            }
            return (
            <div
              key={d.id}
              className="day-detail-item"
              style={itemStyle}
              title={retractedTooltip}
            >
              <div className="day-detail-time">{formatTimeOnly(d.detected_at)}</div>
              <div className="day-detail-name" style={{ color: accentColor }}>{d.commercial_name}</div>
              <div className="day-detail-audio">
                {d.evidence_status === 'available' ? (
                  <>
                    <AudioPlayer
                      src={evidenceUrls[d.id]?.url || ''}
                      isPlaying={activePlayerId === d.id}
                      onPlay={() => handlePlay(d.id)}
                      onPause={() => setActivePlayerId(null)}
                    />
                    <button
                      type="button"
                      onClick={() => handleDownload(d.id)}
                      className="day-detail-download"
                      title="Baixar áudio"
                      aria-label="Baixar evidência de áudio"
                    >
                      <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
                        <path d="M8 2v8M5 7l3 3 3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                        <path d="M2 12h12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                      </svg>
                    </button>
                  </>
                ) : (
                  <span className="text-muted" style={{ fontSize: 11 }}>
                    {d.evidence_status || 'indisponível'}
                  </span>
                )}
              </div>
            </div>
            )
          })}
        </div>
      </div>
    </div>,
    document.body
  )
}
