import { useMemo, useState, useEffect, useRef } from 'react'
import RSelect from './RSelect'
import { useAuth } from '../contexts/AuthContext'

function todayISO() {
  return new Date().toISOString().slice(0, 10)
}
function daysAgoISO(n) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}
function firstOfMonthISO() {
  const d = new Date()
  return new Date(d.getFullYear(), d.getMonth(), 1).toISOString().slice(0, 10)
}

// Reused from DetectionsPage — keep visuals consistent across both screens.
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

export default function AirtimeFiltersBar({
  campaigns = [],
  clients = [],
  campaignId,
  from,
  to,
  q,
  onCampaignChange,
  onFromChange,
  onToChange,
  onRangeChange,
  onQChange,
  onExportClick,
  exporting = false,
}) {
  const { isAdmin } = useAuth()

  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const campaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id,
      label: c.name,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
      startDate: c.start_date,
      endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  const selectedCampaignOption = campaignOptions.find(o => o.value === campaignId) ?? null
  const selectedCampaignRaw = campaigns.find(c => c.id === campaignId) ?? null

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
            <span style={{ color: '#cbd5e1', fontSize: 11, flexShrink: 0 }}>|</span>
          )}
          <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {opt.label}
          </span>
        </div>
      </div>
    )
  }

  // Preset detection — derive which chip is "active" purely from current
  // from/to values. "custom" is the fallback when no preset matches.
  const activePreset = useMemo(() => {
    const t = todayISO()
    if (from === daysAgoISO(7) && to === t) return 'last7'
    if (from === firstOfMonthISO() && to === t) return 'thisMonth'
    if (selectedCampaignRaw && from === selectedCampaignRaw.start_date) {
      const tEnd = selectedCampaignRaw.end_date && selectedCampaignRaw.end_date < t
        ? selectedCampaignRaw.end_date
        : t
      if (to === tEnd) return 'fullCampaign'
    }
    return 'custom'
  }, [from, to, selectedCampaignRaw])

  function applyPreset(p) {
    // Combine from+to into a single onRangeChange call so we don't trigger
    // two consecutive URL writes that read stale state and clobber each
    // other (the bug that made presets silently fail).
    if (p === 'last7') {
      onRangeChange({ from: daysAgoISO(7), to: todayISO() })
    } else if (p === 'thisMonth') {
      onRangeChange({ from: firstOfMonthISO(), to: todayISO() })
    } else if (p === 'fullCampaign' && selectedCampaignRaw) {
      const t = todayISO()
      const tEnd = selectedCampaignRaw.end_date && selectedCampaignRaw.end_date < t
        ? selectedCampaignRaw.end_date
        : t
      onRangeChange({ from: selectedCampaignRaw.start_date, to: tEnd })
    }
  }

  // Debounced q — propagate to parent 300ms after the last keystroke.
  const [localQ, setLocalQ] = useState(q ?? '')
  const debounceRef = useRef(null)
  useEffect(() => {
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

  const invalidRange = from && to && from > to

  return (
    <div className="airtime-filters">
      <div className="airtime-filters-row">
        <div className="airtime-filters-campaign">
          <label htmlFor="airtime-campaign-select">Campanha</label>
          <RSelect
            inputId="airtime-campaign-select"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={opt => onCampaignChange(opt?.value ?? '')}
            formatOptionLabel={formatCampaignOption}
            placeholder="Selecione uma campanha"
            isClearable
          />
        </div>

        <div className="airtime-filters-dates">
          <div className="airtime-filters-date">
            <label htmlFor="airtime-from">De</label>
            <input
              id="airtime-from"
              type="date"
              value={from ?? ''}
              onChange={e => onFromChange(e.target.value)}
              className={'input' + (invalidRange ? ' input-error' : '')}
            />
          </div>
          <div className="airtime-filters-date">
            <label htmlFor="airtime-to">Até</label>
            <input
              id="airtime-to"
              type="date"
              value={to ?? ''}
              onChange={e => onToChange(e.target.value)}
              className={'input' + (invalidRange ? ' input-error' : '')}
            />
          </div>
        </div>

        <div className="airtime-filters-search">
          <label htmlFor="airtime-search">Buscar</label>
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
              placeholder="Emissora, material…"
              value={localQ}
              onChange={handleQ}
              className="airtime-filters-search-input"
            />
          </div>
        </div>

        {isAdmin && (
          <div className="airtime-filters-export-wrap">
            <button
              type="button"
              className="airtime-filters-export"
              onClick={onExportClick}
              disabled={!campaignId || invalidRange || exporting}
              title={!campaignId ? 'Selecione uma campanha' : 'Exportar CSV'}
            >
              {exporting ? 'Gerando…' : '↓ Exportar CSV'}
            </button>
          </div>
        )}
      </div>

      <div className="airtime-filters-presets">
        {[
          { id: 'last7',        label: 'Últimos 7 dias' },
          { id: 'thisMonth',    label: 'Este mês' },
          { id: 'fullCampaign', label: 'Campanha inteira', disabled: !selectedCampaignRaw },
          { id: 'custom',       label: 'Personalizado', readonly: true },
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
    </div>
  )
}
