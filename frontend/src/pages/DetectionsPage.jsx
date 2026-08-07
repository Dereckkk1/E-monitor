import { Fragment, useState, useMemo, useRef, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  useCampaigns, useStations, useClients,
  useCampaignMaterials, useMaterials, useDistributionRules,
  useMaterialTypes, useDailySummary, useCampaignPricing,
  useClientTargetPmm,
} from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import RSelect from '../components/RSelect'
import DistributionGrid from '../components/DistributionGrid'
import DayDetailModal from '../components/DayDetailModal'
import CoverageSummary from '../components/CoverageSummary'
import FlowStepper from '../components/FlowStepper'
import CampaignReportsMenu from '../components/CampaignReportsMenu'
import AirtimePaginator from '../components/AirtimePaginator'
import { tokenize, matchesAllTokens } from '../utils/search'
import { safeLogoUrl } from '../utils/logoUrl'
import { buildGridReportModel } from '../utils/gridReport'
import { buildGridRows } from '../utils/gridRows'
import {
  parseLocalDate, monthFromDate, isoFromDate, monthToRange,
  monthLabel, rangeLabel, defaultRangeForCampaign, campaignRangeISO, formatCampaignPeriod,
  enumerateVisibleDays,
} from '../utils/dates'

const STEP_LABELS = ['Competência', 'Campanha', 'Período']
const PAGE_SIZE_OPTIONS = [5, 10, 15]
const DEFAULT_PAGE_SIZE = 5

// ── Client mini avatar (for campaign select) ──────────────────────

