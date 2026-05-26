import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import CustomTooltip from './CustomTooltip'

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4']
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null
  const rows = [
    { name: 'AB', value: cp.ab },
    { name: 'C',  value: cp.c },
    { name: 'DE', value: cp.de },
  ]
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <ResponsiveContainer width="100%" height={260}>
        <BarChart data={rows} layout="vertical" margin={{ top: 8, right: 24, bottom: 8, left: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" horizontal={false} />
          <XAxis type="number" tickFormatter={v => fmtCompact.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <YAxis type="category" dataKey="name" tick={{ fontSize: 12, fontWeight: 600 }} />
          <Tooltip
            cursor={{ fill: 'rgba(232,30,117,0.05)' }}
            content={<CustomTooltip formatter={v => `${fmtCompact.format(v)} impactos`} />}
          />
          <Bar dataKey="value" name="Impactos" radius={[0, 8, 8, 0]}>
            {rows.map((_, i) => <Cell key={i} fill={COLORS[i]} />)}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
