import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  useCampaigns, useClients, useDetectionsPaged, useMaterialAggregate, useCampaignPricing,
  exportDetectionsCsv,
} from '../api/hooks'
import AirtimeFiltersBar from '../components/AirtimeFiltersBar'
import AirtimeDetectionRow from '../components/AirtimeDetectionRow'
import AirtimeMaterialPanel from '../components/AirtimeMaterialPanel'
import AirtimePaginator from '../components/AirtimePaginator'
import AirtimeGhostPreview from '../components/AirtimeGhostPreview'
import './AirtimeReportPage.css'

function todayISO() { return new Date().toISOString().slice(0, 10) }
function daysAgoISO(n) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}

// Convert YYYY-MM-DD → full RFC3339 in São Paulo local time (UTC-3) so the
// backend WHERE clause aligns with how daily_play_summary buckets detections
// (same pattern as the existing /detections page).
function isoToRFC3339Start(iso) {
  return new Date(`${iso}T00:00:00.000-03:00`).toISOString()
}
function isoToRFC3339End(iso) {
  return new Date(`${iso}T23:59:59.999-03:00`).toISOString()
}

function fmtRangeLabel(from, to) {
  return `${from.split('-').reverse().join('/')} e ${to.split('-').reverse().join('/')}`
}

export default function AirtimeReportPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { data: campaigns = [] } = useCampaigns()
  const { data: clients = [] } = useClients()

  // URL é fonte de verdade — usuário consegue compartilhar o link e estado.
  const campaignId = searchParams.get('campaign_id') ?? ''
  const from = searchParams.get('from') ?? daysAgoISO(7)
  const to   = searchParams.get('to')   ?? todayISO()
  const q    = searchParams.get('q')    ?? ''
  const page = parseInt(searchParams.get('page') ?? '1', 10) || 1

  function setFilters(patch) {
    const next = new URLSearchParams(searchParams)
    Object.entries(patch).forEach(([k, v]) => {
      if (v === '' || v == null) next.delete(k)
      else next.set(k, String(v))
    })
    setSearchParams(next, { replace: true })
  }

  // Hidrata as datas no primeiro mount (URL sem from/to). replace evita
  // poluir o histórico do browser.
  useEffect(() => {
    if (!searchParams.get('from') || !searchParams.get('to')) {
      setFilters({ from: daysAgoISO(7), to: todayISO() })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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
    campaignId: campaignId || null,
    from: fromRFC,
    to: toRFC,
    q,
    page,
    pageSize: 10,
  })
  const { data: aggResp, isLoading: loadingAgg } = useMaterialAggregate({
    campaignId: campaignId || null,
    from: fromRFC,
    to: toRFC,
    q,
  })
  const { data: pricingList = [] } = useCampaignPricing(campaignId || null)
  const pricingByStation = useMemo(() => {
    const m = {}
    for (const p of pricingList) m[p.station_id] = p
    return m
  }, [pricingList])

  const detections = detResp?.data ?? []
  const total = detResp?.total ?? 0
  const totalPages = detResp?.total_pages ?? 1

  // Apenas um player ativo na página por vez.
  const [activePlayerId, setActivePlayerId] = useState(null)
  // Cross-highlight: passa a flutuar quando o usuário hovera o painel direito.
  const [highlightedMaterialId, setHighlightedMaterialId] = useState(null)

  // Export
  const [exporting, setExporting] = useState(false)
  async function handleExport() {
    if (!campaignId || invalidRange) return
    setExporting(true)
    try {
      await exportDetectionsCsv({ campaignId, from: fromRFC, to: toRFC, q })
    } catch {
      window.alert('Não foi possível gerar o CSV. Tente novamente.')
    } finally {
      setExporting(false)
    }
  }

  function handleCampaignChange(id) {
    setFilters({ campaign_id: id || '', page: '1' })
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
  function handleQChange(v) {
    setFilters({ q: v || '', page: '1' })
    setActivePlayerId(null)
  }
  function handlePageChange(p) {
    setFilters({ page: String(p) })
    setActivePlayerId(null)
    // Rola o container da lista pro topo (e não a janela inteira) pra preservar
    // o header sticky no campo de visão.
    const list = document.querySelector('.airtime-list')
    if (list) list.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  const showList = !!campaignId && !invalidRange
  const isLoadingData = showList && (loadingList || isFetching)

  return (
    <div className="airtime-page">
      <div className="page-header">
        <h2>Relatório Data e Hora</h2>
      </div>

      <AirtimeFiltersBar
        campaigns={campaigns}
        clients={clients}
        campaignId={campaignId}
        from={from}
        to={to}
        q={q}
        onCampaignChange={handleCampaignChange}
        onFromChange={handleFromChange}
        onToChange={handleToChange}
        onQChange={handleQChange}
        onExportClick={handleExport}
        exporting={exporting}
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

      {!campaignId ? (
        <AirtimeGhostPreview
          title="Selecione uma campanha"
          description="Escolha uma campanha no filtro acima para ver as veiculações detectadas."
        />
      ) : invalidRange ? null : (
        <>
          <div className="airtime-list">
            {isLoadingData && detections.length === 0 ? (
              <SkeletonList />
            ) : detections.length === 0 ? (
              <AirtimeGhostPreview
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
                    pricingByStation={pricingByStation}
                    isPlaying={activePlayerId === d.id}
                    onPlayRequest={setActivePlayerId}
                    onPlayClose={() => setActivePlayerId(null)}
                    highlighted={highlightedMaterialId === d.commercial_id}
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

// Skeleton mirror-exact: matches the new card geometry — time-block,
// station block, play, chevron on top row + meta band below.
function SkeletonList() {
  return (
    <>
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="airtime-row airtime-row-skel" aria-hidden>
          <div className="airtime-row-stripe airtime-skel-stripe" />
          <div className="airtime-row-time-block">
            <div className="airtime-skel-line" style={{ width: 78, height: 14 }} />
            <div className="airtime-skel-line" style={{ width: 60, height: 9, marginTop: 6 }} />
          </div>
          <div className="airtime-row-station">
            <div className="airtime-skel-circle" style={{ width: 44, height: 44, borderRadius: 8 }} />
            <div className="airtime-row-station-text">
              <div className="airtime-skel-line" style={{ width: 130, height: 12 }} />
              <div className="airtime-skel-line" style={{ width: 90, height: 9, marginTop: 6 }} />
            </div>
          </div>
          <div className="airtime-skel-circle" style={{ width: 40, height: 40 }} />
          <div />
          <div className="airtime-row-meta">
            <div className="airtime-skel-line" style={{ width: 220, height: 11 }} />
            <div className="airtime-skel-line" style={{ width: 140, height: 11 }} />
          </div>
        </div>
      ))}
    </>
  )
}
