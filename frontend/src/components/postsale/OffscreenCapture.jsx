// OffscreenCapture.jsx — renderiza o mapa e os indicadores FORA da tela e
// avisa quando estão prontos pra virar PNG.
//
// Por que não reusar /live-map e /insights inteiras: aquelas páginas trazem
// barra de filtros, toolbar e navegação, que não devem entrar na foto. Aqui
// montamos só o miolo, a partir dos MESMOS componentes e dos MESMOS endpoints
// que elas usam — BrazilMap e os gráficos de components/insights recebem
// `data` por prop, então não há refatoração nem segunda fonte de verdade.
//
// A espera é pelo estado de sucesso do React Query, nunca por setTimeout puro:
// capturar antes do dado chegar produziria um "mapa" de skeleton no zip do
// cliente. O pequeno atraso extra depois do sucesso existe só pro Recharts
// terminar o layout dos gráficos.
import { useEffect, useRef } from 'react'

import { useInsights, useLiveMap } from '../../api/hooks'
import BrazilMap from '../BrazilMap'
import KpiCards from '../insights/KpiCards'
import InvestmentToggleCard from '../insights/InvestmentToggleCard'
import GenderCard from '../insights/GenderCard'
import ClassPyramidChart from '../insights/ClassPyramidChart'
import AgeRangeChart from '../insights/AgeRangeChart'
import BroadcastShareChart from '../insights/BroadcastShareChart'
import DailySummaryChart from '../insights/DailySummaryChart'
// A foto usa a grade e os cards do /insights, então o CSS de lá é dependência
// REAL deste componente — não algo que "por sorte" já está no bundle porque
// alguma outra página importou. Todo seletor do arquivo é prefixado .in-*, não
// vaza no wizard. Import idempotente: o Vite dedupa o módulo.
import '../../pages/InsightsPage.css'

// Tempo pro Recharts assentar depois que o dado chegou.
const LAYOUT_SETTLE_MS = 400

export default function OffscreenCapture({ job, onReady, onError }) {
  const mapRef = useRef(null)
  const insightsRef = useRef(null)
  const firedFor = useRef(null)

  const campaignId = job?.campaignId ?? null
  // includeTerminal: o pós-venda fecha campanha que já acabou, inclusive
  // cancelada — e pra /live-map campanha cancelada é 404 por definição. Sem
  // isto, fechar uma campanha cancelada aborta o envio inteiro.
  const map = useLiveMap(campaignId, { includeTerminal: true })
  const insights = useInsights({
    clientId: job?.clientId,
    campaignIds: campaignId ? [campaignId] : [],
    from: job?.from,
    to: job?.to,
  })

  const mapReady = map.isSuccess
  const insReady = insights.isSuccess
  // Qual das duas falhou e com que status: sem isso o admin vê só "não foi
  // possível" e o diagnóstico vira caça ao console.
  const failure = map.isError
    ? { which: 'o mapa', status: map.error?.response?.status }
    : insights.isError
      ? { which: 'os indicadores', status: insights.error?.response?.status }
      : null
  const failedWhich = failure?.which ?? null
  const failedStatus = failure?.status ?? null

  useEffect(() => {
    if (!job) return
    if (firedFor.current === campaignId) return

    if (failedWhich) {
      firedFor.current = campaignId
      onError?.(new Error(
        `Não foi possível carregar ${failedWhich} de "${job.campaignName}"` +
        `${failedStatus ? ` (erro ${failedStatus})` : ''}. Nada foi enviado.`,
      ))
      return
    }
    if (!mapReady || !insReady) return

    firedFor.current = campaignId
    const t = setTimeout(() => {
      onReady?.({ mapNode: mapRef.current, insightsNode: insightsRef.current })
    }, LAYOUT_SETTLE_MS)
    return () => clearTimeout(t)
  }, [job, campaignId, mapReady, insReady, failedWhich, failedStatus, onReady, onError])

  if (!job) return null

  const stations = map.data?.stations ?? []
  const data = insights.data

  return (
    <div className="psa-offscreen" aria-hidden="true">
      <div ref={mapRef} className="psc-map-frame">
        <div className="psc-frame-title">Emissoras monitoradas</div>
        <BrazilMap stations={stations} />
      </div>

      <div ref={insightsRef} className="psc-insights-frame">
        <div className="psc-frame-title">{job.campaignName}</div>
        {data && (
          <>
            {/* Mesmas faixas, mesma ordem e MESMAS classes do InsightsPage —
                inclusive as modificadoras de consolidado e de target. A foto é
                o /insights, então a grade tem que ser a de lá: qualquer grade
                própria aqui diverge da tela na primeira mudança do dashboard. */}
            <div className={`in-row in-row--cards${data.consolidated ? ' in-row--cards--4' : ''}${(data?.kpis?.stations_with_target ?? 0) > 0 ? ' in-row--cards--target' : ''}`}>
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
        )}
      </div>
    </div>
  )
}
