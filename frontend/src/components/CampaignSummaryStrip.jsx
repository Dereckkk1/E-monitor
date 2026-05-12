function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

function Metric({ label, value, accent = false }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 1, minWidth: 0 }}>
      <span style={{
        fontSize: 9, fontWeight: 700, letterSpacing: '0.10em',
        color: 'var(--c-text-3)', textTransform: 'uppercase',
      }}>
        {label}
      </span>
      <span style={{
        fontSize: 13, fontWeight: 600,
        color: accent ? 'var(--c-action)' : 'var(--c-text)',
        fontFamily: 'var(--font-heading)',
        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
      }}>
        {value}
      </span>
    </div>
  )
}

function Divider() {
  return (
    <span style={{
      width: 1, height: 28, background: 'var(--c-border)',
      flexShrink: 0,
    }} />
  )
}

/**
 * Sticky context strip below the stepper. Shows a live summary of the
 * campaign being edited so the user always knows what they're building.
 */
export default function CampaignSummaryStrip({
  name, clientName, startDate, endDate,
  stationCount = 0, materialCount = 0,
  distributedCount = 0, distributedTotal = 0,
}) {
  // Coverage colour: green (all covered), amber (partial), red (none).
  const coverageTone = distributedTotal === 0 ? null
    : distributedCount === distributedTotal ? 'good'
    : distributedCount === 0 ? 'bad'
    : 'warn'

  const coverageStyles = {
    good: { bg: '#dcfce7', fg: 'var(--c-success)', dot: 'var(--c-success)' },
    warn: { bg: '#fef9c3', fg: '#a16207',          dot: '#ca8a04' },
    bad:  { bg: '#fee2e2', fg: 'var(--c-danger)',  dot: 'var(--c-danger)' },
  }
  const cov = coverageTone ? coverageStyles[coverageTone] : null

  const dateRange = startDate && endDate ? `${fmtDate(startDate)} → ${fmtDate(endDate)}` : '—'

  return (
    <div style={{
      padding: '14px 32px',
      background: 'var(--c-bg)',
      borderBottom: '1px solid var(--c-border)',
      display: 'flex', alignItems: 'center', gap: 22,
      flexWrap: 'wrap', minHeight: 56,
    }}>
      {/* Campaign name + client (anchor) */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 1, flex: '1 1 220px', minWidth: 0 }}>
        <span style={{
          fontSize: 9, fontWeight: 700, letterSpacing: '0.10em',
          color: 'var(--c-text-3)', textTransform: 'uppercase',
        }}>
          Campanha
        </span>
        <span style={{
          fontSize: 14, fontWeight: 700,
          color: 'var(--c-text)', fontFamily: 'var(--font-heading)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {name || 'Nova campanha'}
          {clientName && (
            <span style={{ color: 'var(--c-text-3)', fontWeight: 500, marginLeft: 8 }}>
              · {clientName}
            </span>
          )}
        </span>
      </div>

      <Divider />
      <Metric label="Período"   value={dateRange} />

      <Divider />
      <Metric label="Emissoras" value={stationCount} accent={stationCount > 0} />

      <Divider />
      <Metric label="Materiais" value={materialCount} accent={materialCount > 0} />

      {/* Coverage chip — only when there's something to distribute */}
      {cov && (
        <div style={{
          marginLeft: 'auto',
          padding: '6px 12px', borderRadius: 'var(--radius-full)',
          background: cov.bg, color: cov.fg,
          fontSize: 11, fontWeight: 700,
          fontFamily: 'var(--font-heading)',
          display: 'flex', alignItems: 'center', gap: 7,
          whiteSpace: 'nowrap',
        }}>
          <span style={{
            width: 6, height: 6, borderRadius: '50%',
            background: cov.dot, flexShrink: 0,
          }} />
          {distributedCount} de {distributedTotal} distribuídos
        </div>
      )}
    </div>
  )
}
