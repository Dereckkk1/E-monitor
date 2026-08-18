import { PieChart, Pie, Cell, Tooltip, Legend, ResponsiveContainer, Label } from 'recharts'

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
      <div className="in-donut-wrap">
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            {/* Geometria toda relativa, nada em px fixo. O recharts já desconta
                a legenda vertical da área do gráfico, então cx=50% centraliza
                a rosca no espaço QUE SOBRA (com cx=35% + raio fixo de 92px ela
                escapava pela esquerda do card assim que a legenda comia largura
                — o corte em telas médias). Os raios em % seguem o menor lado da
                área útil, então a rosca encolhe junto em vez de vazar. */}
            <Pie
              data={rows}
              dataKey="value"
              cx="50%"
              cy="50%"
              innerRadius="60%"
              outerRadius="88%"
              paddingAngle={2}
              isAnimationActive={false}
            >
              {rows.map((r, i) => <Cell key={i} fill={r.fill} />)}
              <Label
                content={({ viewBox }) => {
                  // O rótulo central acompanha o raio: com a rosca menor, um
                  // corpo fixo de 22px estourava o furo do donut.
                  const { cx, cy, innerRadius } = viewBox
                  const r = innerRadius || 60
                  const big = Math.max(14, Math.min(22, r * 0.36))
                  const small = Math.max(8, Math.min(10, r * 0.16))
                  return (
                    // pointer-events:none — o rótulo central ficava por cima do
                    // furo do donut e sequestrava o hover das fatias.
                    <g style={{ pointerEvents: 'none' }}>
                      <text x={cx} y={cy - big * 0.3} textAnchor="middle" dominantBaseline="middle"
                            style={{ fontFamily: 'Space Grotesk, sans-serif', fontSize: big, fontWeight: 700, fill: '#06055B' }}>
                        {fmtBR.format(total)}
                      </text>
                      <text x={cx} y={cy + big * 0.66} textAnchor="middle" dominantBaseline="middle"
                            style={{ fontSize: small, fill: '#6b7280', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
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
                        <span className="in-tooltip-dot" style={{ background: p.payload?.fill }} />
                        <span className="in-tooltip-name">Total</span>
                        <span className="in-tooltip-value">{fmtBR.format(p.value)} ({pct}%)</span>
                      </div>
                    </div>
                  </div>
                )
              }}
            />
            {/* maxWidth trava o quanto a legenda pode roubar da rosca: sem
                teto, "Extras / Sem faixa definida: 0" numa linha só levava
                metade do card e espremia o gráfico. Com teto, o rótulo longo
                quebra em 2 linhas e a rosca mantém o espaço dela. */}
            <Legend
              layout="vertical"
              align="right"
              verticalAlign="middle"
              iconType="circle"
              wrapperStyle={{ fontSize: 12, maxWidth: '46%', lineHeight: 1.45 }}
              formatter={(v, e) => {
                const val = e?.payload?.value || 0
                return `${v}: ${fmtBR.format(val)}`
              }}
            />
          </PieChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
