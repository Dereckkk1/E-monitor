function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

/**
 * Compact 1-line summary strip used at the top of the wizard.
 *
 * Props:
 *  - name: string
 *  - clientName: string
 *  - startDate: ISO string
 *  - endDate: ISO string
 *  - stationCount: number
 *  - materialCount: number
 *  - distributedCount: number — how many (material × station) combinations have rules
 *  - distributedTotal: number — total combinations needed
 */
export default function CampaignSummaryStrip({
  name, clientName, startDate, endDate,
  stationCount = 0, materialCount = 0,
  distributedCount = 0, distributedTotal = 0,
}) {
  const dot = (
    <span style={{ width: 3, height: 3, borderRadius: '50%', background: 'var(--c-text-3)', display: 'inline-block' }} />
  )
  const coverage = distributedTotal > 0
    ? distributedCount === distributedTotal ? 'good'
    : distributedCount === 0 ? 'bad'
    : 'warn'
    : null

  const coverageStyle = coverage === 'good' ? { background: '#dcfce7', color: 'var(--c-success)' }
    : coverage === 'warn' ? { background: '#fef9c3', color: 'var(--c-warning)' }
    : coverage === 'bad'  ? { background: '#fee2e2', color: 'var(--c-danger)' }
    : null

  return (
    <div style={{
      padding: '11px 24px', background: 'var(--c-bg)',
      borderBottom: '1px solid var(--c-surface-2)',
      fontSize: 11, color: 'var(--c-text-2)',
      display: 'flex', gap: 18, alignItems: 'center', flexWrap: 'wrap',
    }}>
      <span><strong style={{ color: 'var(--c-text)', fontWeight: 600 }}>{name || 'Nova campanha'}</strong></span>
      {clientName && <>{dot}<span>{clientName}</span></>}
      {startDate && endDate && <>{dot}<span>{fmtDate(startDate)} — {fmtDate(endDate)}</span></>}
      {dot}<span><strong style={{ color: 'var(--c-text)', fontWeight: 600 }}>{stationCount}</strong> emissora{stationCount !== 1 && 's'}</span>
      {dot}<span><strong style={{ color: 'var(--c-text)', fontWeight: 600 }}>{materialCount}</strong> material{materialCount !== 1 && 'is'}</span>
      {coverage && (
        <span style={{
          marginLeft: 'auto', padding: '3px 9px', borderRadius: 'var(--radius-full)',
          fontWeight: 600, fontSize: 11,
          ...coverageStyle,
        }}>{distributedCount} / {distributedTotal} distribuídos</span>
      )}
    </div>
  )
}
