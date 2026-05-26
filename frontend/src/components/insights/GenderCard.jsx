import { IconPeople } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')

export default function GenderCard({ data }) {
  const g = data?.kpis?.gender
  if (!g) return null
  const total = g.m + g.f
  const mPct = total > 0 ? (g.m / total) * 100 : 0
  const fPct = total > 0 ? (g.f / total) * 100 : 0

  return (
    <div className="in-card">
      <div className="in-card-head">
        <span className="in-card-icon"><IconPeople /></span>
        <span className="in-card-label">Gênero (M / F)</span>
      </div>
      <div className="in-bar-stacked">
        <div className="in-bar-stacked-fill in-bar-stacked-fill--m" style={{ width: `${mPct}%` }} />
        <div className="in-bar-stacked-fill in-bar-stacked-fill--f" style={{ width: `${fPct}%` }} />
      </div>
      <div className="in-bar-legend">
        <span><strong>M:</strong> {mPct.toFixed(0)}% · {fmtBR.format(g.m)}</span>
        <span><strong>F:</strong> {fPct.toFixed(0)}% · {fmtBR.format(g.f)}</span>
      </div>
    </div>
  )
}
