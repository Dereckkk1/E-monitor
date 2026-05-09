import { useState, useRef, useCallback, useMemo } from 'react'

// ── Resolution tiers ─────────────────────────────────────────────────────────
function blockMsForWindow(windowMs) {
  if (windowMs > 2 * 24 * 60 * 60 * 1000) return 60 * 60 * 1000        // >2d → 1h
  if (windowMs > 6 * 60 * 60 * 1000)       return 15 * 60 * 1000        // >6h → 15m
  return 60 * 1000                                                         // ≤6h → 1m
}

// ── Convert raw events into contiguous segments ───────────────────────────────
function eventsToSegments(events, periodStart, periodEnd) {
  if (!events || events.length === 0) {
    return [{ start: periodStart, end: periodEnd, type: 'up' }]
  }

  const sorted = [...events].sort((a, b) => new Date(a.event_at) - new Date(b.event_at))
  const segments = []
  let cursor = periodStart

  for (const ev of sorted) {
    const evAt = new Date(ev.event_at)
    if (evAt <= cursor) continue

    if (evAt > cursor) {
      segments.push({ start: cursor, end: evAt, type: 'up' })
    }

    if (ev.event_type === 'down') {
      const dur = ev.duration_seconds != null ? ev.duration_seconds * 1000 : 0
      const end = dur > 0 ? new Date(evAt.getTime() + dur) : evAt
      segments.push({ start: evAt, end: end > periodEnd ? periodEnd : end, type: 'down' })
      cursor = end > periodEnd ? periodEnd : end
    } else {
      cursor = evAt
    }
  }

  if (cursor < periodEnd) {
    segments.push({ start: cursor, end: periodEnd, type: 'up' })
  }

  return segments
}

function fmtDuration(ms) {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}min ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}min`
}

function fmtTimestamp(d) {
  return d.toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

export default function HealthTimeline({ events, periodStart, periodEnd, onZoomToEvent }) {
  const [viewStart, setViewStart] = useState(periodStart)
  const [viewEnd, setViewEnd]     = useState(periodEnd)
  const [tooltip, setTooltip]     = useState(null)
  const [drag, setDrag]           = useState(null)
  const containerRef = useRef(null)

  const resetView = useCallback(() => {
    setViewStart(periodStart)
    setViewEnd(periodEnd)
  }, [periodStart, periodEnd])

  const windowMs  = viewEnd - viewStart
  const blockMs   = blockMsForWindow(windowMs)
  const isZoomed  = viewStart !== periodStart || viewEnd !== periodEnd

  const segments = useMemo(
    () => eventsToSegments(events, new Date(periodStart), new Date(periodEnd)),
    [events, periodStart, periodEnd],
  )

  function xToMs(x) {
    const rect = containerRef.current.getBoundingClientRect()
    const ratio = (x - rect.left) / rect.width
    return viewStart + ratio * windowMs
  }

  function onMouseDown(e) {
    setDrag({ startX: e.clientX, startMs: xToMs(e.clientX), currentX: e.clientX })
  }

  function onMouseMove(e) {
    if (!drag) return
    setDrag(d => ({ ...d, currentX: e.clientX }))
  }

  function onMouseUp(e) {
    if (!drag) return
    const endMs = xToMs(e.clientX)
    const lo = Math.min(drag.startMs, endMs)
    const hi = Math.max(drag.startMs, endMs)
    const minWindow = 2 * 60 * 1000
    if (hi - lo > minWindow) {
      setViewStart(lo)
      setViewEnd(hi)
    }
    setDrag(null)
  }

  function onMouseLeave() {
    setDrag(null)
    setTooltip(null)
  }

  let selectionStyle = null
  if (drag && containerRef.current) {
    const rect  = containerRef.current.getBoundingClientRect()
    const x1    = Math.min(drag.startX, drag.currentX) - rect.left
    const x2    = Math.max(drag.startX, drag.currentX) - rect.left
    selectionStyle = {
      position: 'absolute', top: 0, bottom: 0,
      left: Math.max(0, x1), width: Math.min(x2, rect.width) - Math.max(0, x1),
      background: 'rgba(232, 30, 117, 0.12)',
      border: '1px solid rgba(232, 30, 117, 0.4)',
      borderRadius: 2, pointerEvents: 'none',
    }
  }

  const visibleBlocks = []
  const startMs  = viewStart
  const endMs    = viewEnd

  for (const seg of segments) {
    const segStart = Math.max(seg.start.getTime(), startMs)
    const segEnd   = Math.min(seg.end.getTime(), endMs)
    if (segEnd <= segStart) continue

    const leftPct  = ((segStart - startMs) / windowMs) * 100
    const widthPct = ((segEnd - segStart) / windowMs) * 100

    visibleBlocks.push({
      key: `${seg.type}-${segStart}`,
      type: seg.type,
      leftPct,
      widthPct,
      segStart: new Date(segStart),
      segEnd:   new Date(segEnd),
      durMs:    segEnd - segStart,
    })
  }

  const ticks = []
  const tickMs = blockMs * Math.ceil((windowMs / blockMs) / 8)
  let t = Math.ceil(viewStart / tickMs) * tickMs
  while (t <= viewEnd) {
    ticks.push({ ms: t, leftPct: ((t - viewStart) / windowMs) * 100 })
    t += tickMs
  }

  return (
    <div className="htl-wrap">
      <div
        ref={containerRef}
        className="htl-bar"
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={onMouseLeave}
        style={{ position: 'relative', userSelect: 'none' }}
      >
        {visibleBlocks.map(b => (
          <div
            key={b.key}
            className={`htl-seg htl-seg--${b.type}`}
            style={{ left: `${b.leftPct}%`, width: `${b.widthPct}%` }}
            onMouseEnter={e => {
              const label = b.type === 'down'
                ? `Offline · ${fmtTimestamp(b.segStart)} – ${fmtTimestamp(b.segEnd)} · ${fmtDuration(b.durMs)}`
                : b.type === 'up'
                ? `Online · ${fmtTimestamp(b.segStart)} · ${fmtDuration(b.durMs)}`
                : `Sem dados · ${fmtTimestamp(b.segStart)}`
              setTooltip({ x: e.clientX, y: e.clientY, content: label })
            }}
            onMouseLeave={() => setTooltip(null)}
          />
        ))}
        {selectionStyle && <div style={selectionStyle} />}
      </div>

      <div className="htl-axis">
        {ticks.map(tick => (
          <div key={tick.ms} className="htl-tick" style={{ left: `${tick.leftPct}%` }}>
            <span className="htl-tick-label">
              {new Date(tick.ms).toLocaleString('pt-BR', {
                day: '2-digit', month: '2-digit',
                hour: '2-digit', minute: '2-digit',
              })}
            </span>
          </div>
        ))}
      </div>

      <div className="htl-controls">
        {isZoomed && (
          <button className="htl-reset-btn" onClick={resetView} type="button">
            Resetar zoom
          </button>
        )}
        <span className="htl-resolution">
          {blockMs >= 3600000 ? '1h/bloco' : blockMs >= 900000 ? '15min/bloco' : '1min/bloco'}
        </span>
      </div>

      {tooltip && (
        <div
          className="htl-tooltip"
          style={{ left: tooltip.x + 12, top: tooltip.y - 36, position: 'fixed' }}
        >
          {tooltip.content}
        </div>
      )}
    </div>
  )
}
