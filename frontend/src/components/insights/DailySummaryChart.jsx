import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts'

const fmtBR = new Intl.NumberFormat('pt-BR')
const monthLabel = new Intl.DateTimeFormat('pt-BR', { month: 'short', year: '2-digit' })

const COLORS = {
  programado: '#9ca3af',
  in_slot:    '#10b981',
  out_slot:   '#f59e0b',
  out_date:   '#8b5cf6',
  deficit:    '#ef4444',
  extras:     '#3b82f6',
}

function fmtBucket(b, gran) {
  if (!b) return ''
  if (gran === 'month') {
    const [y, m] = b.split('-')
    return monthLabel.format(new Date(Number(y), Number(m) - 1, 1))
  }
  return b.slice(8, 10) + '/' + b.slice(5, 7)
}

export default function DailySummaryChart({ data }) {
  const gran = data?.period?.granularity || 'day'
  const rows = (data?.buckets || []).map(b => ({
    ...b,
    bucket_label: fmtBucket(b.bucket, gran),
  }))
  if (!rows.length) {
    return (
      <div className="in-chart-card">
        <h3 className="in-chart-title">
          Resumo {gran === 'month' ? 'mensal' : 'diário'} da campanha
        </h3>
        <div className="in-loading">Sem dados no período selecionado.</div>
      </div>
    )
  }

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">
        Resumo {gran === 'month' ? 'mensal' : 'diário'} da campanha
      </h3>
      <ResponsiveContainer width="100%" height={360}>
        <BarChart data={rows} margin={{ top: 8, right: 16, bottom: 24, left: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
          <XAxis
            dataKey="bucket_label"
            tick={{ fontSize: 11, fill: '#6b7280' }}
            interval="preserveStartEnd"
          />
          <YAxis tickFormatter={v => fmtBR.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <Tooltip
            cursor={{ fill: 'rgba(232,30,117,0.05)' }}
            content={({ active, payload, label }) => {
              if (!active || !payload || payload.length === 0) return null
              return (
                <div className="in-tooltip">
                  <div className="in-tooltip-label">{label}</div>
                  <div className="in-tooltip-rows">
                    {payload.map((p, i) => (
                      <div key={i} className="in-tooltip-row">
                        <span className="in-tooltip-dot" style={{ background: p.color }} />
                        <span className="in-tooltip-name">{p.name}</span>
                        <span className="in-tooltip-value">{fmtBR.format(p.value)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )
            }}
          />
          <Legend wrapperStyle={{ fontSize: 11, paddingTop: 8 }} iconType="circle" />
          <Bar dataKey="programado" name="Programado"    fill={COLORS.programado} radius={[4,4,0,0]} />
          <Bar dataKey="in_slot"    name="Dentro da faixa" fill={COLORS.in_slot}    radius={[4,4,0,0]} />
          <Bar dataKey="out_slot"   name="Fora da faixa"   fill={COLORS.out_slot}   radius={[4,4,0,0]} />
          <Bar dataKey="out_date"   name="Fora da data"    fill={COLORS.out_date}   radius={[4,4,0,0]} />
          <Bar dataKey="deficit"    name="Déficit"         fill={COLORS.deficit}    radius={[4,4,0,0]} />
          <Bar dataKey="extras"     name="Extras"          fill={COLORS.extras}     radius={[4,4,0,0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
