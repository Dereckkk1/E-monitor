import CardValue from './CardValue'
import { IconWallet } from './icons'

const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

// Mantém o nome do arquivo (InvestmentToggleCard) pra não quebrar imports,
// mas o toggle foi removido — só mostra o valor executado.
export default function InvestmentToggleCard({ data }) {
  const inv = data?.kpis?.investido
  if (!inv) return null
  const title = data?.consolidated
    ? 'Pacote consolidado: valor total contratado (fixo — não varia com o período selecionado).'
    : 'Valor veiculado no período (por inserção: soma das veiculações × valor unitário).'
  return (
    <div className="in-card" title={title}>
      <div className="in-card-head">
        <span className="in-card-icon"><IconWallet /></span>
        <span className="in-card-label">Investido</span>
      </div>
      <CardValue>{fmtCurrency.format(inv.executado)}</CardValue>
    </div>
  )
}
