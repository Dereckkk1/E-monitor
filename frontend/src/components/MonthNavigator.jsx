const MONTH_NAMES = ['Janeiro', 'Fevereiro', 'Março', 'Abril', 'Maio', 'Junho',
  'Julho', 'Agosto', 'Setembro', 'Outubro', 'Novembro', 'Dezembro']

/**
 * Month navigator: < [Junho 2026] >
 *
 * Props:
 *  - value: Date (any day of the month being displayed)
 *  - onChange: (Date) => void — fires with first-of-month for new month
 *  - minDate?: Date — disables prev button if it would go before this
 *  - maxDate?: Date — disables next button if it would go after this
 */
export default function MonthNavigator({ value, onChange, minDate, maxDate }) {
  const y = value.getFullYear()
  const m = value.getMonth()
  const label = `${MONTH_NAMES[m]} ${y}`

  const prev = new Date(y, m - 1, 1)
  const next = new Date(y, m + 1, 1)

  const prevDisabled = minDate && prev < new Date(minDate.getFullYear(), minDate.getMonth(), 1)
  const nextDisabled = maxDate && next > new Date(maxDate.getFullYear(), maxDate.getMonth(), 1)

  return (
    <div style={{
      display: 'inline-flex', alignItems: 'center',
      border: '1px solid #e2e8f0', borderRadius: 8, overflow: 'hidden', background: '#fff',
    }}>
      <button
        onClick={() => onChange(prev)}
        disabled={prevDisabled}
        style={{
          padding: '7px 9px', border: 0, background: 'transparent',
          color: prevDisabled ? '#cbd5e1' : '#64748b',
          cursor: prevDisabled ? 'not-allowed' : 'pointer',
        }}
        aria-label="Mês anterior"
      >‹</button>
      <span style={{
        padding: '7px 12px', fontWeight: 600, color: '#0f172a', fontSize: 12,
        borderLeft: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
      }}>{label}</span>
      <button
        onClick={() => onChange(next)}
        disabled={nextDisabled}
        style={{
          padding: '7px 9px', border: 0, background: 'transparent',
          color: nextDisabled ? '#cbd5e1' : '#64748b',
          cursor: nextDisabled ? 'not-allowed' : 'pointer',
        }}
        aria-label="Próximo mês"
      >›</button>
    </div>
  )
}
