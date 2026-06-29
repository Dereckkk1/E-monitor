import { useEffect, useMemo, useState } from 'react'
import RSelect from '../RSelect'
import { useAuth } from '../../contexts/AuthContext'
import { useClients, useCampaignsPaged, useStations } from '../../api/hooks'
import { safeLogoUrl } from '../../utils/logoUrl'

// MiniAvatar quadrado (logo ou iniciais). Espelha o ClientMiniAvatar de
// AirtimeFiltersBar pra manter consistência visual em selects do projeto.
function MiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  const safe = safeLogoUrl(logo)
  if (safe && !imgError) {
    return (
      <img
        src={safe}
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

function formatClientOption(opt, { context }) {
  const size = context === 'value' ? 18 : 22
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
      <MiniAvatar name={opt.label} logo={opt.raw?.logo_url} size={size} />
      <span style={{ fontWeight: 600, color: '#06055B', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
        {opt.label}
      </span>
    </div>
  )
}

function formatStationOption(opt, { context }) {
  const size = context === 'value' ? 18 : 24
  const s = opt.raw
  const freq = s?.frequency_mhz ? `${s.band || 'FM'} ${Number(s.frequency_mhz).toFixed(1)}` : (s?.band || '')
  const cityState = [s?.city, s?.state].filter(Boolean).join('/')
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
      <MiniAvatar name={opt.label} logo={s?.logo_url} size={size} />
      <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0, gap: 1 }}>
        <span style={{ fontWeight: 600, color: '#06055B', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
          {opt.label}
        </span>
        {(freq || cityState) && context !== 'value' && (
          <span style={{ fontSize: 11, color: '#6b7280', whiteSpace: 'nowrap' }}>
            {freq}{freq && cityState ? ' · ' : ''}{cityState}
          </span>
        )}
      </div>
    </div>
  )
}

function firstOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), 1)).toISOString().slice(0, 10)
}
function lastOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth() + 1, 0)).toISOString().slice(0, 10)
}

