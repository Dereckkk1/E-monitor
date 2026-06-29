import { useMemo, useState, useEffect, useRef } from 'react'
import RSelect from './RSelect'
import CampaignReportsMenu from './CampaignReportsMenu'
import { safeLogoUrl } from '../utils/logoUrl'
import { parseLocalDate } from '../utils/dates'

function pad2(n) { return String(n).padStart(2, '0') }
function isoFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}
function todayISO() { return isoFromDate(new Date()) }
function daysAgoISO(n) {
  const d = new Date(); d.setDate(d.getDate() - n)
  return isoFromDate(d)
}
function monthLabel(ymStr) {
  if (!ymStr) return ''
  const [y, m] = ymStr.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
}
function monthToRange(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  const start = new Date(y, m - 1, 1, 0, 0, 0, 0)
  const end   = new Date(y, m, 0, 23, 59, 59, 999)
  return { start, end }
}

// Format a campaign's [start_date, end_date] window as pt-BR "dd-mm-aaaa – dd-mm-aaaa".
// Duplicated from DetectionsPage by design — keeps each page's campaign-select
// rendering self-contained; the helper is small.
function formatCampaignPeriod(startISO, endISO) {
  if (!startISO || !endISO) return ''
  const a = parseLocalDate(startISO)
  const b = parseLocalDate(endISO)
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return ''
  const fmt = d => `${pad2(d.getDate())}-${pad2(d.getMonth() + 1)}-${d.getFullYear()}`
  return `${fmt(a)} – ${fmt(b)}`
}

// ClientMiniAvatar mirrors DetectionsPage's avatar to keep the campaign
// dropdown visuals identical across the app.
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
      width: size, height: size, borderRadius: 4,
      background: '#fce7f3', color: '#E81E75',
      fontSize: Math.round(size * 0.42), fontWeight: 700,
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      flexShrink: 0, userSelect: 'none',
    }}>
      {initials}
    </div>
  )
}

/**
 * Filter bar for /airtime-report. Mirrors the 3-step flow from /detections:
 * Competência → Campanha → Período. Competência filters the campaign select
 * (only campaigns whose lifecycle overlaps the month appear). Date range
 * defaults to the intersection of (month ∩ campaign) but can be pushed
 * outside that window via the presets.
 *
 * Props:
 *   - competence: 'YYYY-MM' | '' — controlled by parent (URL-backed)
 *   - onCompetenceChange(v)
 *   - campaignId, onCampaignChange(id)
 *   - from, to: 'YYYY-MM-DD' — date range
 *   - onFromChange, onToChange, onRangeChange({from,to})
 *   - q: search string (debounced internally before propagation)
 *   - onQChange(v)
 *
 * Export agora vive em CampaignReportsMenu — renderizado inline aqui;
 * não precisa mais de onExportClick/exporting vindo de fora.
 */
