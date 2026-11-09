import { useMemo, useRef, useState } from 'react'
import FiltersBar from '../components/insights/FiltersBar'
import KpiCards from '../components/insights/KpiCards'
import { kpiColumns } from '../utils/insightsCards'
import InvestmentToggleCard from '../components/insights/InvestmentToggleCard'
import GenderCard from '../components/insights/GenderCard'
import ClassPyramidChart from '../components/insights/ClassPyramidChart'
import AgeRangeChart from '../components/insights/AgeRangeChart'
import BroadcastShareChart from '../components/insights/BroadcastShareChart'
import DailySummaryChart from '../components/insights/DailySummaryChart'
import EmptyTutorial from '../components/insights/EmptyTutorial'
import SkeletonLoader from '../components/insights/SkeletonLoader'
import { IconImage, IconFilePdf } from '../components/insights/icons'
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
  const { isAdmin, user, clientIds } = useAuth()
  // O /insights é por cliente: admin escolhe, e a agência (carteira com 2+)
  // também precisa escolher — o backend recusa a chamada sem client_id. Só o
  // Cliente de um cliente só já entra com ele preenchido, como sempre foi.
  const canPickClient = isAdmin || clientIds.length > 1
  const [filters, setFilters] = useState({
    clientId: canPickClient ? null : (user?.client_id ?? null),
    campaignIds: [],
    from: firstOfMonthISO(),
    to: lastOfMonthISO(),
    stationIds: [],
    materialIds: [],
  })

  const { data, isPending, error } = useInsights({
    clientId: filters.clientId,
    campaignIds: filters.campaignIds,
    from: filters.from,
    to: filters.to,
    stationIds: filters.stationIds,
    materialIds: filters.materialIds,
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
      // "Você ainda não tem campanhas" só faz sentido pra quem enxerga um
      // cliente só: quem escolheu um cliente da carteira precisa é escolher a
      // campanha, igual ao admin.
      return canPickClient ? 'no-campaigns' : 'client-no-campaigns'
    }
    if (data && data.kpis && data.kpis.veiculacoes_total === 0) return 'no-data'
    return null
  }, [filters.clientId, filters.campaignIds, data, canPickClient])

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
        {/* Exportar não é filtro e não deve aparecer dentro do próprio
            print — por isso os botões ficam no header, fora do .in-capture. */}
        <div className="in-header-actions">
          <button
            type="button"
            className="in-btn-outline"
            onClick={handleExportImage}
            disabled={!data || !!emptyVariant}
            title="Baixar o dashboard como imagem (PNG)"
          >
            <IconImage />
            Imagem
          </button>
          <button
            type="button"
            className="in-btn-outline"
            onClick={handleExportPDF}
            disabled={!data || !!emptyVariant}
            title="Baixar o dashboard como PDF"
          >
            <IconFilePdf />
            PDF
          </button>
        </div>
      </header>

      {/* dashboardRef envolve filtros + body pra que o print/PDF inclua
          o contexto dos filtros aplicados. */}
      <div ref={dashboardRef} className="in-capture">
      <FiltersBar value={filters} onChange={setFilters} />

      <div className="in-body">
        {error && <div className="in-error">Erro ao carregar: {String(error.message || error)}</div>}

        {/* O filtro de material recorta com exatidão o que sai da tocada
            (impactos, veiculações, demografia), mas o contrato não tem
            dimensão de material — é precificado por campanha × emissora ×
            TIPO × dia. Os valores em R$ e o "programado" do gráfico viram
            rateio pela participação do material nas veiculações, e isso
            precisa estar dito na tela: são os números que viram cobrança. */}
        {data?.material_prorated && (
          <div className="in-prorated-note">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                 strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="9" />
              <path d="M12 8h.01M11 12h1v4h1" />
            </svg>
            <div>
              <strong>Valores rateados por material.</strong>{' '}
              <span>
                Impactos, veiculações e perfil de audiência são exatos.
                Investido, bonificação, CPM e o programado do gráfico são
                rateio: o contrato é fechado por tipo de inserção, não por
                material, então cada valor entra na proporção das veiculações
                deste material
                {typeof data.material_share === 'number' && data.material_share > 0
                  ? ` (${(data.material_share * 100).toFixed(1)}% da seleção)`
                  : ''}.
              </span>
            </div>
          </div>
        )}

        {emptyVariant ? (
          <EmptyTutorial variant={emptyVariant} />
        ) : isPending && !data ? (
          <SkeletonLoader />
        ) : data ? (
          <>
            {/* O número de cards da linha varia com os dados (target liga
                dois; bonificação, um; Investido some sem payload), então quem
                decide a divisão é o kpiColumns: fileiras iguais com teto de 5,
                nunca uma fileira com buraco. As classes modificadoras de antes
                (--cards--4 / --cards--target) sumiram junto com a duplicação
                da regra que elas exigiam. */}
            <div className="in-row in-row--cards" style={{ '--in-cards-cols': kpiColumns(data) }}>
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
