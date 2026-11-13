// RecordingEditorPage.jsx — /admin/gravacoes/:id/editar.
//
// Marca trechos de uma gravação e exporta em MP3 (um por vez ou todos num .zip).
// O corte e a forma de onda são do servidor; aqui só se desenha, toca e manda os
// tempos. Ver docs/features/recording-clip-editor.md.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import {
  fetchRecordingAudio, fetchRecordingClip, fetchRecordingClipsZip, useRecording, useRecordingPeaks,
} from '../api/hooks'
import { useAlert } from '../components/ConfirmModal'
import FlowEmptyState from '../components/FlowEmptyState'
import { WaveDetail, WaveOverview } from '../components/RecordingWaveform'
import StationAvatar from '../components/StationAvatar'
import {
  audioStartMs, clockAt, formatClock, formatOffset, formatSpan, gapMarkers, gapsInside, loadClips, makeClip,
  offsetAt, parseClockInput, saveClips, updateClip, zipName,
} from '../utils/recordingEditor'
import { errorMessage, recordingStation, statusInfo, windowLabel } from '../utils/recordings'
import './RecordingEditorPage.css'

const MIN_SPAN = 5 // s: zoom máximo
const NUDGE = 0.1 // s: ajuste fino das pontas
const NEW_CLIP_BACK = 30 // s: "O" sem "I" fecha um trecho dos últimos 30s

function IconPlay() {
  return <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><path d="M3.5 2.5V11.5L11 7L3.5 2.5Z" fill="currentColor" /></svg>
}
function IconPause() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true">
      <rect x="3.5" y="2.5" width="2.4" height="9" rx="0.6" fill="currentColor" />
      <rect x="8.1" y="2.5" width="2.4" height="9" rx="0.6" fill="currentColor" />
    </svg>
  )
}
function IconDownload() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M7 2V9M7 9L4 6M7 9L10 6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M2.5 11H11.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}
function IconTrash() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
      <path d="M2 3.5H12M5.5 3.5V2.5C5.5 2.22 5.72 2 6 2H8C8.28 2 8.5 2.22 8.5 2.5V3.5M5.5 6.5V10.5M8.5 6.5V10.5M3 3.5L3.5 11.5C3.5 11.78 3.72 12 4 12H10C10.28 12 10.5 11.78 10.5 11.5L11 3.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function saveBlob(blob, filename) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Revogar na hora pode abortar o download antes de começar.
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}

function isTyping(el) {
  const tag = el?.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el?.isContentEditable
}

