import { useState } from 'react'

// AirtimeMaterialPanel renders the sticky-right "Total por áudio" summary for
// /reports/airtime. Each row is a material with a color-stripe, the title,
// the count and a horizontal bar scaled to (count / max). Hovering a row
// pings the parent so the corresponding cards in the list highlight (cross-
// highlight pattern from the spec).
export default function AirtimeMaterialPanel({
  aggregate,
  loading = false,
  highlightedMaterialId = null,
  onHover = () => {},
  onLeave = () => {},
}) {
  const [showAll, setShowAll] = useState(false)

  if (loading) {
    return (
      <aside className="airtime-panel airtime-panel-loading" aria-label="Total de veiculações por material">
        <h3 className="airtime-panel-title">Total por áudio</h3>
        <div className="airtime-panel-divider" />
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="airtime-panel-row-skel">
            <div className="airtime-skel-line" style={{ width: '60%' }} />
            <div className="airtime-skel-line" style={{ width: '100%', height: 6, marginTop: 6 }} />
          </div>
        ))}
      </aside>
    )
  }

  const items = aggregate?.data ?? []
  const max = items[0]?.count ?? 1
  const visible = showAll ? items : items.slice(0, 8)

  return (
    <aside className="airtime-panel" aria-label="Total de veiculações por material">
      <h3 className="airtime-panel-title">
        <span>Total por áudio</span>
        <span
          className="airtime-panel-info-icon"
          title="Considera detecções confirmadas no período (incluindo fora da faixa/data)"
          aria-hidden
        >ⓘ</span>
      </h3>
      <div className="airtime-panel-divider" />

      {items.length === 0 ? (
        <div className="airtime-panel-empty">Nenhum material no período.</div>
      ) : (
        <ul className="airtime-panel-list">
          {visible.map(item => {
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

      {items.length > 8 && !showAll && (
        <button type="button" className="airtime-panel-more" onClick={() => setShowAll(true)}>
          Ver todos ({items.length})
        </button>
      )}

      <div className="airtime-panel-divider" />
      <div className="airtime-panel-footer">
        <div className="airtime-panel-total-row">
          <span className="airtime-panel-total-label">Total geral</span>
          <span className="airtime-panel-total-value">{aggregate?.total_detections ?? 0}</span>
        </div>
        <div className="airtime-panel-meta">
          Materiais distintos: {aggregate?.distinct_materials ?? 0}
        </div>
      </div>
    </aside>
  )
}