export default function FiltersBar({ value, onChange, onExportImage, onExportPDF }) {
  const { isAdmin, user } = useAuth()

  // /clients é scope-aware: viewer recebe lista de 1 (o próprio cliente),
  // admin recebe a lista inteira. Usamos a mesma fonte pras duas roles —
  // admin pra montar o select, viewer pra resolver nome+logo do próprio
  // cliente no chip travado.
  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data]
  )
  const ownClient = useMemo(() => {
    if (isAdmin) return null
    return (clientsQ.data || []).find(c => c.id === user?.client_id) || null
  }, [clientsQ.data, isAdmin, user?.client_id])

  // Carrega todas as campanhas — backend já filtra pelo scope do cliente.
  // Para admin, filtramos pelo client selecionado no client-side aqui mesmo.
  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })

  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])

  const campOpts = useMemo(() => {
    const scoped = isAdmin
      ? allCampaigns.filter(c => !value.clientId || c.client_id === value.clientId)
      : allCampaigns
    // Política "manter e marcar": canceladas (terminais) não são oferecidas
    // para uma nova seleção, mas se já estiverem selecionadas (deep-link /
    // estado salvo) permanecem visíveis e contando no histórico, rotuladas
    // como "(cancelada)" para não passarem por ativas.
    const selected = new Set(value.campaignIds || [])
    return scoped
      .filter(c => c.status !== 'cancelada' || selected.has(c.id))
      .map(c => ({
        value: c.id,
        label: c.status === 'cancelada' ? `${c.name} (cancelada)` : c.name,
        raw: c,
      }))
  }, [allCampaigns, value.clientId, value.campaignIds, isAdmin])

  const stationsQ = useStations()

  // useStations devolve { data: [...], total, page, ... } (paginado). Sem
  // params, pega a 1ª página. Pra essa tela isso costuma cobrir todas, mas
  // se o cliente tiver >page_size estações o multi-select ficará incompleto
  // — TODO se isso virar problema, listar via endpoint dedicado.
  const stationsList = useMemo(() => {
    const d = stationsQ.data
    if (Array.isArray(d)) return d
    if (Array.isArray(d?.data)) return d.data
    return []
  }, [stationsQ.data])

  const stationOpts = useMemo(() => {
    if (!value.campaignIds || value.campaignIds.length === 0) return []
    const targetSets = allCampaigns
      .filter(c => value.campaignIds.includes(c.id))
      .map(c => new Set(c.target_stations || []))
    if (targetSets.length === 0) return []
    const union = new Set()
    for (const set of targetSets) for (const id of set) union.add(id)
    return stationsList
      .filter(s => union.has(s.id))
      .map(s => ({ value: s.id, label: s.name, raw: s }))
  }, [allCampaigns, stationsList, value.campaignIds])

  const fullRange = useMemo(() => {
    const selected = allCampaigns.filter(c => value.campaignIds.includes(c.id))
    if (selected.length === 0) return null
    const starts = selected.map(c => c.start_date).filter(Boolean).sort()
    const ends = selected.map(c => c.end_date).filter(Boolean).sort()
    if (!starts.length || !ends.length) return null
    return { from: starts[0], to: ends[ends.length - 1] }
  }, [allCampaigns, value.campaignIds])

  // Para role cliente: trava o clientId no próprio.
  useEffect(() => {
    if (!isAdmin && user?.client_id && value.clientId !== user.client_id) {
      onChange({ ...value, clientId: user.client_id })
    }
    // Não dependemos de `value` inteiro pra evitar loop: só recalcula quando
    // a sessão troca ou o clientId desvia.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin, user?.client_id])

  return (
    <div className="in-filters">
      <div className="in-filters-row">
        {isAdmin ? (
          <div className="in-filter">
            <label className="in-filter-label">Cliente</label>
            <RSelect
              options={clientOpts}
              value={clientOpts.find(o => o.value === value.clientId) || null}
              onChange={opt => onChange({
                ...value,
                clientId: opt?.value || null,
                campaignIds: [],
                stationIds: [],
              })}
              placeholder="Selecione…"
              isLoading={clientsQ.isPending}
              isClearable
              formatOptionLabel={formatClientOption}
            />
          </div>
        ) : (
          <div className="in-filter in-filter--locked">
            <label className="in-filter-label">Cliente</label>
            <div className="in-locked-chip" style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              <MiniAvatar name={ownClient?.name || user?.email || ''} logo={ownClient?.logo_url} size={20} />
              <span>{ownClient?.name || user?.email || 'Sua conta'}</span>
            </div>
          </div>
        )}

        <div className="in-filter in-filter--wide">
          <label className="in-filter-label">Campanhas</label>
          <RSelect
            isMulti
            options={campOpts}
            value={campOpts.filter(o => value.campaignIds.includes(o.value))}
            onChange={opts => onChange({
              ...value,
              campaignIds: (opts || []).map(o => o.value),
              stationIds: [], // resetar emissoras quando campanhas mudam
            })}
            placeholder="Selecione 1 ou mais…"
            isDisabled={isAdmin && !value.clientId}
            isLoading={campaignsQ.isPending}
            closeMenuOnSelect={false}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">De</label>
          <input
            type="date"
            className="in-date"
            value={value.from || firstOfMonthISO()}
            onChange={e => onChange({ ...value, from: e.target.value })}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">Até</label>
          <input
            type="date"
            className="in-date"
            value={value.to || lastOfMonthISO()}
            onChange={e => onChange({ ...value, to: e.target.value })}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">&nbsp;</label>
          <button
            type="button"
            className="in-chip"
            disabled={!fullRange}
            onClick={() => fullRange && onChange({ ...value, from: fullRange.from, to: fullRange.to })}
            title={fullRange ? `${fullRange.from} → ${fullRange.to}` : 'Selecione campanhas primeiro'}
          >
            Período completo
          </button>
        </div>

        <div className="in-filter in-filter--wide">
          <label className="in-filter-label">Emissoras</label>
          <RSelect
            isMulti
            options={stationOpts}
            value={stationOpts.filter(o => (value.stationIds || []).includes(o.value))}
            onChange={opts => onChange({
              ...value,
              stationIds: (opts || []).map(o => o.value),
            })}
            placeholder="Todas (padrão)"
            isDisabled={!value.campaignIds || value.campaignIds.length === 0}
            closeMenuOnSelect={false}
            formatOptionLabel={formatStationOption}
          />
        </div>
      </div>

      <div className="in-filters-actions">
        <button type="button" className="in-btn-outline" onClick={onExportImage}>↓ Imagem</button>
        <button type="button" className="in-btn-outline" onClick={onExportPDF}>↓ PDF</button>
      </div>
    </div>
  )
}
