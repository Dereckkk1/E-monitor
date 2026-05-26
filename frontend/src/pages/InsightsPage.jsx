import { useMemo, useRef, useState } from 'react'
import FiltersBar from '../components/insights/FiltersBar'
import KpiCards from '../components/insights/KpiCards'
import InvestmentToggleCard from '../components/insights/InvestmentToggleCard'
import GenderCard from '../components/insights/GenderCard'
import ClassPyramidChart from '../components/insights/ClassPyramidChart'
import AgeRangeChart from '../components/insights/AgeRangeChart'
import BroadcastShareChart from '../components/insights/BroadcastShareChart'
import DailySummaryChart from '../components/insights/DailySummaryChart'
import EmptyTutorial from '../components/insights/EmptyTutorial'
import SkeletonLoader from '../components/insights/SkeletonLoader'
import { useInsights } from '../api/hooks'
import { useAuth } from '../contexts/AuthContext'
import { exportInsightsPNG, exportInsightsPDF } from '../utils/exportInsights'
import './InsightsPage.css'

function firstOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), 1)).toISOString().slice(0, 10)
}
function lastOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth() + 1, 0)).toISOString().slice(0, 10)
}

export default function InsightsPage() {
  const { isAdmin, user } = useAuth()
  const [filters, setFilters] = useState({
    clientId: isAdmin ? null : (user?.client_id ?? null),
    campaignIds: [],
    from: firstOfMonthISO(),
    to: lastOfMonthISO(),
    stationIds: [],
  })

  const { data, isPending, error } = useInsights({
    clientId: filters.clientId,
    campaignIds: filters.campaignIds,
    from: filters.from,
    to: filters.to,
    stationIds: filters.stationIds,
  })

  const dashboardRef = useRef(null)

  const clientName = useMemo(() => {
    if (!isAdmin) return user?.client_name || user?.email || 'Sua conta'
    // No payload do backend não vem client_name explícito; resolver client_name
    // pelo seletor de cliente acontece dentro do FiltersBar (hook useClients).
    // Aqui, deixamos vazio — o export inclui só nome se o caller passar.
    return ''
  }, [isAdmin, user])

  const emptyVariant = useMemo(() => {
    if (!filters.clientId) return 'no-client'
    if (!filters.campaignIds || filters.campaignIds.length === 0) {
      return isAdmin ? 'no-campaigns' : 'client-no-campaigns'
    }
    if (data && data.kpis && data.kpis.veiculacoes_total === 0) return 'no-data'
    return null
  }, [filters.clientId, filters.campaignIds, data, isAdmin])

  const handleExportImage = async () => {
    if (!dashboardRef.current) return
    await exportInsightsPNG(dashboardRef.current, { clientName, period: data?.period })
  }
  const handleExportPDF = async () => {
    if (!dashboardRef.current) return
    await exportInsightsPDF(dashboardRef.current, {
      clientName,
      period: data?.period,
      campaigns: data?.campaigns,
    })
  }

  return (
    <div className="in-page">
      <header className="in-header">
        <h1 className="in-title">Dashboard de Veiculação</h1>
      </header>

      {/* dashboardRef envolve filtros + body pra que o print/PDF inclua
          o contexto dos filtros aplicados. */}
      <div ref={dashboardRef} className="in-capture">
      <FiltersBar
        value={filters}
        onChange={setFilters}
        onExportImage={handleExportImage}
        onExportPDF={handleExportPDF}
      />

      <div className="in-body">
        {error && <div className="in-error">Erro ao carregar: {String(error.message || error)}</div>}

        {emptyVariant ? (
          <EmptyTutorial variant={emptyVariant} />
        ) : isPending && !data ? (
          <SkeletonLoader />
        ) : data ? (
          <>
            <div className="in-row in-row--cards">
              <KpiCards data={data} />
              <InvestmentToggleCard data={data} />
              <GenderCard data={data} />
            </div>
            <div className="in-row in-row--charts">
              <ClassPyramidChart data={data} />
              <AgeRangeChart data={data} />
              <BroadcastShareChart data={data} />
            </div>
            <div className="in-row">
              <DailySummaryChart data={data} />
            </div>
          </>
        ) : null}
      </div>
      </div>

    </div>
  )
}