function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  const safeLogo = safeLogoUrl(logo)
  if (safeLogo && !imgError) {
    return (
      <img
        src={safeLogo}
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

// ── Skeleton (also used as ghost backdrop on empty states) ───────

// Mirror DistributionGrid's "stop at today" rule so the loading state has the
// same column count as the data view that follows it.
function skeletonDayCount(monthDate) {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  if (!monthDate) return today.getDate()
  const year = monthDate.getFullYear()
  const monthIdx = monthDate.getMonth()
  const daysInMonth = new Date(year, monthIdx + 1, 0).getDate()
  const isCurrentMonth = year === today.getFullYear() && monthIdx === today.getMonth()
  const isFutureMonth  = year > today.getFullYear() ||
    (year === today.getFullYear() && monthIdx > today.getMonth())
  return isFutureMonth ? 0 : (isCurrentMonth ? today.getDate() : daysInMonth)
}

function skeletonHits(dayCount, rowSeed) {
  const hits = new Set()
  for (let i = 0; i < dayCount; i++) {
    if (((i * 7 + rowSeed * 11) % 17) < 6) hits.add(i)
  }
  return hits
}

const SKEL_STATIONS = [
  { nameW: 108, placeW: 74, materials: [{ titleW: 60 }, { titleW: 76 }] },
  { nameW: 95,  placeW: 82, materials: [{ titleW: 68 }] },
  { nameW: 120, placeW: 68, materials: [{ titleW: 62 }, { titleW: 90 }] },
]

function SkeletonCalendar({ monthDate }) {
  const dayCount = skeletonDayCount(monthDate)
  const days = Array.from({ length: dayCount }, (_, i) => i)
  const ROW_SUM_W = 200
  const STATION_TOTAL_W = 180
  // Espelha o layout inline do DistributionGrid: bloco da emissora à esquerda
  // (240px, row-span sobre os materiais) + coluna de label do material (116px).
  const STATION_INFO_W = 240
  const MATERIAL_LABEL_W = 116
  const gridTemplate = `${STATION_INFO_W}px ${MATERIAL_LABEL_W}px repeat(${dayCount}, 88px) 1fr ${ROW_SUM_W}px ${STATION_TOTAL_W}px`

  let matRowIdx = 0

  return (
    <div style={{
      overflowX: 'auto', background: '#fff',
      border: '1px solid #f1f5f9', borderRadius: 12,
    }}>
      <div style={{
        display: 'grid', gridTemplateColumns: gridTemplate,
        fontSize: 12, minWidth: 'fit-content',
      }}>
        <div style={{ ...skelHead, left: 0, zIndex: 3 }} />
        <div style={{ ...skelHead, left: STATION_INFO_W, zIndex: 3 }} />
        {days.map(i => (
          <div key={`hd-${i}`} style={skelHead}>
            <div className="skeleton" style={{ width: 22, height: 9,  borderRadius: 3 }} />
            <div className="skeleton" style={{ width: 16, height: 11, borderRadius: 3, marginTop: 3 }} />
          </div>
        ))}
        <div style={{ ...skelHead, gridColumn: '-3 / -1', borderLeft: '2px solid #e2e8f0', borderRight: 'none', right: 0, zIndex: 3 }}>
          <div className="skeleton" style={{ width: 44, height: 10, borderRadius: 3 }} />
        </div>

        {SKEL_STATIONS.map((s, si) => (
          <Fragment key={`s-${si}`}>
            {s.materials.map((m, mi) => {
              const hits = skeletonHits(dayCount, matRowIdx++)
              return (
                <Fragment key={`m-${si}-${mi}`}>
                  {mi === 0 && (
                    <div style={{
                      gridColumn: '1 / 2',
                      gridRow: `span ${s.materials.length}`,
                      background: '#fff',
                      borderBottom: '1px solid #e2e8f0',
                      borderRight: '1px solid #e2e8f0',
                      padding: '12px 14px',
                      display: 'flex', alignItems: 'center', gap: 12,
                      position: 'sticky', left: 0, zIndex: 3,
                    }}>
                      <div className="skeleton" style={{ width: 40, height: 40, borderRadius: '50%', flexShrink: 0 }} />
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                        <div className="skeleton" style={{ width: s.nameW,  height: 12, borderRadius: 4 }} />
                        <div className="skeleton" style={{ width: s.placeW, height: 9,  borderRadius: 3 }} />
                        <div className="skeleton" style={{ width: s.placeW - 8, height: 9, borderRadius: 3 }} />
                      </div>
                    </div>
                  )}
                  <div style={{
                    padding: '11px 12px', background: '#fafbfc',
                    borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
                    display: 'flex', alignItems: 'center', gap: 8,
                    position: 'sticky', left: STATION_INFO_W, zIndex: 2,
                  }}>
                    <div className="skeleton" style={{ width: 3, height: 16, borderRadius: 2 }} />
                    <div className="skeleton" style={{ width: m.titleW, height: 11, borderRadius: 4 }} />
                  </div>
                  {days.map(d => (
                    <div key={`c-${si}-${mi}-${d}`} style={skelCell}>
                      {hits.has(d) && (
                        <div className="skeleton" style={{ width: 26, height: 26, borderRadius: 6 }} />
                      )}
                    </div>
                  ))}
                  <div style={{
                    ...skelCell, gridColumn: '-3 / -2', justifyContent: 'flex-start',
                    borderLeft: '2px solid #e2e8f0', padding: '8px 10px', gap: 10,
                    position: 'sticky', right: STATION_TOTAL_W, zIndex: 2, background: '#fff',
                  }}>
                    <div style={{ display: 'flex', gap: 3, flex: 1 }}>
                      {Array.from({ length: 6 }).map((_, k) => (
                        <div key={k} className="skeleton" style={{ width: 22, height: 16, borderRadius: 4 }} />
                      ))}
                    </div>
                  </div>
                  {mi === 0 && (
                    <div style={{
                      ...skelCell, gridColumn: '-2 / -1', gridRow: `span ${s.materials.length}`,
                      flexDirection: 'column', alignItems: 'stretch', justifyContent: 'center',
                      padding: '10px 12px', gap: 6,
                      borderLeft: '1px solid #f1f5f9',
                      position: 'sticky', right: 0, zIndex: 2, background: '#fff',
                    }}>
                      <div className="skeleton" style={{ width: '90%', height: 16, borderRadius: 999 }} />
                      <div className="skeleton" style={{ width: '90%', height: 16, borderRadius: 999 }} />
                    </div>
                  )}
                </Fragment>
              )
            })}
          </Fragment>
        ))}
      </div>
    </div>
  )
}

const skelHead = {
  background: '#fafbfc',
  borderBottom: '2px solid #e2e8f0',
  borderRight: '1px solid #f1f5f9',
  padding: '9px 6px',
  height: 44,
  position: 'sticky',
  top: 0,
  zIndex: 1,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 2,
}

const skelCell = {
  height: 56,
  borderBottom: '1px solid #f1f5f9',
  borderRight: '1px solid #f1f5f9',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
}

function SkeletonCoverage() {
  const stats = [
    { labelW: 56, badgeW: 38 },
    { labelW: 80, badgeW: 32 },
    { labelW: 46, badgeW: 34 },
    { labelW: 44, badgeW: 32 },
    { labelW: 68, badgeW: 36 },
  ]
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 24,
      padding: '14px 18px', marginBottom: 14,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div className="skeleton" style={{ width: 110, height: 10, borderRadius: 3 }} />
        <div className="skeleton" style={{ width: 72,  height: 28, borderRadius: 6 }} />
      </div>
      <div style={{ height: 40, width: 1, background: '#e2e8f0' }} />
      {stats.map((s, i) => (
        <div key={i} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div className="skeleton" style={{ width: s.labelW, height: 9,  borderRadius: 3 }} />
          <div className="skeleton" style={{ width: s.badgeW, height: 18, borderRadius: 999 }} />
        </div>
      ))}
    </div>
  )
}

// ── Empty state ──────────────────────────────────────────────────

// Progressive-disclosure stepper. Shown above the empty-state card so users
// learn the 3-step filter path (Competência → Campanha → Período em dias)
// without reading prose. Each step has 3 visual states (pending/active/done).
function GhostBackdrop({ monthDate }) {
  return (
    <div className="detection-empty-ghost" aria-hidden="true">
      <SkeletonCoverage />
      <SkeletonCalendar monthDate={monthDate} />
    </div>
  )
}

const ICONS = {
  calendar: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <path d="M3 10h18M8 3v4M16 3v4" />
    </svg>
  ),
  campaign: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1z" />
      <path d="M16 8a4 4 0 0 1 0 8" />
      <path d="M19 5a8 8 0 0 1 0 14" />
    </svg>
  ),
  alert: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
      <path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
    </svg>
  ),
  search: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="7" />
      <path d="m21 21-4.3-4.3" />
    </svg>
  ),
}

