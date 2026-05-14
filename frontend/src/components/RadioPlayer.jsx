import { useCallback, useEffect, useRef, useState } from 'react'
import StationAvatar from './StationAvatar'
import { useRadioPlayer } from '../contexts/RadioPlayerContext'

const STORAGE_KEY = 'radioPlayerPosition'

function readSavedPosition() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const pos = JSON.parse(raw)
    if (typeof pos?.x === 'number' && typeof pos?.y === 'number') return pos
  } catch { /* ignore corrupt JSON */ }
  return null
}

// ── Icons (inline SVG, Phosphor-style) ───────────────────────────

function IcPlay() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
      <path d="M8 5v14l11-7L8 5z" />
    </svg>
  )
}
function IcPause() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
      <rect x="6" y="5" width="4" height="14" rx="1" />
      <rect x="14" y="5" width="4" height="14" rx="1" />
    </svg>
  )
}
function IcVolume() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" stroke="none" />
      <path d="M15.54 8.46a5 5 0 0 1 0 7.07" />
      <path d="M19.07 4.93a10 10 0 0 1 0 14.14" />
    </svg>
  )
}
function IcMute() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" stroke="none" />
      <line x1="22" y1="9" x2="16" y2="15" />
      <line x1="16" y1="9" x2="22" y2="15" />
    </svg>
  )
}
function IcClose() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
      <path d="M4 4l8 8M12 4l-8 8" />
    </svg>
  )
}
function IcDrag() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <circle cx="6" cy="4" r="1.2" />
      <circle cx="10" cy="4" r="1.2" />
      <circle cx="6" cy="8" r="1.2" />
      <circle cx="10" cy="8" r="1.2" />
      <circle cx="6" cy="12" r="1.2" />
      <circle cx="10" cy="12" r="1.2" />
    </svg>
  )
}

/**
 * Floating radio player. Ported from E-radios marketplace (RadioPlayer
 * component) — same drag/volume/error logic, adapted to Radiocheck's
 * design tokens and inline SVG icon set.
 *
 * Mounted once at the App root inside <RadioPlayerProvider>. Becomes
 * visible when any caller fires `toggleStation({ url, name, logo })`.
 */
