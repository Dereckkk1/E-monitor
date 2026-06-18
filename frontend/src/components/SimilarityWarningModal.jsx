import { useEffect, useRef, useState } from 'react'
import { useDeleteMaterial, useAcknowledgeSimilarity } from '../api/hooks'
import api from '../api/client'
import SimilarityTimeline from './SimilarityTimeline'

/**
 * Blocking decision modal. Fires when a freshly-uploaded material is ≥50%
 * similar to another material of the same client.
 *
 * Hard rules:
 *  - No X button.
 *  - Clicking the backdrop does nothing.
 *  - ESC is captured and ignored.
 *  - The only exits are "Manter os dois" (ack) and "Remover material novo"
 *    (delete). One of those MUST be chosen before the operator can proceed.
 *
 * Lifecycle callbacks (BOTH required):
 *  - onKept(material)    → fires AFTER ack succeeds. Parent should continue
 *                          the upload flow (typically: link material to campaign).
 *  - onRemoved(material) → fires AFTER delete succeeds. Parent should skip
 *                          linking and treat the upload as cancelled.
 */
export default function SimilarityWarningModal({
  newMaterial,
  similarMaterial,
  onKept,
  onRemoved,
}) {
  const del = useDeleteMaterial()
  const ack = useAcknowledgeSimilarity()
  const busy = del.isPending || ack.isPending
  const headlineRef = useRef(null)

  // Headline = % do material novo que é igual (own_cov). Cai no score legado
  // (max) se o backend ainda não trouxe segmentos.
  const seg = newMaterial.similarity_segments
  const pct = Math.round(((seg?.own_cov ?? newMaterial.similarity_score) ?? 0) * 100)

  // Capture ESC and prevent any global handler from closing this modal.
  useEffect(() => {
    function onKeyDown(e) {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
      }
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [])

  // Focus the headline for screen readers when the modal opens. Avoids
  // focus landing on whichever button hovers under the cursor.
  useEffect(() => { headlineRef.current?.focus() }, [])

  async function handleRemove() {
    if (busy) return
    await del.mutateAsync(newMaterial.id)
    onRemoved?.(newMaterial)
  }

  async function handleKeep() {
    if (busy) return
    await ack.mutateAsync(newMaterial.id)
    onKept?.(newMaterial)
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="similarity-modal-headline"
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(6,5,91,0.62)',  // navy-tinted scrim, not pure black
        backdropFilter: 'blur(8px)',
        WebkitBackdropFilter: 'blur(8px)',
        zIndex: 80,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: 24,
        animation: 'sim-modal-fade-in 200ms cubic-bezier(0.16,1,0.3,1)',
      }}
      // Intentional: NO onClick handler on the backdrop. Clicking outside
      // does nothing — the operator must decide.
    >
      <style>{`
        @keyframes sim-modal-fade-in {
          from { opacity: 0; }
          to   { opacity: 1; }
        }
        @keyframes sim-modal-rise {
          from { transform: translateY(8px); opacity: 0; }
          to   { transform: translateY(0);   opacity: 1; }
        }
      `}</style>

      <div
        style={{
          background: 'var(--c-surface)',
          borderRadius: 'var(--radius-lg)',
          boxShadow: '0 30px 60px -15px rgba(6,5,91,0.45), 0 12px 24px -8px rgba(6,5,91,0.18)',
          width: 'min(720px, 100%)',
          maxHeight: 'calc(100vh - 48px)',
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
          animation: 'sim-modal-rise 280ms cubic-bezier(0.16,1,0.3,1)',
        }}
      >
        {/* ── Header zone (generous breathing) ────────────────────── */}
        <header style={{ padding: '28px 32px 22px' }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 8,
            padding: '5px 11px',
            background: '#fef3c7',
            color: '#92400e',
            borderRadius: 'var(--radius-full)',
            fontSize: 10.5, fontWeight: 700, letterSpacing: '0.14em',
            textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 9v4" />
              <path d="M12 17h.01" />
              <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            </svg>
            Decisão necessária
          </div>

          <h2
            ref={headlineRef}
            tabIndex={-1}
            id="similarity-modal-headline"
            style={{
              margin: '14px 0 0',
              fontFamily: 'var(--font-heading)',
              fontWeight: 700,
              fontSize: 22,
              lineHeight: 1.25,
              letterSpacing: '-0.01em',
              color: 'var(--c-text)',
              outline: 'none',
            }}
          >
            Esse material é <span style={{ color: 'var(--c-action)' }}>{pct}%</span> idêntico a um já existente
          </h2>

          <p style={{
            margin: '10px 0 0',
            color: 'var(--c-text-2)',
            fontSize: 13.5,
            lineHeight: 1.55,
            maxWidth: 580,
          }}>
            Manter os dois faz a mesma veiculação ser contabilizada duas vezes nos
            relatórios. Compare os áudios e decida — você não vai conseguir
            seguir sem responder.
          </p>
        </header>

        {/* ── Timeline: onde os dois batem ────────────────────────── */}
        {seg && (
          <div style={{ padding: '2px 32px 18px' }}>
            <SimilarityTimeline
              newTitle={newMaterial.title}
              otherTitle={similarMaterial.title}
              data={seg}
            />
          </div>
        )}

        {/* ── Body: side-by-side comparison cards ─────────────────── */}
        <div style={{
          padding: '4px 32px 24px',
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          gap: 14,
          alignItems: 'stretch',
        }}>
          <ComparisonCard
            tag="material novo"
            tagColor="var(--c-action)"
            highlight
            title={newMaterial.title}
            durationSeconds={newMaterial.duration_seconds}
            materialId={newMaterial.id}
          />
          <ComparisonCard
            tag="já existente"
            tagColor="var(--c-text-2)"
            title={similarMaterial.title}
            durationSeconds={similarMaterial.duration_seconds}
            materialId={similarMaterial.id}
          />
        </div>

        {/* ── Footer: decision row ────────────────────────────────── */}
        <footer style={{
          padding: '18px 32px 24px',
          background: 'var(--c-bg)',
          borderTop: '1px solid var(--c-border)',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 12,
          flexWrap: 'wrap',
        }}>
          <span style={{
            fontSize: 11.5,
            color: 'var(--c-text-3)',
            fontWeight: 500,
          }}>
            Você não pode fechar sem decidir.
          </span>
          <div style={{ display: 'flex', gap: 10 }}>
            <DecisionButton
              variant="danger-outline"
              onClick={handleRemove}
              disabled={busy}
              loading={del.isPending}
            >
              {del.isPending ? 'Removendo…' : 'Remover material novo'}
            </DecisionButton>
            <DecisionButton
              variant="primary"
              onClick={handleKeep}
              disabled={busy}
              loading={ack.isPending}
            >
              {ack.isPending ? 'Salvando…' : 'Manter os dois'}
            </DecisionButton>
          </div>
        </footer>
      </div>
    </div>
  )
}

