import { useState, useMemo, useRef, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  useCampaigns, useStations, useClients,
  useCampaignMaterials, useMaterials, useDistributionRules,
  useMaterialTypes, useDailySummary,
} from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import RSelect from '../components/RSelect'
import DistributionGrid from '../components/DistributionGrid'
import FlowStepper from '../components/FlowStepper'
import AirtimePaginator from '../components/AirtimePaginator'
import MaterialPlaybackList from '../components/MaterialPlaybackList'
import { tokenize, matchesAllTokens } from '../utils/search'
import { safeLogoUrl } from '../utils/logoUrl'
import {
  parseLocalDate, monthFromDate, isoFromDate, monthToRange,
  monthLabel, rangeLabel, defaultRangeForCampaign, campaignRangeISO, formatCampaignPeriod,
} from '../utils/dates'

const STEP_LABELS = ['Competência', 'Campanha', 'Período']
const PAGE_SIZE_OPTIONS = [5, 10, 15]
const DEFAULT_PAGE_SIZE = 5

// ── Client mini avatar (campaign select) ─────────────────────────
function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  const safeLogo = safeLogoUrl(logo)
  if (safeLogo && !imgError) {
    return (
      <img
        src={safeLogo} alt={name} width={size} height={size}
        style={{ width: size, height: size, borderRadius: 4, objectFit: 'cover', flexShrink: 0, border: '1px solid #e2e8f0', display: 'block' }}
        onError={() => setImgError(true)}
      />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div style={{
      width: size, height: size, borderRadius: 4, background: '#fce7f3', color: '#E81E75',
      fontSize: Math.round(size * 0.42), fontWeight: 700,
      display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
      fontFamily: "'Fira Sans Condensed', sans-serif", userSelect: 'none',
    }}>{initials}</div>
  )
}

// ── Plan summary strip ────────────────────────────────────────────
// Lead figure (Σ programado) + supporting chips, in the CoverageSummary
// family used on /detections. Enriched with a slim programmed/total bar.
function PlanSummary({ totalExpected, materials, programmedMaterials, stations }) {
  const pct = materials > 0 ? Math.round((programmedMaterials / materials) * 100) : 0
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 24, flexWrap: 'wrap',
      padding: '14px 18px', marginBottom: 14,
      background: 'var(--c-surface)', border: '1px solid var(--c-border)',
      borderRadius: 12, boxShadow: 'var(--shadow-sm)',
    }}>
      {/* Lead figure */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
        <span style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: '0.04em', textTransform: 'uppercase', color: 'var(--c-text-3)', fontFamily: 'var(--font-heading)' }}>
          Inserções programadas
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 7 }}>
          <span style={{ fontSize: 28, fontWeight: 700, color: 'var(--c-action)', fontFamily: 'var(--font-heading)', lineHeight: 1, fontVariantNumeric: 'tabular-nums' }}>
            {totalExpected.toLocaleString('pt-BR')}
          </span>
          <span style={{ fontSize: 12, color: 'var(--c-text-3)' }}>no período</span>
        </span>
      </div>

      <div style={{ height: 42, width: 1, background: 'var(--c-border)' }} />

      {/* Materials + programmed proportion */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, minWidth: 180 }}>
        <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 6, fontFamily: 'var(--font-heading)' }}>
          <span style={{ fontSize: 17, fontWeight: 700, color: 'var(--c-text)', fontVariantNumeric: 'tabular-nums' }}>{materials}</span>
          <span style={{ fontSize: 11.5, color: 'var(--c-text-2)', fontWeight: 600 }}>{materials === 1 ? 'material' : 'materiais'}</span>
          <span style={{ fontSize: 11.5, color: 'var(--c-text-3)' }}>· {programmedMaterials} programad{programmedMaterials === 1 ? 'o' : 'os'}</span>
        </span>
        <div style={{ height: 6, borderRadius: 999, background: 'var(--c-surface-2)', overflow: 'hidden' }}>
          <div style={{ height: '100%', width: `${pct}%`, background: 'var(--c-success)', borderRadius: 999, transition: 'width 220ms cubic-bezier(0.16,1,0.3,1)' }} />
        </div>
      </div>

      <div style={{ height: 42, width: 1, background: 'var(--c-border)' }} />

      {/* Stations with plan */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
        <span style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: '0.04em', textTransform: 'uppercase', color: 'var(--c-text-3)', fontFamily: 'var(--font-heading)' }}>
          Emissoras com plano
        </span>
        <span style={{ fontSize: 17, fontWeight: 700, color: 'var(--c-text)', fontFamily: 'var(--font-heading)', fontVariantNumeric: 'tabular-nums' }}>
          {stations}
        </span>
      </div>
    </div>
  )
}