export default function AirtimeFiltersBar({
  campaigns = [],
  clients = [],
  competence,
  onCompetenceChange,
  campaignId,
  from,
  to,
  q,
  onCampaignChange,
  onFromChange,
  onToChange,
  onRangeChange,
  onQChange,
}) {
  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const allCampaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id,
      // Sufixo só no valor selecionado (deep-link); o dropdown exclui canceladas.
      label: c.status === 'cancelada' ? `${c.name} (cancelada)` : c.name,
      status: c.status,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
      startDate: c.start_date,
      endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  // Campaign options scoped by competence (same semantics as /detections).
  // Canceladas ficam fora do seletor, mas seguem resolvíveis via
  // allCampaignOptions (deep-link de relatório histórico continua abrindo).
  const campaignOptions = useMemo(() => {
    if (!competence) return []
    const { start, end } = monthToRange(competence)
    return allCampaignOptions.filter(o => {
      if (o.status === 'cancelada') return false
      if (!o.startDate || !o.endDate) return false
      const cs = parseLocalDate(o.startDate)
      const ce = parseLocalDate(o.endDate)
      return cs <= end && ce >= start
    })
  }, [allCampaignOptions, competence])

  // Selected campaign object — used both for the controlled value of the
  // select and for the "Campanha inteira" preset detection below.
  const selectedCampaignOption = useMemo(
    () => allCampaignOptions.find(o => o.value === campaignId) ?? null,
    [allCampaignOptions, campaignId])
  const selectedCampaignRaw = useMemo(
    () => campaigns.find(c => c.id === campaignId) ?? null,
    [campaigns, campaignId])

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
              <span style={{ color: '#cbd5e1', fontSize: 11, flexShrink: 0 }}>|</span>
            )}
            <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {opt.label}
            </span>
            {isValue && period && (
              <>
                <span style={{ color: '#cbd5e1', fontSize: 11, flexShrink: 0 }}>•</span>
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

  // Step state drives the active/locked/done classes on each filter cell.
  const step = !competence ? 1 : !campaignId ? 2 : 3
  const invalidRange = from && to && from > to

  // Preset detection — derive which chip is "active" purely from current
  // from/to values. "custom" is the fallback when no preset matches.
  const activePreset = useMemo(() => {
    const t = todayISO()
    if (from === daysAgoISO(7) && to === t) return 'last7'
    if (from === daysAgoISO(30) && to === t) return 'last30'
    if (competence) {
      const { start, end } = monthToRange(competence)
      const sISO = isoFromDate(start)
      const eISO = isoFromDate(end)
      const monthEndCapped = eISO < t ? eISO : t
      if (from === sISO && to === monthEndCapped) return 'fullMonth'
      if (selectedCampaignRaw) {
        const cs = String(selectedCampaignRaw.start_date).slice(0, 10)
        const ce = String(selectedCampaignRaw.end_date).slice(0, 10)
        const intStart = cs > sISO ? cs : sISO
        const intEnd   = ce < monthEndCapped ? ce : monthEndCapped
        if (from === intStart && to === intEnd) return 'monthCampaign'
      }
    }
    if (selectedCampaignRaw) {
      const start = String(selectedCampaignRaw.start_date).slice(0, 10)
      const end   = String(selectedCampaignRaw.end_date).slice(0, 10)
      const tEnd = end && end < t ? end : t
      if (from === start && to === tEnd) return 'fullCampaign'
    }
    return 'custom'
  }, [from, to, competence, selectedCampaignRaw])

  function applyPreset(p) {
    const t = todayISO()
    if (p === 'last7')  return onRangeChange({ from: daysAgoISO(7),  to: t })
    if (p === 'last30') return onRangeChange({ from: daysAgoISO(30), to: t })
    if (p === 'fullMonth' && competence) {
      const { start, end } = monthToRange(competence)
      const sISO = isoFromDate(start)
      const eISO = isoFromDate(end)
      return onRangeChange({ from: sISO, to: eISO < t ? eISO : t })
    }
    if (p === 'monthCampaign' && competence && selectedCampaignRaw) {
      const { start, end } = monthToRange(competence)
      const sISO = isoFromDate(start)
      const eISO = isoFromDate(end)
      const monthEndCapped = eISO < t ? eISO : t
      const cs = String(selectedCampaignRaw.start_date).slice(0, 10)
      const ce = String(selectedCampaignRaw.end_date).slice(0, 10)
      return onRangeChange({ from: cs > sISO ? cs : sISO, to: ce < monthEndCapped ? ce : monthEndCapped })
    }
    if (p === 'fullCampaign' && selectedCampaignRaw) {
      const start = String(selectedCampaignRaw.start_date).slice(0, 10)
      const end   = String(selectedCampaignRaw.end_date).slice(0, 10)
      const tEnd = end && end < t ? end : t
      return onRangeChange({ from: start, to: tEnd })
    }
  }

  // Debounced q — propagate to parent 300ms after the last keystroke.
  const [localQ, setLocalQ] = useState(q ?? '')
  const debounceRef = useRef(null)
  useEffect(() => {
    // External resets (parent zeroing q via "Limpar filtros" etc.) need to
    // pull the local input back into sync. The compare guards against the
    // setState-in-effect lint complaint when nothing actually changed.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLocalQ(prev => prev === (q ?? '') ? prev : (q ?? ''))
  }, [q])
  function handleQ(e) {
    const v = e.target.value
    setLocalQ(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => onQChange(v), 300)
  }
  useEffect(() => () => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
  }, [])

  const campaignCount = campaignOptions.length

  return (
    <div>
      {/* 3-step filter bar */}
      <div className="flow-filters">
        <div className={`flow-filter ${step === 1 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="airtime-month">
            <span className="flow-filter-label-step">1</span>
            Competência
          </label>
          <input
            id="airtime-month"
            className="flow-month-input"
            type="month"
            value={competence ?? ''}
            onChange={e => onCompetenceChange(e.target.value)}
          />
        </div>

        <div className={`flow-filter ${
          !competence ? 'flow-filter--locked' :
          step === 2 ? 'flow-filter--active' : 'flow-filter--done'
        }`}>
          <label className="flow-filter-label" htmlFor="airtime-campaign">
            <span className="flow-filter-label-step">2</span>
            Campanha
            {competence && step === 2 && campaignCount > 0 && (
              <span style={{ marginLeft: 'auto', textTransform: 'none', letterSpacing: 0, fontSize: 11, fontWeight: 600, color: 'var(--c-text-3)' }}>
                {campaignCount === 1 ? '1 disponível' : `${campaignCount} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="airtime-campaign"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={opt => onCampaignChange(opt?.value ?? '')}
            formatOptionLabel={formatCampaignOption}
            isDisabled={!competence}
            placeholder={
              !competence ? 'Escolha uma competência primeiro' :
              campaignCount === 0 ? `Nenhuma campanha em ${monthLabel(competence)}` :
              `${campaignCount === 1 ? '1 campanha' : `${campaignCount} campanhas`} em ${monthLabel(competence)}`
            }
            noOptionsMessage={() => `Nenhuma campanha em ${monthLabel(competence)}`}
            isClearable
          />
        </div>

        <div className={`flow-filter ${
          step < 3 ? 'flow-filter--locked' : 'flow-filter--active'
        }`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">3</span>
            Período
          </label>
          <div className={'flow-range' + (invalidRange ? ' flow-range--error' : '')}>
            <input
              type="date"
              value={from ?? ''}
              onChange={e => onFromChange(e.target.value)}
              disabled={step < 3}
              aria-label="Data de início"
            />
            <span className="flow-range-arrow">→</span>
            <input
              type="date"
              value={to ?? ''}
              onChange={e => onToChange(e.target.value)}
              disabled={step < 3}
              aria-label="Data de fim"
            />
          </div>
        </div>
      </div>

      {/* Range presets (only meaningful once we're at step 3). */}
      {step === 3 && (
        <div className="airtime-filters-presets">
          {[
            { id: 'monthCampaign', label: 'Vigência no mês', disabled: !selectedCampaignRaw },
            { id: 'fullMonth',     label: 'Mês inteiro' },
            { id: 'fullCampaign',  label: 'Campanha inteira', disabled: !selectedCampaignRaw },
            { id: 'last7',         label: 'Últimos 7 dias' },
            { id: 'last30',        label: 'Últimos 30 dias' },
            { id: 'custom',        label: 'Personalizado', readonly: true },
          ].map(p => (
            <button
              key={p.id}
              type="button"
              className={'airtime-preset-chip' + (activePreset === p.id ? ' active' : '')}
              disabled={p.disabled || p.readonly}
              onClick={() => !p.readonly && applyPreset(p.id)}
            >{p.label}</button>
          ))}
        </div>
      )}

      {/* Secondary toolbar: search + export. Visible whenever a campaign is set. */}
      {step === 3 && (
        <div className="flow-toolbar">
          <div className="airtime-filters-search-wrap">
            <span className="airtime-filters-search-icon" aria-hidden>
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
                <circle cx="7" cy="7" r="5" />
                <path d="M11 11l3 3" strokeLinecap="round" />
              </svg>
            </span>
            <input
              id="airtime-search"
              type="text"
              placeholder="Buscar emissora, material…"
              value={localQ}
              onChange={handleQ}
              className="airtime-filters-search-input"
            />
          </div>

          {/* Menu unificado de relatórios — substituiu o botão "Exportar CSV"
              admin-only que vivia aqui. Agora viewer também consegue baixar
              consolidado/PDF (escopado ao próprio cliente no backend); CSV
              detalhado continua admin-only — o componente esconde a opção. */}
          <div style={{ marginLeft: 'auto' }}>
            <CampaignReportsMenu
              campaignId={campaignId}
              from={from}
              to={to}
              variant="compact"
              placement="bottom-end"
              disabled={!campaignId || invalidRange}
              disabledReason={!campaignId ? 'Selecione uma campanha' : 'Intervalo inválido'}
              label="Relatórios"
            />
          </div>
        </div>
      )}
    </div>
  )
}
