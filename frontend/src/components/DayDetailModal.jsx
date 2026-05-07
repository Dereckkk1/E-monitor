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
          {list.map(d => {
            const retracted = !!d.retracted_at
            const retractedTooltip = retracted
              ? `Retratada em ${formatRetractedAt(d.retracted_at)} — versão maior detectada`
              : undefined
            const itemStyle = retracted
              ? { textDecoration: 'line-through', opacity: 0.55 }
              : undefined
            return (
            <div
              key={d.id}
              className="day-detail-item"
              style={itemStyle}
              title={retractedTooltip}
            >
              <div className="day-detail-time">{formatTimeOnly(d.detected_at)}</div>
              <div className="day-detail-name">{d.commercial_name}</div>
              <div className="day-detail-audio">
                {d.evidence_status === 'available' ? (
                  <>
                    <AudioPlayer
                      src={`/v1/internal/detections/${d.id}/evidence`}
                      isPlaying={activePlayerId === d.id}
                      onPlay={() => setActivePlayerId(d.id)}
                      onPause={() => setActivePlayerId(null)}
                    />
                    <a
                      href={`/v1/internal/detections/${d.id}/evidence`}
                      download={`veiculacao-${d.id}.mp3`}
                      className="day-detail-download"
                      title="Baixar áudio"
                      aria-label="Baixar evidência de áudio"
                    >
                      <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
                        <path d="M8 2v8M5 7l3 3 3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                        <path d="M2 12h12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
                      </svg>
                    </a>
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
