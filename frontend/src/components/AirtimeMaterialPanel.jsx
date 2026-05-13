// AirtimeMaterialPanel renders the "Total por áudio" summary as a full-width
// section below the detection list. Items are laid out in a responsive grid
// (auto-fill, minmax 280px) so on wide screens it shows 3-4 columns, on
// narrow stays 1. Each item has color stripe, title, count and a scaled
// horizontal bar. Hover pings the parent for cross-highlight in the list.
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
        <div className="airtime-panel-grid">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="airtime-panel-row-skel">
              <div className="airtime-skel-line" style={{ width: '60%', height: 12 }} />
              <div className="airtime-skel-line" style={{ width: '100%', height: 6, marginTop: 8 }} />
            </div>
          ))}
        </div>
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
        <ul className="airtime-panel-grid">
          {items.map(item => {
            const pct = Math.max(2, (item.count / max) * 100)
            const color = item.material_type_color || '#94a3b8'
            const hl = highlightedMaterialId === item.material_id
            return (
              <li
                key={item.material_id}
                className={'airtime-panel-row' + (hl ? ' highlighted' : '')}
                onMouseEnter={() => onHover(item.material_id)}
                onMouseLeave={onLeave}
              >
                <div className="airtime-panel-row-top">
                  <span className="airtime-panel-row-stripe" style={{ background: color }} aria-hidden />
                  <span className="airtime-panel-row-title" title={item.material_title}>
                    {item.material_title}
                  </span>
                  <span className="airtime-panel-row-count">{item.count}</span>
                </div>
                <div className="airtime-panel-row-bar-bg">
                  <div
                    className="airtime-panel-row-bar-fill"
                    style={{ width: `${pct}%`, background: color }}
                  />
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
