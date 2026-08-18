import { useMemo, useState, useEffect, useRef } from 'react'
import RSelect from './RSelect'
import CampaignReportsMenu from './CampaignReportsMenu'
import { safeLogoUrl } from '../utils/logoUrl'
import { parseLocalDate } from '../utils/dates'
import { campaignsUnionRange } from '../utils/campaignRange'

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
 * Filter bar for /airtime-report. Fluxo de 4 passos:
 * Cliente → Competência → Campanhas → Período.
 *
 * O cliente é o primeiro recorte (mesma ordem de /insights e /live-map) e é
 * quem garante que a seleção múltipla de campanhas nunca mistura clientes —
 * a lista, o painel de materiais e o rótulo de público-alvo assumem um cliente
 * só. Pra quem não tem escolha (viewer de 1 cliente) o passo vira um chip
 * travado e não custa clique. Competência filtra as campanhas (só as que
 * cruzam o mês aparecem) e o período nasce na interseção (mês ∩ união das
 * vigências), podendo ser esticado pelos presets.
 *
 * Props:
 *   - clients, campaigns: catálogos completos (scope-aware, vindos da página)
 *   - clientId, onClientChange(id)
 *   - canPickClient: false → chip travado no lugar do select
 *   - competence: 'YYYY-MM' | '' — controlado pelo pai (URL-backed)
 *   - onCompetenceChange(v)
 *   - campaignIds: string[] — seleção múltipla
 *   - onCampaignsChange(ids)
 *   - from, to: 'YYYY-MM-DD' — date range
 *   - onFromChange, onToChange, onRangeChange({from,to})
 *   - q: search string (debounced internally before propagation)
 *   - onQChange(v)
 *
 * Export vive em CampaignReportsMenu — renderizado inline aqui; com mais de
 * uma campanha selecionada o próprio menu pergunta de qual delas é o relatório.
 */
