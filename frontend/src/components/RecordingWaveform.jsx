// RecordingWaveform.jsx — forma de onda do editor de trechos.
//
// Dois componentes:
//  - WaveOverview: a gravação inteira numa faixa baixa, com a janela de zoom
//    arrastável e os trechos marcados;
//  - WaveDetail: a parte em zoom, onde se clica (agulha), arrasta (trecho novo) e
//    puxa as alças dos trechos.
//
// A onda é desenhada em canvas a partir dos picos que o servidor calcula (6h de
// áudio não cabem decodificadas no navegador). Trechos, alças, buracos e agulha
// são DOM por cima: assim têm cursor, title e foco sem hit-test manual.
// Ver docs/features/recording-clip-editor.md.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { bucketPeaks, rulerStep } from '../utils/recordingEditor'

const RULER_H = 22
const DRAG_PX = 4

function cssVar(el, name, fallback) {
  const v = el ? getComputedStyle(el).getPropertyValue(name).trim() : ''
  return v || fallback
}

function useWidth(ref) {
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return undefined
    const ro = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)))
    ro.observe(el)
    setWidth(Math.floor(el.getBoundingClientRect().width))
    return () => ro.disconnect()
  }, [ref])
  return width
}

/** Desenha a onda espelhada no canvas, normalizada pelo pico da gravação inteira. */
function drawWave(canvas, { peaks, pps, start, end, width, height, top = 0, maxPeak, color }) {
  const dpr = window.devicePixelRatio || 1
  canvas.width = Math.max(1, Math.floor(width * dpr))
  canvas.height = Math.max(1, Math.floor(height * dpr))
  canvas.style.width = `${width}px`
  canvas.style.height = `${height}px`
  const ctx = canvas.getContext('2d')
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, width, height)
  const cols = bucketPeaks(peaks, pps, start, end, width)
  const waveH = height - top
  const mid = top + waveH / 2
  const scale = (waveH / 2 - 2) / Math.max(1, maxPeak)
  ctx.fillStyle = color
  for (let x = 0; x < cols.length; x++) {
    const h = Math.max(0.5, cols[x] * scale)
    ctx.fillRect(x, mid - h, 1, h * 2)
  }
  return ctx
}

function useMaxPeak(peaks) {
  return useMemo(() => {
    let m = 0
    if (peaks) for (let i = 0; i < peaks.length; i++) if (peaks[i] > m) m = peaks[i]
    return m
  }, [peaks])
}

export function WaveOverview({ peaks, pps, duration, view, onViewChange, clips, playhead, gapMarks }) {
  const wrapRef = useRef(null)
  const canvasRef = useRef(null)
  const width = useWidth(wrapRef)
  const maxPeak = useMaxPeak(peaks)
  const height = 44
  const drag = useRef(null)

  useEffect(() => {
    if (!canvasRef.current || width === 0 || duration <= 0) return
    drawWave(canvasRef.current, {
      peaks, pps, start: 0, end: duration, width, height, maxPeak,
      color: cssVar(wrapRef.current, '--c-text-3', '#8a8f98'),
    })
  }, [peaks, pps, duration, width, maxPeak])

  const px = t => (duration > 0 ? (t / duration) * width : 0)
  const span = view.end - view.start

  function tAt(clientX) {
    const r = wrapRef.current.getBoundingClientRect()
    return Math.max(0, Math.min(duration, ((clientX - r.left) / r.width) * duration))
  }

  // Clique centraliza a janela ali; arrastar a janela a desloca.
  function onPointerDown(e) {
    if (duration <= 0) return
    e.currentTarget.setPointerCapture(e.pointerId)
    const t = tAt(e.clientX)
    const inside = t >= view.start && t <= view.end
    drag.current = { offset: inside ? t - view.start : span / 2 }
    move(t)
  }
  function move(t) {
    const start = Math.max(0, Math.min(duration - span, t - drag.current.offset))
    onViewChange({ start, end: start + span })
  }
  function onPointerMove(e) {
    if (drag.current) move(tAt(e.clientX))
  }
  function onPointerUp() {
    drag.current = null
  }

  return (
    <div
      ref={wrapRef}
      className="rw-overview"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      role="presentation"
    >
      <canvas ref={canvasRef} />
      {clips.map(c => (
        <span key={c.id} className="rw-ov-clip" style={{ left: px(c.from), width: Math.max(2, px(c.to) - px(c.from)) }} />
      ))}
      {gapMarks.map(g => <span key={`g${g.t}`} className="rw-ov-gap" style={{ left: px(g.t) }} />)}
      <span className="rw-ov-view" style={{ left: px(view.start), width: Math.max(4, px(view.end) - px(view.start)) }} />
      <span className="rw-playhead rw-playhead--thin" style={{ left: px(playhead) }} />
    </div>
  )
}

