import BadgePill from './BadgePill'

/**
 * Top-of-page coverage summary for /detections.
 *
 * Props:
 *  - summary: Array<daily_play_summary row>
 *    Sums all rows (across station × material × day) to produce period totals.
 */
export default function CoverageSummary({ summary }) {
  const totals = summary.reduce((acc, s) => ({
    expected: acc.expected + (s.expected ?? 0),
    in_slot:  acc.in_slot  + (s.in_slot  ?? 0),
    deficit:  acc.deficit  + (s.deficit  ?? 0),
    bonus:    acc.bonus    + (s.bonus    ?? 0),
    out_slot: acc.out_slot + (s.out_slot ?? 0),
    out_date: acc.out_date + (s.out_date ?? 0),
  }), { expected: 0, in_slot: 0, deficit: 0, bonus: 0, out_slot: 0, out_date: 0 })

  const coveragePct = totals.expected > 0
    ? Math.round((totals.in_slot / totals.expected) * 100)
    : null

  const coverageColor = coveragePct == null ? '#94a3b8'
    : coveragePct >= 95 ? '#15803d'
    : coveragePct >= 80 ? '#a16207'
    : '#b91c1c'

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 24,
      padding: '14px 18px', marginBottom: 14,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        <span style={{ fontSize: 11, color: '#64748b', textTransform: 'uppercase', fontWeight: 600, letterSpacing: '0.04em' }}>
          Cobertura do plano
        </span>
        <span style={{ fontSize: 32, fontWeight: 700, color: coverageColor, lineHeight: 1 }}>
          {coveragePct != null ? `${coveragePct}%` : '—'}
        </span>
      </div>

      <div style={{ height: 40, width: 1, background: '#e2e8f0' }} />

      <Stat label="Esperado" value={totals.expected} variant="gray" />
      <Stat label="Tocou (faixa)" value={totals.in_slot} variant="green" />
      {totals.deficit > 0 && <Stat label="Faltou" value={totals.deficit} variant="red" />}
      {totals.bonus > 0 && <Stat label="Bônus" value={totals.bonus} variant="blue" prefix="+" />}
      {totals.out_slot > 0 && <Stat label="Fora faixa" value={totals.out_slot} variant="yellow" prefix="+" />}
      {totals.out_date > 0 && <Stat label="Fora data" value={totals.out_date} variant="purple" prefix="+" />}
    </div>
  )
}

function Stat({ label, value, variant, prefix }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
      <span style={{ fontSize: 10, color: '#64748b', textTransform: 'uppercase', fontWeight: 600 }}>{label}</span>
      <BadgePill variant={variant} value={value} prefix={prefix ?? ''} />
    </div>
  )
}
