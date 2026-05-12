import { useState, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

/**
 * Inline popover for setting per-cell override.
 *
 * Props:
 *  - open: bool
 *  - anchorRect: DOMRect | null (from the clicked cell)
 *  - onClose: () => void
 *  - onApply: (newValue) => void
 *  - onRevert: () => void   // remove the override
 *  - currentRuleValue: number  // what the rule would produce (gray badge)
 *  - currentOverrideValue?: number | null  // existing override if any
 *  - materialTitle: string
 *  - stationName: string
 *  - date: ISO string
 */
export default function OverridePopover({
  open, anchorRect, onClose, onApply, onRevert,
  currentRuleValue, currentOverrideValue = null,
  materialTitle, stationName, date,
}) {
  const [value, setValue] = useState(currentOverrideValue ?? currentRuleValue ?? 0)
  const ref = useRef(null)

  useEffect(() => {
    setValue(currentOverrideValue ?? currentRuleValue ?? 0)
  }, [currentOverrideValue, currentRuleValue, open])

  useEffect(() => {
    if (!open) return
    function onMouseDown(e) {
      if (ref.current && !ref.current.contains(e.target)) onClose()
    }
    function onKey(e) { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, onClose])

  if (!open || !anchorRect) return null

  const top = anchorRect.top + window.scrollY - 200
  const left = Math.max(8, anchorRect.left + window.scrollX + anchorRect.width / 2 - 120)
  const dateStr = date ? date.slice(0, 10).split('-').reverse().join('/') : '—'

  return createPortal(
    <div ref={ref} style={{
      position: 'absolute', top, left, width: 240, zIndex: 60,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
      padding: '14px 16px',
      boxShadow: '0 10px 25px -5px rgba(15,23,42,0.18), 0 4px 10px -4px rgba(15,23,42,0.08)',
      fontSize: 12,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8, color: '#64748b' }}>
        <span>{stationName} · {materialTitle}</span>
        <strong style={{ color: '#0f172a' }}>{dateStr}</strong>
      </div>
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: 8, background: '#fafbfc', borderRadius: 6, marginBottom: 10,
      }}>
        <span style={{ color: '#64748b' }}>Regra: {currentRuleValue}×/dia</span>
      </div>
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: '#475569' }}>Override:</span>
        <button onClick={() => setValue(v => Math.max(0, v - 1))} style={stepperBtn}>−</button>
        <input type="number" min="0" max="100" value={value}
          onChange={e => setValue(Number(e.target.value))}
          style={{
            width: 56, padding: '5px 8px', border: '1px solid #e2e8f0',
            borderRadius: 6, textAlign: 'center', fontWeight: 700, color: '#b45309',
            background: '#fef9c3', fontFamily: 'inherit',
          }} />
        <button onClick={() => setValue(v => Math.min(100, v + 1))} style={stepperBtn}>+</button>
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        {currentOverrideValue != null && (
          <button onClick={onRevert} style={{
            flex: 1, padding: 6, borderRadius: 6, fontSize: 11, fontWeight: 600,
            background: '#fafbfc', color: '#475569', border: '1px solid #e2e8f0', cursor: 'pointer',
          }}>↺ Voltar à regra</button>
        )}
        <button onClick={() => onApply(value)} className="btn btn-primary btn-sm" style={{ flex: 1 }}>
          Aplicar
        </button>
      </div>
    </div>,
    document.body
  )
}

const stepperBtn = {
  width: 24, height: 24, borderRadius: 6, border: '1px solid #e2e8f0',
  background: '#fff', fontWeight: 700, cursor: 'pointer', color: '#475569',
}
