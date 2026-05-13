// AirtimeMaterialPanel — "Total por áudio" summary below the detection list.
//
// Layout: table-like rows with ID | • Comercial + bar | Total. Each material
// gets a stable color derived from its UUID so the same material always
// shows up with the same hue across page loads. Bars are full-width below
// the title, scaled against the top counter.

const PALETTE = [
  '#A16207', // amber-700
  '#E11D48', // rose-600
  '#9333EA', // purple-600
  '#3B82F6', // blue-500
  '#0891B2', // cyan-600
  '#16A34A', // green-600
  '#EA580C', // orange-600
  '#0D9488', // teal-600
  '#7C3AED', // violet-600
  '#DB2777', // pink-600
  '#65A30D', // lime-600
  '#1D4ED8', // indigo-700
  '#BE123C', // rose-700
  '#15803D', // green-700
  '#C2410C', // orange-700
  '#581C87', // purple-900
]

function hashColor(key) {
  if (!key) return PALETTE[0]
  let h = 0
  for (let i = 0; i < key.length; i++) {
    h = (h * 31 + key.charCodeAt(i)) | 0
  }
  return PALETTE[Math.abs(h) % PALETTE.length]
}

function fmtId(shortId, materialId) {
  if (shortId != null) return String(shortId)
  // Fallback when short_id isn't available — last 6 chars of the UUID.
  return materialId ? materialId.slice(-6).toUpperCase() : '—'
}

export default function AirtimeMaterialPanel({
  aggregate,
  loading = false,
  highlightedMaterialId = null,
  onHover = () => {},
  onLeave = () => {},
}) {
  if (loading) {
    return (
      <section className="airtime-panel airtime-panel-loading" aria-label="Total de veiculações por material">
        <header className="airtime-panel-header">
          <h3 className="airtime-panel-title">Total por áudio</h3>
        </header>
        <table className="airtime-panel-table">
          <thead>
            <tr>
              <th className="airtime-panel-th-id">ID</th>
              <th className="airtime-panel-th-name">Comercial</th>
              <th className="airtime-panel-th-total">Total</th>
            </tr>
          </thead>
          <tbody>
            {Array.from({ length: 6 }).map((_, i) => (
              <tr key={i} className="airtime-panel-tr-skel">
                <td><div className="airtime-skel-line" style={{ width: 50, height: 11 }} /></td>
                <td><div className="airtime-skel-line" style={{ width: '70%', height: 11 }} /></td>
                <td><div className="airtime-skel-line" style={{ width: 30, height: 11, marginLeft: 'auto' }} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    )
  }

  const items = aggregate?.data ?? []
  const max = items[0]?.count ?? 1

  return (
    <section className="airtime-panel" aria-label="Total de veiculações por material">
      <header className="airtime-panel-header">
        <h3 className="airtime-panel-title">Total por áudio</h3>
        <div className="airtime-panel-summary">
          <span className="airtime-panel-summary-label">Total geral</span>
          <span className="airtime-panel-summary-value">{aggregate?.total_detections ?? 0}</span>
          <span className="airtime-panel-summary-sep">·</span>
          <span className="airtime-panel-summary-meta">
            {aggregate?.distinct_materials ?? 0} materiais distintos
          </span>
        </div>
      </header>

      {items.length === 0 ? (
        <div className="airtime-panel-empty">Nenhum material no período.</div>
      ) : (
        <table className="airtime-panel-table">
          <thead>
            <tr>
              <th className="airtime-panel-th-id">ID</th>
              <th className="airtime-panel-th-name">Comercial</th>
              <th className="airtime-panel-th-total">Total</th>
            </tr>
          </thead>
          <tbody>
            {items.map(item => {
              const pct = Math.max(2, (item.count / max) * 100)
              const color = hashColor(item.material_id)
              const hl = highlightedMaterialId === item.material_id
              return (
                <tr
                  key={item.material_id}
                  className={'airtime-panel-tr' + (hl ? ' highlighted' : '')}
                  onMouseEnter={() => onHover(item.material_id)}
                  onMouseLeave={onLeave}
                  style={{ '--material-color': color }}
                >
                  <td className="airtime-panel-td-id">
                    {fmtId(item.material_short_id, item.material_id)}
                  </td>
                  <td className="airtime-panel-td-name">
                    <div className="airtime-panel-name-line">
                      <span className="airtime-panel-name-dot" style={{ background: color }} aria-hidden />
                      <span className="airtime-panel-name-text" title={item.material_title} style={{ color }}>
                        {item.material_title}
                      </span>
                    </div>
                    <div className="airtime-panel-bar-bg">
                      <div
                        className="airtime-panel-bar-fill"
                        style={{ width: `${pct}%`, background: color }}
                      />
                    </div>
                  </td>
                  <td className="airtime-panel-td-total">{item.count}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </section>
  )
}
