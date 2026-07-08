import { IconChartBars, IconMoney, IconGift } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

export default function KpiCards({ data }) {
  const k = data?.kpis
  if (!k) return null
  return (
    <>
      <div className="in-card" title="Impactos = soma de (detecções × PMM) por estação.">
        <div className="in-card-head">
          <span className="in-card-icon"><IconChartBars /></span>
          <span className="in-card-label">Impactos</span>
        </div>
        <div className="in-card-value in-card-value--num">{fmtBR.format(k.impactos)}</div>
      </div>

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><IconMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.cpm)}</div>
      </div>

      {/* Consolidado (estilo fornecedor): a Bonificação some — o card não é
          renderizado, e o grid de cards vira 4 colunas (ver InsightsPage). */}
      {!data?.consolidated && (
        <div className="in-card">
          <div className="in-card-head">
            <span className="in-card-icon"><IconGift /></span>
            <span className="in-card-label">Bonificação</span>
          </div>
          <div className="in-card-value">{fmtCurrency.format(k.bonificacao.valor)}</div>
        </div>
      )}
    </>
  )
}