export function WaveDetail({
  peaks, pps, duration, view, onZoom, onPan, clips, selectedId, onSelect, onSeek, onCreate, onResize,
  playhead, markIn, gapMarks, clockLabel,
}) {
  const wrapRef = useRef(null)
  const canvasRef = useRef(null)
  const width = useWidth(wrapRef)
  const maxPeak = useMaxPeak(peaks)
  const height = 180
  const drag = useRef(null)
  const [draft, setDraft] = useState(null)

  const span = Math.max(0.001, view.end - view.start)
  const px = t => ((t - view.start) / span) * width
  const visible = (a, b) => b >= view.start && a <= view.end

  useEffect(() => {
    if (!canvasRef.current || width === 0) return
    const el = wrapRef.current
    const ctx = drawWave(canvasRef.current, {
      peaks, pps, start: view.start, end: view.end, width, height, top: RULER_H, maxPeak,
      color: cssVar(el, '--c-action', '#3b5bdb'),
    })
    // Régua em hora do ar.
    const step = rulerStep(span, width)
    ctx.fillStyle = cssVar(el, '--c-text-3', '#8a8f98')
    ctx.strokeStyle = cssVar(el, '--c-border', '#d0d4da')
    ctx.font = '11px system-ui, sans-serif'
    ctx.textBaseline = 'top'
    ctx.beginPath()
    ctx.moveTo(0, RULER_H - 0.5)
    ctx.lineTo(width, RULER_H - 0.5)
    for (let t = Math.ceil(view.start / step) * step; t <= view.end; t += step) {
      const x = Math.round(px(t)) + 0.5
      ctx.moveTo(x, RULER_H - 7)
      ctx.lineTo(x, RULER_H)
      ctx.fillText(clockLabel(t, step), x + 3, 4)
    }
    ctx.stroke()
  // px depende de view/width, já nas dependências.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [peaks, pps, view.start, view.end, width, maxPeak, clockLabel])

  // Roda do mouse dá zoom em volta do cursor; Shift (ou rolagem lateral) desloca.
  // Listener nativo porque o onWheel do React é passivo e não deixa impedir a rolagem da página.
  useEffect(() => {
    const el = wrapRef.current
    if (!el) return undefined
    function onWheel(e) {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      const t = view.start + ((e.clientX - r.left) / r.width) * span
      if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
        onPan(((e.shiftKey ? e.deltaY : e.deltaX) / r.width) * span)
      } else {
        onZoom(e.deltaY > 0 ? 1.25 : 0.8, t)
      }
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [view.start, span, onZoom, onPan])

  function tAt(clientX) {
    const r = wrapRef.current.getBoundingClientRect()
    return Math.max(0, Math.min(duration, view.start + ((clientX - r.left) / r.width) * span))
  }

  function onPointerDown(e) {
    if (e.button !== 0 || duration <= 0) return
    const edge = e.target.dataset?.edge
    const clipId = e.target.closest?.('[data-clip]')?.dataset.clip
    e.currentTarget.setPointerCapture(e.pointerId)
    const t = tAt(e.clientX)
    if (edge && clipId) {
      drag.current = { mode: 'resize', clipId, edge }
      onSelect(clipId)
      return
    }
    drag.current = { mode: 'pending', x: e.clientX, t, clipId }
  }

  function onPointerMove(e) {
    const d = drag.current
    if (!d) return
    const t = tAt(e.clientX)
    if (d.mode === 'resize') {
      onResize(d.clipId, d.edge, t)
    } else if (d.mode === 'pending' && Math.abs(e.clientX - d.x) >= DRAG_PX) {
      d.mode = 'create'
    }
    if (d.mode === 'create') setDraft({ from: Math.min(d.t, t), to: Math.max(d.t, t) })
  }

  function onPointerUp(e) {
    const d = drag.current
    drag.current = null
    if (!d) return
    if (d.mode === 'create') {
      const t = tAt(e.clientX)
      setDraft(null)
      onCreate(Math.min(d.t, t), Math.max(d.t, t))
    } else if (d.mode === 'pending') {
      if (d.clipId) onSelect(d.clipId)
      else onSelect(null)
      onSeek(d.t)
    }
  }

  return (
    <div
      ref={wrapRef}
      className="rw-detail"
      style={{ height }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={() => { drag.current = null; setDraft(null) }}
      role="presentation"
    >
      <canvas ref={canvasRef} />
      {clips.filter(c => visible(c.from, c.to)).map(c => (
        <div
          key={c.id}
          data-clip={c.id}
          className={`rw-clip${c.id === selectedId ? ' rw-clip--sel' : ''}`}
          style={{ left: px(c.from), width: Math.max(1, px(c.to) - px(c.from)), top: RULER_H }}
          title={c.name}
        >
          <span className="rw-clip-label">{clips.indexOf(c) + 1}</span>
          <span className="rw-handle rw-handle--from" data-edge="from" title="Arraste para ajustar o início" />
          <span className="rw-handle rw-handle--to" data-edge="to" title="Arraste para ajustar o fim" />
        </div>
      ))}
      {draft && (
        <div className="rw-clip rw-clip--draft" style={{ left: px(draft.from), width: Math.max(1, px(draft.to) - px(draft.from)), top: RULER_H }} />
      )}
      {gapMarks.filter(g => visible(g.t, g.t)).map(g => (
        <div key={`g${g.t}`} className="rw-gap" style={{ left: px(g.t), top: RULER_H }} title={`Buraco: ${g.seconds}s sem áudio aqui (o arquivo emenda)`}>
          <span className="rw-gap-label">buraco {g.seconds}s</span>
        </div>
      ))}
      {markIn != null && visible(markIn, markIn) && (
        <span className="rw-markin" style={{ left: px(markIn), top: RULER_H }} title="Início marcado (I). Aperte O para fechar o trecho." />
      )}
      {visible(playhead, playhead) && <span className="rw-playhead" style={{ left: px(playhead) }} />}
    </div>
  )
}
