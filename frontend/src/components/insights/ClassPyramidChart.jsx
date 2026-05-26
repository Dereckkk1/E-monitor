// Pirâmide de classe social via Recharts Funnel (que renderiza
// trapézios proporcionais). Para virar pirâmide (e não funil),
// passamos `size` crescente de cima pra baixo: AB pequeno no topo,
// C médio no meio, DE largo na base. O valor real (impactos) vai
// no label — não controla o tamanho do shape.

import { FunnelChart, Funnel, LabelList, ResponsiveContainer } from 'recharts'

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // AB / C / DE
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtPct = (v, t) => (t > 0 ? ((v / t) * 100).toFixed(1) : '0.0')

// Tamanhos sintéticos pra dar a forma "pirâmide" — não tem nada a ver
// com os valores reais. Crescentes de cima pra baixo: 30 → 65 → 100.
const PYRAMID_SIZES = [30, 65, 100]

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null

  const rows = [
    { name: 'A/B', size: PYRAMID_SIZES[0], value: cp.ab, fill: COLORS[0] },
    { name: 'C',   size: PYRAMID_SIZES[1], value: cp.c,  fill: COLORS[1] },
    { name: 'D/E', size: PYRAMID_SIZES[2], value: cp.de, fill: COLORS[2] },
  ]
  const total = rows.reduce((s, r) => s + r.value, 0)

  // Label custom: renderiza 2 linhas (nome + valor) centradas no shape.
  const renderLabel = (props) => {
    const { x, y, width, height, name, value } = props
    const cx = x + width / 2
    const cy = y + height / 2
    return (
      <g>
        <text
          x={cx}
          y={cy - 4}
          textAnchor="middle"
          dominantBaseline="middle"
          style={{ fontSize: 11, fontWeight: 600, fill: '#06055B' }}
        >
          Classe {name}
        </text>
        <text
          x={cx}
          y={cy + 12}
          textAnchor="middle"
          dominantBaseline="middle"
          style={{
            fontSize: 14,
            fontWeight: 700,
            fill: '#06055B',
            fontFamily: "'Space Grotesk', sans-serif",
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {fmtBR.format(value)}
        </text>
      </g>
    )
  }

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <div className="in-pyramid">
        <ResponsiveContainer width="100%" height={220}>
          <FunnelChart>
            <Funnel
              dataKey="size"
              data={rows}
              isAnimationActive={false}
              stroke="white"
              strokeWidth={2}
            >
              <LabelList content={renderLabel} />
            </Funnel>
          </FunnelChart>
        </ResponsiveContainer>
        <div className="in-pyramid-legend">
          {rows.map(r => (
            <div key={r.name} className="in-pyramid-legend-row">
              <span className="in-pyramid-dot" style={{ background: r.fill }} />
              <span className="in-pyramid-name">Classe {r.name}</span>
              <span className="in-pyramid-pct">{fmtPct(r.value, total)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
