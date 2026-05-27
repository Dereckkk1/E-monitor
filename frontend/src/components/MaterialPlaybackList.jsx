import { useState, useRef, useEffect } from 'react'
import api from '../api/client'
import TypeIconPill from './TypeIconPill'

// Duration in seconds → "12.3s" | "—"
function fmtDuration(secs) {
  if (secs == null) return '—'
  return `${Number(secs).toFixed(1)}s`
}

const FP_BADGE = {
  ready:      { label: 'Pronto',   bg: '#dcfce7', fg: 'var(--c-success)', dot: 'var(--c-success)' },
  generating: { label: 'Gerando',  bg: '#fef9c3', fg: '#a16207',          dot: '#ca8a04' },
  pending:    { label: 'Pendente', bg: 'var(--c-surface-2)', fg: 'var(--c-text-2)', dot: 'var(--c-text-3)' },
  failed:     { label: 'Falhou',   bg: '#fee2e2', fg: 'var(--c-danger)',  dot: 'var(--c-danger)' },
}

/**
 * Read-only playable list of a campaign's materials, grouped by type.
 * Lists EVERY material the campaign has — programmed or not — so the user
 * can hear what's attached and see at a glance which ones are scheduled.
 *
 * Props:
 *  - materials: Array<Material>  (hydrated; id, title, type_id,
 *               duration_seconds, fingerprint_status, master_storage_path)
 *  - typeById: Record<typeId, {id, name, color}>
 *  - programmedTypeIds: Set<typeId>  (types present in any distribution rule)
 */