export default function AirtimeFiltersBar({
  campaigns = [],
  clients = [],
  clientId,
  canPickClient = true,
  competence,
  onCompetenceChange,
  campaignIds = [],
  from,
  to,
  q,
  onClientChange,
  onCampaignsChange,
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

  const clientOptions = useMemo(() => clients.map(c => ({
    value: c.id,
    label: c.name,
    logo: c.logo_url ?? null,
  })), [clients])

  const selectedClient = useMemo(
    () => clientMap.get(clientId) ?? null,
    [clientMap, clientId])

  const allCampaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id,
      // Sufixo só no valor selecionado (deep-link); o dropdown exclui canceladas.
      label: c.status === 'cancelada' ? `${c.name} (cancelada)` : c.name,
      status: c.status,
      clientId: c.client_id,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
      startDate: c.start_date,
      endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  // Campaign options scoped by client + competence (mesma semântica de
  // /detections pro mês, mesma de /live-map pro cliente).
  // Canceladas ficam fora do seletor, mas seguem resolvíveis via
  // allCampaignOptions (deep-link de relatório histórico continua abrindo).
  const campaignOptions = useMemo(() => {
    if (!clientId || !competence) return []
    const { start, end } = monthToRange(competence)
    return allCampaignOptions.filter(o => {
      if (o.clientId !== clientId) return false
      if (o.status === 'cancelada') return false
      if (!o.startDate || !o.endDate) return false
      const cs = parseLocalDate(o.startDate)
      const ce = parseLocalDate(o.endDate)
      return cs <= end && ce >= start
    })
  }, [allCampaignOptions, clientId, competence])

  // Objetos selecionados — alimentam o value do select, os presets de período
  // e o menu de relatórios.
  const selectedOptions = useMemo(
    () => allCampaignOptions.filter(o => campaignIds.includes(o.value)),
    [allCampaignOptions, campaignIds])
  const selectedCampaignsRaw = useMemo(
    () => campaigns.filter(c => campaignIds.includes(c.id)),
    [campaigns, campaignIds])
  const unionRange = useMemo(
    () => campaignsUnionRange(selectedCampaignsRaw),
    [selectedCampaignsRaw])

  function formatClientOption(opt, { context }) {
    const size = context === 'value' ? 18 : 22
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
        <ClientMiniAvatar name={opt.label} logo={opt.logo} size={size} />
        <span style={{
          fontWeight: 600, color: '#06055B', fontSize: 13,
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>{opt.label}</span>
      </div>
    )
  }

  function formatCampaignOption(opt, { context }) {
    const period = formatCampaignPeriod(opt.startDate, opt.endDate)
    // Chip do multi-select: só o nome. O cliente já está travado no passo 1 e
    // repeti-lo em cada chip estouraria a largura da barra.
    if (context === 'value') {
      return (
        <span style={{ fontSize: 12.5, color: '#06055B', whiteSpace: 'nowrap' }}>
          {opt.label}
        </span>
      )
    }
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7, minWidth: 0 }}>
        <ClientMiniAvatar name={opt.clientName} logo={opt.clientLogo} size={22} />
        <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, gap: 1 }}>
          <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {opt.label}
          </span>
          {period && (
            <span style={{ color: '#94a3b8', fontSize: 11, whiteSpace: 'nowrap' }}>
              {period}
            </span>
          )}
        </div>
      </div>
    )
  }

  // Step state drives the active/locked/done classes on each filter cell.
  const hasCampaigns = campaignIds.length > 0
  const step = !clientId ? 1 : !competence ? 2 : !hasCampaigns ? 3 : 4
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
      if (unionRange) {
        const intStart = unionRange.start > sISO ? unionRange.start : sISO
        const intEnd   = unionRange.end < monthEndCapped ? unionRange.end : monthEndCapped
        if (from === intStart && to === intEnd) return 'monthCampaign'
      }
    }
    if (unionRange) {
      const tEnd = unionRange.end && unionRange.end < t ? unionRange.end : t
      if (from === unionRange.start && to === tEnd) return 'fullCampaign'
    }
    return 'custom'
  }, [from, to, competence, unionRange])

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
    if (p === 'monthCampaign' && competence && unionRange) {
      const { start, end } = monthToRange(competence)
      const sISO = isoFromDate(start)
      const eISO = isoFromDate(end)
      const monthEndCapped = eISO < t ? eISO : t
      return onRangeChange({
        from: unionRange.start > sISO ? unionRange.start : sISO,
        to:   unionRange.end < monthEndCapped ? unionRange.end : monthEndCapped,
      })
    }
    if (p === 'fullCampaign' && unionRange) {
      const tEnd = unionRange.end && unionRange.end < t ? unionRange.end : t
      return onRangeChange({ from: unionRange.start, to: tEnd })
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
  const multi = campaignIds.length > 1

  return (
    <div>
      {/* 4-step filter bar */}
      <div className="flow-filters flow-filters--auto at-flow">
        <div className={`flow-filter ${step === 1 ? 'flow-filter--active' : 'flow-filter--done'}`}>
          <label className="flow-filter-label" htmlFor="airtime-client">
            <span className="flow-filter-label-step">1</span>
            Cliente
          </label>
          {canPickClient ? (
            <RSelect
              inputId="airtime-client"
              options={clientOptions}
              value={clientOptions.find(o => o.value === clientId) ?? null}
              onChange={opt => onClientChange(opt?.value ?? '')}
              formatOptionLabel={formatClientOption}
              placeholder="Selecione…"
              isClearable
            />
          ) : (
            <div className="flow-locked-chip">
              <ClientMiniAvatar
                name={selectedClient?.name ?? ''}
                logo={selectedClient?.logo_url}
                size={22}
              />
              <span>{selectedClient?.name ?? 'Sua conta'}</span>
            </div>
          )}
        </div>

        <div className={`flow-filter ${
          !clientId ? 'flow-filter--locked' :
          step === 2 ? 'flow-filter--active' : 'flow-filter--done'
        }`}>
          <label className="flow-filter-label" htmlFor="airtime-month">
            <span className="flow-filter-label-step">2</span>
            Competência
          </label>
          <input
            id="airtime-month"
            className="flow-month-input"
            type="month"
            value={competence ?? ''}
            onChange={e => onCompetenceChange(e.target.value)}
            disabled={!clientId}
          />
        </div>

        <div className={`flow-filter ${
          !clientId || !competence ? 'flow-filter--locked' :
          step === 3 ? 'flow-filter--active' : 'flow-filter--done'
        }`}>
          <label className="flow-filter-label" htmlFor="airtime-campaign">
            <span className="flow-filter-label-step">3</span>
            Campanhas
            {clientId && competence && campaignCount > 0 && (
              <span className="flow-filter-hint">
                {hasCampaigns
                  ? `${campaignIds.length} de ${campaignCount}`
                  : campaignCount === 1 ? '1 disponível' : `${campaignCount} disponíveis`}
              </span>
            )}
          </label>
          <RSelect
            inputId="airtime-campaign"
            isMulti
            closeMenuOnSelect={false}
            options={campaignOptions}
            value={selectedOptions}
            onChange={opts => onCampaignsChange((opts || []).map(o => o.value))}
            formatOptionLabel={formatCampaignOption}
            isDisabled={!clientId || !competence}
            placeholder={
              !clientId ? 'Escolha um cliente primeiro' :
              !competence ? 'Escolha uma competência primeiro' :
              campaignCount === 0 ? `Nenhuma campanha em ${monthLabel(competence)}` :
              'Selecione 1 ou mais…'
            }
            noOptionsMessage={() => `Nenhuma campanha em ${monthLabel(competence)}`}
            isClearable
          />
        </div>

        <div className={`flow-filter ${
          step < 4 ? 'flow-filter--locked' : 'flow-filter--active'
        }`}>
          <label className="flow-filter-label">
            <span className="flow-filter-label-step">4</span>
            Período
          </label>
          <div className={'flow-range' + (invalidRange ? ' flow-range--error' : '')}>
            <input
              type="date"
              value={from ?? ''}
              onChange={e => onFromChange(e.target.value)}
              disabled={step < 4}
              aria-label="Data de início"
            />
            <span className="flow-range-arrow">→</span>
            <input
              type="date"
              value={to ?? ''}
              onChange={e => onToChange(e.target.value)}
              disabled={step < 4}
              aria-label="Data de fim"
            />
          </div>
        </div>
      </div>

      {/* Range presets (only meaningful once we're at the last step). */}
      {step === 4 && (
        <div className="airtime-filters-presets">
          {[
            { id: 'monthCampaign', label: 'Vigência no mês', disabled: !unionRange },
            { id: 'fullMonth',     label: 'Mês inteiro' },
            { id: 'fullCampaign',  label: multi ? 'Campanhas inteiras' : 'Campanha inteira', disabled: !unionRange },
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
      {step === 4 && (
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
              detalhado continua admin-only — o componente esconde a opção.
              Relatório é POR CAMPANHA: com seleção múltipla o menu abre com um
              seletor de campanha em cima do de período. */}
          <div style={{ marginLeft: 'auto' }}>
            <CampaignReportsMenu
              campaignId={campaignIds[0] ?? ''}
              campaignOptions={selectedOptions.map(o => ({ id: o.value, name: o.label }))}
              from={from}
              to={to}
              variant="compact"
              placement="bottom-end"
              disabled={!hasCampaigns || invalidRange}
              disabledReason={!hasCampaigns ? 'Selecione uma campanha' : 'Intervalo inválido'}
              label="Relatórios"
            />
          </div>
        </div>
      )}
    </div>
  )
}
