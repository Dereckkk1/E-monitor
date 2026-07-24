import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer, Label } from 'recharts'

const fmtBR = new Intl.NumberFormat('pt-BR')

const COLORS = {
  inSlot:  '#10b981',
  outSlot: '#f59e0b',
  outDate: '#8b5cf6',
  orphan:  '#3b82f6',
}

export default function BroadcastShareChart({ data }) {
  const b = data?.veiculacoes_breakdown
  if (!b) return null
  const total = b.in_slot + b.out_slot + b.out_date + b.extras_orphan
  const rows = [
    { name: 'Dentro da faixa', value: b.in_slot, fill: COLORS.inSlot },
    { name: 'Fora da faixa',   value: b.out_slot, fill: COLORS.outSlot },
    { name: 'Fora da data',    value: b.out_date, fill: COLORS.outDate },
    { name: 'Extras / Sem faixa definida', value: b.extras_orphan, fill: COLORS.orphan },
  ]
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">% Veiculações</h3>
      {/* Donut centrado (cx/cy 50%) na própria área + legenda em HTML ao lado.
          O flex .in-donut-layout desce a legenda pra baixo do donut quando o
          card aperta, então nada estoura em telas estreitas (ver
          InsightsPage.css). */}
      <div className="in-donut-layout">
        <div className="in-donut-chart">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie
                data={rows}
                dataKey="value"
                cx="50%"
                cy="50%"
                innerRadius={58}
                outerRadius={88}
                paddingAngle={2}
                isAnimationActive={false}
              >
                {rows.map((r, i) => <Cell key={i} fill={r.fill} />)}
                <Label
                  content={({ viewBox }) => {
                    const { cx, cy } = viewBox
                    return (
                      <g>
                        <text x={cx} y={cy - 6} textAnchor="middle" dominantBaseline="middle"
                              style={{ fontFamily: 'Space Grotesk, sans-serif', fontSize: 22, fontWeight: 700, fill: '#06055B' }}>
                          {fmtBR.format(total)}
                        </text>
                        <text x={cx} y={cy + 14} textAnchor="middle" dominantBaseline="middle"
                              style={{ fontSize: 10, fill: '#6b7280', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
                          veiculações
                        </text>
                      </g>
                    )
                  }}
                />
              </Pie>
              <Tooltip
                content={({ active, payload }) => {
                  if (!active || !payload || payload.length === 0) return null
                  const p = payload[0]
                  const pct = total > 0 ? ((p.value / total) * 100).toFixed(1) : '0'
                  return (
                    <div className="in-tooltip">
                      <div className="in-tooltip-label">{p.name}</div>
                      <div className="in-tooltip-rows">
                        <div className="in-tooltip-row">
                          <span className="in-tooltip-name">Total</span>
                          <span className="in-tooltip-value">{fmtBR.format(p.value)} ({pct}%)</span>
                        </div>
                      </div>
                    </div>
                  )
                }}
              />
            </PieChart>
          </ResponsiveContainer>
        </div>
        <ul className="in-donut-legend">
          {rows.map((r, i) => {
            const pct = total > 0 ? ((r.value / total) * 100).toFixed(0) : '0'
            return (
              <li key={i} className="in-donut-legend-row">
                <span className="in-donut-legend-dot" style={{ background: r.fill }} />
                <span className="in-donut-legend-name">{r.name}</span>
                <span className="in-donut-legend-value">{fmtBR.format(r.value)} · {pct}%</span>
              </li>
            )
          })}
        </ul>
      </div>
    </div>
  )
}
