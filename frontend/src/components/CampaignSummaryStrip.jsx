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
    <span style={{ width: 3, height: 3, borderRadius: '50%', background: '#cbd5e1', display: 'inline-block' }} />
  )
  const coverage = distributedTotal > 0
    ? distributedCount === distributedTotal ? 'good'
    : distributedCount === 0 ? 'bad'
    : 'warn'
    : null

  const coverageStyle = coverage === 'good' ? { background: '#dcfce7', color: '#15803d' }
    : coverage === 'warn' ? { background: '#fef9c3', color: '#a16207' }
    : coverage === 'bad'  ? { background: '#fee2e2', color: '#b91c1c' }
    : null

  return (
    <div style={{
      padding: '11px 24px', background: '#fafbfc',
      borderBottom: '1px solid #f1f5f9',
      fontSize: 11, color: '#475569',
      display: 'flex', gap: 18, alignItems: 'center', flexWrap: 'wrap',
    }}>
      <span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{name || 'Nova campanha'}</strong></span>
      {clientName && <>{dot}<span>{clientName}</span></>}
      {startDate && endDate && <>{dot}<span>{fmtDate(startDate)} — {fmtDate(endDate)}</span></>}
      {dot}<span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{stationCount}</strong> emissora{stationCount !== 1 && 's'}</span>
      {dot}<span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{materialCount}</strong> material{materialCount !== 1 && 'is'}</span>
      {coverage && (
        <span style={{
          marginLeft: 'auto', padding: '3px 9px', borderRadius: 999,
          fontWeight: 600, fontSize: 11,
          ...coverageStyle,
        }}>{distributedCount} / {distributedTotal} distribuídos</span>
      )}
    </div>
  )
}
