// Pirâmide de classe social. Geometria FIXA — 3 trapézios empilhados,
// AB no topo (mais estreito), C no meio, DE na base. A largura aumenta
// progressivamente pra dar volume "piramidal" sem sacrificar legibilidade
// dos labels.
//
// Layout: pirâmide no topo (largura toda do card), legenda horizontal
// abaixo dela. Labels em 2 linhas (Classe XX em cima, valor embaixo)
// pra caber dentro do shape mesmo quando o número é longo.

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

// viewBox 200×100 (proporção 2:1 horizontal) — dá mais espaço lateral
// pros labels caberem dentro dos trapézios.
const LAYOUT = [
  { name: 'AB', y: 4,    h: 28, topPct: 22, botPct: 52 },
  { name: 'C',  y: 34,   h: 28, topPct: 52, botPct: 78 },
  { name: 'DE', y: 64,   h: 28, topPct: 78, botPct: 99 },
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
  const VB_W = 200

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <svg viewBox={`0 0 ${VB_W} 100`} preserveAspectRatio="xMidYMid meet" className="in-pyramid-svg">
          {layers.map(l => {
            const topW = (l.topPct / 100) * VB_W
            const botW = (l.botPct / 100) * VB_W
            const xTop = (VB_W - topW) / 2
            const xBot = (VB_W - botW) / 2
            const points = [
              `${xTop},${l.y}`,
              `${xTop + topW},${l.y}`,
              `${xBot + botW},${l.y + l.h}`,
              `${xBot},${l.y + l.h}`,
            ].join(' ')
            const cx = VB_W / 2
            const cy = l.y + l.h / 2
            return (
              <g key={l.name}>
                <polygon points={points} fill={l.color} />
                <text
                  x={cx}
                  y={cy - 2}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  className="in-pyramid-label-name"
                >
                  Classe {l.label}
                </text>
                <text
                  x={cx}
                  y={cy + 6}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  className="in-pyramid-label-value"
                >
                  {fmtBR.format(l.value)}
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