/* ─────────────────────────────────────────────────────────────────── */

function ComparisonCard({ tag, tagColor, highlight, title, durationSeconds, materialId }) {
  // The <audio> element cannot send Authorization headers directly, and
  // /v1/internal/materials/{id}/audio is behind the JWT-gated middleware.
  // We fetch the blob via the axios client (which injects the token) and
  // hand the player an object URL. Revoked on unmount to avoid leaks.
  const [blobUrl, setBlobUrl] = useState(null)
  const [loadError, setLoadError] = useState(null)

  useEffect(() => {
    let cancelled = false
    let createdUrl = null
    setLoadError(null)
    setBlobUrl(null)

    api.get(`/materials/${materialId}/audio`, { responseType: 'blob' })
      .then(resp => {
        if (cancelled) return
        createdUrl = URL.createObjectURL(resp.data)
        setBlobUrl(createdUrl)
      })
      .catch(err => {
        if (cancelled) return
        setLoadError(err?.response?.status === 404 ? 'Áudio indisponível' : 'Falha ao carregar')
      })

    return () => {
      cancelled = true
      if (createdUrl) URL.revokeObjectURL(createdUrl)
    }
  }, [materialId])

  return (
    <div style={{
      background: 'var(--c-surface)',
      border: `1.5px solid ${highlight ? 'var(--c-action-300, #F472B6)' : 'var(--c-border)'}`,
      borderRadius: 'var(--radius-md)',
      padding: 14,
      display: 'flex', flexDirection: 'column', gap: 10,
      boxShadow: highlight ? '0 0 0 4px color-mix(in srgb, var(--c-action) 8%, transparent)' : 'none',
    }}>
      <div style={{
        display: 'inline-flex', alignSelf: 'flex-start',
        padding: '3px 9px',
        background: highlight ? 'var(--c-action)' : 'var(--c-surface-2)',
        color: highlight ? '#fff' : tagColor,
        borderRadius: 'var(--radius-full)',
        fontSize: 9.5, fontWeight: 700, letterSpacing: '0.14em',
        textTransform: 'uppercase',
        fontFamily: 'var(--font-heading)',
      }}>{tag}</div>

      <div style={{
        fontSize: 14, fontWeight: 700,
        fontFamily: 'var(--font-heading)',
        color: 'var(--c-text)',
        lineHeight: 1.35,
        // Two-line clamp without losing context
        display: '-webkit-box',
        WebkitLineClamp: 2,
        WebkitBoxOrient: 'vertical',
        overflow: 'hidden',
        minHeight: 36,
      }}>{title}</div>

      <div style={{
        display: 'flex', alignItems: 'center', gap: 6,
        fontSize: 11, color: 'var(--c-text-3)',
      }}>
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
        {durationSeconds?.toFixed?.(1) ?? '—'}s
      </div>

      {loadError ? (
        <div style={{
          padding: '10px 12px',
          background: 'color-mix(in srgb, var(--c-danger) 8%, transparent)',
          border: '1px dashed color-mix(in srgb, var(--c-danger) 35%, transparent)',
          borderRadius: 'var(--radius-sm)',
          color: 'var(--c-danger)',
          fontSize: 11.5, fontWeight: 600,
          textAlign: 'center',
        }}>
          {loadError}
        </div>
      ) : blobUrl ? (
        <audio
          controls
          preload="metadata"
          src={blobUrl}
          style={{ width: '100%', marginTop: 2 }}
        />
      ) : (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '10px 12px',
          background: 'var(--c-surface-2)',
          borderRadius: 'var(--radius-sm)',
          color: 'var(--c-text-3)',
          fontSize: 11.5,
        }}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{
            animation: 'spin 0.8s linear infinite',
          }}>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
            <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </svg>
          Carregando áudio…
        </div>
      )}
    </div>
  )
}

