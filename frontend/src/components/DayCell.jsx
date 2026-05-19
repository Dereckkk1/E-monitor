import { useRef, useState } from 'react'
import BadgePill from './BadgePill'

const OUTSIDE_BG = 'repeating-linear-gradient(45deg, #fafbfc 0 5px, #f1f5f9 5px 10px)'
const OVERRIDE_BG = '#fef9c3'

/**
 * Cell of the distribution grid.
 *
 * Props:
 *  - expected, inSlot, deficit, bonus, outSlot, outDate: number | null
 *  - isOutsideRange, hasOverride, hasPendingDraft: bool
 *  - onClick: (event) => void — fires when the badge area is clicked (opens popover in edit mode)
 *  - onIncrement, onDecrement: () => void — optional. When provided, hover shows +/- micro-buttons.
 *  - hint: string — tooltip
 */
export default function DayCell({
  expected = null, inSlot = null, deficit = null,
  bonus = null, outSlot = null, outDate = null,
  isOutsideRange = false,
  hasOverride = false, hasPendingDraft = false,
  onClick, onIncrement, onDecrement, hint,
}) {
  const [hovered, setHovered] = useState(false)
  const cellRef = useRef(null)
  const disabled = isOutsideRange
  const showStepper = !disabled && (onIncrement || onDecrement) && hovered

  const bg = hasOverride ? OVERRIDE_BG
    : isOutsideRange ? OUTSIDE_BG
    : '#fff'

  const showExpected = expected != null && expected > 0
  const showInSlot   = inSlot != null && inSlot > 0
  const showDeficit  = deficit != null && deficit > 0
  const showBonus    = bonus != null && bonus > 0
  const showOutSlot  = outSlot != null && outSlot > 0
  const showOutDate  = outDate != null && outDate > 0

  // Stop propagation so clicking +/- doesn't also fire onClick (which would open the popover).
  // Forwarda o rect da CÉLULA (não do botão) pra permitir que o caller
  // âncore um popover quando o +/- cair em célula ambígua (D3 do spec
  // override-time-window).
  function handleIncrement(e) {
    e.stopPropagation()
    onIncrement?.(cellRef.current?.getBoundingClientRect())
  }
  function handleDecrement(e) {
    e.stopPropagation()
    onDecrement?.(cellRef.current?.getBoundingClientRect())
  }

  return (
    <div
      ref={cellRef}
      onClick={disabled ? undefined : onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
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
        boxShadow: hasPendingDraft
          ? 'inset 0 0 0 2px #f59e0b'
          : !disabled && hovered ? 'inset 0 0 0 1px var(--c-action)' : 'none',
        transition: 'box-shadow 80ms',
      }}
    >
      {hasOverride && (
        <span style={{
          position: 'absolute', top: 3, right: 3,
          width: 4, height: 4, borderRadius: '50%',
          background: hasPendingDraft ? '#f59e0b' : '#b45309',
        }} />
      )}

      {/* Badges (dimmed while hovering to make room for +/-) */}
      <div style={{
        display: 'flex', gap: 2, alignItems: 'center',
        opacity: showStepper ? 0.3 : 1,
        transition: 'opacity 80ms',
      }}>
        {showExpected && <BadgePill variant={hasOverride ? 'orange' : 'gray'} value={expected} />}
        {showInSlot   && <BadgePill variant="green"  value={inSlot} />}
        {showDeficit  && <BadgePill variant="red"    value={-deficit} />}
        {showBonus    && <BadgePill variant="blue"   value={bonus} prefix="+" />}
        {showOutSlot  && <BadgePill variant="yellow" value={outSlot} prefix="+" />}
        {showOutDate  && <BadgePill variant="purple" value={outDate} prefix="+" />}
      </div>

      {/* +/- micro-buttons (visible on hover when callbacks provided) */}
      {showStepper && (
        <div style={{
          position: 'absolute', inset: 0,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '0 4px', pointerEvents: 'none',
        }}>
          <button
            onClick={handleDecrement}
            style={{
              width: 18, height: 18, borderRadius: 4, border: 0,
              background: 'var(--c-surface)', color: 'var(--c-action)',
              fontWeight: 700, fontSize: 13, lineHeight: 1, cursor: 'pointer',
              pointerEvents: 'auto',
              boxShadow: '0 1px 3px rgba(15,23,42,0.15)',
            }}
            aria-label="Diminuir"
            title="Diminuir"
          >−</button>
          <button
            onClick={handleIncrement}
            style={{
              width: 18, height: 18, borderRadius: 4, border: 0,
              background: 'var(--c-action)', color: '#fff',
              fontWeight: 700, fontSize: 13, lineHeight: 1, cursor: 'pointer',
              pointerEvents: 'auto',
              boxShadow: '0 1px 3px rgba(232,30,117,0.4)',
            }}
            aria-label="Aumentar"
            title="Aumentar"
          >+</button>
        </div>
      )}
    </div>
  )
}
