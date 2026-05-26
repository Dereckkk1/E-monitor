const fmtBR = new Intl.NumberFormat('pt-BR')

export default function CustomTooltip({ active, payload, label, formatter }) {
  if (!active || !payload || payload.length === 0) return null
  return (
    <div className="in-tooltip">
      {label && <div className="in-tooltip-label">{label}</div>}
      <div className="in-tooltip-rows">
        {payload.map((p, i) => (
          <div key={i} className="in-tooltip-row">
            <span className="in-tooltip-dot" style={{ background: p.color }} />
            <span className="in-tooltip-name">{p.name}</span>
            <span className="in-tooltip-value">
              {formatter ? formatter(p.value) : fmtBR.format(p.value)}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
