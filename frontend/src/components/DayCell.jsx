import BadgePill from './BadgePill'

const WEEKEND_BG = '#f8fafc'
const OUTSIDE_BG = 'repeating-linear-gradient(45deg, #fafbfc 0 5px, #f1f5f9 5px 10px)'
const OVERRIDE_BG = '#fef9c3'

/**
 * Cell of the distribution grid.
 *
 * Props:
 *  - expected: number | null         — gray badge (plan)
 *  - inSlot:   number | null         — green badge (played in slot)
 *  - deficit:  number | null         — red badge (still owed)
 *  - bonus:    number | null         — blue badge "+N"
 *  - outSlot:  number | null         — yellow badge "+N"
 *  - outDate:  number | null         — purple badge "+N"
 *  - isWeekend: bool                 — gray background, not clickable
 *  - isOutsideRange: bool            — striped pattern, not clickable
 *  - hasOverride: bool               — orange tint + dot indicator
 *  - onClick: (event) => void        — receives the click event so callers can read currentTarget.getBoundingClientRect()
 *  - hint: string                    — tooltip text
 */
export default function DayCell({
  expected = null, inSlot = null, deficit = null,
  bonus = null, outSlot = null, outDate = null,
  isWeekend = false, isOutsideRange = false,
  hasOverride = false, onClick, hint,
}) {
  const disabled = isWeekend || isOutsideRange
  const bg = hasOverride ? OVERRIDE_BG
    : isOutsideRange ? OUTSIDE_BG
    : isWeekend ? WEEKEND_BG
    : '#fff'

  const showExpected = expected != null && expected > 0
  const showInSlot   = inSlot != null && inSlot > 0
  const showDeficit  = deficit != null && deficit > 0
  const showBonus    = bonus != null && bonus > 0
  const showOutSlot  = outSlot != null && outSlot > 0
  const showOutDate  = outDate != null && outDate > 0

  return (
    <div
      onClick={disabled ? undefined : onClick}
      title={hint}
      style={{
        position: 'relative',
        padding: '8px 4px',
        borderBottom: '1px solid #f1f5f9',
        borderRight: '1px solid #f1f5f9',
        background: bg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 2,
        minHeight: 38,
        cursor: disabled ? 'default' : 'pointer',
      }}
      onMouseEnter={(e) => { if (!disabled) e.currentTarget.style.boxShadow = `inset 0 0 0 1px #cbd5e1` }}
      onMouseLeave={(e) => { e.currentTarget.style.boxShadow = 'none' }}
    >
      {hasOverride && (
        <span style={{
          position: 'absolute', top: 3, right: 3,
          width: 4, height: 4, borderRadius: '50%', background: '#b45309',
        }} />
      )}
      {showExpected && <BadgePill variant={hasOverride ? 'orange' : 'gray'} value={expected} />}
      {showInSlot   && <BadgePill variant="green"  value={inSlot} />}
      {showDeficit  && <BadgePill variant="red"    value={-deficit} />}
      {showBonus    && <BadgePill variant="blue"   value={bonus} prefix="+" />}
      {showOutSlot  && <BadgePill variant="yellow" value={outSlot} prefix="+" />}
      {showOutDate  && <BadgePill variant="purple" value={outDate} prefix="+" />}
    </div>
  )
}
