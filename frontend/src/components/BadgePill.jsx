const VARIANT_STYLES = {
  gray:   { background: '#e2e8f0', color: '#475569' },
  green:  { background: '#dcfce7', color: '#15803d' },
  red:    { background: '#fee2e2', color: '#b91c1c' },
  blue:   { background: '#dbeafe', color: '#1d4ed8' },
  yellow: { background: '#fef3c7', color: '#b45309' },
  purple: { background: '#ede9fe', color: '#6d28d9' },
  orange: { background: '#fde68a', color: '#b45309' },
}

/**
 * Compact colored pill with a number. Used in DayCell and grid headers.
 *
 * Props:
 *  - variant: 'gray' | 'green' | 'red' | 'blue' | 'yellow' | 'purple' | 'orange'
 *  - value: number to display (prefix shown for +/- variants)
 *  - prefix: optional string prepended to value (e.g. '+', '-')
 *  - title: optional tooltip
 */
export default function BadgePill({ variant = 'gray', value, prefix = '', title }) {
  const style = VARIANT_STYLES[variant] ?? VARIANT_STYLES.gray
  return (
    <span
      title={title}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        minWidth: 18,
        height: 18,
        padding: '0 5px',
        borderRadius: 999,
        fontSize: 10,
        fontWeight: 700,
        lineHeight: 1,
        ...style,
      }}
    >
      {prefix}{value}
    </span>
  )
}
