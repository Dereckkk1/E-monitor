import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'

const CATEGORY_META = {
  in_slot:  { label: 'Dentro da faixa',  className: 'airtime-cat-green' },
  out_slot: { label: 'Fora da faixa',    className: 'airtime-cat-yellow' },
  out_date: { label: 'Fora da data',     className: 'airtime-cat-purple' },
  orphan:   { label: 'Bônus',             className: 'airtime-cat-blue' },
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

  const stripe = detection.material_type_color || '#94a3b8'
  const cat = CATEGORY_META[detection.category] ?? { label: detection.category, className: 'airtime-cat-gray' }
  const cost = resolveCost(detection, pricingByStation)
  const pmm = fmtPMM(detection.station_pmm)
  const noEvidence = detection.evidence_status !== 'available'

  // Dial: "102,7 FM · São Paulo–SP" — gracefully omits missing parts.
  const dialParts = []
  if (detection.station_frequency_mhz != null) {
    const freq = String(detection.station_frequency_mhz).replace('.', ',')
    dialParts.push(`${freq} ${detection.station_band ?? ''}`.trim())
  }
  const place = [detection.station_city, detection.station_state].filter(Boolean).join('–')
  if (place) dialParts.push(place)
  const dial = dialParts.join(' · ')

  // Material subline: "Spot 30s · Rôgga"
  const materialSub = [detection.material_type_name, detection.client_name].filter(Boolean).join(' · ')

  return (
    <article
      className={
        'airtime-row' +
        (highlighted ? ' airtime-row-highlight' : '') +
        (isPlaying ? ' airtime-row-playing' : '')
      }
      aria-labelledby={`airtime-mat-${detection.id}`}
    >
      <div className="airtime-row-stripe" style={{ background: stripe }} aria-hidden />

      <button
        type="button"
        className="airtime-row-play"
        onClick={handlePlayClick}
        disabled={noEvidence}
        title={noEvidence ? 'Sem áudio disponível' : (isPlaying ? 'Pausar' : 'Reproduzir')}
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
        <span className="airtime-row-time">{fmtTime(detection.detected_at)}</span>
        <span className="airtime-row-date">{fmtDate(detection.detected_at)}</span>
      </div>

      <div className="airtime-row-station">
        <div className="airtime-row-logo">
          <StationAvatar station={stationForAvatar} size={40} />
        </div>
        <div className="airtime-row-station-text">
          <span className="airtime-row-station-name">{detection.station_name}</span>
          {dial && <span className="airtime-row-station-dial">{dial}</span>}
        </div>
      </div>

      <div className="airtime-row-material" id={`airtime-mat-${detection.id}`}>
        <span className="airtime-row-dot" style={{ background: stripe }} aria-hidden />
        <div className="airtime-row-material-text">
          <span className="airtime-row-material-title">{detection.commercial_name}</span>
          {materialSub && <span className="airtime-row-material-sub">{materialSub}</span>}
        </div>
      </div>

      <div className="airtime-row-metrics">
        <div className="airtime-row-metric">
          <span className="airtime-row-metric-label">PMM</span>
          <span
            className="airtime-row-metric-value"
            title={detection.station_pmm != null ? String(Math.round(detection.station_pmm)) : undefined}
          >
            {pmm ?? '—'}
          </span>
        </div>
        <div className="airtime-row-metric">
          <span className="airtime-row-metric-label">Custo</span>
          {cost.mode === 'consolidated' ? (
            <span className="airtime-row-cost-badge" title="Plano consolidado — sem custo por inserção">
              Plano
            </span>
          ) : cost.value != null ? (
            <span className="airtime-row-metric-value airtime-row-cost">{fmtCost(cost.value)}</span>
          ) : (
            <span className="airtime-row-metric-value">—</span>
          )}
        </div>
      </div>

      <span className={`airtime-row-cat ${cat.className}`}>{cat.label}</span>

      <button
        type="button"
        className="airtime-row-chevron"
        onClick={() => navigate(`/detections/${detection.id}`)}
        aria-label="Ver detalhes da veiculação"
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
          <path d="M6 4l4 4-4 4" />
        </svg>
      </button>

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
