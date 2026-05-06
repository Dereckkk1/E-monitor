import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import {
  detectionsFor,
  formatLongDay,
  formatTimeOnly,
  stationLabel,
} from '../pages/detections/utils'

export default function DayDetailModal({ station, dayKey, buckets, onClose }) {
  const [activePlayerId, setActivePlayerId] = useState(null)

  useEffect(() => {
    function onKey(e) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  if (!station || !dayKey) return null

  const list = detectionsFor(buckets, station.id, dayKey)
  const { primary, secondary } = stationLabel(station)

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
          {list.map(d => (
            <div key={d.id} className="day-detail-item">
              <div className="day-detail-time">{formatTimeOnly(d.detected_at)}</div>
              <div className="day-detail-name">{d.commercial_name}</div>
              <div className="day-detail-audio">
                {d.evidence_status === 'available' ? (
                  <AudioPlayer
                    src={`/v1/internal/detections/${d.id}/evidence`}
                    isPlaying={activePlayerId === d.id}
                    onPlay={() => setActivePlayerId(d.id)}
                    onPause={() => setActivePlayerId(null)}
                  />
                ) : (
                  <span className="text-muted" style={{ fontSize: 11 }}>
                    {d.evidence_status || 'indisponível'}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>,
    document.body
  )
}