export default function RadioPlayer() {
  const { currentStation, stopStation } = useRadioPlayer()
  const audioRef = useRef(null)
  const playerRef = useRef(null)

  const [isPlaying, setIsPlaying] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [hasError, setHasError] = useState(false)
  const [volume, setVolume] = useState(0.8)
  const [isMuted, setIsMuted] = useState(false)
  const [showVolume, setShowVolume] = useState(false)

  // Drag state — same algorithm as E-radios: mousedown anchors the offset,
  // mousemove updates position (clamped to the viewport), mouseup persists.
  const [position, setPosition] = useState(readSavedPosition)
  const dragState = useRef({
    dragging: false, startX: 0, startY: 0,
    startLeft: 0, startTop: 0, moved: false,
  })

  const clampPosition = useCallback((x, y) => {
    const el = playerRef.current
    if (!el) return { x, y }
    const rect = el.getBoundingClientRect()
    const maxX = window.innerWidth - rect.width
    const maxY = window.innerHeight - rect.height
    return {
      x: Math.max(0, Math.min(x, maxX)),
      y: Math.max(0, Math.min(y, maxY)),
    }
  }, [])

  const onDragStart = useCallback((clientX, clientY) => {
    const el = playerRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    dragState.current = {
      dragging: true,
      startX: clientX, startY: clientY,
      startLeft: rect.left, startTop: rect.top,
      moved: false,
    }
  }, [])

  const onDragMove = useCallback((clientX, clientY) => {
    const ds = dragState.current
    if (!ds.dragging) return
    const dx = clientX - ds.startX
    const dy = clientY - ds.startY
    if (Math.abs(dx) > 3 || Math.abs(dy) > 3) ds.moved = true
    if (!ds.moved) return
    setPosition(clampPosition(ds.startLeft + dx, ds.startTop + dy))
  }, [clampPosition])

  const onDragEnd = useCallback(() => {
    dragState.current.dragging = false
    setPosition(prev => {
      if (prev) {
        try { localStorage.setItem(STORAGE_KEY, JSON.stringify(prev)) } catch { /* private mode */ }
      }
      return prev
    })
  }, [])

  // Mouse + touch event wiring
  useEffect(() => {
    const onMouseMove = (e) => onDragMove(e.clientX, e.clientY)
    const onMouseUp = () => onDragEnd()
    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
    return () => {
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
    }
  }, [onDragMove, onDragEnd])

  useEffect(() => {
    const onTouchMove = (e) => {
      if (!dragState.current.dragging) return
      const t = e.touches[0]
      onDragMove(t.clientX, t.clientY)
    }
    const onTouchEnd = () => onDragEnd()
    window.addEventListener('touchmove', onTouchMove, { passive: true })
    window.addEventListener('touchend', onTouchEnd)
    return () => {
      window.removeEventListener('touchmove', onTouchMove)
      window.removeEventListener('touchend', onTouchEnd)
    }
  }, [onDragMove, onDragEnd])

  // Re-clamp on resize so the player doesn't drift off-screen when the
  // window shrinks (e.g. opening devtools).
  useEffect(() => {
    const onResize = () => {
      setPosition(prev => prev ? clampPosition(prev.x, prev.y) : prev)
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [clampPosition])

  function handleMouseDown(e) {
    e.preventDefault()
    onDragStart(e.clientX, e.clientY)
  }
  function handleTouchStart(e) {
    const t = e.touches[0]
    onDragStart(t.clientX, t.clientY)
  }

  const streamingUrl = currentStation?.url
  const broadcasterName = currentStation?.name
  const broadcasterLogo = currentStation?.logo

  function handlePlayToggle() {
    const audio = audioRef.current
    if (!audio) return
    if (isPlaying) {
      audio.pause()
      setIsPlaying(false)
      return
    }
    setIsLoading(true)
    setHasError(false)
    audio.load()
    audio.play().then(() => {
      setIsPlaying(true)
      setIsLoading(false)
    }).catch((err) => {
      if (err.name !== 'AbortError') {
        setHasError(true)
        setIsLoading(false)
      }
    })
  }

  function handleVolumeChange(e) {
    const v = parseFloat(e.target.value)
    setVolume(v)
    if (audioRef.current) audioRef.current.volume = v
    if (v === 0) setIsMuted(true)
    else if (isMuted) setIsMuted(false)
  }

  function toggleMute() {
    if (!audioRef.current) return
    if (isMuted) {
      audioRef.current.volume = volume || 0.8
      setIsMuted(false)
    } else {
      audioRef.current.volume = 0
      setIsMuted(true)
    }
  }

  function handleClose() {
    if (audioRef.current) {
      audioRef.current.pause()
      audioRef.current.src = ''
    }
    stopStation()
  }

  // Autoplay on station change. Wires audio events for status display
  // (loading / playing / error / waiting / pause).
  useEffect(() => {
    const audio = audioRef.current
    if (!audio || !streamingUrl) return

    audio.volume = volume

    const onCanPlay  = () => { setIsLoading(false); setHasError(false) }
    const onPlaying  = () => { setIsPlaying(true);  setIsLoading(false) }
    const onError    = () => { setHasError(true);   setIsLoading(false); setIsPlaying(false) }
    const onWaiting  = () => setIsLoading(true)
    const onPause    = () => setIsPlaying(false)

    audio.addEventListener('canplay', onCanPlay)
    audio.addEventListener('playing', onPlaying)
    audio.addEventListener('error',   onError)
    audio.addEventListener('waiting', onWaiting)
    audio.addEventListener('pause',   onPause)

    setIsLoading(true)
    setHasError(false)
    audio.src = streamingUrl
    audio.load()
    audio.play().catch((err) => {
      if (err.name !== 'AbortError') {
        setHasError(true)
        setIsLoading(false)
      }
    })

    return () => {
      audio.removeEventListener('canplay', onCanPlay)
      audio.removeEventListener('playing', onPlaying)
      audio.removeEventListener('error',   onError)
      audio.removeEventListener('waiting', onWaiting)
      audio.removeEventListener('pause',   onPause)
      audio.pause()
      audio.src = ''
    }
    // volume is intentionally not in deps — we set it on mount; the
    // dedicated handleVolumeChange covers live changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [streamingUrl])

  if (!currentStation) return null

  const positionStyle = position
    ? { left: position.x, top: position.y, bottom: 'auto', right: 'auto' }
    : {}

  const statusText = hasError ? 'Erro' : isLoading ? 'Conectando…' : isPlaying ? 'Ao vivo' : 'Pausado'

  return (
    <div
      ref={playerRef}
      className={'radio-player' + (position ? ' radio-player--dragged' : '')}
      style={positionStyle}
      role="dialog"
      aria-label={`Player ${broadcasterName ?? ''}`}
    >
      <audio ref={audioRef} preload="none" />

      {isPlaying && <div className="radio-player-live-bar" aria-hidden />}

      <div className="radio-player-content">
        {/* Drag handle — only this region triggers movement. */}
        <button
          type="button"
          className="radio-player-drag"
          onMouseDown={handleMouseDown}
          onTouchStart={handleTouchStart}
          title="Arraste pra mover"
          aria-label="Arrastar player"
        >
          <IcDrag />
        </button>

        <div className={'radio-player-logo' + (isPlaying ? ' radio-player-logo--playing' : '')}>
          <StationAvatar
            station={{ name: broadcasterName ?? '?', logo_url: broadcasterLogo ?? null }}
            size={36}
          />
        </div>

        <div className="radio-player-info">
          <span className="radio-player-name" title={broadcasterName}>{broadcasterName}</span>
          <span className={'radio-player-status' + (hasError ? ' is-error' : isPlaying ? ' is-live' : '')}>
            {isPlaying && <span className="radio-player-status-dot" aria-hidden />}
            {statusText}
          </span>
        </div>

        <div className="radio-player-controls">
          <button
            type="button"
            className={'radio-player-play' + (isPlaying ? ' is-playing' : '')}
            onClick={handlePlayToggle}
            disabled={hasError}
            title={isPlaying ? 'Pausar' : 'Reproduzir'}
            aria-label={isPlaying ? 'Pausar' : 'Reproduzir'}
          >
            {isLoading ? <span className="radio-player-spinner" /> : isPlaying ? <IcPause /> : <IcPlay />}
          </button>

          <div
            className="radio-player-volume"
            onMouseEnter={() => setShowVolume(true)}
            onMouseLeave={() => setShowVolume(false)}
          >
            <button
              type="button"
              className="radio-player-volume-btn"
              onClick={toggleMute}
              title={isMuted ? 'Ativar som' : 'Mutar'}
              aria-label={isMuted ? 'Ativar som' : 'Mutar'}
            >
              {isMuted ? <IcMute /> : <IcVolume />}
            </button>
            <div className={'radio-player-volume-popover' + (showVolume ? ' is-open' : '')}>
              <input
                type="range"
                min="0" max="1" step="0.05"
                value={isMuted ? 0 : volume}
                onChange={handleVolumeChange}
                className="radio-player-volume-slider"
                aria-label="Volume"
              />
            </div>
          </div>

          <button
            type="button"
            className="radio-player-close"
            onClick={handleClose}
            title="Fechar player"
            aria-label="Fechar player"
          >
            <IcClose />
          </button>
        </div>
      </div>
    </div>
  )
}
