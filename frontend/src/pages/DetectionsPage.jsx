import { useState, useMemo } from 'react'
import { useCampaigns, useDetections } from '../api/hooks'
import AudioPlayer from '../components/AudioPlayer'

// ── Helpers ──────────────────────────────────────────────────────

function formatDateTime(isoString) {
  if (!isoString) return '—'
  const d = new Date(isoString)
  const day   = String(d.getDate()).padStart(2, '0')
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const year  = d.getFullYear()
  const hh    = String(d.getHours()).padStart(2, '0')
  const mm    = String(d.getMinutes()).padStart(2, '0')
  const ss    = String(d.getSeconds()).padStart(2, '0')
  return `${day}/${month}/${year} ${hh}:${mm}:${ss}`
}

function formatDuration(startMs, endMs) {
  if (!startMs && !endMs) return '—'
  const diff = (endMs - startMs) / 1000
  if (diff <= 0) return '—'
  const m = Math.floor(diff / 60)
  const s = Math.round(diff % 60)
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

function startOfDay(date) {
  const d = new Date(date)
  d.setHours(0, 0, 0, 0)
  return d
}

function endOfDay(date) {
  const d = new Date(date)
  d.setHours(23, 59, 59, 999)
  return d
}

function toDateInputValue(date) {
  const d = new Date(date)
  const year  = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day   = String(d.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function defaultPeriod() {
  const end   = endOfDay(new Date())
  const start = startOfDay(new Date(Date.now() - 6 * 24 * 60 * 60 * 1000))
  return { start, end, preset: '7d' }
}

// ── Skeleton ─────────────────────────────────────────────────────

function SkeletonTable() {
  const widths = [
    ['18%', '14%', '8%', '22%', '24%'],
    ['16%', '14%', '8%', '28%', '24%'],
    ['20%', '14%', '8%', '20%', '24%'],
    ['17%', '14%', '8%', '26%', '24%'],
    ['19%', '14%', '8%', '22%', '24%'],
  ]
  return (
    <div className="card" style={{ overflow: 'hidden' }}>
      {/* Fake thead */}
      <div style={{
        display: 'flex',
        gap: 16,
        padding: '8px 12px',
        borderBottom: '1px solid var(--c-border)',
        background: 'var(--c-surface-2)',
      }}>
        {['18%', '14%', '8%', '22%', '24%'].map((w, i) => (
          <div key={i} className="skeleton-cell" style={{ width: w, height: 12 }} />
        ))}
      </div>
      {widths.map((row, ri) => (
        <div key={ri} className="skeleton-row">
          {row.map((w, ci) => (
            <div key={ci} className="skeleton-cell" style={{ width: w }} />
          ))}
        </div>
      ))}
    </div>
  )
}

// ── Empty states ─────────────────────────────────────────────────

function EmptyNoCampaign() {
  return (
    <div className="detection-empty">
      <div className="detection-empty-icon">
        <svg width="56" height="56" viewBox="0 0 56 56" fill="none">
          <circle cx="28" cy="28" r="27" stroke="currentColor" strokeWidth="2" />
          <path d="M14 28c0-7.732 6.268-14 14-14" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          <path d="M19 28c0-4.97 4.03-9 9-9"      stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          <path d="M24 28c0-2.21 1.79-4 4-4"       stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          <circle cx="28" cy="28" r="2" fill="currentColor" />
          <path d="M42 28c0 7.732-6.268 14-14 14"  stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          <path d="M37 28c0 4.97-4.03 9-9 9"       stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          <path d="M32 28c0 2.21-1.79 4-4 4"       stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      </div>
      <h3>Selecione uma campanha</h3>
      <p>Escolha uma campanha acima para ver as veiculações detectadas.</p>
    </div>
  )
}

function EmptyNoDetections({ periodLabel }) {
  return (
    <div className="detection-empty">
      <div className="detection-empty-icon">
        <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
          <rect x="6" y="12" width="36" height="28" rx="4" stroke="currentColor" strokeWidth="2" />
          <path d="M14 8h20" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          <path d="M18 24h12M18 30h8" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </div>
      <h3>Nenhuma veiculação encontrada</h3>
      <p>Nenhuma veiculação detectada {periodLabel ? `no período ${periodLabel}` : 'no período selecionado'}.</p>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────

export default function DetectionsPage() {
  const { data: campaigns = [], isLoading: loadingCampaigns } = useCampaigns()

  const [selectedCampaignId, setSelectedCampaignId] = useState('')
  const [period, setPeriod] = useState(defaultPeriod)
  const [activePlayerId, setActivePlayerId] = useState(null)

  // Build detection filters — only run when campaign is selected
  const detectionFilters = useMemo(() => {
    if (!selectedCampaignId) return null
    return {
      campaign_id: selectedCampaignId,
      start_date:  period.start.toISOString(),
      end_date:    period.end.toISOString(),
      limit:       200,
    }
  }, [selectedCampaignId, period])

  const {
    data: detections = [],
    isLoading: loadingDetections,
    isFetching,
  } = useDetections(detectionFilters ?? {})

  // Suppress query when no campaign selected
  const showDetections  = !!selectedCampaignId
  const isLoadingData   = showDetections && (loadingDetections || isFetching)

  // ── Campaign change ───────────────────────────────────────────
  function handleCampaignChange(e) {
    setSelectedCampaignId(e.target.value)
    setPeriod(defaultPeriod())
    setActivePlayerId(null)
  }

  // ── Period presets ────────────────────────────────────────────
  function applyPreset(preset) {
    const now = new Date()
    let start, end
    if (preset === 'today') {
      start = startOfDay(now)
      end   = endOfDay(now)
    } else if (preset === '7d') {
      start = startOfDay(new Date(now - 6 * 24 * 60 * 60 * 1000))
      end   = endOfDay(now)
    } else if (preset === '30d') {
      start = startOfDay(new Date(now - 29 * 24 * 60 * 60 * 1000))
      end   = endOfDay(now)
    }
    setPeriod({ start, end, preset })
    setActivePlayerId(null)
  }

  function handleStartDateChange(e) {
    if (!e.target.value) return
    const d = startOfDay(new Date(e.target.value + 'T00:00:00'))
    setPeriod(p => ({ ...p, start: d, preset: null }))
    setActivePlayerId(null)
  }

  function handleEndDateChange(e) {
    if (!e.target.value) return
    const d = endOfDay(new Date(e.target.value + 'T00:00:00'))
    setPeriod(p => ({ ...p, end: d, preset: null }))
    setActivePlayerId(null)
  }

  // ── Period label for empty state ──────────────────────────────
  const periodLabel = useMemo(() => {
    const fmt = d => formatDateTime(d.toISOString()).slice(0, 10)
    return `${fmt(period.start)} – ${fmt(period.end)}`
  }, [period])

  // ── Render ────────────────────────────────────────────────────
  return (
    <div>
      <div className="page-header">
        <h2>Veiculações</h2>
      </div>

      {/* Campaign selector */}
      <div className="detection-header">
        <div className="campaign-selector-wrap">
          <label htmlFor="campaign-select">Campanha</label>
          <select
            id="campaign-select"
            className="select"
            value={selectedCampaignId}
            onChange={handleCampaignChange}
            disabled={loadingCampaigns}
          >
            <option value="">
              {loadingCampaigns ? 'Carregando campanhas…' : 'Selecione uma campanha'}
            </option>
            {campaigns.map(c => (
              <option key={c.id} value={c.id}>
                {c.name}{c.client_name ? ` — ${c.client_name}` : ''}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* Period filters — only visible after campaign is selected */}
      {showDetections && (
        <div className="period-filters">
          <button
            className={`period-pill${period.preset === 'today' ? ' active' : ''}`}
            onClick={() => applyPreset('today')}
          >
            Hoje
          </button>
          <button
            className={`period-pill${period.preset === '7d' ? ' active' : ''}`}
            onClick={() => applyPreset('7d')}
          >
            7 dias
          </button>
          <button
            className={`period-pill${period.preset === '30d' ? ' active' : ''}`}
            onClick={() => applyPreset('30d')}
          >
            30 dias
          </button>

          <div className="period-divider" />

          <div className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 0 }}>
            <label style={{ marginBottom: 0, fontSize: 12, color: 'var(--c-text-3)', fontWeight: 600 }}>De</label>
            <input
              className="input"
              type="date"
              style={{ width: 'auto' }}
              value={toDateInputValue(period.start)}
              onChange={handleStartDateChange}
            />
          </div>

          <div className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 0 }}>
            <label style={{ marginBottom: 0, fontSize: 12, color: 'var(--c-text-3)', fontWeight: 600 }}>Até</label>
            <input
              className="input"
              type="date"
              style={{ width: 'auto' }}
              value={toDateInputValue(period.end)}
              onChange={handleEndDateChange}
            />
          </div>
        </div>
      )}

      {/* Content area */}
      {!showDetections ? (
        <EmptyNoCampaign />
      ) : isLoadingData ? (
        <SkeletonTable />
      ) : detections.length === 0 ? (
        <EmptyNoDetections periodLabel={periodLabel} />
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th>Emissora</th>
                <th>Data / Hora</th>
                <th>Duração</th>
                <th>Comercial</th>
                <th>Áudio</th>
              </tr>
            </thead>
            <tbody>
              {detections.map(d => (
                <tr key={d.id}>
                  <td style={{ fontWeight: 500 }}>{d.station_name}</td>
                  <td style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>
                    {formatDateTime(d.detected_at)}
                  </td>
                  <td style={{ whiteSpace: 'nowrap' }}>
                    {formatDuration(d.match_start_offset_ms, d.match_end_offset_ms)}
                  </td>
                  <td>{d.commercial_name}</td>
                  <td>
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
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