// ── Loading skeleton (replaces "Carregando…" text) ───────────────
function MaterialsSkeleton() {
  return (
    <>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 24,
        padding: '14px 18px', marginBottom: 14,
        background: 'var(--c-surface)', border: '1px solid var(--c-border)', borderRadius: 12,
      }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div className="skeleton" style={{ width: 110, height: 9, borderRadius: 3 }} />
          <div className="skeleton" style={{ width: 70, height: 26, borderRadius: 6 }} />
        </div>
        <div style={{ height: 42, width: 1, background: 'var(--c-border)' }} />
        <div className="skeleton" style={{ width: 150, height: 30, borderRadius: 6 }} />
        <div style={{ height: 42, width: 1, background: 'var(--c-border)' }} />
        <div className="skeleton" style={{ width: 60, height: 30, borderRadius: 6 }} />
      </div>
      <div style={{ background: 'var(--c-surface)', border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)', overflow: 'hidden', marginBottom: 14 }}>
        <div style={{ padding: '13px 16px', borderBottom: '1px solid var(--c-border)', background: 'var(--c-bg)', display: 'flex', alignItems: 'center', gap: 10 }}>
          <div className="skeleton" style={{ width: 28, height: 28, borderRadius: 8 }} />
          <div className="skeleton" style={{ width: 160, height: 12, borderRadius: 4 }} />
        </div>
        {[68, 54, 80].map((w, i) => (
          <div key={i} style={{ padding: '12px 16px', borderBottom: '1px solid var(--c-border)', display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div className="skeleton" style={{ width: `${w}%`, height: 11, borderRadius: 4 }} />
              <div className="skeleton" style={{ width: 90, height: 9, borderRadius: 3 }} />
            </div>
            <div className="skeleton" style={{ width: 96, height: 22, borderRadius: 999 }} />
            <div className="skeleton" style={{ width: 32, height: 32, borderRadius: 8 }} />
            <div className="skeleton" style={{ width: 32, height: 32, borderRadius: 8 }} />
          </div>
        ))}
      </div>
      <div style={{ background: 'var(--c-surface)', border: '1px solid var(--c-border)', borderRadius: 12, padding: 16, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {Array.from({ length: 4 }).map((_, r) => (
          <div key={r} style={{ display: 'flex', gap: 6 }}>
            <div className="skeleton" style={{ width: 200, height: 30, borderRadius: 6 }} />
            {Array.from({ length: 8 }).map((_, c) => (
              <div key={c} className="skeleton" style={{ flex: 1, height: 30, borderRadius: 6 }} />
            ))}
          </div>
        ))}
      </div>
    </>
  )
}

// ── Empty-state ghost backdrop (shadow UI, DESIGN.md §4.7) ────────
function GhostBackdrop() {
  return (
    <div className="detection-empty-ghost" aria-hidden="true">
      <div style={{
        display: 'flex', alignItems: 'center', gap: 24, padding: '14px 18px', marginBottom: 14,
        background: '#fff', border: '1px solid #f1f5f9', borderRadius: 12,
      }}>
        <div className="skeleton" style={{ width: 80, height: 30, borderRadius: 6 }} />
        <div className="skeleton" style={{ width: 150, height: 24, borderRadius: 6 }} />
        <div className="skeleton" style={{ width: 60, height: 24, borderRadius: 6 }} />
      </div>
      <div style={{ background: '#fff', border: '1px solid #f1f5f9', borderRadius: 12, overflow: 'hidden', marginBottom: 14 }}>
        {[0, 1].map(i => (
          <div key={i} style={{ padding: '12px 16px', borderBottom: '1px solid #f1f5f9', display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div className="skeleton" style={{ width: `${60 - i * 10}%`, height: 11, borderRadius: 4 }} />
              <div className="skeleton" style={{ width: 80, height: 9, borderRadius: 3 }} />
            </div>
            <div className="skeleton" style={{ width: 26, height: 26, borderRadius: 8 }} />
          </div>
        ))}
      </div>
    </div>
  )
}

const ICONS = {
  calendar: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M3 10h18M8 3v4M16 3v4" /></svg>),
  campaign: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1z" /><path d="M16 8a4 4 0 0 1 0 8" /><path d="M19 5a8 8 0 0 1 0 14" /></svg>),
  alert: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 9v4" /><path d="M12 17h.01" /><path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" /></svg>),
  music: (<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" /></svg>),
}

function MaterialsEmpty({ variant, monthLabelText, campaignName, campaignCount, isAdmin, onPickMonth, onPickCampaign, onEditCampaign }) {
  let step, icon, title, body, cta
  let iconMod = ''
  if (variant === 'no-month') {
    step = 1; icon = ICONS.calendar
    title = <>Comece pela <strong>competência</strong></>
    body = <>Escolha o mês de referência. As campanhas que cruzam esse período ficam disponíveis em seguida.</>
    cta = <button type="button" className="detection-empty-cta" onClick={onPickMonth}>Escolher competência</button>
  } else if (variant === 'no-campaign') {
    step = 2; icon = ICONS.campaign
    const count = campaignCount ?? 0
    title = <>Escolha uma <strong>campanha</strong> de {monthLabelText}</>
    body = count === 0
      ? <>Nenhuma campanha vigente em <strong>{monthLabelText}</strong>. Troque a competência.</>
      : <>{count === 1 ? '1 campanha vigente' : `${count} campanhas vigentes`} nesse mês. Selecione uma pra ouvir os materiais e ver o programado.</>
    cta = count > 0
      ? <button type="button" className="detection-empty-cta" onClick={onPickCampaign}>Abrir lista de campanhas</button>
      : <button type="button" className="detection-empty-cta detection-empty-cta--ghost" onClick={onPickMonth}>Trocar competência</button>
  } else { // no-rules
    step = 3; icon = ICONS.alert; iconMod = ' detection-empty-icon--warn'
    title = <>Campanha sem materiais nem regras</>
    body = <><strong>{campaignName}</strong> ainda não tem materiais vinculados nem regras de distribuição. Sem isso, não há o que ouvir nem programação pra mostrar.</>
    cta = isAdmin
      ? <button type="button" className="detection-empty-cta" onClick={onEditCampaign}>Editar campanha</button>
      : null
  }
  return (
    <div className="detection-empty" role="status" aria-live="polite">
      <GhostBackdrop />
      <div className="detection-empty-card">
        <FlowStepper step={step} steps={STEP_LABELS} />
        <div className={`detection-empty-icon${iconMod}`}>{icon}</div>
        <h3>{title}</h3>
        <p>{body}</p>
        {cta && <div className="detection-empty-actions">{cta}</div>}
      </div>
    </div>
  )
}

// ── Main page ─────────────────────────────────────────────────────
export default function MaterialsPage() {
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const { data: campaigns = [], isLoading: loadingCampaigns } = useCampaigns()
  const { data: clients = [] } = useClients()

  const [searchParams] = useSearchParams()
  const deepLinkCampaignId = searchParams.get('campaign_id') ?? ''

  const [selectedCampaignId, setSelectedCampaignId] = useState(deepLinkCampaignId)
  const [selectedMonthRaw, setSelectedMonthRaw] = useState('')
  const [userRange, setUserRange] = useState({ start: '', end: '' })
  const [search, setSearch] = useState('')
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [page, setPage] = useState(1)

  const monthInputRef = useRef(null)
  const focusCampaignSelect = useCallback(() => {
    document.getElementById('materials-campaign')?.focus()
  }, [])

  // Stations fetched for EVERYONE (admin + client). /stations is readable by
  // any authenticated user (see App.jsx), so — unlike /detections — we don't
  // gate by isAdmin: the grid needs station objects to render rows for clients.
  const { data: stationsResp } = useStations({ limit: 2000 })
  const stationCatalog = useMemo(() => stationsResp?.data ?? [], [stationsResp])

  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const selectedCampaign = useMemo(
    () => campaigns.find(c => c.id === selectedCampaignId) ?? null,
    [campaigns, selectedCampaignId])

  const monthFromDeepLink = useMemo(() => {
    if (!deepLinkCampaignId || campaigns.length === 0) return ''
    const c = campaigns.find(c => c.id === deepLinkCampaignId)
    if (!c?.start_date) return ''
    const cs = parseLocalDate(c.start_date)
    const now = new Date()
    return monthFromDate(cs > now ? cs : now)
  }, [deepLinkCampaignId, campaigns])

  const selectedMonth = selectedMonthRaw || monthFromDeepLink || monthFromDate(new Date())

  const defaultRange = useMemo(
    () => defaultRangeForCampaign(selectedMonth, selectedCampaign),
    [selectedMonth, selectedCampaign])
  const rangeStart = userRange.start || defaultRange.start
  const rangeEnd   = userRange.end   || defaultRange.end

  // Bounds dos date pickers = campanha INTEIRA, pra liberar o range além do mês
  // corrente (o default segue mensal). Ver docs/features/materials-page.md.
  const pickerBounds = useMemo(
    () => campaignRangeISO(selectedCampaign),
    [selectedCampaign])

  const monthRangeISO = useMemo(() => {
    if (!selectedMonth) return { from: '', to: '' }
    const { start, end } = monthToRange(selectedMonth)
    return { from: isoFromDate(start), to: isoFromDate(end) }
  }, [selectedMonth])

  // Janela do fetch = união do mês com o range escolhido (idem /detections):
  // narrow no mês não refaz fetch; estender pra outro mês amplia a busca.
  const fetchFrom = rangeStart && rangeStart < monthRangeISO.from ? rangeStart : monthRangeISO.from
  const fetchTo   = rangeEnd   && rangeEnd   > monthRangeISO.to   ? rangeEnd   : monthRangeISO.to

  // Campaign-scoped data
  const { data: clientLibrary = [] } = useMaterials(selectedCampaign?.client_id ?? null)
  const materialsById = useMemo(
    () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
    [clientLibrary])
  const { data: campaignMaterials = [] } = useCampaignMaterials(selectedCampaignId || null)
  const { data: distributionRules = [] } = useDistributionRules(selectedCampaignId || null)
  const { data: materialTypes = [] } = useMaterialTypes()
  const { data: summary = [], isLoading: loadingSummary, isFetching } =
    useDailySummary(selectedCampaignId || null, fetchFrom, fetchTo)

  // All materials the campaign has (programmed or not).
  const linkedMaterials = useMemo(
    () => campaignMaterials.map(cm => materialsById[cm.material_id]).filter(Boolean),
    [campaignMaterials, materialsById])

  // Type ids present in any distribution rule → "programmed".
  const programmedTypeIds = useMemo(
    () => new Set(distributionRules.map(r => r.type_id)),
    [distributionRules])

  const typeById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t])),
    [materialTypes])

  // station → Set<typeId> in scope (has a linked material of that type)
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) {
        if (!m.has(sid)) m.set(sid, new Set())
        m.get(sid).add(mat.type_id)
      }
    }
    return m
  }, [campaignMaterials, materialsById])

  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeSet] of typesInScopeByStation.entries()) {
      for (const tid of typeSet) {
        const type = typeById[tid]
        if (!type) continue
        const matching = distributionRules.filter(rule =>
          rule.type_id === tid && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          materialId: tid,
          materialTitle: type.name,
          typeColor: type.color ?? '#94a3b8',
          ruleSummary: first ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}` : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [typesInScopeByStation, typeById, distributionRules])

  // Client-side narrow to the picked range.
  const rangedSummary = useMemo(() => {
    if (!rangeStart || !rangeEnd) return summary
    return summary.filter(s => {
      const d = (s.for_date ?? '').slice(0, 10)
      return d >= rangeStart && d <= rangeEnd
    })
  }, [summary, rangeStart, rangeEnd])

  // cellData: PROGRAMADO ONLY — keep just `expected` so DayCell renders only
  // the gray pill (no green/red/etc).
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of rangedSummary) {
      const key = `${s.station_id}|${s.type_id}|${s.for_date.slice(0, 10)}`
      m.set(key, { expected: s.expected })
    }
    return m
  }, [rangedSummary])

  const monthDate = useMemo(() => {
    if (!selectedMonth) return new Date()
    const [y, m] = selectedMonth.split('-').map(Number)
    return new Date(y, m - 1, 1)
  }, [selectedMonth])

  const gridStart = rangeStart || selectedCampaign?.start_date || ''
  const gridEnd   = rangeEnd   || selectedCampaign?.end_date   || ''

  // ── Campaign options ──────────────────────────────────────────
  const allCampaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id,
      // Sufixo só no valor selecionado (deep-link); o dropdown exclui canceladas.
      label: c.status === 'cancelada' ? `${c.name} (cancelada)` : c.name,
      status: c.status,
      clientName: client?.name ?? '', clientLogo: client?.logo_url ?? null,
      startDate: c.start_date, endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  // Canceladas ficam fora do seletor (e do contador), mas seguem resolvíveis
  // via allCampaignOptions para o deep-link histórico não quebrar.
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

  const selectedCampaignOption = useMemo(
    () => allCampaignOptions.find(o => o.value === selectedCampaignId) ?? null,
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
            {opt.clientName && <span style={{ fontWeight: 600, color: '#06055B', whiteSpace: 'nowrap', fontSize: 13 }}>{opt.clientName}</span>}
            {opt.clientName && <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>|</span>}
            <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{opt.label}</span>
            {isValue && period && (<>
              <span style={{ color: '#cbd5e1', fontSize: 11, fontWeight: 400, flexShrink: 0 }}>•</span>
              <span style={{ color: '#94a3b8', fontSize: 12, whiteSpace: 'nowrap', flexShrink: 0 }}>{period}</span>
            </>)}
          </div>
          {!isValue && period && <span style={{ color: '#94a3b8', fontSize: 11, whiteSpace: 'nowrap' }}>{period}</span>}
        </div>
      </div>
    )
  }

  // ── Search filter ─────────────────────────────────────────────
  const filteredRows = useMemo(() => {
    const tokens = tokenize(search)
    if (tokens.length === 0) return rows
    const stationById = new Map(stationCatalog.map(s => [s.id, s]))
    const fields = ['name', 'city', 'state', 'band', 'freq', 'title']
    return rows.filter(r => {
      const st = stationById.get(r.stationId)
      if (!st) return false
      const haystack = {
        name: st.name ?? '', city: st.city ?? '', state: st.state ?? '',
        band: st.band ?? '', freq: st.frequency_mhz != null ? String(st.frequency_mhz) : '',
        title: r.materialTitle ?? '',
      }
      return matchesAllTokens(haystack, fields, tokens)
    })
  }, [rows, search, stationCatalog])

  // ── Pagination (by station) ───────────────────────────────────
  const uniqueStationIds = useMemo(() => {
    const seen = new Set(); const list = []
    for (const r of filteredRows) {
      if (!seen.has(r.stationId)) { seen.add(r.stationId); list.push(r.stationId) }
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
    [filteredRows, pagedStationIds])

  // ── Plan summary stats ────────────────────────────────────────
  const planStats = useMemo(() => {
    const stationSet = new Set()
    let totalExpected = 0
    for (const s of rangedSummary) {
      totalExpected += s.expected ?? 0
      if ((s.expected ?? 0) > 0) stationSet.add(s.station_id)
    }
    const programmedMaterials = linkedMaterials.filter(m => m.type_id && programmedTypeIds.has(m.type_id)).length
    return { materials: linkedMaterials.length, programmedMaterials, stations: stationSet.size, totalExpected }
  }, [rangedSummary, linkedMaterials, programmedTypeIds])

  // ── Filter step + empty variant ───────────────────────────────
  const filterStep = !selectedMonth ? 1 : !selectedCampaignId ? 2 : 3
  const showContent = filterStep === 3
  const isLoadingData = showContent && (loadingSummary || isFetching)
  const hasRows = rows.length > 0
  const hasMaterials = linkedMaterials.length > 0
  const hasPlanInRange = rangedSummary.some(s => (s.expected ?? 0) > 0)

  let emptyVariant = null
  if (filterStep === 1) emptyVariant = 'no-month'
  else if (filterStep === 2) emptyVariant = 'no-campaign'
  else if (!isLoadingData && !hasRows && !hasMaterials) emptyVariant = 'no-rules'

  const campaignCount = campaignOptions.length

  // ── Handlers ──────────────────────────────────────────────────
  function handleMonthChange(e) {
    const v = e.target.value
    setSelectedMonthRaw(v)
    setUserRange({ start: '', end: '' })
    setPage(1)
    if (selectedCampaignId) {
      const c = campaigns.find(cc => cc.id === selectedCampaignId)
      if (c && v) {
        const { start, end } = monthToRange(v)
        const cs = parseLocalDate(c.start_date)
        const ce = parseLocalDate(c.end_date)
        if (!(cs <= end && ce >= start)) setSelectedCampaignId('')
      } else { setSelectedCampaignId('') }
    }
  }
  function handleCampaignChange(opt) {
    setSelectedCampaignId(opt?.value ?? '')
    setUserRange({ start: '', end: '' })
    setPage(1)
  }
  function handleSearchChange(e) { setSearch(e.target.value); setPage(1) }
  function handlePageSizeChange(n) { setPageSize(n); setPage(1) }
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
  function handleResetRange() { setUserRange({ start: '', end: '' }) }

  const rangeBounds = defaultRange
  const rangeIsCustom = !!(userRange.start || userRange.end) &&
    rangeBounds.start && rangeBounds.end &&
    (rangeStart !== rangeBounds.start || rangeEnd !== rangeBounds.end)

  // ── Render ────────────────────────────────────────────────────
  return (
    <div>
      <div className="page-header">
        <h2>Materiais</h2>
      </div>

      {/* 3-step filter bar */}
      <div className="flow-filters">
        <div className={`flow-filter ${filterStep === 1 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="materials-month">
            <span className="flow-filter-label-step">1</span>Competência
          </label>
          <input id="materials-month" ref={monthInputRef} className="flow-month-input" type="month"
                 value={selectedMonth} onChange={handleMonthChange} placeholder="Selecione o mês" />
        </div>

        <div className={`flow-filter ${!selectedMonth ? 'flow-filter--locked' : filterStep === 2 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="materials-campaign">
            <span className="flow-filter-label-step">2</span>Campanha
            {selectedMonth && filterStep === 2 && campaignCount > 0 && (
              <span style={{ marginLeft: 'auto', textTransform: 'none', letterSpacing: 0, fontSize: 11, fontWeight: 600, color: 'var(--c-text-3)' }}>
                {campaignCount === 1 ? '1 disponível' : `${campaignCount} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="materials-campaign"
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

        <div className={`flow-filter ${filterStep < 3 ? 'flow-filter--locked' : 'flow-filter--active'}`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">3</span>Período
            {rangeIsCustom && (
              <button type="button" className="flow-range-reset" onClick={handleResetRange}
                      title="Resetar pro intervalo completo da campanha no mês" style={{ marginLeft: 'auto' }}>
                Resetar
              </button>
            )}
          </label>
          <div className="flow-range">
            <input type="date" value={rangeStart} min={pickerBounds.start || undefined} max={pickerBounds.end || undefined}
                   onChange={handleRangeStart} disabled={filterStep < 3} aria-label="Data de início" />
            <span className="flow-range-arrow">→</span>
            <input type="date" value={rangeEnd} min={pickerBounds.start || undefined} max={pickerBounds.end || undefined}
                   onChange={handleRangeEnd} disabled={filterStep < 3} aria-label="Data de fim" />
          </div>
        </div>
      </div>

      {/* Secondary toolbar: search */}
      {showContent && !emptyVariant && (
        <div className="flow-toolbar">
          <div className="stations-search">
            <span className="stations-search-icon">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
                <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
              </svg>
            </span>
            <input className="input stations-search-input" type="text" placeholder="Buscar emissora, cidade…"
                   value={search} onChange={handleSearchChange} />
          </div>
        </div>
      )}

      {/* Content */}
      {emptyVariant ? (
        <MaterialsEmpty
          variant={emptyVariant}
          monthLabelText={selectedMonth ? monthLabel(selectedMonth) : ''}
          campaignName={selectedCampaign?.name ?? ''}
          campaignCount={campaignCount}
          isAdmin={isAdmin}
          onPickMonth={() => {
            const el = monthInputRef.current
            if (!el) return
            el.focus()
            if (typeof el.showPicker === 'function') { try { el.showPicker() } catch { /* focus race */ } }
          }}
          onPickCampaign={focusCampaignSelect}
          onEditCampaign={() => { if (selectedCampaignId) navigate(`/campaigns/${selectedCampaignId}/edit`) }}
        />
      ) : isLoadingData ? (
        <MaterialsSkeleton />
      ) : (
        <>
          <PlanSummary
            totalExpected={planStats.totalExpected}
            materials={planStats.materials}
            programmedMaterials={planStats.programmedMaterials}
            stations={planStats.stations}
          />

          {hasMaterials && (
            <MaterialPlaybackList
              materials={linkedMaterials}
              typeById={typeById}
              programmedTypeIds={programmedTypeIds}
            />
          )}

          {!hasRows ? (
            <div style={{
              padding: '28px 18px', textAlign: 'center', color: 'var(--c-text-2)',
              background: 'var(--c-surface)', border: '1px solid var(--c-border)', borderRadius: 12, fontSize: 13,
            }}>
              Nenhuma regra de distribuição nesta campanha — nada programado pra exibir na grade.
            </div>
          ) : !hasPlanInRange ? (
            <div style={{
              padding: '28px 18px', textAlign: 'center', color: 'var(--c-text-2)',
              background: 'var(--c-surface)', border: '1px solid var(--c-border)', borderRadius: 12, fontSize: 13,
            }}>
              Sem programação entre <strong>{rangeLabel(rangeStart, rangeEnd) || monthLabel(selectedMonth)}</strong>.
            </div>
          ) : (
            <>
              <DistributionGrid
                mode="view"
                summary="plan"
                month={monthDate}
                campaignStart={gridStart}
                campaignEnd={gridEnd}
                visibleStart={gridStart}
                visibleEnd={gridEnd}
                stations={stationCatalog}
                rows={pagedRows}
                cellData={cellData}
                capAtToday={false}
                inlineStationInfo
              />
              {totalStations > 0 && (
                <AirtimePaginator
                  page={safePage} totalPages={totalPages} total={totalStations}
                  pageSize={pageSize} pageSizeOptions={PAGE_SIZE_OPTIONS}
                  onPageSizeChange={handlePageSizeChange} onChange={setPage}
                  singular="emissora" plural="emissoras"
                />
              )}
            </>
          )}
        </>
      )}
    </div>
  )
}