export default function MaterialPlaybackList({ materials, typeById, programmedTypeIds }) {
  const [playingId, setPlayingId] = useState(null)

  // Group by type, sort groups by type name; untyped last.
  const groups = (() => {
    const m = new Map()
    for (const mat of materials) {
      const key = mat.type_id ?? '__none__'
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(mat)
    }
    const entries = [...m.entries()].map(([typeId, list]) => ({
      typeId,
      type: typeId === '__none__' ? null : typeById[typeId] ?? null,
      list: list.slice().sort((a, b) => (a.title ?? '').localeCompare(b.title ?? '')),
    }))
    entries.sort((a, b) => {
      if (a.typeId === '__none__') return 1
      if (b.typeId === '__none__') return -1
      return (a.type?.name ?? '').localeCompare(b.type?.name ?? '')
    })
    return entries
  })()

  const programmedCount = materials.filter(
    m => m.type_id && programmedTypeIds.has(m.type_id),
  ).length

  return (
    <div style={{
      background: 'var(--c-surface)',
      border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-md)',
      overflow: 'hidden',
      marginBottom: 14,
      boxShadow: 'var(--shadow-sm)',
    }}>
      {/* Header / counter */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '13px 16px', borderBottom: '1px solid var(--c-border)',
        background: 'var(--c-bg)',
      }}>
        <span style={{
          width: 28, height: 28, borderRadius: 'var(--radius-md)',
          background: 'color-mix(in srgb, var(--c-action) 12%, transparent)',
          color: 'var(--c-action)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" />
          </svg>
        </span>
        <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--c-text)', fontFamily: 'var(--font-heading)', letterSpacing: '-0.01em' }}>
          Materiais da campanha
        </span>
        <span style={{
          marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 8,
          fontSize: 11.5, color: 'var(--c-text-2)', fontVariantNumeric: 'tabular-nums',
        }}>
          <strong style={{ color: 'var(--c-text)', fontWeight: 700 }}>{materials.length}</strong>
          {materials.length === 1 ? 'material' : 'materiais'}
          <span style={{ color: 'var(--c-text-3)' }}>·</span>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--c-success)' }} />
            {programmedCount} programad{programmedCount === 1 ? 'o' : 'os'}
          </span>
        </span>
      </div>

      {/* Grouped rows */}
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        {groups.map(({ typeId, type, list }) => (
          <div key={typeId}>
            <div style={{
              padding: '8px 16px', background: 'var(--c-bg)',
              borderBottom: '1px solid var(--c-border)',
              display: 'flex', alignItems: 'center', gap: 9,
              fontSize: 10.5, fontWeight: 700, letterSpacing: '0.05em',
              textTransform: 'uppercase', color: 'var(--c-text-2)',
              fontFamily: 'var(--font-heading)',
            }}>
              <TypeIconPill color={type?.color ?? '#94a3b8'} />
              {type?.name ?? 'Sem tipo'}
              <span style={{ color: 'var(--c-text-3)', fontWeight: 500, letterSpacing: 0, textTransform: 'none' }}>
                {list.length}
              </span>
            </div>
            {list.map(mat => (
              <MaterialRow
                key={mat.id}
                material={mat}
                programmed={!!(mat.type_id && programmedTypeIds.has(mat.type_id))}
                isPlaying={playingId === mat.id}
                onPlay={() => setPlayingId(mat.id)}
                onPause={() => setPlayingId(null)}
              />
            ))}
          </div>
        ))}
      </div>
      <style>{`@keyframes mpl-spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}

function MaterialRow({ material, programmed, isPlaying, onPlay, onPause }) {
  const [audioBlobUrl, setAudioBlobUrl] = useState(null)
  const [audioLoading, setAudioLoading] = useState(false)
  const [downloadLoading, setDownloadLoading] = useState(false)
  const [hovered, setHovered] = useState(false)
  const audioRef = useRef(null)

  useEffect(() => () => {
    if (audioBlobUrl) URL.revokeObjectURL(audioBlobUrl)
  }, [audioBlobUrl])

  async function fetchAudioBlob() {
    if (audioBlobUrl) return audioBlobUrl
    const resp = await api.get(`/materials/${material.id}/audio`, { responseType: 'blob' })
    const url = URL.createObjectURL(resp.data)
    setAudioBlobUrl(url)
    return url
  }

  async function togglePlay() {
    if (isPlaying) { onPause(); return }
    if (audioLoading) return
    setAudioLoading(true)
    try {
      await fetchAudioBlob()
      onPlay()
    } catch {
      window.alert('Não foi possível carregar o áudio.')
    } finally {
      setAudioLoading(false)
    }
  }

  async function handleDownload() {
    if (downloadLoading) return
    setDownloadLoading(true)
    try {
      const url = await fetchAudioBlob()
      const ext = material.master_storage_path?.split('.').pop() ?? 'mp3'
      const a = document.createElement('a')
      a.href = url
      a.download = `${material.title}.${ext}`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } catch {
      window.alert('Não foi possível baixar o áudio.')
    } finally {
      setDownloadLoading(false)
    }
  }

  // Drive the <audio> element from isPlaying.
  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    if (isPlaying && audioBlobUrl) {
      el.play().catch(() => onPause())
    } else {
      el.pause()
    }
  }, [isPlaying, audioBlobUrl, onPause])

  const fp = FP_BADGE[material.fingerprint_status] ?? FP_BADGE.pending

  return (
    <div
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        padding: '11px 16px',
        borderBottom: '1px solid var(--c-border)',
        display: 'flex', alignItems: 'center', gap: 14,
        background: isPlaying
          ? 'color-mix(in srgb, var(--c-action) 6%, var(--c-surface))'
          : hovered ? 'var(--c-bg)' : 'var(--c-surface)',
        transition: 'background 150ms ease',
      }}
    >
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13.5, fontWeight: 600, color: 'var(--c-text)',
          fontFamily: 'var(--font-heading)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>
          {material.title}
        </div>
        <div style={{ marginTop: 5, display: 'flex', alignItems: 'center', gap: 8, fontSize: 11, color: 'var(--c-text-2)', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, color: 'var(--c-text)', fontVariantNumeric: 'tabular-nums' }}>{fmtDuration(material.duration_seconds)}</span>
          <span style={{ color: 'var(--c-text-3)' }}>·</span>
          <span style={{
            padding: '2px 8px', borderRadius: 'var(--radius-full)',
            background: fp.bg, color: fp.fg, fontSize: 10, fontWeight: 700,
            display: 'inline-flex', alignItems: 'center', gap: 5,
          }}>
            <span style={{ width: 5, height: 5, borderRadius: '50%', background: fp.dot }} />
            {fp.label}
          </span>
        </div>
      </div>

      {/* Programmed badge */}
      <span style={{
        padding: '4px 11px', borderRadius: 'var(--radius-full)',
        fontSize: 10.5, fontWeight: 700, fontFamily: 'var(--font-heading)',
        letterSpacing: '0.02em', whiteSpace: 'nowrap',
        display: 'inline-flex', alignItems: 'center', gap: 6,
        background: programmed ? '#dcfce7' : 'var(--c-surface-2)',
        color: programmed ? 'var(--c-success)' : 'var(--c-text-3)',
      }}>
        <span style={{
          width: 6, height: 6, borderRadius: '50%',
          background: programmed ? 'var(--c-success)' : 'var(--c-text-3)',
        }} />
        {programmed ? 'programado' : 'sem programação'}
      </span>

      {/* Play */}
      <IconBtn onClick={togglePlay} disabled={audioLoading} isActive={isPlaying}
               title={isPlaying ? 'Pausar' : 'Ouvir material'}>
        {audioLoading ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ animation: 'mpl-spin 0.8s linear infinite' }}>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
            <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </svg>
        ) : isPlaying ? (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
            <rect x="3.5" y="3" width="3" height="10" rx="0.5" /><rect x="9.5" y="3" width="3" height="10" rx="0.5" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M4.5 2.5v11l9-5.5z" /></svg>
        )}
      </IconBtn>

      {/* Download */}
      <IconBtn onClick={handleDownload} disabled={downloadLoading} title="Baixar áudio">
        {downloadLoading ? (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{ animation: 'mpl-spin 0.8s linear infinite' }}>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
            <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <path d="M8 2v8M4.5 7L8 10.5 11.5 7" /><path d="M2.5 12.5h11" />
          </svg>
        )}
      </IconBtn>

      <audio ref={audioRef} src={audioBlobUrl ?? undefined} onEnded={onPause} style={{ display: 'none' }} />
    </div>
  )
}

function IconBtn({ onClick, disabled, isActive, title, children }) {
  const [hov, setHov] = useState(false)
  const active = isActive
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={title}
      onMouseEnter={() => setHov(true)}
      onMouseLeave={() => setHov(false)}
      style={{
        width: 32, height: 32, borderRadius: 'var(--radius-md)',
        background: active ? 'color-mix(in srgb, var(--c-action) 12%, transparent)' : 'transparent',
        border: `1px solid ${active || hov ? 'color-mix(in srgb, var(--c-action) 35%, transparent)' : 'var(--c-border)'}`,
        color: active || hov ? 'var(--c-action)' : 'var(--c-text-3)',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        transition: 'all 120ms ease',
      }}
    >
      {children}
    </button>
  )
}
