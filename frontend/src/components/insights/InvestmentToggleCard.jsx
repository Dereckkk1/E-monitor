import { IconWallet } from './icons'

const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

// Mantém o nome do arquivo (InvestmentToggleCard) pra não quebrar imports,
// mas o toggle foi removido — só mostra o valor executado.
export default function InvestmentToggleCard({ data }) {
  const inv = data?.kpis?.investido
  if (!inv) return null
  return (
    <div
      className="in-card"
      title="Valor veiculado no período. Em pacote consolidado é proporcional ao que foi entregue (fração do contrato), limitado a 100% — o excedente entra em Bonificação. Campanha em andamento mostra o entregue até agora."
    >
      <div className="in-card-head">
        <span className="in-card-icon"><IconWallet /></span>
        <span className="in-card-label">Investido</span>
      </div>
      <div className="in-card-value">{fmtCurrency.format(inv.executado)}</div>
    </div>
  )
}
