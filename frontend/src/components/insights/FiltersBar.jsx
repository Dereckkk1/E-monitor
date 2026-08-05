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

  // /clients é scope-aware: Cliente recebe a própria carteira (1 no caso
  // normal, N quando é agência), admin recebe a lista inteira. Usamos a mesma
  // fonte pras duas roles — o select é o MESMO componente alimentado pelos
  // MESMOS dados; quem decide se ele aparece é o tamanho da carteira.
  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data]
  )
  // Só quem tem escolha vê o seletor: admin (lista inteira) ou carteira com
  // 2+. Cliente de 1 cliente segue com o chip travado, como sempre foi.
  const canPickClient = isAdmin || clientOpts.length > 1
  const ownClient = useMemo(() => {
    if (canPickClient) return null
    return clientOpts[0]?.raw || null
  }, [clientOpts, canPickClient])

  // Carrega todas as campanhas — backend já filtra pelo scope do usuário.
  // O recorte pelo cliente selecionado é client-side aqui mesmo, e vale pra
  // qualquer role: pro Cliente de 1 cliente é no-op (todas as campanhas são
  // dele), pra agência é o que separa a carteira.
  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })

  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])

  const campOpts = useMemo(() => {
    const scoped = value.clientId
      ? allCampaigns.filter(c => c.client_id === value.clientId)
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
  }, [allCampaigns, value.clientId, value.campaignIds])

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
    // start_date/end_date chegam como timestamp ISO ("2026-01-15T00:00:00Z").
    // O <input type="date"> só aceita "YYYY-MM-DD" — sem o slice ele descarta o
    // valor e o campo fica VAZIO (o "Período completo só tira as datas"). Como
    // toda string começa com YYYY-MM-DD, o .sort() lexicográfico acima segue
    // válido pra achar o menor início e o maior fim.
    return { from: starts[0].slice(0, 10), to: ends[ends.length - 1].slice(0, 10) }
  }, [allCampaigns, value.campaignIds])

  // Quem NÃO pode escolher (Cliente com um único cliente) fica travado nele.
  // Agência/admin não são pinados: o /insights é por cliente e o backend exige
  // a escolha explícita quando a carteira tem 2+.
  useEffect(() => {
    if (canPickClient) return
    const own = clientOpts[0]?.value
    if (own && value.clientId !== own) {
      onChange({ ...value, clientId: own })
    }
    // Não dependemos de `value` inteiro pra evitar loop: só recalcula quando
    // a carteira chega/troca.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canPickClient, clientOpts])

  return (
    <div className="in-filters">
      <div className="in-filters-row">
        {canPickClient ? (
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
            isDisabled={canPickClient && !value.clientId}
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
