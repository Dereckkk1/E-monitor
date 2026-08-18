import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  useCampaigns, useClients, useDetectionsPaged, useMaterialAggregate,
  useCampaignPricingByCampaignStation,
} from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import AirtimeFiltersBar from '../components/AirtimeFiltersBar'
import AirtimeDetectionRow from '../components/AirtimeDetectionRow'
import AirtimeMaterialPanel from '../components/AirtimeMaterialPanel'
import AirtimePaginator from '../components/AirtimePaginator'
import AirtimeGhostPreview from '../components/AirtimeGhostPreview'
import { parseLocalDate } from '../utils/dates'
import { campaignsUnionRange } from '../utils/campaignRange'
import './AirtimeReportPage.css'

function pad2(n) { return String(n).padStart(2, '0') }
function isoFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}
function todayISO() { return isoFromDate(new Date()) }
function daysAgoISO(n) {
  const d = new Date(); d.setDate(d.getDate() - n)
  return isoFromDate(d)
}
function monthFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`
}
function monthLabel(ymStr) {
  if (!ymStr) return ''
  const [y, m] = ymStr.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
}
function monthToRange(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  return {
    start: new Date(y, m - 1, 1, 0, 0, 0, 0),
    end:   new Date(y, m, 0, 23, 59, 59, 999),
  }
}

// Intersection of (month ∩ união das campanhas selecionadas) clamped by today,
// used as the default date range when the user picks campaigns. Com mais de uma
// campanha, "a vigência" é a união (menor start, maior end) — é o único
// intervalo que cobre todas sem esconder tocada de nenhuma.
// Returns YYYY-MM-DD strings.
function defaultRangeForCampaigns(ymStr, selected) {
  const union = campaignsUnionRange(selected ?? [])
  if (!ymStr || !union) return { from: '', to: '' }
  const { start: monthStart, end: monthEnd } = monthToRange(ymStr)
  // parseLocalDate avoids the UTC-midnight shift that would push these to
  // the previous calendar day in São Paulo.
  const cStart = parseLocalDate(union.start)
  const cEnd   = parseLocalDate(union.end); cEnd.setHours(23, 59, 59, 999)
  const today  = new Date();                today.setHours(23, 59, 59, 999)
  const start = cStart > monthStart ? cStart : monthStart
  let   end   = cEnd   < monthEnd   ? cEnd   : monthEnd
  if (end > today) end = today
  if (start > end) return { from: '', to: '' }
  return { from: isoFromDate(start), to: isoFromDate(end) }
}

// Convert YYYY-MM-DD → full RFC3339 in São Paulo local time (UTC-3) so the
// backend WHERE clause aligns with how daily_play_summary buckets detections.
// Guards against full ISO timestamps from API responses sneaking in.
function isoToRFC3339Start(iso) {
  const date = iso ? String(iso).slice(0, 10) : ''
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null
  const d = new Date(`${date}T00:00:00.000-03:00`)
  return isNaN(d.getTime()) ? null : d.toISOString()
}
function isoToRFC3339End(iso) {
  const date = iso ? String(iso).slice(0, 10) : ''
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null
  const d = new Date(`${date}T23:59:59.999-03:00`)
  return isNaN(d.getTime()) ? null : d.toISOString()
}

function fmtRangeLabel(from, to) {
  return `${from.split('-').reverse().join('/')} e ${to.split('-').reverse().join('/')}`
}

export default function AirtimeReportPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const { data: campaigns = [] } = useCampaigns()
  // /clients é scope-aware: viewer recebe lista de 1 (o próprio cliente),
  // admin a lista inteira.
  const { data: clients = [] } = useClients()

  // URL is the source of truth — users can share the link with state preserved.
  const competence = searchParams.get('competence') ?? monthFromDate(new Date())
  const from = searchParams.get('from') ?? ''
  const to   = searchParams.get('to')   ?? ''
  const q    = searchParams.get('q')    ?? ''
  const page = parseInt(searchParams.get('page') ?? '1', 10) || 1

  // `campaigns` é o CSV da seleção múltipla; `campaign_id` é o formato antigo
  // de UMA campanha, mantido pra que link/relatório compartilhado antes desta
  // tela virar multi continue abrindo (vira seleção de 1).
  const campaignIds = useMemo(() => {
    const raw = searchParams.get('campaigns') ?? searchParams.get('campaign_id') ?? ''
    return raw.split(',').map(s => s.trim()).filter(Boolean)
  }, [searchParams])

  // Só quem tem escolha vê o seletor de cliente: admin sempre, e agência com
  // carteira de 2+. Pros demais o cliente é o único da carteira — o passo vira
  // chip travado e não custa clique.
  const canPickClient = isAdmin || clients.length > 1
  const urlClientId = searchParams.get('client_id') ?? ''
  const clientId = useMemo(() => {
    if (!canPickClient) return clients[0]?.id ?? ''
    if (urlClientId) return urlClientId
    // Deep-link legado (?campaign_id=…) não carrega cliente: resolve pela
    // campanha, senão o passo 1 ficaria vazio e travaria a tela.
    const first = campaignIds[0] ? campaigns.find(c => c.id === campaignIds[0]) : null
    return first?.client_id ?? ''
  }, [canPickClient, clients, urlClientId, campaignIds, campaigns])

  function setFilters(patch) {
    const next = new URLSearchParams(searchParams)
    Object.entries(patch).forEach(([k, v]) => {
      if (v === '' || v == null) next.delete(k)
      else next.set(k, String(v))
    })
    // A seleção sempre sai gravada em `campaigns`; o param legado morre no
    // primeiro clique pra não sobrar duas fontes de verdade na mesma URL.
    if ('campaigns' in patch) next.delete('campaign_id')
    setSearchParams(next, { replace: true })
  }

  const invalidRange = !!(from && to && from > to)

  const fromRFC = useMemo(
    () => (from && !invalidRange ? isoToRFC3339Start(from) : null),
    [from, invalidRange],
  )
  const toRFC = useMemo(
    () => (to && !invalidRange ? isoToRFC3339End(to) : null),
    [to, invalidRange],
  )

  const {
    data: detResp,
    isLoading: loadingList,
    isFetching,
  } = useDetectionsPaged({
    campaignIds,
    from: fromRFC,
    to: toRFC,
    q,
    page,
    pageSize: 10,
  })
  const { data: aggResp, isLoading: loadingAgg } = useMaterialAggregate({
    campaignIds,
    from: fromRFC,
    to: toRFC,
    q,
  })
  // Pricing é por (campanha, emissora): com várias campanhas na lista, indexar
  // só por emissora mostraria o preço da campanha errada quando duas contratam
  // a mesma rádio.
  const pricingByKey = useCampaignPricingByCampaignStation(campaignIds)

  // Rótulo do público-alvo (clients.target_label) pro title da pill teal das
  // rows. O passo 1 garante um cliente só na tela, então o rótulo é sempre
  // resolvível — o que não vale é rotular linha de um cliente com o target de
  // outro, e por isso ele sai do cliente selecionado, não da campanha.
  const targetLabel = useMemo(() => {
    if (!clientId) return null
    const raw = clients.find(cl => cl.id === clientId)?.target_label
    return (raw ?? '').trim() || null
  }, [clientId, clients])

  const detections = detResp?.data ?? []
  const total = detResp?.total ?? 0
  const totalPages = detResp?.total_pages ?? 1

  // Apenas um player ativo na página por vez.
  const [activePlayerId, setActivePlayerId] = useState(null)
  // Cross-highlight: passa a flutuar quando o usuário hovera o painel direito.
  const [highlightedMaterialId, setHighlightedMaterialId] = useState(null)

  // Export de relatórios agora vive em CampaignReportsMenu (dentro do
  // AirtimeFiltersBar) — substituiu o botão único "Exportar CSV".

  function handleClientChange(id) {
    // Cliente é o recorte mais externo: trocá-lo invalida campanhas e período.
    // A competência sobrevive — o mês continua sendo um mês.
    setFilters({ client_id: id || '', campaigns: '', from: '', to: '', page: '1' })
    setActivePlayerId(null)
  }
  function handleCompetenceChange(v) {
    // Changing competence invalidates campaigns + range — different month,
    // different catalog, different default window.
    setFilters({ competence: v || '', campaigns: '', from: '', to: '', page: '1' })
    setActivePlayerId(null)
  }
  function handleCampaignsChange(ids) {
    if (!ids.length) {
      setFilters({ campaigns: '', from: '', to: '', page: '1' })
      setActivePlayerId(null)
      return
    }
    // Seed the date range to the intersection of competence ∩ união das
    // campanhas escolhidas, so the user lands on something sensible without
    // having to fiddle.
    const selected = campaigns.filter(c => ids.includes(c.id))
    const def = defaultRangeForCampaigns(competence, selected)
    setFilters({ campaigns: ids.join(','), from: def.from, to: def.to, page: '1' })
    setActivePlayerId(null)
  }
  function handleFromChange(v) {
    setFilters({ from: v, page: '1' })
    setActivePlayerId(null)
  }
  function handleToChange(v) {
    setFilters({ to: v, page: '1' })
    setActivePlayerId(null)
  }
  function handleRangeChange({ from: f, to: t }) {
    setFilters({ from: f, to: t, page: '1' })
    setActivePlayerId(null)
  }
  function handleQChange(v) {
    setFilters({ q: v || '', page: '1' })
    setActivePlayerId(null)
  }
  function handlePageChange(p) {
    setFilters({ page: String(p) })
    setActivePlayerId(null)
    const list = document.querySelector('.airtime-list')
    if (list) list.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  // Step state: 1=sem cliente, 2=sem competência, 3=sem campanha, 4=pronto.
  const hasCampaigns = campaignIds.length > 0
  const step = !clientId ? 1 : !competence ? 2 : !hasCampaigns ? 3 : 4
  const showList = step === 4 && !invalidRange
  const isLoadingData = showList && (loadingList || isFetching)

  // Campaign-count hint shown on the "no-campaign" empty state.
  const campaignsInCompetence = useMemo(() => {
    if (!competence || !clientId) return 0
    const { start, end } = monthToRange(competence)
    return campaigns.filter(c => {
      if (c.client_id !== clientId) return false
      if (c.status === 'cancelada') return false
      if (!c.start_date || !c.end_date) return false
      const cs = parseLocalDate(c.start_date)
      const ce = parseLocalDate(c.end_date)
      return cs <= end && ce >= start
    }).length
  }, [campaigns, competence, clientId])

  function focusClientSelect() {
    document.getElementById('airtime-client')?.focus()
  }
  function focusMonthInput() {
    const el = document.getElementById('airtime-month')
    if (!el) return
    el.focus()
    if (typeof el.showPicker === 'function') {
      try { el.showPicker() } catch { /* ignore */ }
    }
  }
  function focusCampaignSelect() {
    document.getElementById('airtime-campaign')?.focus()
  }

  return (
    <div className="airtime-page">
      <div className="page-header">
        <h2>Relatório Data e Hora</h2>
      </div>

      <AirtimeFiltersBar
        campaigns={campaigns}
        clients={clients}
        clientId={clientId}
        canPickClient={canPickClient}
        competence={competence}
        onCompetenceChange={handleCompetenceChange}
        campaignIds={campaignIds}
        from={from}
        to={to}
        q={q}
        onClientChange={handleClientChange}
        onCampaignsChange={handleCampaignsChange}
        onFromChange={handleFromChange}
        onToChange={handleToChange}
        onRangeChange={handleRangeChange}
        onQChange={handleQChange}
      />

      {invalidRange && (
        <div className="airtime-error-card">
          <p>Intervalo inválido. A data inicial precisa ser anterior ou igual à final.</p>
          <button
            type="button"
            className="airtime-error-cta"
            onClick={() => setFilters({ from: daysAgoISO(7), to: todayISO() })}
          >Resetar para últimos 7 dias</button>
        </div>
      )}

      {step === 1 ? (
        <AirtimeGhostPreview
          step={1}
          icon="campaign"
          title="Comece pelo cliente"
          description="Escolha o cliente no filtro acima. As campanhas dele ficam disponíveis logo em seguida."
          ctaLabel="Escolher cliente"
          onCta={focusClientSelect}
        />
      ) : step === 2 ? (
        <AirtimeGhostPreview
          step={2}
          icon="calendar"
          title="Escolha a competência"
          description="Escolha o mês de referência. As campanhas que cruzam esse período ficam disponíveis logo em seguida."
          ctaLabel="Escolher competência"
          onCta={focusMonthInput}
        />
      ) : step === 3 ? (
        <AirtimeGhostPreview
          step={3}
          icon="campaign"
          title={`Escolha as campanhas de ${monthLabel(competence)}`}
          description={
            campaignsInCompetence === 0
              ? `Nenhuma campanha vigente em ${monthLabel(competence)}. Troque a competência ou cadastre uma nova campanha.`
              : `${campaignsInCompetence === 1 ? '1 campanha vigente' : `${campaignsInCompetence} campanhas vigentes`} nesse mês. Pra ver as veiculações, selecione uma ou mais.`
          }
          ctaLabel={campaignsInCompetence > 0 ? 'Abrir lista de campanhas' : null}
          onCta={campaignsInCompetence > 0 ? focusCampaignSelect : null}
        />
      ) : invalidRange ? null : (
        <>
          <div className="airtime-list">
            {isLoadingData && detections.length === 0 ? (
              <SkeletonList />
            ) : detections.length === 0 ? (
              <AirtimeGhostPreview
                step={4}
                icon="search"
                accent="mute"
                title="Nenhuma veiculação no período"
                description={`Não encontramos veiculações entre ${fmtRangeLabel(from, to)}.`}
                ctaLabel="Ampliar para últimos 30 dias"
                onCta={() => setFilters({ from: daysAgoISO(30), to: todayISO(), page: '1' })}
              />
            ) : (
              <>
                {detections.map(d => (
                  <AirtimeDetectionRow
                    key={d.id}
                    detection={d}
                    pricingByKey={pricingByKey}
                    isPlaying={activePlayerId === d.id}
                    onPlayRequest={setActivePlayerId}
                    onPlayClose={() => setActivePlayerId(null)}
                    highlighted={highlightedMaterialId === d.commercial_id}
                    targetLabel={targetLabel}
                  />
                ))}
                <AirtimePaginator
                  page={page}
                  totalPages={totalPages}
                  total={total}
                  pageSize={10}
                  onChange={handlePageChange}
                />
              </>
            )}
          </div>

          {detections.length > 0 && (
            <div className="airtime-panel-wrap">
              <AirtimeMaterialPanel
                aggregate={aggResp}
                loading={loadingAgg}
                campaignCount={campaignIds.length}
                highlightedMaterialId={highlightedMaterialId}
                onHover={setHighlightedMaterialId}
                onLeave={() => setHighlightedMaterialId(null)}
              />
            </div>
          )}
        </>
      )}
    </div>
  )
}

// Skeleton mirror-exact: matches the single-line card geometry — play, date
// + time, station (logo + 2-line text), PMM pill, Custo pill, spacer,
// material info on the right, right colored stripe.
function SkeletonList() {
  return (
    <>
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="airtime-row airtime-row-skel" aria-hidden>
          <div className="airtime-skel-circle" style={{ width: 40, height: 40 }} />
          <div className="airtime-row-time-block">
            <div className="airtime-skel-line" style={{ width: 88, height: 14 }} />
            <div className="airtime-skel-line" style={{ width: 70, height: 14 }} />
          </div>
          <div className="airtime-row-station">
            <div className="airtime-skel-circle" style={{ width: 40, height: 40, borderRadius: 8 }} />
            <div className="airtime-row-station-text">
              <div className="airtime-skel-line" style={{ width: 150, height: 12 }} />
              <div className="airtime-skel-line" style={{ width: 90, height: 10, marginTop: 6 }} />
            </div>
          </div>
          <div className="airtime-skel-line" style={{ width: 72, height: 26, borderRadius: 9999 }} />
          <div className="airtime-skel-line" style={{ width: 84, height: 26, borderRadius: 9999 }} />
          <div />
          <div className="airtime-row-material">
            <div className="airtime-skel-line" style={{ width: 50, height: 9 }} />
            <div className="airtime-skel-line" style={{ width: 160, height: 12, marginTop: 5 }} />
            <div className="airtime-skel-line" style={{ width: 60, height: 9, marginTop: 5 }} />
          </div>
          <div className="airtime-skel-line" style={{ width: 5, height: '100%' }} />
        </div>
      ))}
    </>
  )
}
