import { useCallback, useEffect, useRef, useState } from 'react'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'
import { materialColor } from '../utils/materialColor'

export function pad2(n) { return String(n).padStart(2, '0') }
export function fmtDate(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}/${d.getFullYear()}`
}
export function fmtTime(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`
}
export function freqStr(d) {
  return d.frequency_mhz != null ? String(d.frequency_mhz).replace('.', ',') : null
}

/* Linha do feed "Últimas Veiculações": avatar, data/hora, station + freq +
 * cidade/UF, material com cor + cliente, e player de áudio inline (lazy-load
 * do blob via /detections/{id}/evidence). Compartilhada entre /live-map e
 * /management. */
export function LiveAiringRow({ detection, isPlaying, onPlayRequest, onPlayClose }) {
  const place = [detection.city, detection.state].filter(Boolean).join(' / ')
  const freq = freqStr(detection)
  const matColor = materialColor(detection.commercial_id)

  const [loading, setLoading] = useState(false)
  const [blobUrl, setBlobUrl] = useState(null)
  const blobRef = useRef(null)

  useEffect(() => () => {
    if (blobRef.current) URL.revokeObjectURL(blobRef.current)
  }, [])

  const ensureBlob = useCallback(async () => {
    if (blobRef.current) return blobRef.current
    const resp = await api.get(`/detections/${detection.id}/evidence`, { responseType: 'blob' })
    const url = URL.createObjectURL(resp.data)
    blobRef.current = url
    setBlobUrl(url)
    return url
  }, [detection.id])

  const hasAudio = detection.evidence_status === 'available'

  async function handlePlayClick() {
    if (loading || !hasAudio) return
    if (isPlaying) { onPlayClose(); return }
    setLoading(true)
    try {
      await ensureBlob()
      onPlayRequest(detection.id)
    } catch {
      // silencioso — usuário pode tentar de novo
    } finally {
      setLoading(false)
    }
  }

  return (
    <article
      className={'la-row' + (isPlaying ? ' la-row--playing' : '')}
      style={{ '--material-color': matColor }}
    >
      <button
        type="button"
        className="la-row-play"
        onClick={handlePlayClick}
        disabled={!hasAudio || loading}
        title={!hasAudio ? 'Sem áudio disponível' : (isPlaying ? 'Pausar' : 'Reproduzir')}
        aria-label={isPlaying ? 'Pausar' : `Reproduzir veiculação de ${fmtTime(detection.detected_at)} na ${detection.station_name}`}
      >
        {loading ? (
          <span className="la-row-spinner" aria-hidden />
        ) : isPlaying ? (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor" aria-hidden>
            <rect x="3" y="2" width="3" height="10" rx="1" />
            <rect x="8" y="2" width="3" height="10" rx="1" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor" aria-hidden>
            <path d="M3.5 2.5v9l8-4.5z" />
          </svg>
        )}
      </button>

      <div className="la-row-time-block">
        <span className="la-row-date">{fmtDate(detection.detected_at)}</span>
        <span className="la-row-time">{fmtTime(detection.detected_at)}</span>
      </div>

      <div className="la-row-station">
        <StationAvatar
          station={{ name: detection.station_name, logo_url: detection.station_logo_url }}
          size={36}
        />
        <div className="la-row-station-text">
          <span className="la-row-station-name">
            {detection.station_name}
            {freq && <span className="la-row-station-freq"> · {detection.band} ({freq})</span>}
          </span>
          {place && <span className="la-row-station-place">{place}</span>}
        </div>
      </div>

      <div className="la-row-material">
        <span className="la-row-material-name" style={{ color: matColor }} title={detection.commercial_name}>
          {detection.commercial_name}
        </span>
        {detection.client_name && (
          <span className="la-row-material-client">{detection.client_name}</span>
        )}
      </div>

      <span className="la-row-stripe" aria-hidden />

      {isPlaying && blobUrl && (
        <div className="la-row-player">
          <AudioPlayer
            src={blobUrl}
            isPlaying={isPlaying}
            onPlay={() => onPlayRequest(detection.id)}
            onPause={() => onPlayClose()}
          />
        </div>
      )}
    </article>
  )
}

/* Skeleton shape-matched do feed. */
export function FeedSkeleton() {
  const rows = [0, 1, 2, 3, 4, 5]
  return (
    <div className="la-list">
      {rows.map((i) => (
        <div className="la-row la-row--skel" key={i}>
          <span className="la-skel la-skel-circle" style={{ width: 30, height: 30 }} />
          <div className="la-row-time-block">
            <span className="la-skel" style={{ width: 62, height: 11 }} />
            <span className="la-skel" style={{ width: 52, height: 14, marginTop: 4 }} />
          </div>
          <div className="la-row-station">
            <span className="la-skel la-skel-circle" style={{ width: 36, height: 36 }} />
            <div className="la-row-station-text">
              <span className="la-skel" style={{ width: '78%', height: 13 }} />
              <span className="la-skel" style={{ width: '46%', height: 11, marginTop: 4 }} />
            </div>
          </div>
          <div className="la-row-material">
            <span className="la-skel" style={{ width: '70%', height: 13 }} />
            <span className="la-skel" style={{ width: '40%', height: 11, marginTop: 4 }} />
          </div>
        </div>
      ))}
    </div>
  )
}
