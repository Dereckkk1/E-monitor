import { IconChartBars, IconMoney, IconGift } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

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
        <div className="in-card-value">{fmtCompact.format(k.impactos)}</div>
        <div className="in-card-sub">
          {fmtBR.format(k.veiculacoes_total)} veiculações · {k.stations_with_pmm} de {k.stations_count} emissoras com perfil
        </div>
      </div>

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><IconMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.cpm)}</div>
        <div className="in-card-sub">por mil impactos · base = investido executado</div>
      </div>

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><IconGift /></span>
          <span className="in-card-label">Bonificação</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.bonificacao.valor)}</div>
        <div className="in-card-sub">{fmtBR.format(k.bonificacao.count)} inserções extras</div>
      </div>
    </>
  )
}