/**
 * Unified empty state for /detections. Variants:
 *  - "no-month"      → step 1, asks for competência
 *  - "no-campaign"   → step 2, asks for campanha (with count hint)
 *  - "no-rules"      → campaign has no materials linked
 *  - "no-detections" → filters valid but zero hits in range
 *
 * All variants share the same shell: ghost backdrop + centered focused card.
 */
function DetectionsEmpty({
  variant,
  ghostMonthDate,
  monthLabelText,
  campaignName,
  rangeLabelText,
  campaignCount,
  onPickMonth,
  onPickCampaign,
  onEditCampaign,
  onExpandRange,
}) {
  let content
  if (variant === 'no-month') {
    content = (
      <>
        <FlowStepper step={1} steps={STEP_LABELS} />
        <div className="detection-empty-icon">{ICONS.calendar}</div>
        <h3>Comece pela <strong>competência</strong></h3>
        <p>Escolha o mês de referência. As campanhas que cruzam esse período ficam disponíveis logo em seguida.</p>
        <div className="detection-empty-actions">
          <button type="button" className="detection-empty-cta" onClick={onPickMonth}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="5" width="18" height="16" rx="2" /><path d="M3 10h18M8 3v4M16 3v4" />
            </svg>
            Escolher competência
          </button>
        </div>
      </>
    )
  } else if (variant === 'no-campaign') {
    const count = campaignCount ?? 0
    content = (
      <>
        <FlowStepper step={2} steps={STEP_LABELS} />
        <div className="detection-empty-icon">{ICONS.campaign}</div>
        <h3>Escolha uma <strong>campanha</strong> de {monthLabelText}</h3>
        <p>
          {count === 0
            ? <>Nenhuma campanha vigente em <strong>{monthLabelText}</strong>. Troque a competência ou cadastre uma nova campanha.</>
            : <>{count === 1 ? '1 campanha vigente' : `${count} campanhas vigentes`} nesse mês. Pra ver as veiculações detectadas, selecione uma campanha.</>}
        </p>
        <div className="detection-empty-actions">
          {count > 0 && (
            <button type="button" className="detection-empty-cta" onClick={onPickCampaign}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M6 9l6 6 6-6" />
              </svg>
              Abrir lista de campanhas
            </button>
          )}
          <button type="button" className="detection-empty-cta detection-empty-cta--ghost" onClick={onPickMonth}>
            Trocar competência
          </button>
        </div>
      </>
    )
  } else if (variant === 'no-rules') {
    content = (
      <>
        <FlowStepper step={3} steps={STEP_LABELS} />
        <div className="detection-empty-icon detection-empty-icon--warn">{ICONS.alert}</div>
        <h3>Campanha sem materiais vinculados</h3>
        <p>
          <strong>{campaignName}</strong> ainda não tem materiais nem regras de distribuição. Sem isso, não há o que monitorar.
        </p>
        <div className="detection-empty-actions">
          <button type="button" className="detection-empty-cta" onClick={onEditCampaign}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 20h9" /><path d="M16.5 3.5a2.121 2.121 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
            </svg>
            Editar campanha
          </button>
        </div>
      </>
    )
  } else {
    // no-detections
    content = (
      <>
        <FlowStepper step={3} steps={STEP_LABELS} />
        <div className="detection-empty-icon detection-empty-icon--mute">{ICONS.search}</div>
        <h3>Nenhuma veiculação no período</h3>
        <p>
          Sem detecções entre <strong>{rangeLabelText || monthLabelText}</strong>. Pode ser que a campanha não tenha sido veiculada nesses dias, ou que o range esteja apertado demais.
        </p>
        <div className="detection-empty-actions">
          {onExpandRange && (
            <button type="button" className="detection-empty-cta" onClick={onExpandRange}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21 7v6h-6" /><path d="M3 17v-6h6" /><path d="M21 13a9 9 0 0 1-15 5.5" /><path d="M3 11a9 9 0 0 1 15-5.5" />
              </svg>
              Ampliar pro mês todo
            </button>
          )}
        </div>
      </>
    )
  }

  return (
    <div className="detection-empty" role="status" aria-live="polite">
      <GhostBackdrop monthDate={ghostMonthDate} />
      <div className="detection-empty-card">{content}</div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────

export default function DetectionsPage() {
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const { data: campaigns = [], isLoading: loadingCampaigns } = useCampaigns()
  // /clients é scope-aware: viewer recebe lista de 1 (o próprio cliente),
  // admin recebe a lista inteira. Sem gate, o find(...) funciona pras duas.
  const { data: clients = [] } = useClients()

  const [searchParams] = useSearchParams()
  const deepLinkCampaignId = searchParams.get('campaign_id') ?? ''

  const [selectedCampaignId, setSelectedCampaignId] = useState(deepLinkCampaignId)
  // selectedMonthRaw is the user-picked month; effectiveMonth derives a
  // fallback from a deep-linked campaign so /detections?campaign_id=… still
  // works without forcing the user to pick a competência first.
  const [selectedMonthRaw, setSelectedMonthRaw] = useState('')
  // userRange holds the date-range filter (step 3). Empty strings mean
  // "fall back to the default" (intersection of month ∩ campaign).
  const [userRange, setUserRange] = useState({ start: '', end: '' })
  const [modalCell, setModalCell] = useState(null)
  const [search, setSearch] = useState('')
  // Paginação por emissora (a unidade visual do DistributionGrid em modo
  // inlineStationInfo é o bloco da emissora, que faz row-span sobre seus
  // materiais — fatiar por linha quebraria esse span).
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [page, setPage] = useState(1)
  // TODO F-100: wire up HealthDrawer once the component is built
  // eslint-disable-next-line no-unused-vars
  const [healthStationId, setHealthStationId] = useState(null)

  const monthInputRef = useRef(null)

  const focusCampaignSelect = useCallback(() => {
    // RSelect doesn't forward refs; we focus by the inputId we attached below.
    document.getElementById('detection-campaign')?.focus()
  }, [])

  const { data: stationsResp } = useStations({ limit: 2000, enabled: isAdmin })
  const stationCatalog = useMemo(() => stationsResp?.data ?? [], [stationsResp])

  // Client lookup map
  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const selectedCampaign = useMemo(
    () => campaigns.find(c => c.id === selectedCampaignId) ?? null,
    [campaigns, selectedCampaignId]
  )

  // Deep-link derived month: when URL carries ?campaign_id and the user
  // hasn't picked a month yet, fall back to the month containing the
  // campaign's start (or today's month if the campaign is still in the
  // future). Read-only derivation — no setState in an effect.
  const monthFromDeepLink = useMemo(() => {
    if (!deepLinkCampaignId || campaigns.length === 0) return ''
    const c = campaigns.find(c => c.id === deepLinkCampaignId)
    if (!c?.start_date) return ''
    const cs = parseLocalDate(c.start_date)
    const now = new Date()
    return monthFromDate(cs > now ? cs : now)
  }, [deepLinkCampaignId, campaigns])

  const selectedMonth = selectedMonthRaw || monthFromDeepLink || monthFromDate(new Date())

  // Default date range = intersection of month and campaign. The effective
  // range is whatever the user picked, falling back to the default.
  const defaultRange = useMemo(() =>
    defaultRangeForCampaign(selectedMonth, selectedCampaign),
    [selectedMonth, selectedCampaign])

  const rangeStart = userRange.start || defaultRange.start
  const rangeEnd   = userRange.end   || defaultRange.end

  // Bounds dos date pickers = campanha INTEIRA (não o mês). É isso que deixa o
  // usuário arrastar o range pra meses anteriores/posteriores da campanha —
  // antes o min/max travava no mês corrente. Ver docs/features/detections-view.md.
  const pickerBounds = useMemo(
    () => campaignRangeISO(selectedCampaign),
    [selectedCampaign])

  // Janela do mês selecionado (base do fetch e fallback).
  const monthRangeISO = useMemo(() => {
    if (!selectedMonth) return { from: '', to: '' }
    const { start, end } = monthToRange(selectedMonth)
    return { from: isoFromDate(start), to: isoFromDate(end) }
  }, [selectedMonth])

  // Janela do daily-summary = UNIÃO do mês com o range escolhido. Narrow dentro
  // do mês mantém a janela = mês (não refaz fetch, como antes); estender o range
  // pra outro mês amplia a janela pra buscar aquele período também. Strings ISO
  // comparam lexicograficamente, então </> funcionam direto.
  const fetchFrom = rangeStart && rangeStart < monthRangeISO.from ? rangeStart : monthRangeISO.from
  const fetchTo   = rangeEnd   && rangeEnd   > monthRangeISO.to   ? rangeEnd   : monthRangeISO.to

  // Hydrate the campaign's material library
  const { data: clientLibrary = [] } = useMaterials(selectedCampaign?.client_id ?? null)
  const materialsById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary]
  )

  // Campaign-scoped data
  const { data: campaignMaterials = [] } = useCampaignMaterials(selectedCampaignId || null)
  const { data: distributionRules = [] } = useDistributionRules(selectedCampaignId || null)
  const { data: materialTypes = [] }     = useMaterialTypes()
  const { data: pricingList = [] }       = useCampaignPricing(selectedCampaignId || null)

  const pricingByStation = useMemo(() => {
    const m = {}
    for (const p of pricingList) m[p.station_id] = p
    return m
  }, [pricingList])

  // PMM no target do cliente dono da campanha selecionada. Sem campanha (ou
  // sem cadastro) o mapa fica vazio e a grid não muda em nada.
  const { data: targetPmmRows = [] } = useClientTargetPmm(selectedCampaign?.client_id, {
    enabled: !!selectedCampaign?.client_id,
  })
  const pmmTargetByStation = useMemo(() => {
    const m = {}
    for (const r of targetPmmRows) if (r.pmm_target != null) m[r.station_id] = r.pmm_target
    return m
  }, [targetPmmRows])

  // Rótulo do público-alvo do MESMO cliente (clients.target_label). Só entra no
  // tooltip da pill teal — o label visível fica curto por causa da coluna de
  // 180px. null quando o cliente não cadastrou rótulo.
  const targetLabel = useMemo(() => {
    if (!selectedCampaign?.client_id) return null
    const raw = clientMap.get(selectedCampaign.client_id)?.target_label
    return (raw ?? '').trim() || null
  }, [selectedCampaign, clientMap])

  const {
    data: summary = [],
    isLoading: loadingSummary,
    isFetching,
    refetch,
  } = useDailySummary(selectedCampaignId || null, fetchFrom, fetchTo)

  // Client-side range narrow (drives both CoverageSummary and the visible
  // grid days). If range isn't set yet, fall back to the full month.
  const rangedSummary = useMemo(() => {
    if (!rangeStart || !rangeEnd) return summary
    return summary.filter(s => {
      const d = (s.for_date ?? '').slice(0, 10)
      return d >= rangeStart && d <= rangeEnd
    })
  }, [summary, rangeStart, rangeEnd])

  // Grid clamp: pass the intersection of (user range) ∩ (campaign range) as
  // the campaignStart/campaignEnd so the grid keeps its existing semantics.
  const gridStart = useMemo(() => {
    if (rangeStart) return rangeStart
    return selectedCampaign?.start_date ?? ''
  }, [rangeStart, selectedCampaign])

  const gridEnd = useMemo(() => {
    if (rangeEnd) return rangeEnd
    return selectedCampaign?.end_date ?? ''
  }, [rangeEnd, selectedCampaign])

  // ── Campaign options + filtering ──────────────────────────────
  const allCampaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value:      c.id,
      // Sufixo "(cancelada)" só aparece no valor selecionado via deep-link —
      // a lista do dropdown (campaignOptions) já exclui canceladas.
      label:      c.status === 'cancelada' ? `${c.name} (cancelada)` : c.name,
      status:     c.status,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
      startDate:  c.start_date,
      endDate:    c.end_date,
    }
  }), [campaigns, clientMap])

  // Dropdown só lista campanhas cujo intervalo [start_date, end_date] cruza
  // a competência selecionada. Sem competência, lista vazia (o seletor fica
  // bloqueado mesmo). Campanhas canceladas ficam fora do seletor — mas seguem
  // resolvíveis via allCampaignOptions (deep-link histórico continua abrindo).
  const campaignOptions = useMemo(() => {
    if (!selectedMonth) return []
    const { start, end } = monthToRange(selectedMonth)
    return allCampaignOptions.filter(o => {
      if (o.status === 'cancelada') return false
      if (!o.startDate || !o.endDate) return false
      const cs = parseLocalDate(o.startDate)
      const ce = parseLocalDate(o.endDate)
      return cs <= end && ce >= start
    })
  }, [allCampaignOptions, selectedMonth])

  const selectedCampaignOption = useMemo(() =>
    allCampaignOptions.find(o => o.value === selectedCampaignId) ?? null,
    [allCampaignOptions, selectedCampaignId])

  function formatCampaignOption(opt, { context }) {
    const isValue = context === 'value'
    const size = isValue ? 18 : 22
    const period = formatCampaignPeriod(opt.startDate, opt.endDate)
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
        <ClientMiniAvatar name={opt.clientName} logo={opt.clientLogo} size={size} />
        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, gap: 1 }}>
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
            {isValue && period && (
              <>
                <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>•</span>
                <span style={{ color: '#94a3b8', fontSize: 12, whiteSpace: 'nowrap', flexShrink: 0 }}>
                  {period}
                </span>
              </>
            )}
          </div>
          {!isValue && period && (
            <span style={{ color: '#94a3b8', fontSize: 11, whiteSpace: 'nowrap' }}>
              {period}
            </span>
          )}
        </div>
      </div>
    )
  }

  // ── Type/material plumbing for the grid (unchanged from before) ─
  const typeById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t])),
    [materialTypes]
  )

  // Lookup dos materiais REAIS por (emissora × tipo) — a grade só conhece o
  // tipo (daily_play_summary agrega por type_id), então resolvemos o nome do
  // material aqui pra o relatório mostrar "Spot 30\" · #241 VERISURE Alarme 30s".
  // Um tipo pode ter N materiais na mesma emissora → guardamos todos.
  const materialsByStationType = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) {
        const key = `${sid}|${mat.type_id}`
        if (!m.has(key)) m.set(key, [])
        m.get(key).push({
          shortId: mat.short_id ?? null,
          title: mat.title ?? '—',
          durationSec: mat.duration_seconds ?? null,
        })
      }
    }
    return m
  }, [campaignMaterials, materialsById])

  // Linhas = escopo atual (campaign_materials × target_stations) ∪ pares
  // (emissora, tipo) com veiculação no período. A união é o que impede o
  // histórico de sumir quando o operador tira a emissora do target_stations de
  // um material: o escopo diz o que se monitora daqui pra frente, não reescreve
  // o que já tocou. Regra e testes em utils/gridRows.js.
  const rows = useMemo(() => buildGridRows({
    campaignMaterials, materialsById, summary: rangedSummary,
    typeById, distributionRules,
  }), [campaignMaterials, materialsById, rangedSummary, typeById, distributionRules])

  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of rangedSummary) {
      const key = `${s.station_id}|${s.type_id}|${s.for_date.slice(0, 10)}`
      m.set(key, { ...s, hasOverride: false })
    }
    return m
  }, [rangedSummary])

  const monthDate = useMemo(() => {
    if (!selectedMonth) return new Date()
    const [y, m] = selectedMonth.split('-').map(Number)
    return new Date(y, m - 1, 1)
  }, [selectedMonth])

  const filteredRows = useMemo(() => {
    const tokens = tokenize(search)
    if (tokens.length === 0) return rows
    const stationById = new Map(stationCatalog.map(s => [s.id, s]))
    const fields = ['name', 'city', 'state', 'band', 'freq', 'title']
    return rows.filter(r => {
      const st = stationById.get(r.stationId)
      if (!st) return false
      const haystack = {
        name:  st.name  ?? '',
        city:  st.city  ?? '',
        state: st.state ?? '',
        band:  st.band  ?? '',
        freq:  st.frequency_mhz != null ? String(st.frequency_mhz) : '',
        title: r.materialTitle ?? '',
      }
      return matchesAllTokens(haystack, fields, tokens)
    })
  }, [rows, search, stationCatalog])

  // ── Paginação (por emissora) ──────────────────────────────────
  // Lista de IDs únicos de emissora preservando a ordem em que aparecem em
  // `filteredRows`. É essa lista que é fatiada por (page, pageSize); depois
  // re-filtramos `filteredRows` pra manter só as linhas dessas emissoras.
  const uniqueStationIds = useMemo(() => {
    const seen = new Set()
    const list = []
    for (const r of filteredRows) {
      if (!seen.has(r.stationId)) {
        seen.add(r.stationId)
        list.push(r.stationId)
      }
    }
    return list
  }, [filteredRows])

  const totalStations = uniqueStationIds.length
  const totalPages = Math.max(1, Math.ceil(totalStations / pageSize))
  const safePage = Math.min(page, totalPages)

  const pagedStationIds = useMemo(() => {
    const start = (safePage - 1) * pageSize
    return new Set(uniqueStationIds.slice(start, start + pageSize))
  }, [uniqueStationIds, safePage, pageSize])

  const pagedRows = useMemo(
    () => filteredRows.filter(r => pagedStationIds.has(r.stationId)),
    [filteredRows, pagedStationIds]
  )

  // ── Modelo do relatório WYSIWYG (Relatórios da toolbar) ────────
  // Espelha a grade: MESMOS dias (enumerateVisibleDays, cap-at-today idêntico ao
  // DistributionGrid), TODAS as linhas que batem com a busca (filteredRows, não
  // as paginadas) e os MESMOS números do cellData. O CSV/PDF saem daqui — ver
  // utils/gridReport.js + pdfReport.js.
  const reportDays = useMemo(() => enumerateVisibleDays({
    month: monthDate,
    campaignStart: gridStart, campaignEnd: gridEnd,
    visibleStart: gridStart, visibleEnd: gridEnd,
    capAtToday: true,
  }), [monthDate, gridStart, gridEnd])

  const reportModel = useMemo(() => buildGridReportModel({
    campaign: selectedCampaign,
    client: selectedCampaign ? clientMap.get(selectedCampaign.client_id) ?? null : null,
    filteredRows,
    stations: stationCatalog,
    days: reportDays,
    cellData,
    filterInfo: {
      search: search.trim(),
      periodLabel: rangeLabel(rangeStart, rangeEnd),
    },
    materialLookup: materialsByStationType,
    pmmTargetByStation,
  }), [selectedCampaign, clientMap, filteredRows, stationCatalog, reportDays, cellData, search, rangeStart, rangeEnd, materialsByStationType, pmmTargetByStation])

  // Só liga o modo WYSIWYG quando temos o catálogo de emissoras pra resolver
  // nomes/dial (admin). Sem catálogo (ex.: viewer), cai no relatório backend
  // atual — sem regressão. Ver docs/features/detections-report-wysiwyg.md.
  const gridReport = stationCatalog.length > 0
    ? { model: reportModel, filterNote: reportModel.header.filterLabel }
    : null

  // ── Filter step state ─────────────────────────────────────────
  // Drives both the filter bar UI (which field is disabled vs active vs done)
  // and the empty-state variant. Single source of truth.
  const filterStep = !selectedMonth ? 1 : !selectedCampaignId ? 2 : 3

  function handleMonthChange(e) {
    const v = e.target.value
    setSelectedMonthRaw(v)
    // Changing the month invalidates user-picked range — the picked dates
    // probably don't belong to the new month at all.
    setUserRange({ start: '', end: '' })
    setPage(1)
    // …and may invalidate the campaign selection if the new month doesn't
    // overlap with it.
    if (selectedCampaignId) {
      const c = campaigns.find(cc => cc.id === selectedCampaignId)
      if (c && v) {
        const { start, end } = monthToRange(v)
        const cs = parseLocalDate(c.start_date)
        const ce = parseLocalDate(c.end_date)
        if (!(cs <= end && ce >= start)) setSelectedCampaignId('')
      } else {
        setSelectedCampaignId('')
      }
    }
    setModalCell(null)
  }

  function handleCampaignChange(opt) {
    setSelectedCampaignId(opt?.value ?? '')
    // Different campaign → different date bounds → reset the user range.
    setUserRange({ start: '', end: '' })
    setModalCell(null)
    setPage(1)
  }

  function handleSearchChange(e) {
    setSearch(e.target.value)
    setPage(1)
  }

  function handlePageSizeChange(n) {
    setPageSize(n)
    setPage(1)
  }

  function handleRangeStart(e) {
    const v = e.target.value
    setUserRange(prev => {
      const next = { start: v, end: prev.end || rangeEnd }
      if (v && next.end && v > next.end) next.end = v
      return next
    })
  }

  function handleRangeEnd(e) {
    const v = e.target.value
    setUserRange(prev => {
      const next = { start: prev.start || rangeStart, end: v }
      if (v && next.start && v < next.start) next.start = v
      return next
    })
  }

  function handleResetRange() {
    setUserRange({ start: '', end: '' })
  }

  // Bounds for the date pickers = the full month ∩ campaign window. Outside
  // those, dates are meaningless on this page.
  const rangeBounds = defaultRange

  // Is the user range narrower than the full intersection? Used to show
  // the "reset" affordance.
  const rangeIsCustom = !!(userRange.start || userRange.end) &&
    rangeBounds.start && rangeBounds.end &&
    (rangeStart !== rangeBounds.start || rangeEnd !== rangeBounds.end)

  // ── Empty-state variant decision ──────────────────────────────
  // Search is intentionally NOT part of this decision — when the user
  // searches and gets zero matches, we leave the grid visible (with its own
  // empty inside) instead of replacing the whole screen.
  const showDetections = filterStep === 3
  const isLoadingData  = showDetections && (loadingSummary || isFetching)
  const hasRows = rows.length > 0
  const hasDetectionsInRange = rangedSummary.length > 0

  let emptyVariant = null
  if (filterStep === 1) emptyVariant = 'no-month'
  else if (filterStep === 2) emptyVariant = 'no-campaign'
  else if (!isLoadingData && !hasRows) emptyVariant = 'no-rules'
  else if (!isLoadingData && !hasDetectionsInRange) emptyVariant = 'no-detections'

  // Compute campaign count for current month (used in empty-state copy)
  const campaignCount = campaignOptions.length

  // ── Render ────────────────────────────────────────────────────
  return (
    <div>
      <div className="page-header">
        <h2>Veiculações</h2>
      </div>

      {/* 3-step filter bar: Competência → Campanha → Período em dias. */}
      <div className="flow-filters">
        <div className={`flow-filter ${filterStep === 1 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="detection-month">
            <span className="flow-filter-label-step">1</span>
            Competência
          </label>
          <input
            id="detection-month"
            ref={monthInputRef}
            className="flow-month-input"
            type="month"
            value={selectedMonth}
            onChange={handleMonthChange}
            placeholder="Selecione o mês"
          />
        </div>

        <div className={`flow-filter ${
          !selectedMonth ? 'flow-filter--locked' :
          filterStep === 2 ? 'flow-filter--active' : 'flow-filter--done'
        }`}>
          <label className="flow-filter-label" htmlFor="detection-campaign">
            <span className="flow-filter-label-step">2</span>
            Campanha
            {selectedMonth && filterStep === 2 && campaignCount > 0 && (
              <span style={{ marginLeft: 'auto', textTransform: 'none', letterSpacing: 0, fontSize: 11, fontWeight: 600, color: 'var(--c-text-3)' }}>
                {campaignCount === 1 ? '1 disponível' : `${campaignCount} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="detection-campaign"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={handleCampaignChange}
            formatOptionLabel={formatCampaignOption}
            isDisabled={!selectedMonth || loadingCampaigns}
            isLoading={loadingCampaigns}
            placeholder={
              !selectedMonth ? 'Escolha uma competência primeiro' :
              loadingCampaigns ? 'Carregando…' :
              campaignCount === 0 ? `Nenhuma campanha em ${monthLabel(selectedMonth)}` :
              `${campaignCount === 1 ? '1 campanha' : `${campaignCount} campanhas`} em ${monthLabel(selectedMonth)}`
            }
            isClearable
            noOptionsMessage={() => `Nenhuma campanha em ${monthLabel(selectedMonth)}`}
          />
        </div>

        <div className={`flow-filter ${
          filterStep < 3 ? 'flow-filter--locked' : 'flow-filter--active'
        }`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">3</span>
            Período
            {rangeIsCustom && (
              <button
                type="button"
                className="flow-range-reset"
                onClick={handleResetRange}
                title="Resetar pro intervalo completo da campanha no mês"
                style={{ marginLeft: 'auto' }}
              >
                Resetar
              </button>
            )}
          </label>
          <div className="flow-range">
            <input
              type="date"
              value={rangeStart}
              min={pickerBounds.start || undefined}
              max={pickerBounds.end || undefined}
              onChange={handleRangeStart}
              disabled={filterStep < 3}
              aria-label="Data de início"
            />
            <span className="flow-range-arrow">→</span>
            <input
              type="date"
              value={rangeEnd}
              min={pickerBounds.start || undefined}
              max={pickerBounds.end || undefined}
              onChange={handleRangeEnd}
              disabled={filterStep < 3}
              aria-label="Data de fim"
            />
          </div>
        </div>
      </div>

      {/* Secondary toolbar: station search + refresh. Only when we have data. */}
      {showDetections && (
        <div className="flow-toolbar">
          <div className="stations-search">
            <span className="stations-search-icon">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
                <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
              </svg>
            </span>
            <input
              className="input stations-search-input"
              type="text"
              placeholder="Buscar emissora, cidade…"
              value={search}
              onChange={handleSearchChange}
            />
          </div>

          {/* Relatórios: CSV consolidado / detalhado / PDF da campanha
              selecionada, no recorte de datas atual. */}
          <div style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            <CampaignReportsMenu
              campaignId={selectedCampaignId}
              from={rangeStart}
              to={rangeEnd}
              variant="compact"
              placement="bottom-end"
              label="Relatórios"
              gridReport={gridReport}
            />
          </div>

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
        </div>
      )}

      {/* Content */}
      {emptyVariant ? (
        <DetectionsEmpty
          variant={emptyVariant}
          ghostMonthDate={monthDate}
          monthLabelText={selectedMonth ? monthLabel(selectedMonth) : ''}
          campaignName={selectedCampaign?.name ?? ''}
          rangeLabelText={rangeLabel(rangeStart, rangeEnd)}
          campaignCount={campaignCount}
          onPickMonth={() => {
            const el = monthInputRef.current
            if (!el) return
            el.focus()
            if (typeof el.showPicker === 'function') {
              try { el.showPicker() } catch { /* showPicker can throw if focus stolen */ }
            }
          }}
          onPickCampaign={focusCampaignSelect}
          onEditCampaign={() => {
            if (selectedCampaignId) navigate(`/campaigns/${selectedCampaignId}/edit`)
          }}
          onExpandRange={rangeIsCustom ? handleResetRange : null}
        />
      ) : isLoadingData ? (
        <>
          <SkeletonCoverage />
          <SkeletonCalendar monthDate={monthDate} />
        </>
      ) : (
        <>
          <CoverageSummary summary={rangedSummary} />
          <DistributionGrid
            mode="view"
            month={monthDate}
            campaignStart={gridStart}
            campaignEnd={gridEnd}
            visibleStart={gridStart}
            visibleEnd={gridEnd}
            stations={stationCatalog}
            rows={pagedRows}
            cellData={cellData}
            pricingByStation={pricingByStation}
            pmmTargetByStation={pmmTargetByStation}
            targetLabel={targetLabel}
            inlineStationInfo
            onCellClick={(stationId, typeId, dateISO) =>
              setModalCell({ stationId, typeId, dateISO })}
            onStationClick={(stationId) => setHealthStationId(stationId)}
          />
          {totalStations > 0 && (
            <AirtimePaginator
              page={safePage}
              totalPages={totalPages}
              total={totalStations}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={handlePageSizeChange}
              onChange={setPage}
              singular="emissora"
              plural="emissoras"
            />
          )}
        </>
      )}

      {modalCell && (
        <DayDetailModal
          stationId={modalCell.stationId}
          typeId={modalCell.typeId}
          dateISO={modalCell.dateISO}
          campaignId={selectedCampaignId}
          station={stationCatalog.find(s => s.id === modalCell.stationId) ?? null}
          materialType={typeById[modalCell.typeId] ?? null}
          cellSummary={cellData.get(`${modalCell.stationId}|${modalCell.typeId}|${modalCell.dateISO}`) ?? null}
          rules={distributionRules.filter(r =>
            r.type_id === modalCell.typeId &&
            r.station_ids.includes(modalCell.stationId))}
          availableMaterials={campaignMaterials
            .filter(cm => (cm.target_stations ?? []).includes(modalCell.stationId))
            .map(cm => materialsById[cm.material_id])
            .filter(m => m && m.type_id === modalCell.typeId)}
          onClose={() => setModalCell(null)}
        />
      )}
    </div>
  )
}
