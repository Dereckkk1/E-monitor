import { formatPeriod } from './PeriodLabel.js'

// Chip textual discreto que descreve a janela [from, to] atualmente aplicada.
// Passe acumuladoFrom (a data-âncora "desde sempre") pra que o modo acumulado
// vire "acumulado até <dia>". Renderiza nada sem from/to.
export default function PeriodLabel({ from, to, acumuladoFrom = null }) {
  if (!from || !to) return null
  return <span className="period-label">{formatPeriod(from, to, acumuladoFrom)}</span>
}
