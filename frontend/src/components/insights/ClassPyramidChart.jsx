// Pirâmide de classe social — triângulo PERFEITO (slope constante das
// laterais) dividido em 3 fatias horizontais cuja ALTURA é proporcional
// ao % de impactos daquela classe.
//
// Geometria:
//   - Triângulo total: apex em (120, 4), base de (10, 152) a (230, 152)
//   - Altura total = 148; base = 220
//   - Slope esquerda: x(y) = 120 - 110/148 * (y - 4)
//   - Slope direita:  x(y) = 120 + 110/148 * (y - 4)
//
// Para uma classe com % = p, a fatia ocupa p × 148 units de altura.
// Os limites verticais são calculados em sequência: AB começa no apex,
// C continua, DE fecha na base.

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

const VB_W = 240
const VB_H = 160
const APEX_X = 120
const APEX_Y = 4
const BASE_Y = 152
const BASE_HALF_WIDTH = 110 // de 10 a 230

// Retorna o x da lateral esquerda do triângulo para um dado y.
function leftX(y) {
  const t = (y - APEX_Y) / (BASE_Y - APEX_Y)
  return APEX_X - BASE_HALF_WIDTH * t
}
function rightX(y) {
  const t = (y - APEX_Y) / (BASE_Y - APEX_Y)
  return APEX_X + BASE_HALF_WIDTH * t
}

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null

  const total = cp.ab + cp.c + cp.de
  // Frações (somam 1.0). Quando total=0, default igualitário pra não
  // quebrar a renderização.
  const frac = total > 0
    ? [cp.ab / total, cp.c / total, cp.de / total]
    : [1 / 3, 1 / 3, 1 / 3]

  const H = BASE_Y - APEX_Y // 148
  const y0 = APEX_Y
  const y1 = APEX_Y + frac[0] * H
  const y2 = APEX_Y + (frac[0] + frac[1]) * H
  const y3 = BASE_Y

  // Vértices: AB triângulo, C e DE trapézios. Cada lado segue a slope
  // constante do triângulo total → pirâmide perfeitamente reta.
  const ab = `${APEX_X},${y0} ${rightX(y1)},${y1} ${leftX(y1)},${y1}`
  const c  = `${leftX(y1)},${y1} ${rightX(y1)},${y1} ${rightX(y2)},${y2} ${leftX(y2)},${y2}`
  const de = `${leftX(y2)},${y2} ${rightX(y2)},${y2} ${rightX(y3)},${y3} ${leftX(y3)},${y3}`

  const rows = [
    { name: 'A/B', value: cp.ab, color: COLORS[0], points: ab, yMid: (y0 + y1) / 2, yLow: y1 - 4 },
    { name: 'C',   value: cp.c,  color: COLORS[1], points: c,  yMid: (y1 + y2) / 2, yLow: null },
    { name: 'D/E', value: cp.de, color: COLORS[2], points: de, yMid: (y2 + y3) / 2, yLow: null },
  ]

  // Pro AB (triângulo, apex no topo) o label vai mais perto da BASE da
  // camada, onde tem mais espaço horizontal. Pras outras (trapézios) o
  // meio basta.
  const labelY = (r, isAB) => (isAB ? r.yMid + (r.yLow - r.yMid) * 0.4 : r.yMid)

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <svg
          viewBox={`0 0 ${VB_W} ${VB_H}`}
          preserveAspectRatio="xMidYMid meet"
          className="in-pyramid-svg"
        >
          {rows.map((r, i) => {
            const isAB = i === 0
            const ly = labelY(r, isAB)
            // Se a fatia tiver altura zero (0%), pula o label pra não
            // sobrepor com a próxima.
            const sliceH = i === 0 ? (y1 - y0) : i === 1 ? (y2 - y1) : (y3 - y2)
            const showLabel = sliceH > 8
            return (
              <g key={r.name}>
                <polygon
                  points={r.points}
                  fill={r.color}
                  stroke="white"
                  strokeWidth="1.2"
                />
                {showLabel && (
                  <>
                    <text
                      x={APEX_X}
                      y={ly - 4}
                      textAnchor="middle"
                      dominantBaseline="middle"
                      className="in-pyramid-label-name"
                    >
                      Classe {r.name}
                    </text>
                    <text
                      x={APEX_X}
                      y={ly + 6}
                      textAnchor="middle"
                      dominantBaseline="middle"
                      className="in-pyramid-label-value"
                    >
                      {fmtBR.format(r.value)}
                    </text>
                  </>
                )}
              </g>
            )
          })}
        </svg>
        <div className="in-pyramid-legend">
          {rows.map(r => (
            <div key={r.name} className="in-pyramid-legend-row">
              <span className="in-pyramid-dot" style={{ background: r.color }} />
              <span className="in-pyramid-name">Classe {r.name}</span>
              <span className="in-pyramid-pct">{fmtPct(r.value, total)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
