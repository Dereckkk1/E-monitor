import { IconChartBars, IconMoney, IconGift } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

export default function KpiCards({ data }) {
  const k = data?.kpis
  if (!k) return null
  // Bloco "no target" só aparece quando o cliente tem cadastro de público-alvo
  // em pelo menos 1 emissora. Sem cadastro (stations_with_target ausente ou
  // 0 — inclui payload de API antiga em cache), a tela fica idêntica à de
  // antes da feature.
  const hasTarget = (k.stations_with_target ?? 0) > 0
  return (
    <>
      <div className="in-card" title="Impactos = soma de (detecções × PMM) por estação.">
        <div className="in-card-head">
          <span className="in-card-icon"><IconChartBars /></span>
          <span className="in-card-label">Impactos</span>
        </div>
        <div className="in-card-value in-card-value--num">{fmtBR.format(k.impactos)}</div>
      </div>

      {hasTarget && (
        <div className="in-card" title={`Impactos no target = soma de (detecções × PMM no target do cliente) por estação. ${k.stations_with_target} de ${k.stations_count} emissoras com target cadastrado.`}>
          <div className="in-card-head">
            <span className="in-card-icon"><IconChartBars /></span>
            <span className="in-card-label">Impactos no target</span>
          </div>
          <div className="in-card-value in-card-value--num">{fmtBR.format(k.impactos_target ?? 0)}</div>
          <div className="in-card-sub">{k.stations_with_target} de {k.stations_count} emissoras</div>
        </div>
      )}

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><IconMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.cpm)}</div>
      </div>

      {hasTarget && (
        <div className="in-card" title="CPM no target = investido executado ÷ impactos no target × 1000. Sempre dinâmico, mesmo em campanha com CPM fixo — o CPM fixo é contratado sobre a audiência total, não sobre o recorte de público-alvo.">
          <div className="in-card-head">
            <span className="in-card-icon"><IconMoney /></span>
            <span className="in-card-label">CPM no target</span>
          </div>
          <div className="in-card-value">{fmtCurrency.format(k.cpm_target ?? 0)}</div>
        </div>
      )}

      {/* Consolidado (estilo fornecedor): a Bonificação some — o card não é
          renderizado. O grid (.in-row--cards) se auto-ajusta ao número de
          cards (ver InsightsPage.css), então não precisa contar colunas aqui. */}
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
