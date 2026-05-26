// Pirâmide de classe social — SVG custom (não Recharts) pra ter a forma
// trapezoidal real. AB no topo (menor base), C no meio (maior), DE embaixo.
// Cada faixa é um trapézio centralizado horizontalmente, com largura
// proporcional aos impactos da camada.

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE (do mais saturado pro mais claro)
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null

  const layers = [
    { name: 'AB', value: cp.ab, color: COLORS[0] },
    { name: 'C',  value: cp.c,  color: COLORS[1] },
    { name: 'DE', value: cp.de, color: COLORS[2] },
  ]
  const total = layers.reduce((s, l) => s + l.value, 0)
  const max = Math.max(...layers.map(l => l.value), 1)

  // Geometria: SVG viewbox 100×100; cada camada ocupa 1/3 da altura.
  // largura da camada = (value / max) * 90 (margem 5% nas laterais).
  const layerH = 28
  const padY = 4
  const widthFor = v => (v / max) * 90

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <svg viewBox="0 0 100 100" preserveAspectRatio="xMidYMid meet" className="in-pyramid-svg">
          {layers.map((l, i) => {
            const y = padY + i * layerH
            const w = widthFor(l.value)
            // Trapézio: top width afunila pra cima (mais estreito), bottom mais largo.
            // Pra parecer pirâmide, AB (topo) deve ter top mais estreito que C, etc.
            const next = layers[i + 1]
            const wNext = next ? widthFor(next.value) : w
            const topW = i === 0 ? w * 0.35 : w  // AB tem topo ainda mais estreito (vértice)
            const bottomW = wNext > w ? wNext : w
            const xTop = (100 - topW) / 2
            const xBot = (100 - bottomW) / 2
            const points = [
              `${xTop},${y}`,
              `${xTop + topW},${y}`,
              `${xBot + bottomW},${y + layerH - 2}`,
              `${xBot},${y + layerH - 2}`,
            ].join(' ')
            return (
              <g key={l.name}>
                <polygon points={points} fill={l.color} />
                <text
                  x="50"
                  y={y + layerH / 2 + 1}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  className="in-pyramid-label"
                >
                  <tspan className="in-pyramid-label-name">{l.name}</tspan>
                  <tspan dx="6" className="in-pyramid-label-value">{fmtCompact.format(l.value)}</tspan>
                </text>
              </g>
            )
          })}
        </svg>
        <div className="in-pyramid-legend">
          {layers.map(l => (
            <div key={l.name} className="in-pyramid-legend-row">
              <span className="in-pyramid-dot" style={{ background: l.color }} />
              <span className="in-pyramid-name">Classe {l.name}</span>
              <span className="in-pyramid-pct">{fmtPct(l.value, total)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