export default function RecordingEditorPage() {
  const { id } = useParams()
  const alert = useAlert()
  const { data: rec, isLoading, isError, error } = useRecording(id)
  const peaksQ = useRecordingPeaks(id, Boolean(rec?.has_audio))

  const audioRef = useRef(null)
  const [audioUrl, setAudioUrl] = useState('')
  const [audioProgress, setAudioProgress] = useState(0)
  const [audioError, setAudioError] = useState('')
  const [audioDuration, setAudioDuration] = useState(0)

  const peaks = peaksQ.data?.peaks
  const pps = peaksQ.data?.pps ?? 20
  // O player manda; enquanto o áudio baixa, vale a duração dos picos.
  const duration = audioDuration || (peaks ? peaks.length / pps : 0)

  // null = ainda não mexeu: a visão é a gravação inteira.
  const [viewState, setViewState] = useState(null)
  const view = useMemo(() => viewState ?? { start: 0, end: duration }, [viewState, duration])
  const setView = useCallback(u => {
    setViewState(prev => (typeof u === 'function' ? u(prev ?? { start: 0, end: duration }) : u))
  }, [duration])
  const [playhead, setPlayhead] = useState(0)
  const [playing, setPlaying] = useState(false)
  const playUntil = useRef(null)
  const [clips, setClips] = useState(() => loadClips(id))
  const [selectedId, setSelectedId] = useState(null)
  const [markIn, setMarkIn] = useState(null)
  const [fade, setFade] = useState(true)
  const [busy, setBusy] = useState({}) // id do trecho (ou 'zip') → exportando
  const [goTo, setGoTo] = useState('')

  const audioStart = useMemo(() => (rec && duration ? audioStartMs(rec, duration) : 0), [rec, duration])
  const gapMarks = useMemo(() => (rec && duration ? gapMarkers(rec, audioStart) : []), [rec, audioStart, duration])
  const clockOf = useCallback(t => clockAt(rec, t, audioStart), [rec, audioStart])

  useEffect(() => { saveClips(id, clips) }, [id, clips])

  // Baixa o áudio para tocar. A onda chega antes (é pequena), então já dá para marcar.
  useEffect(() => {
    if (!rec?.has_audio) return undefined
    const ctrl = new AbortController()
    let url = ''
    fetchRecordingAudio(rec.id, { signal: ctrl.signal, onProgress: p => setAudioProgress(p ?? 0) })
      .then(blob => { url = URL.createObjectURL(blob); setAudioUrl(url) })
      .catch(err => { if (!ctrl.signal.aborted) setAudioError(errorMessage(err)) })
    return () => { ctrl.abort(); if (url) URL.revokeObjectURL(url) }
  }, [rec?.id, rec?.has_audio])

  // Agulha acompanha o player; para no fim do trecho quando tocando um trecho.
  useEffect(() => {
    if (!playing) return undefined
    let raf = 0
    const tick = () => {
      const a = audioRef.current
      if (a) {
        const t = a.currentTime
        if (playUntil.current != null && t >= playUntil.current) {
          a.pause()
          a.currentTime = playUntil.current
          playUntil.current = null
          setPlaying(false)
          setPlayhead(a.currentTime)
          return
        }
        setPlayhead(t)
        // Tocando fora da tela, a visão vira a página.
        setView(v => {
          const span = v.end - v.start
          if (t < v.start || t > v.end) {
            const start = Math.max(0, Math.min(duration - span, t - span * 0.05))
            return { start, end: start + span }
          }
          return v
        })
      }
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [playing, duration, setView])

  const seek = useCallback(t => {
    const x = Math.max(0, Math.min(duration, t))
    setPlayhead(x)
    playUntil.current = null
    if (audioRef.current && audioUrl) audioRef.current.currentTime = x
  }, [duration, audioUrl])

  const togglePlay = useCallback(() => {
    const a = audioRef.current
    if (!a || !audioUrl) return
    if (playing) {
      a.pause()
      setPlaying(false)
      return
    }
    if (Math.abs(a.currentTime - playhead) > 0.05) a.currentTime = playhead
    a.play().then(() => setPlaying(true)).catch(() => setPlaying(false))
  }, [playing, playhead, audioUrl])

  const playClip = useCallback(c => {
    const a = audioRef.current
    if (!a || !audioUrl) return
    a.currentTime = c.from
    setPlayhead(c.from)
    playUntil.current = c.to
    setSelectedId(c.id)
    a.play().then(() => setPlaying(true)).catch(() => setPlaying(false))
  }, [audioUrl])

  const zoom = useCallback((factor, center) => {
    setView(v => {
      const old = v.end - v.start
      const span = Math.max(Math.min(MIN_SPAN, duration), Math.min(duration, old * factor))
      const c = center ?? (v.start + v.end) / 2
      let start = c - ((c - v.start) / old) * span
      start = Math.max(0, Math.min(duration - span, start))
      return { start, end: start + span }
    })
  }, [duration, setView])

  const pan = useCallback(delta => {
    setView(v => {
      const span = v.end - v.start
      const start = Math.max(0, Math.min(duration - span, v.start + delta))
      return { start, end: start + span }
    })
  }, [duration, setView])

  const centerOn = useCallback(t => {
    setView(v => {
      const span = v.end - v.start
      const start = Math.max(0, Math.min(duration - span, t - span / 2))
      return { start, end: start + span }
    })
  }, [duration, setView])

  const addClip = useCallback((from, to) => {
    if (to - from < 0.5) return
    const c = makeClip(rec, from, to, duration, audioStart)
    setClips(cs => [...cs, c].sort((a, b) => a.from - b.from))
    setSelectedId(c.id)
  }, [rec, duration, audioStart])

  const patchClip = useCallback((clipId, patch) => {
    setClips(cs => cs.map(c => (c.id === clipId ? updateClip(rec, c, patch, duration, audioStart) : c)))
  }, [rec, duration, audioStart])

  const removeClip = useCallback(clipId => {
    setClips(cs => cs.filter(c => c.id !== clipId))
    setSelectedId(s => (s === clipId ? null : s))
  }, [])

  // Atalhos. Não valem enquanto se digita (nome do trecho, "ir para").
  useEffect(() => {
    function onKey(e) {
      if (isTyping(e.target) || e.ctrlKey || e.metaKey || e.altKey) return
      const sel = clips.find(c => c.id === selectedId)
      switch (e.key) {
        case ' ':
          e.preventDefault()
          togglePlay()
          break
        case 'i': case 'I':
          if (sel) patchClip(sel.id, { from: playhead })
          else setMarkIn(playhead)
          break
        case 'o': case 'O':
          if (sel) patchClip(sel.id, { to: playhead })
          else {
            addClip(markIn ?? Math.max(0, playhead - NEW_CLIP_BACK), playhead)
            setMarkIn(null)
          }
          break
        case 'ArrowLeft': case 'ArrowRight': {
          e.preventDefault()
          const step = (e.shiftKey ? 5 : 1) * (e.key === 'ArrowLeft' ? -1 : 1)
          const t = Math.max(0, Math.min(duration, playhead + step))
          seek(t)
          if (t < view.start || t > view.end) centerOn(t)
          break
        }
        case '+': case '=':
          zoom(0.5, playhead)
          break
        case '-': case '_':
          zoom(2, playhead)
          break
        case '0':
          setView({ start: 0, end: duration })
          break
        case 'Delete': case 'Backspace':
          if (sel) { e.preventDefault(); removeClip(sel.id) }
          break
        case 'Escape':
          setSelectedId(null)
          setMarkIn(null)
          break
        default:
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [clips, selectedId, playhead, markIn, duration, view, togglePlay, patchClip, addClip, removeClip, seek, zoom, centerOn, setView])

  const onResize = useCallback((clipId, edge, t) => patchClip(clipId, { [edge]: t }), [patchClip])

  const clockLabel = useCallback((t, step) => {
    const s = formatClock(clockOf(t))
    return step >= 60 ? s.slice(0, 5) : s
  }, [clockOf])

  function jumpTo(e) {
    e.preventDefault()
    const ms = parseClockInput(rec, goTo)
    if (ms == null) return
    const t = Math.min(duration, offsetAt(rec, ms, audioStart))
    seek(t)
    // Zoom de 2 minutos em volta: o "ir para" é para achar um ponto, não para ver tudo.
    const span = Math.min(duration, Math.max(MIN_SPAN, Math.min(view.end - view.start, 120)))
    const start = Math.max(0, Math.min(duration - span, t - span / 2))
    setView({ start, end: start + span })
  }

  async function exportOne(c) {
    setBusy(b => ({ ...b, [c.id]: true }))
    try {
      const blob = await fetchRecordingClip(rec.id, { from: c.from, to: c.to, fade, name: c.name })
      saveBlob(blob, `${c.name}.mp3`)
    } catch (err) {
      await alert(errorMessage(err))
    } finally {
      setBusy(b => ({ ...b, [c.id]: false }))
    }
  }

  async function exportAll() {
    setBusy(b => ({ ...b, zip: true }))
    try {
      const name = zipName(rec)
      const blob = await fetchRecordingClipsZip(rec.id, { clips, fade, name })
      saveBlob(blob, `${name}.zip`)
    } catch (err) {
      await alert(errorMessage(err))
    } finally {
      setBusy(b => ({ ...b, zip: false }))
    }
  }

  const back = <Link to="/admin/gravacoes" className="re-back">← Gravações</Link>

  if (isLoading) return <div>{back}<p className="re-muted">Carregando…</p></div>
  if (isError || !rec || !rec.has_audio) {
    const notFound = error?.response?.status === 404
    return (
      <div>
        {back}
        <FlowEmptyState
          tone="mute"
          title={notFound ? 'Gravação não encontrada' : !rec ? 'Não foi possível abrir a gravação' : 'Esta gravação não tem áudio'}
          description={rec?.status === 'expirada' ? 'O áudio passou dos 15 dias e foi apagado.' : 'Volte para a lista e escolha uma gravação com áudio.'}
          actions={<Link to="/admin/gravacoes" className="btn btn-secondary">Voltar para Gravações</Link>}
        />
      </div>
    )
  }

  const info = statusInfo(rec.status)
  const station = recordingStation(rec)
  const ready = Boolean(audioUrl)
  const totalSelected = clips.reduce((s, c) => s + (c.to - c.from), 0)

  return (
    <div className="re-page">
      {back}
      <div className="page-header re-header">
        <div className="re-title">
          <h1>Editar trechos</h1>
          <div className="re-identity">
            <StationAvatar station={station} size={32} />
            <div>
              <div className="re-identity-name">{station.name}</div>
              <div className="re-identity-sub">{[station.sub, windowLabel(rec)].filter(Boolean).join(' · ')}</div>
            </div>
            <span className={`badge ${info.badge}`}>{info.label}</span>
          </div>
        </div>
      </div>

      <div className="re-toolbar">
        <button
          type="button"
          className="btn btn-primary re-play"
          onClick={togglePlay}
          disabled={!ready}
          title={playing ? 'Pausar (espaço)' : 'Tocar (espaço)'}
          aria-label={playing ? 'Pausar' : 'Tocar'}
        >
          {playing ? <IconPause /> : <IconPlay />}
        </button>
        <div className="re-now" aria-live="off">
          <span className="re-now-clock" title="Hora do ar aproximada (o stream atrasa em relação ao ar, e a posição dos buracos tem erro de até 1 minuto)">
            {duration > 0 ? formatClock(clockOf(playhead)) : '--:--:--'} <span className="re-approx">≈</span>
          </span>
          <span className="re-now-offset">{formatOffset(playhead)} / {formatOffset(duration)}</span>
        </div>
        <form className="re-goto" onSubmit={jumpTo}>
          <label htmlFor="re-goto">Ir para</label>
          <input id="re-goto" className="flow-input" placeholder="14:30" value={goTo} onChange={e => setGoTo(e.target.value)} inputMode="numeric" />
        </form>
        <div className="re-zoom" role="group" aria-label="Zoom">
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => zoom(2)} title="Menos zoom (-)">−</button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => zoom(0.5)} title="Mais zoom (+)">+</button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => setView({ start: 0, end: duration })} title="Ver tudo (0)">Tudo</button>
        </div>
        {!ready && !audioError && (
          <div className="re-loading" title="O áudio vem inteiro para tocar; dá para marcar trechos enquanto isso">
            <span className="re-loading-bar"><span style={{ transform: `scaleX(${audioProgress})` }} /></span>
            Baixando áudio {audioProgress ? `${Math.round(audioProgress * 100)}%` : '…'}
          </div>
        )}
        {audioError && <span className="re-error-inline">Áudio: {audioError}</span>}
      </div>

      {peaksQ.isLoading && <div className="re-wave-loading">Preparando a forma de onda… gravações longas levam alguns segundos na primeira vez.</div>}
      {peaksQ.isError && <p className="re-error-inline">Não foi possível montar a forma de onda: {errorMessage(peaksQ.error)}</p>}
      {peaks && duration > 0 && view.end > 0 && (
        <div className="re-wave">
          <WaveOverview
            peaks={peaks} pps={pps} duration={duration} view={view} onViewChange={setView}
            clips={clips} playhead={playhead} gapMarks={gapMarks}
          />
          <WaveDetail
            peaks={peaks} pps={pps} duration={duration} view={view} onZoom={zoom} onPan={pan}
            clips={clips} selectedId={selectedId} onSelect={setSelectedId} onSeek={seek} onCreate={addClip}
            onResize={onResize} playhead={playhead} markIn={markIn} gapMarks={gapMarks} clockLabel={clockLabel}
          />
          <p className="re-help">
            Arraste na onda para marcar um trecho · clique para posicionar · roda do mouse dá zoom (Shift + roda anda) ·
            <kbd>espaço</kbd> toca · <kbd>I</kbd>/<kbd>O</kbd> marcam início/fim · <kbd>←</kbd><kbd>→</kbd> 1s (Shift 5s) ·
            <kbd>Del</kbd> apaga o trecho selecionado
          </p>
        </div>
      )}

      <section className="re-clips" aria-label="Trechos">
        <div className="re-clips-head">
          <h2>Trechos {clips.length > 0 && <span className="re-count">{clips.length} · {formatSpan(totalSelected)}</span>}</h2>
          <label className="re-fade">
            <input type="checkbox" checked={fade} onChange={e => setFade(e.target.checked)} />
            Fade curto nas pontas
          </label>
          <button
            type="button"
            className="btn btn-primary"
            onClick={exportAll}
            disabled={clips.length === 0 || busy.zip}
          >
            {busy.zip ? 'Gerando .zip…' : 'Baixar todos (.zip)'}
          </button>
        </div>

        {clips.length === 0 ? (
          <p className="re-muted re-clips-empty">
            Nenhum trecho ainda. Arraste sobre a forma de onda, ou toque e use <kbd>I</kbd> e <kbd>O</kbd> para marcar início e fim.
          </p>
        ) : (
          <ol className="re-clip-list">
            {clips.map((c, i) => {
              const crossing = gapsInside(gapMarks, c.from, c.to)
              return (
                <li
                  key={c.id}
                  className={`re-clip${c.id === selectedId ? ' re-clip--sel' : ''}`}
                  onClick={() => setSelectedId(c.id)}
                >
                  <span className="re-clip-n">{i + 1}</span>
                  <div className="re-clip-main">
                    <input
                      className="re-clip-name"
                      value={c.name}
                      onChange={e => patchClip(c.id, { name: e.target.value })}
                      onBlur={e => { if (!e.target.value.trim()) patchClip(c.id, { name: '' }) }}
                      aria-label={`Nome do trecho ${i + 1}`}
                    />
                    <div className="re-clip-times">
                      <EdgeControl
                        label="Início" t={c.from} clock={clockOf(c.from)}
                        onNudge={d => patchClip(c.id, { from: c.from + d })}
                        onJump={() => { seek(c.from); centerOn(c.from) }}
                      />
                      <span className="re-arrow">→</span>
                      <EdgeControl
                        label="Fim" t={c.to} clock={clockOf(c.to)}
                        onNudge={d => patchClip(c.id, { to: c.to + d })}
                        onJump={() => { seek(c.to); centerOn(c.to) }}
                      />
                      <span className="re-clip-len">{formatSpan(c.to - c.from)}</span>
                    </div>
                    {crossing.length > 0 && (
                      <p className="re-clip-warn">
                        Contém {crossing.length === 1 ? 'um buraco' : `${crossing.length} buracos`} de{' '}
                        {crossing.map(g => `${g.seconds}s`).join(', ')}: o áudio emenda ali, falta esse tempo do ar.
                      </p>
                    )}
                  </div>
                  <div className="re-clip-actions">
                    <button type="button" className="btn btn-icon btn-sm" onClick={e => { e.stopPropagation(); playClip(c) }} disabled={!ready} title="Ouvir o trecho" aria-label="Ouvir o trecho">
                      <IconPlay />
                    </button>
                    <button type="button" className="btn btn-icon btn-sm" onClick={e => { e.stopPropagation(); exportOne(c) }} disabled={busy[c.id]} title="Baixar MP3" aria-label="Baixar MP3">
                      <IconDownload />
                    </button>
                    <button type="button" className="btn btn-icon btn-danger-ghost btn-sm" onClick={e => { e.stopPropagation(); removeClip(c.id) }} title="Excluir trecho" aria-label="Excluir trecho">
                      <IconTrash />
                    </button>
                  </div>
                </li>
              )
            })}
          </ol>
        )}
      </section>

      {audioUrl && (
        <audio
          ref={audioRef}
          src={audioUrl}
          preload="auto"
          onLoadedMetadata={e => { if (Number.isFinite(e.currentTarget.duration)) setAudioDuration(e.currentTarget.duration) }}
          onEnded={() => { setPlaying(false); playUntil.current = null }}
          onPause={() => setPlaying(false)}
        >
          <track kind="captions" />
        </audio>
      )}
    </div>
  )
}

function EdgeControl({ label, t, clock, onNudge, onJump }) {
  return (
    <span className="re-edge">
      <button type="button" className="re-nudge" onClick={e => { e.stopPropagation(); onNudge(-NUDGE) }} title={`${label} 0,1s antes`} aria-label={`${label} 0,1s antes`}>‹</button>
      <button type="button" className="re-edge-time" onClick={e => { e.stopPropagation(); onJump() }} title={`${label}: ir para este ponto (${formatOffset(t)} no arquivo)`}>
        {formatClock(clock)}
      </button>
      <button type="button" className="re-nudge" onClick={e => { e.stopPropagation(); onNudge(NUDGE) }} title={`${label} 0,1s depois`} aria-label={`${label} 0,1s depois`}>›</button>
    </span>
  )
}
