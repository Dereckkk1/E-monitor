import { useState, useEffect, useRef, useMemo } from 'react'
import { createPortal } from 'react-dom'

/**
 * Inline popover for setting per-cell override (count + time window).
 *
 * Props:
 *  - open: bool
 *  - anchorRect: DOMRect | null
 *  - onClose: () => void
 *  - onApply: (newValue, timeStart, timeEnd, applyToOthers) => void | Promise<void>
 *  - onRevert: () => void
 *  - currentRuleValue: number       — what the rule(s) would produce
 *  - currentOverrideValue?: number | null
 *  - currentRuleWindows: Array<{time_start, time_end}>  — every applicable rule's window
 *  - currentOverrideWindow?: {time_start, time_end} | null
 *  - lastUsedWindow?: {time_start, time_end} | null
 *  - materialTitle: string
 *  - stationName: string
 *  - date: ISO string
 */
export default function OverridePopover({
  open, anchorRect, onClose, onApply, onRevert,
  currentRuleValue, currentOverrideValue = null,
  currentRuleWindows = [],
  currentOverrideWindow = null,
  lastUsedWindow = null,
  materialTitle, stationName, date,
  zIndex = 60,
  allowReplicate = true,
}) {
  // Default window resolution (priority order — first match wins):
  //   1. existing override → use its window
  //   2. exactly 1 applicable rule → use its window
  //   3. multiple rules → no default, force the user to pick via chip
  //   4. no rules + lastUsedWindow → use last
  //   5. fallback 06:00–22:00
  const defaultWindow = useMemo(() => {
    if (currentOverrideWindow) return currentOverrideWindow
    if (currentRuleWindows.length === 1) return currentRuleWindows[0]
    if (currentRuleWindows.length > 1)   return null
    if (lastUsedWindow)                  return lastUsedWindow
    return { time_start: '06:00', time_end: '22:00' }
  }, [currentOverrideWindow, currentRuleWindows, lastUsedWindow])

  const [value, setValue] = useState(currentOverrideValue ?? currentRuleValue ?? 0)
  const [timeStart, setTimeStart] = useState(defaultWindow?.time_start ?? '')
  const [timeEnd,   setTimeEnd]   = useState(defaultWindow?.time_end   ?? '')
  const [applyToOthers, setApplyToOthers] = useState(false)
  const ref = useRef(null)

  const ruleSpan = currentRuleWindows.length === 1 ? currentRuleWindows[0] : null
  const isNarrower = ruleSpan && /^[0-2]\d:[0-5]\d$/.test(timeStart) && /^[0-2]\d:[0-5]\d$/.test(timeEnd)
    && (timeStart > ruleSpan.time_start || timeEnd < ruleSpan.time_end)

  // Reset state when popover re-opens or context changes.
  useEffect(() => {
    setValue(currentOverrideValue ?? currentRuleValue ?? 0)
    setTimeStart(defaultWindow?.time_start ?? '')
    setTimeEnd(defaultWindow?.time_end ?? '')
    setApplyToOthers(false)
  }, [currentOverrideValue, currentRuleValue, defaultWindow, open])

  // Close on outside click / Esc.
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

  // Abre acima da âncora quando há espaço (grid do DistributionStep); senão
  // abaixo (ex.: botão no header do modal, colado no topo do viewport — sem o
  // fallback o top ia fortemente negativo e o popover sumia acima da tela).
  const POPOVER_H = 320
  const openAbove = anchorRect.top >= POPOVER_H + 12
  const top = openAbove
    ? anchorRect.top + window.scrollY - POPOVER_H
    : anchorRect.bottom + window.scrollY + 8
  const left = Math.max(8, anchorRect.left + window.scrollX + anchorRect.width / 2 - 150)
  const dateStr = date ? date.slice(0, 10).split('-').reverse().join('/') : '—'
  const multiRule = currentRuleWindows.length > 1 && !currentOverrideWindow

  // Validation: HH:MM with leading zero, time_end > time_start.
  const validRange = /^[0-2]\d:[0-5]\d$/.test(timeStart)
                  && /^[0-2]\d:[0-5]\d$/.test(timeEnd)
                  && timeEnd > timeStart
  // count=0 ainda precisa de faixa válida porque o backend força NOT NULL.
  const canApply = validRange

  return createPortal(
    <div ref={ref} style={{
      position: 'absolute', top, left, width: 300, zIndex,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
      padding: '14px 16px',
      boxShadow: '0 10px 25px -5px rgba(15,23,42,0.18), 0 4px 10px -4px rgba(15,23,42,0.08)',
      fontSize: 12,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8, color: '#64748b' }}>
        <span>{stationName} · {materialTitle}</span>
        <strong style={{ color: '#0f172a' }}>{dateStr}</strong>
      </div>

      {/* Contexto: o que as rules dizem */}
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: 8, background: '#fafbfc', borderRadius: 6, marginBottom: 10,
      }}>
        <span style={{ color: '#64748b' }}>Regra: {currentRuleValue}×/dia</span>
      </div>

      {/* Alerta multi-rule + chips */}
      {multiRule && (
        <div style={{
          padding: 8, marginBottom: 10, background: '#fef3c7',
          border: '1px solid #fcd34d', borderRadius: 6, color: '#78350f',
        }}>
          <div style={{ marginBottom: 6, fontWeight: 600 }}>
            ⚠ Esta célula tem {currentRuleWindows.length} regras:
          </div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 6 }}>
            {currentRuleWindows.map((w, i) => (
              <button
                key={i}
                type="button"
                onClick={() => { setTimeStart(w.time_start); setTimeEnd(w.time_end) }}
                style={{
                  padding: '3px 8px', borderRadius: 999,
                  border: '1px solid #fcd34d', background: '#fff',
                  cursor: 'pointer', fontSize: 11, color: '#78350f',
                }}
              >
                {w.time_start}–{w.time_end}
              </button>
            ))}
          </div>
          <div style={{ fontSize: 11 }}>
            Definir override substituirá todas neste dia.
          </div>
        </div>
      )}

      {/* Count stepper */}
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: '#475569' }}>Veiculações:</span>
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

      {/* Time window inputs (disabled when count=0) */}
      <div style={{ marginBottom: 10, opacity: value === 0 ? 0.5 : 1 }}>
        <div style={{ fontSize: 11, color: '#475569', marginBottom: 4 }}>
          {value === 0
            ? 'Faixa horária (inativa quando 0 veiculações):'
            : 'Faixa horária:'}
        </div>
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <input type="time" value={timeStart} onChange={e => setTimeStart(e.target.value)}
                 disabled={value === 0} style={timeInputStyle} />
          <span style={{ color: '#64748b' }}>–</span>
          <input type="time" value={timeEnd} onChange={e => setTimeEnd(e.target.value)}
                 disabled={value === 0} style={timeInputStyle} />
        </div>
        {!validRange && timeStart && timeEnd && (
          <div style={{ fontSize: 11, color: '#b91c1c', marginTop: 4 }}>
            Faixa inválida: fim deve ser maior que início.
          </div>
        )}
      </div>

      {/* Replication checkbox — oculto quando o chamador edita UMA célula só
          (ex.: modal do dia). applyToOthers fica no default false, então não
          renderizar já basta pra não replicar silenciosamente. */}
      {allowReplicate && (
        <label style={{
          display: 'flex', gap: 6, alignItems: 'flex-start',
          marginBottom: 12, cursor: 'pointer', fontSize: 11, color: '#475569',
        }}>
          <input type="checkbox" checked={applyToOthers}
                 onChange={e => setApplyToOthers(e.target.checked)}
                 style={{ marginTop: 2 }} />
          <span>
            Aplicar essa mesma faixa nas demais células deste tipo neste mês
            <div style={{ fontSize: 10, color: '#94a3b8', marginTop: 2 }}>
              (afeta apenas células que já têm override)
            </div>
          </span>
        </label>
      )}

      {isNarrower && (
        <div style={{
          padding: 8, marginBottom: 10, background: '#fffbeb',
          border: '1px solid #fde68a', borderRadius: 6, color: '#92400e', fontSize: 11, lineHeight: 1.4,
        }}>
          ⚠ Faixa mais estreita que a regra ({ruleSpan.time_start}–{ruleSpan.time_end}) —
          veiculações fora dela contam como fora do prazo.
        </div>
      )}

      <div style={{ display: 'flex', gap: 6 }}>
        {currentOverrideValue != null && (
          <button onClick={onRevert} style={{
            flex: 1, padding: 6, borderRadius: 6, fontSize: 11, fontWeight: 600,
            background: '#fafbfc', color: '#475569', border: '1px solid #e2e8f0', cursor: 'pointer',
          }}>↺ Voltar à regra</button>
        )}
        <button
          onClick={() => onApply(value, timeStart, timeEnd, applyToOthers)}
          disabled={!canApply}
          className="btn btn-primary btn-sm"
          style={{ flex: 1, opacity: canApply ? 1 : 0.5, cursor: canApply ? 'pointer' : 'not-allowed' }}
        >
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

const timeInputStyle = {
  flex: 1, padding: '5px 8px', border: '1px solid #e2e8f0',
  borderRadius: 6, fontFamily: 'inherit', fontSize: 12, color: '#0f172a',
}
