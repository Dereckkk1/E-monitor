import { useEffect, useMemo, useState } from 'react'
import RSelect from '../RSelect'
import { useAuth } from '../../contexts/AuthContext'
import { useClients, useCampaignsPaged, useStations, useMaterials, useCampaignMaterialsMany } from '../../api/hooks'
import { buildLogoUrl } from '../../utils/logoUrl'

// MiniAvatar quadrado (logo ou iniciais). Espelha o ClientMiniAvatar de
// AirtimeFiltersBar pra manter consistência visual em selects do projeto.
function MiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  // buildLogoUrl, não safeLogoUrl: stations.logo_url é o path relativo do
  // AppSheet ("Rádios 2_Images/x.png"), que sem resolver 404a e cai nas
  // iniciais. Cliente costuma ter URL absoluta e passa direto pelos dois.
  const safe = buildLogoUrl(logo)
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
          // Mesmo motivo do badge de duração dos materiais: cinza fixo some no
          // fundo rosa da opção selecionada.
          <span style={{ fontSize: 11, color: 'inherit', opacity: 0.65, whiteSpace: 'nowrap' }}>
            {freq}{freq && cityState ? ' · ' : ''}{cityState}
          </span>
        )}
      </div>
    </div>
  )
}

// No MENU o nome quebra linha; no chip ele trunca.
//
// Nomes de material compartilham prefixo longo ("ASAAS - CAMPANHA
// PUBLICITÁRIA …") e o que diferencia um do outro está no FIM. Truncar com
// reticências na largura da coluna deixava a lista inteira com o mesmo texto
// visível — impossível escolher. Quebrar em 2 linhas custa altura no menu (que
// rola) e devolve a parte que importa. O chip continua truncando: ali o
// controle não pode crescer.
function formatMaterialOption(opt, { context }) {
  const m = opt.raw
  const dur = m?.duration_seconds ? `${Math.round(m.duration_seconds)}s` : null
  const noMenu = context !== 'value'
  return (
    <div style={{ display: 'flex', alignItems: noMenu ? 'flex-start' : 'center', gap: 8, minWidth: 0 }}>
      <span style={{
        fontWeight: 600, color: '#06055B', fontSize: 13, minWidth: 0,
        ...(noMenu
          ? { whiteSpace: 'normal', overflowWrap: 'anywhere', lineHeight: 1.35 }
          : { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }),
      }}>
        {opt.label}
      </span>
      {dur && noMenu && (
        // color herdado + opacidade, não um cinza fixo: na opção SELECIONADA o
        // react-select pinta o fundo de rosa e o texto de branco, e um #6b7280
        // fixo ficava ilegível ali.
        <span style={{ fontSize: 11, color: 'inherit', opacity: 0.65, whiteSpace: 'nowrap', flexShrink: 0, marginLeft: 'auto', paddingTop: 2 }}>{dur}</span>
      )}
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

export default function FiltersBar({ value, onChange }) {
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

  // Emissoras-alvo das campanhas selecionadas — a lista que o passo 4 oferece.
  // Sai das próprias campanhas (target_stations), então não depende de nenhuma
  // busca no catálogo.
  const targetStationIds = useMemo(() => {
    if (!value.campaignIds || value.campaignIds.length === 0) return []
    const union = new Set()
    for (const c of allCampaigns) {
      if (!value.campaignIds.includes(c.id)) continue
      for (const id of c.target_stations || []) union.add(id)
    }
    return [...union]
  }, [allCampaigns, value.campaignIds])

  const hasClient = !!value.clientId
  const hasCampaigns = (value.campaignIds || []).length > 0

  // Resolve SÓ essas emissoras (rótulo, dial, praça, logo) via ?ids=.
  //
  // Antes era `useStations()` sem parâmetro nenhum, e aí mora o bug que fazia
  // o seletor vir vazio em prod sem erro no console: sem `limit`, o backend
  // devolve a 1ª página de 20, ordenada por monitoring_status/pmm/nome. Com
  // 7,5 mil emissoras no catálogo, essas 20 quase nunca incluem as da
  // campanha — o filtro `union.has(s.id)` embaixo derrubava tudo e o campo
  // ficava sem opção. Em dev o banco era pequeno e as 20 cobriam o caso.
  //
  // A saída NÃO é subir o limit: o catálogo inteiro serializado passa de 10 MB
  // (é o que /detections e /materials fazem hoje com limit=2000, ~3 MB). Aqui
  // pedimos o conjunto exato — dezenas de linhas.
  const stationsQ = useStations({
    ids: targetStationIds.join(','),
    enabled: targetStationIds.length > 0,
  })

  const stationOpts = useMemo(() => {
    const d = stationsQ.data
    const list = Array.isArray(d) ? d : (Array.isArray(d?.data) ? d.data : [])
    // O recorte por target_stations é redundante contra o backend que entende
    // ?ids= (ele já devolve exatamente esse conjunto) e é a rede de segurança
    // contra o que NÃO entende: API antiga ignora o parâmetro e responde as 20
    // primeiras do catálogo, que apareceriam aqui como emissoras alheias à
    // campanha. Com o filtro, o pior caso volta a ser "seletor vazio" em vez de
    // "seletor com opções erradas". Importa na janela entre o deploy do
    // frontend (Cloudflare Pages, automático no push) e o do backend
    // (deploy.sh na VM) — ver docs/features/broadcaster-search.md.
    const alvo = new Set(targetStationIds)
    return list
      .filter(s => alvo.has(s.id))
      .map(s => ({ value: s.id, label: s.name, raw: s }))
  }, [stationsQ.data, targetStationIds])

  // Materiais oferecidos = os VINCULADOS às campanhas selecionadas, e não a
  // biblioteca inteira do cliente: filtrar por um material que não roda nessas
  // campanhas devolveria a tela zerada sem explicar por quê.
  //
  // São duas fontes porque o vínculo e o rótulo moram em lugares diferentes:
  // /campaigns/{id}/materials devolve só os ids (campaign_materials é tabela de
  // ligação), e o título/duração vêm da biblioteca do cliente.
  const linkedMaterials = useCampaignMaterialsMany(value.campaignIds || [])
  const libraryQ = useMaterials(value.clientId ?? null)

  // Depende de linkedMaterials.key (string estável), não do Set: o hook remonta
  // o Set a cada render e usá-lo como dependência refaria este useMemo sempre.
  const linkedKey = linkedMaterials.key
  const materialOpts = useMemo(() => {
    if (!hasCampaigns || !linkedKey) return []
    const linked = new Set(linkedKey.split(','))
    const lib = Array.isArray(libraryQ.data) ? libraryQ.data : []
    return lib
      .filter(m => linked.has(m.id))
      .map(m => ({ value: m.id, label: m.title || `Material ${m.short_id}`, raw: m }))
      .sort((a, b) => a.label.localeCompare(b.label, 'pt-BR'))
  }, [libraryQ.data, linkedKey, hasCampaigns])

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

  // Passo em que o usuário está: cada um destrava o próximo. Emissoras e
  // materiais ficam de fora porque são refino, não pré-requisito.
  const stationCount = (value.stationIds || []).length
  const materialCount = (value.materialIds || []).length

  // Barra de filtros em passos — mesma das telas de Veiculação. Cadeia real:
  // o backend recusa /insights sem client_id, e a lista de emissoras só existe
  // depois das campanhas. Exportar não é filtro: os botões vivem no header da
  // página (fora do trecho capturado no PNG/PDF).
  return (
    <div className="flow-filters flow-filters--auto in-flow">
      <div className={`flow-filter ${hasClient ? 'flow-filter--done' : 'flow-filter--active'}`}>
        <label className="flow-filter-label" htmlFor="in-client">
          <span className="flow-filter-label-step">1</span>
          Cliente
        </label>
        {canPickClient ? (
          <RSelect
            inputId="in-client"
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
        ) : (
          <div className="flow-locked-chip">
            <MiniAvatar name={ownClient?.name || user?.email || ''} logo={ownClient?.logo_url} size={22} />
            <span>{ownClient?.name || user?.email || 'Sua conta'}</span>
          </div>
        )}
      </div>

      <div className={`flow-filter ${
        !hasClient ? 'flow-filter--locked' :
        hasCampaigns ? 'flow-filter--done' : 'flow-filter--active'
      }`}>
        <label className="flow-filter-label" htmlFor="in-campaigns">
          <span className="flow-filter-label-step">2</span>
          Campanhas
          {hasClient && (
            <span className="flow-filter-hint">
              {hasCampaigns
                ? `${value.campaignIds.length} de ${campOpts.length}`
                : campOpts.length === 1 ? '1 disponível' : `${campOpts.length} disponíveis`}
            </span>
          )}
        </label>
        <RSelect
          inputId="in-campaigns"
          isMulti
          compactValues
          options={campOpts}
          value={campOpts.filter(o => value.campaignIds.includes(o.value))}
          onChange={opts => onChange({
            ...value,
            campaignIds: (opts || []).map(o => o.value),
            stationIds: [], // resetar emissoras quando campanhas mudam
          })}
          placeholder={
            !hasClient ? 'Escolha um cliente primeiro' :
            campaignsQ.isPending ? 'Carregando…' :
            campOpts.length === 0 ? 'Nenhuma campanha deste cliente' :
            'Selecione 1 ou mais…'
          }
          isDisabled={canPickClient && !hasClient}
          isLoading={campaignsQ.isPending}
          closeMenuOnSelect={false}
        />
      </div>

      <div className={`flow-filter ${hasCampaigns ? 'flow-filter--done' : ''}`}>
        <label className="flow-filter-label">
          <span className="flow-filter-label-step">3</span>
          Período
          <button
            type="button"
            className="flow-range-reset flow-filter-hint"
            disabled={!fullRange}
            onClick={() => fullRange && onChange({ ...value, from: fullRange.from, to: fullRange.to })}
            title={fullRange ? `${fullRange.from} → ${fullRange.to}` : 'Selecione campanhas primeiro'}
          >
            Período completo
          </button>
        </label>
        <div className="flow-range">
          <input
            type="date"
            value={value.from || firstOfMonthISO()}
            max={value.to || undefined}
            onChange={e => onChange({ ...value, from: e.target.value })}
            aria-label="Data de início"
          />
          <span className="flow-range-arrow">→</span>
          <input
            type="date"
            value={value.to || lastOfMonthISO()}
            min={value.from || undefined}
            onChange={e => onChange({ ...value, to: e.target.value })}
            aria-label="Data de fim"
          />
        </div>
      </div>

      <div className={`flow-filter flow-filter--optional ${
        !hasCampaigns ? 'flow-filter--locked' : stationCount ? 'flow-filter--done' : ''
      }`}>
        <label className="flow-filter-label" htmlFor="in-stations">
          <span className="flow-filter-label-step">4</span>
          Emissoras
          {stationCount
            ? <span className="flow-filter-hint">{stationCount} de {stationOpts.length}</span>
            : <span className="flow-filter-tag">opcional</span>}
        </label>
        <RSelect
          inputId="in-stations"
          isMulti
          compactValues
          options={stationOpts}
          value={stationOpts.filter(o => (value.stationIds || []).includes(o.value))}
          onChange={opts => onChange({
            ...value,
            stationIds: (opts || []).map(o => o.value),
          })}
          placeholder="Todas (padrão)"
          isDisabled={!hasCampaigns}
          closeMenuOnSelect={false}
          formatOptionLabel={formatStationOption}
        />
      </div>

      {/* Materiais — refino opcional, como Emissoras. Recorta impactos,
          veiculações e demografia de forma exata; o que está em R$ passa a ser
          rateio (o contrato é por tipo, não por material) e a tela avisa. */}
      <div className={`flow-filter flow-filter--optional ${
        !hasCampaigns ? 'flow-filter--locked' : materialCount ? 'flow-filter--done' : ''
      }`}>
        <label className="flow-filter-label" htmlFor="in-materials">
          <span className="flow-filter-label-step">5</span>
          Materiais
          {materialCount
            ? <span className="flow-filter-hint">{materialCount} de {materialOpts.length}</span>
            : <span className="flow-filter-tag">opcional</span>}
        </label>
        <RSelect
          inputId="in-materials"
          isMulti
          compactValues
          options={materialOpts}
          value={materialOpts.filter(o => (value.materialIds || []).includes(o.value))}
          onChange={opts => onChange({
            ...value,
            materialIds: (opts || []).map(o => o.value),
          })}
          placeholder={
            !hasCampaigns ? 'Escolha as campanhas primeiro' :
            (linkedMaterials.isPending || libraryQ.isPending) ? 'Carregando…' :
            materialOpts.length === 0 ? 'Nenhum material nestas campanhas' :
            'Todos (padrão)'
          }
          isDisabled={!hasCampaigns}
          isLoading={linkedMaterials.isPending || libraryQ.isPending}
          closeMenuOnSelect={false}
          formatOptionLabel={formatMaterialOption}
        />
      </div>
    </div>
  )
}
