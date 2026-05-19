// Numbered paginator with ellipsis. Shows first/last + window of ±1 around
// the current page. Renders a count-only line when there's a single page so
// the user still sees "N veiculações" but doesn't see meaningless nav.
//
// Quando `pageSizeOptions` é passado, renderiza um seletor de tamanho de
// página à direita dos controles (usado em /detections).

import './AirtimePaginator.css'

export default function AirtimePaginator({
  page, totalPages, total, pageSize, onChange,
  pageSizeOptions = null,
  onPageSizeChange = null,
  singular = 'veiculação',
  plural = 'veiculações',
}) {
  const showSizePicker = Array.isArray(pageSizeOptions) &&
    pageSizeOptions.length > 1 &&
    typeof onPageSizeChange === 'function'

  const sizePicker = showSizePicker ? (
    <label className="airtime-paginator-size">
      <span>Por página:</span>
      <select
        className="airtime-paginator-size-select"
        value={pageSize}
        onChange={e => onPageSizeChange(Number(e.target.value))}
        aria-label="Itens por página"
      >
        {pageSizeOptions.map(n => (
          <option key={n} value={n}>{n}</option>
        ))}
      </select>
    </label>
  ) : null

  if (totalPages <= 1) {
    return (
      <div className="airtime-paginator">
        <span className="airtime-paginator-info">
          {total} {total === 1 ? singular : plural}
        </span>
        {sizePicker}
      </div>
    )
  }

  const first = Math.max(1, (page - 1) * pageSize + 1)
  const last  = Math.min(total, page * pageSize)

  const set = new Set([1, totalPages, page])
  for (let i = page - 1; i <= page + 1; i++) {
    if (i >= 1 && i <= totalPages) set.add(i)
  }
  const sorted = Array.from(set).sort((a, b) => a - b)

  const items = []
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] > sorted[i - 1] + 1) items.push({ ellipsis: true, key: `e-${i}` })
    items.push({ page: sorted[i], key: sorted[i] })
  }

  return (
    <nav className="airtime-paginator" aria-label="Paginação">
      <span className="airtime-paginator-info">
        Mostrando {first}–{last} de {total} {total === 1 ? singular : plural}
      </span>
      <div className="airtime-paginator-controls">
        <button
          type="button"
          className="airtime-paginator-btn"
          disabled={page <= 1}
          onClick={() => onChange(page - 1)}
          aria-label="Página anterior"
        >‹</button>
        {items.map(item =>
          item.ellipsis ? (
            <span key={item.key} className="airtime-paginator-ellipsis" aria-hidden>…</span>
          ) : (
            <button
              key={item.key}
              type="button"
              className={'airtime-paginator-btn' + (item.page === page ? ' active' : '')}
              aria-current={item.page === page ? 'page' : undefined}
              aria-label={`Página ${item.page}`}
              onClick={() => onChange(item.page)}
            >{item.page}</button>
          )
        )}
        <button
          type="button"
          className="airtime-paginator-btn"
          disabled={page >= totalPages}
          onClick={() => onChange(page + 1)}
          aria-label="Próxima página"
        >›</button>
        {sizePicker}
      </div>
    </nav>
  )
}
