import { useState, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useCampaigns, useDetections, useStations, useClients } from '../api/hooks'
import RSelect from '../components/RSelect'
import DetectionsCalendar from '../components/DetectionsCalendar'
import DayDetailModal from '../components/DayDetailModal'
import { bucketDetections } from './detections/utils'

// ── Helpers ──────────────────────────────────────────────────────

function currentMonthValue() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

function prevMonthValue(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  const d = new Date(y, m - 2, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

function monthToRange(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  const start = new Date(y, m - 1, 1, 0, 0, 0, 0)
  const end   = new Date(y, m, 0, 23, 59, 59, 999)
  return { start, end }
}

function monthLabel(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
}

// ── Client mini avatar (for campaign select) ──────────────────────

function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  if (logo && !imgError) {
    return (
      <img
        src={logo}
        alt={name}
        width={size}
        height={size}
        style={{
          width: size, height: size,
          borderRadius: 4,
          objectFit: 'cover',
          flexShrink: 0,
          border: '1px solid #e2e8f0',
          display: 'block',
        }}
        onError={() => setImgError(true)}
      />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div style={{
      width: size, height: size,
      borderRadius: 4,
      background: '#fce7f3',
      color: '#E81E75',
      fontSize: Math.round(size * 0.42),
      fontWeight: 700,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      flexShrink: 0,
      fontFamily: "'Fira Sans Condensed', sans-serif",
      userSelect: 'none',
    }}>
      {initials}
    </div>
  )
}

// ── Skeleton ─────────────────────────────────────────────────────

const SKEL_DAYS = 14
const SKEL_HITS = [
  new Set([1, 3, 6, 9, 12]),
  new Set([0, 2, 5, 8, 11]),
  new Set([2, 4, 7, 10, 13]),
  new Set([1, 5, 6,  9, 12]),
  new Set([3, 4, 8, 11, 13]),
]
const SKEL_NAME_WIDTHS  = [108, 120, 95, 115, 102]
const SKEL_PLACE_WIDTHS = [74,  82,  68, 78,  72]

function SkeletonCalendar() {
  const days = Array.from({ length: SKEL_DAYS }, (_, i) => i)
  return (
    <div className="calendar-card">
      <div className="calendar-grid">

        {/* Header row */}
        <div className="calendar-header-row">
          <div className="calendar-corner" />
          <div className="calendar-days-header">
            {days.map(i => (
              <div key={i} className="calendar-day-header">
                <div className="skeleton" style={{ width: 22, height: 10, borderRadius: 3 }} />
                <div className="skeleton" style={{ width: 16, height: 8,  borderRadius: 3, marginTop: 3 }} />
              </div>
            ))}
          </div>
        </div>

        {/* Station rows */}
        {SKEL_HITS.map((hits, ri) => (
          <div key={ri} className="calendar-row">
            <div className="calendar-station">
              <div className="skeleton" style={{ width: 40, height: 40, borderRadius: '50%', flexShrink: 0 }} />
              <div className="calendar-station-text">
                <div className="skeleton" style={{ width: SKEL_NAME_WIDTHS[ri],  height: 12, borderRadius: 4 }} />
                <div className="skeleton" style={{ width: SKEL_PLACE_WIDTHS[ri], height: 9,  borderRadius: 3, marginTop: 5 }} />
              </div>
            </div>
            <div className="calendar-cells">
              {days.map(i => (
                <div key={i} className="calendar-cell">
                  {hits.has(i) && (
                    <div className="skeleton" style={{ width: 26, height: 26, borderRadius: 6 }} />
                  )}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
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
      <p>Nenhuma veiculação detectada {periodLabel ? `em ${periodLabel}` : 'no período selecionado'}.</p>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────

export default function DetectionsPage() {
  const { data: campaigns = [], isLoading: loadingCampaigns } = useCampaigns()
  const { data: clients = [] } = useClients()

  const [searchParams] = useSearchParams()
  const [selectedCampaignId, setSelectedCampaignId] = useState(
    () => searchParams.get('campaign_id') ?? ''
  )
  const [selectedMonth, setSelectedMonth] = useState(currentMonthValue)
  const [modalCell, setModalCell] = useState(null)

  const { data: stationsResp } = useStations({ limit: 2000 })
  const stationCatalog = stationsResp?.data ?? []

  // Derive period from selected month
  const period = useMemo(() => monthToRange(selectedMonth), [selectedMonth])

  // Client lookup map
  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  // Build detection filters — only run when campaign is selected
  const detectionFilters = useMemo(() => {
    if (!selectedCampaignId) return null
    return {
      campaign_id: selectedCampaignId,
      start_date:  period.start.toISOString(),
      end_date:    period.end.toISOString(),
      limit:       5000,
    }
  }, [selectedCampaignId, period])

  const {
    data: detections = [],
    isLoading: loadingDetections,
    isFetching,
    refetch,
  } = useDetections(detectionFilters)

  const showDetections = !!selectedCampaignId
  const isLoadingData  = showDetections && (loadingDetections || isFetching)

  // ── Campaign change ───────────────────────────────────────────
  function handleCampaignChange(opt) {
    setSelectedCampaignId(opt?.value ?? '')
    setSelectedMonth(currentMonthValue())
    setModalCell(null)
  }

  const campaignOptions = campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value:      c.id,
      label:      c.name,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
    }
  })

  const selectedCampaignOption = campaignOptions.find(o => o.value === selectedCampaignId) ?? null

  function formatCampaignOption(opt, { context }) {
    const size = context === 'value' ? 18 : 22
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
        <ClientMiniAvatar name={opt.clientName} logo={opt.clientLogo} size={size} />
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 5, minWidth: 0, overflow: 'hidden' }}>
          {opt.clientName && (
            <span style={{ fontWeight: 600, color: '#06055B', whiteSpace: 'nowrap', fontSize: 13 }}>
              {opt.clientName}
            </span>
          )}
          {opt.clientName && (
            <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>|</span>
          )}
          <span style={{
            color: '#4b5563',
            fontSize: 13,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}>
            {opt.label}
          </span>
        </div>
      </div>
    )
  }

  const targetStations = useMemo(() => {
    if (!selectedCampaignId || stationCatalog.length === 0) return []
    const campaign = campaigns.find(c => c.id === selectedCampaignId)
    if (!campaign) return []
    const ids = new Set(campaign.target_stations ?? [])
    return stationCatalog
      .filter(s => ids.has(s.id))
      .sort((a, b) => a.name.localeCompare(b.name, 'pt-BR'))
  }, [selectedCampaignId, campaigns, stationCatalog])

  // ── Month navigation ──────────────────────────────────────────
  const currentMonth = currentMonthValue()
  const prevMonth    = prevMonthValue(currentMonth)

  function handleMonthChange(e) {
    if (!e.target.value) return
    setSelectedMonth(e.target.value)
    setModalCell(null)
  }

  // ── Render ────────────────────────────────────────────────────
  return (
    <div>
      <div className="page-header">
        <h2>Veiculações</h2>
      </div>

      {/* Campaign selector + refresh */}
      <div className="detection-header">
        <div className="campaign-selector-wrap">
          <label htmlFor="campaign-select">Campanha</label>
          <RSelect
            inputId="campaign-select"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={handleCampaignChange}
            formatOptionLabel={formatCampaignOption}
            isDisabled={loadingCampaigns}
            isLoading={loadingCampaigns}
            placeholder={loadingCampaigns ? 'Carregando campanhas…' : 'Selecione uma campanha'}
            isClearable
          />
        </div>

        {showDetections && (
          <button
            className="btn-refresh"
            onClick={() => refetch()}
            disabled={isFetching}
            title="Atualizar veiculações"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              style={{ flexShrink: 0 }}
              className={isFetching ? 'spinning' : ''}
            >
              <path
                d="M13.5 8A5.5 5.5 0 1 1 8 2.5a5.47 5.47 0 0 1 3.5 1.27"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
              <path
                d="M11.5 1.5V4H14"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            Atualizar
          </button>
        )}
      </div>

      {/* Period filters — monthly */}
      {showDetections && (
        <div className="period-filters">
          <button
            className={`period-pill${selectedMonth === currentMonth ? ' active' : ''}`}
            onClick={() => { setSelectedMonth(currentMonth); setModalCell(null) }}
          >
            Mês atual
          </button>
          <button
            className={`period-pill${selectedMonth === prevMonth ? ' active' : ''}`}
            onClick={() => { setSelectedMonth(prevMonth); setModalCell(null) }}
          >
            Mês anterior
          </button>

          <div className="period-divider" />

          <div className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 6, marginBottom: 0 }}>
            <label style={{ marginBottom: 0, fontSize: 12, color: 'var(--c-text-3)', fontWeight: 600 }}>
              Período
            </label>
            <input
              className="input-month"
              type="month"
              value={selectedMonth}
              onChange={handleMonthChange}
            />
          </div>
        </div>
      )}

      {/* Content area */}
      {!showDetections ? (
        <EmptyNoCampaign />
      ) : isLoadingData ? (
        <SkeletonCalendar />
      ) : detections.length === 0 ? (
        <EmptyNoDetections periodLabel={monthLabel(selectedMonth)} />
      ) : (
        <DetectionsCalendar
          stations={targetStations}
          detections={detections}
          period={period}
          onCellClick={(station, dayKey) => setModalCell({ station, dayKey })}
        />
      )}

      {modalCell && (
        <DayDetailModal
          station={modalCell.station}
          dayKey={modalCell.dayKey}
          buckets={bucketDetections(detections)}
          onClose={() => setModalCell(null)}
        />
      )}
    </div>
  )
}
