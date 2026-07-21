import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'
import { materialColor } from '../utils/materialColor'
import { EVIDENCE_EXPIRED_SHORT } from '../utils/evidenceRetention'

const CATEGORY_DOT = {
  in_slot:  '#16A34A',
  out_slot: '#D97706',
  out_date: '#9333EA',
  orphan:   '#2563EB',
}

function fmtDate(iso) {
  return new Date(iso).toLocaleDateString('pt-BR')
}
function fmtTime(iso) {
  const d = new Date(iso)
  return [d.getHours(), d.getMinutes(), d.getSeconds()]
    .map(n => String(n).padStart(2, '0')).join(':')
}
function fmtPMM(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}
function fmtCost(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' }).format(n)
}

function resolveCost(detection, pricingByStation) {
  const p = pricingByStation?.[detection.station_id]
  if (!p) return { value: null, mode: 'none' }
  if (p.mode === 'consolidated') return { value: null, mode: 'consolidated' }
  if (p.mode === 'per_insertion') {
    const t = (p.per_type ?? []).find(t => t.type_id === detection.type_id)
    return { value: t?.unit_value ?? null, mode: 'per_insertion' }
  }
  return { value: null, mode: 'none' }
}

function IconHeadset() {
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M2 11v-3a6 6 0 0 1 12 0v3" />
      <path d="M2 11a1.5 1.5 0 0 1 1.5-1.5h1V13H3.5A1.5 1.5 0 0 1 2 11.5Z" fill="currentColor" stroke="none" />
      <path d="M14 11a1.5 1.5 0 0 0-1.5-1.5h-1V13h1A1.5 1.5 0 0 0 14 11.5Z" fill="currentColor" stroke="none" />
    </svg>
  )
}

function IconMoney() {
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <rect x="1.5" y="4" width="13" height="8" rx="1.5" />
      <circle cx="8" cy="8" r="1.5" />
      <path d="M4 5.5v.01M12 10.5v.01" />
    </svg>
  )
}

function IconCheck() {
  return (
    <svg width="10" height="10" viewBox="0 0 12 12" fill="none" aria-hidden>
      <circle cx="6" cy="6" r="6" fill="#E81E75" />
      <path d="M3.5 6l1.6 1.6L8.5 4.2" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

export default function AirtimeDetectionRow({
  detection,
  pricingByStation,
  isPlaying,
  onPlayRequest,
  onPlayClose,
  highlighted = false,
}) {
  const navigate = useNavigate()
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

  async function handlePlayClick() {
    if (loading) return
    if (isPlaying) {
      onPlayClose()
      return
    }
    setLoading(true)
    try {
      await ensureBlob()
      onPlayRequest(detection.id)
    } catch {
      // Silent: user clicks again to retry.
    } finally {
      setLoading(false)
    }
  }

  const stationForAvatar = {
    name: detection.station_name,
    logo_url: detection.station_logo_url,
  }

  const matColor = materialColor(detection.commercial_id)
  const cost = resolveCost(detection, pricingByStation)
  const pmm = fmtPMM(detection.station_pmm)
  const pmmTarget = detection.station_pmm_target != null ? fmtPMM(detection.station_pmm_target) : null
  const noEvidence = detection.evidence_status !== 'available'
  const isExpired = detection.evidence_status === 'expired'
  const noAudioTitle = isExpired ? EVIDENCE_EXPIRED_SHORT : 'Sem áudio disponível'
  const catDotColor = CATEGORY_DOT[detection.category] ?? '#94a3b8'

  // Dial line: "Classic Pan - FM (88.3) FM" style — name plus dial inline.
  const freqStr = detection.station_frequency_mhz != null
    ? String(detection.station_frequency_mhz).replace('.', ',')
    : null
  const place = [detection.station_city, detection.station_state].filter(Boolean).join(' / ')

  return (
    <article
      className={
        'airtime-row' +
        (highlighted ? ' airtime-row-highlight' : '') +
        (isPlaying ? ' airtime-row-playing' : '')
      }
      style={{ '--material-color': matColor }}
      aria-labelledby={`airtime-mat-${detection.id}`}
    >
      <button
        type="button"
        className="airtime-row-play"
        onClick={handlePlayClick}
        disabled={noEvidence}
        title={noEvidence ? noAudioTitle : (isPlaying ? 'Pausar' : 'Reproduzir')}
        aria-label={
          isPlaying
            ? 'Pausar'
            : `Reproduzir veiculação de ${fmtTime(detection.detected_at)} na ${detection.station_name}`
        }
      >
        {loading ? (
          <span className="airtime-row-spinner" aria-hidden />
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

      <div className="airtime-row-time-block">
        <span className="airtime-row-date">{fmtDate(detection.detected_at)}</span>
        <span className="airtime-row-time">{fmtTime(detection.detected_at)}</span>
      </div>

      <div className="airtime-row-station">
        <div className="airtime-row-logo">
          <StationAvatar station={stationForAvatar} size={40} />
          <span className="airtime-row-logo-check" aria-hidden><IconCheck /></span>
        </div>
        <div className="airtime-row-station-text">
          <span className="airtime-row-station-name">
            {detection.station_name}
            {freqStr && <span className="airtime-row-station-freq"> - {detection.station_band} ({freqStr})</span>}
          </span>
          {place && <span className="airtime-row-station-place">{place}</span>}
        </div>
      </div>

      <div className="airtime-row-pmm-stack">
        <div className="airtime-row-pill airtime-row-pill-pmm" title={detection.station_pmm != null ? `PMM: ${Math.round(detection.station_pmm)}` : 'PMM não cadastrado'}>
          <IconHeadset />
          <span>{pmm ?? '—'}</span>
        </div>
        {pmmTarget != null && (
          <div className="airtime-row-pill airtime-row-pill-target"
               title={`PMM no target: ${detection.station_pmm_target}`}>
            <IconHeadset />
            <span>
              {pmmTarget}
              <span className="airtime-row-pill-suffix-full"> target</span>
              <span className="airtime-row-pill-suffix-short"> tgt</span>
            </span>
          </div>
        )}
      </div>

      <div className={'airtime-row-pill airtime-row-pill-cost' + (cost.value == null && cost.mode !== 'consolidated' ? ' is-empty' : '')}
           title={cost.mode === 'consolidated' ? 'Plano consolidado — sem custo por inserção' : undefined}>
        <IconMoney />
        {cost.mode === 'consolidated' ? (
          <span>Plano</span>
        ) : cost.value != null ? (
          <span>{fmtCost(cost.value)}</span>
        ) : (
          <span>—</span>
        )}
      </div>

      <div className="airtime-row-material" id={`airtime-mat-${detection.id}`}>
        {detection.material_type_name && (
          <span className="airtime-row-material-type">{detection.material_type_name}</span>
        )}
        <button
          type="button"
          className="airtime-row-material-name"
          style={{ color: matColor }}
          onClick={() => navigate(`/detections/${detection.id}`)}
          title={detection.commercial_name}
        >
          <span className="airtime-row-cat-dot" style={{ background: catDotColor }} aria-hidden />
          {detection.commercial_name}
        </button>
        {detection.client_name && (
          <span className="airtime-row-material-client">{detection.client_name}</span>
        )}
      </div>

      <div className="airtime-row-stripe-right" aria-hidden />

      {isPlaying && blobUrl && (
        <div className="airtime-row-player">
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