/* ─────────────────────────────────────────────────────────────────── */

function DecisionButton({ variant, onClick, disabled, loading, children }) {
  const base = {
    padding: '10px 18px',
    borderRadius: 'var(--radius-md)',
    fontSize: 13,
    fontWeight: 700,
    fontFamily: 'var(--font-heading)',
    cursor: disabled ? 'not-allowed' : 'pointer',
    transition: 'all 100ms cubic-bezier(0.16,1,0.3,1)',
    display: 'inline-flex', alignItems: 'center', gap: 8,
    minWidth: 170,
    justifyContent: 'center',
    letterSpacing: '0.01em',
  }
  const stylesByVariant = {
    primary: {
      background: 'var(--c-action)',
      color: '#fff',
      border: '1.5px solid var(--c-action)',
      boxShadow: disabled ? 'none' : '0 1px 2px rgba(232,30,117,0.18)',
    },
    'danger-outline': {
      background: 'transparent',
      color: 'var(--c-danger)',
      border: '1.5px solid color-mix(in srgb, var(--c-danger) 35%, transparent)',
    },
  }
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      style={{
        ...base,
        ...stylesByVariant[variant],
        opacity: disabled && !loading ? 0.45 : 1,
      }}
      onMouseEnter={e => {
        if (disabled) return
        if (variant === 'primary') {
          e.currentTarget.style.background = 'var(--c-action-600, #C4185E)'
          e.currentTarget.style.transform = 'translateY(-1px)'
        } else {
          e.currentTarget.style.background = 'color-mix(in srgb, var(--c-danger) 10%, transparent)'
          e.currentTarget.style.borderColor = 'var(--c-danger)'
        }
      }}
      onMouseLeave={e => {
        if (variant === 'primary') {
          e.currentTarget.style.background = 'var(--c-action)'
          e.currentTarget.style.transform = 'translateY(0)'
        } else {
          e.currentTarget.style.background = 'transparent'
          e.currentTarget.style.borderColor = 'color-mix(in srgb, var(--c-danger) 35%, transparent)'
        }
      }}
    >
      {loading && (
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" style={{
          animation: 'spin 0.8s linear infinite',
        }}>
          <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2.5" strokeOpacity="0.25" />
          <path d="M21 12a9 9 0 0 1-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
      )}
      {children}
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </button>
  )
}
