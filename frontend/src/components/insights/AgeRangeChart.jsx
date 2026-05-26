import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell, LabelList } from 'recharts'
import CustomTooltip from './CustomTooltip'

const COLORS = ['#f9a8d4', '#ec4899', '#E81E75']
const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function AgeRangeChart({ data }) {
  const a = data?.age_ranges
  if (!a) return null
  const rows = [
    { name: '18-24', value: a.r18_24 },
    { name: '25-49', value: a.r25_49 },
    { name: '50+',   value: a.r50_plus },
  ]
  const total = rows.reduce((s, r) => s + r.value, 0)
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Faixa etária</h3>
      <ResponsiveContainer width="100%" height={260}>
        <BarChart data={rows} margin={{ top: 24, right: 16, bottom: 8, left: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
          <XAxis dataKey="name" tick={{ fontSize: 12, fontWeight: 600 }} />
          <YAxis tickFormatter={v => fmtCompact.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <Tooltip
            cursor={{ fill: 'rgba(232,30,117,0.05)' }}
            content={<CustomTooltip formatter={v => `${fmtBR.format(v)} impactos`} />}
          />
          <Bar dataKey="value" name="Impactos" radius={[8, 8, 0, 0]}>
            {rows.map((_, i) => <Cell key={i} fill={COLORS[i]} />)}
            <LabelList
              dataKey="value"
              position="top"
              formatter={(v) => {
                const pct = total > 0 ? ((v / total) * 100).toFixed(0) : 0
                return `${fmtBR.format(v)} · ${pct}%`
              }}
              style={{ fontSize: 11, fontWeight: 600, fill: '#06055B' }}
            />
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
