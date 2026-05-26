// Pirâmide de classe social. Geometria FIXA (não depende da quantidade) —
// 3 camadas de altura igual: AB no topo afunilando até vértice, C trapézio
// no meio, DE trapézio largo na base. Os valores das camadas viram apenas
// labels — a forma da pirâmide é sempre a mesma.

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

// Geometria fixa em coordenadas SVG (viewBox 100×100):
// - 3 camadas de altura 27 (com gap 1.5 entre elas)
// - AB: triângulo (vértice no topo, base em 30% de largura)
// - C: trapézio (topo 30%, base 64%)
// - DE: trapézio (topo 64%, base 96%)
const LAYOUT = [
  { name: 'AB', y: 5,    h: 27, topPct: 0,    botPct: 30 },
  { name: 'C',  y: 33.5, h: 27, topPct: 30,   botPct: 64 },
  { name: 'DE', y: 62,   h: 27, topPct: 64,   botPct: 96 },
]

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null

  const layers = [
    { ...LAYOUT[0], value: cp.ab, color: COLORS[0], label: 'A/B' },
    { ...LAYOUT[1], value: cp.c,  color: COLORS[1], label: 'C' },
    { ...LAYOUT[2], value: cp.de, color: COLORS[2], label: 'D/E' },
  ]
  const total = layers.reduce((s, l) => s + l.value, 0)

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <svg viewBox="0 0 100 100" preserveAspectRatio="xMidYMid meet" className="in-pyramid-svg">
          {layers.map(l => {
            const xTop = (100 - l.topPct) / 2
            const xBot = (100 - l.botPct) / 2
            const points = l.topPct === 0
              ? `50,${l.y} ${xBot},${l.y + l.h} ${xBot + l.botPct},${l.y + l.h}`
              : [
                  `${xTop},${l.y}`,
                  `${xTop + l.topPct},${l.y}`,
                  `${xBot + l.botPct},${l.y + l.h}`,
                  `${xBot},${l.y + l.h}`,
                ].join(' ')
            const labelY = l.topPct === 0 ? l.y + l.h * 0.7 : l.y + l.h / 2 + 1
            return (
              <g key={l.name}>
                <polygon points={points} fill={l.color} />
                <text
                  x="50"
                  y={labelY}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  className="in-pyramid-label"
                >
                  <tspan className="in-pyramid-label-name">{l.label}:</tspan>
                  <tspan dx="2" className="in-pyramid-label-value">{fmtBR.format(l.value)}</tspan>
                </text>
              </g>
            )
          })}
        </svg>
        <div className="in-pyramid-legend">
          {layers.map(l => (
            <div key={l.name} className="in-pyramid-legend-row">
              <span className="in-pyramid-dot" style={{ background: l.color }} />
              <span className="in-pyramid-name">Classe {l.label}</span>
              <span className="in-pyramid-pct">{fmtPct(l.value, total)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
