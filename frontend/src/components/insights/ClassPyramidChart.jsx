// Pirâmide de classe social — SVG manual com geometria fixa de pirâmide.
//
// Recharts Funnel não serve aqui: ele sempre afunila pra ponta no FIM
// (DE acabaria em triângulo). Pirâmide tem o ponto no TOPO (AB) e base
// larga embaixo (DE). Como é um shape simples e estático, SVG manual
// é mais leve que adicionar lib só pra isso.
//
// Layout:
//   - viewBox 240×160 (proporção horizontal pra dar espaço aos labels)
//   - AB: triângulo (vértice no topo)
//   - C:  trapézio meio
//   - DE: trapézio base
//   - labels em 2 linhas centralizados no centroide de cada shape

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

// Geometria — alturas iguais (cada camada 48 unidades). Larguras crescem
// progressivamente de 0 (vértice) até 220 (base).
//   AB:  vértice em x=120, base 60-180 (largura 120) — h=4..52
//   C:   topo 60-180, base 30-210 (largura 180)        — h=52..100
//   DE:  topo 30-210, base 10-230 (largura 220)        — h=100..152
const VB_W = 240
const VB_H = 160
const LAYERS = [
  { name: 'AB', color: COLORS[0], points: '120,4 180,52 60,52' },
  { name: 'C',  color: COLORS[1], points: '60,52 180,52 210,100 30,100' },
  { name: 'DE', color: COLORS[2], points: '30,100 210,100 230,152 10,152' },
]

// Centro do shape (X sempre 120, Y é o meio vertical de cada camada).
// Pro triângulo AB, descemos um pouco pra ficar onde o shape é mais largo.
const LABEL_POS = [
  { x: 120, y: 38 },  // AB — y=38 (~75% da camada, onde tem mais espaço)
  { x: 120, y: 76 },  // C  — meio da camada (h=52..100, centro=76)
  { x: 120, y: 126 }, // DE — meio da camada (h=100..152, centro=126)
]

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null

  const rows = [
    { ...LAYERS[0], ...LABEL_POS[0], label: 'A/B', value: cp.ab },
    { ...LAYERS[1], ...LABEL_POS[1], label: 'C',   value: cp.c },
    { ...LAYERS[2], ...LABEL_POS[2], label: 'D/E', value: cp.de },
  ]
  const total = rows.reduce((s, r) => s + r.value, 0)

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <svg
          viewBox={`0 0 ${VB_W} ${VB_H}`}
          preserveAspectRatio="xMidYMid meet"
          className="in-pyramid-svg"
        >
          {rows.map(r => (
            <g key={r.name}>
              <polygon
                points={r.points}
                fill={r.color}
                stroke="white"
                strokeWidth="1.2"
              />
              <text
                x={r.x}
                y={r.y - 4}
                textAnchor="middle"
                dominantBaseline="middle"
                className="in-pyramid-label-name"
              >
                Classe {r.label}
              </text>
              <text
                x={r.x}
                y={r.y + 7}
                textAnchor="middle"
                dominantBaseline="middle"
                className="in-pyramid-label-value"
              >
                {fmtBR.format(r.value)}
              </text>
            </g>
          ))}
        </svg>
        <div className="in-pyramid-legend">
          {rows.map(r => (
            <div key={r.name} className="in-pyramid-legend-row">
              <span className="in-pyramid-dot" style={{ background: r.color }} />
              <span className="in-pyramid-name">Classe {r.label}</span>
              <span className="in-pyramid-pct">{fmtPct(r.value, total)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
