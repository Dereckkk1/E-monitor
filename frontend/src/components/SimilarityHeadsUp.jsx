import { useEffect, useRef } from 'react'
import SimilarityTimeline from './SimilarityTimeline'

/**
 * SimilarityHeadsUp — aviso NÃO-bloqueante de sobreposição parcial (25–50%),
 * mostrado no upload. Diferente do SimilarityWarningModal:
 *  - severidade "informação" (azul), não "decisão" (âmbar);
 *  - UMA ação ("Entendi, seguir") que sempre prossegue (nunca remove);
 *  - ESC e clique no backdrop fecham normalmente.
 *
 * Props:
 *  - newMaterial: material recém-subido (carrega similarity_segments + title).
 *  - similarMaterial: o material existente mais parecido (title).
 *  - onContinue(): segue o fluxo (vincula o material).
 */
export default function SimilarityHeadsUp({ newMaterial, similarMaterial, onContinue }) {
  const headlineRef = useRef(null)
  const seg = newMaterial.similarity_segments
  const pct = Math.round(((seg?.own_cov ?? newMaterial.similarity_score) ?? 0) * 100)

  // ESC fecha (não-bloqueante) — captura na fase de captura pra ganhar de
  // handlers globais.
  useEffect(() => {
    function onKeyDown(e) {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onContinue?.() }
    }
    window.addEventListener('keydown', onKeyDown, true)
    return () => window.removeEventListener('keydown', onKeyDown, true)
  }, [onContinue])

  useEffect(() => { headlineRef.current?.focus() }, [])

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="sim-headsup-headline"
      onClick={onContinue}
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(6,5,91,0.52)',
        backdropFilter: 'blur(8px)', WebkitBackdropFilter: 'blur(8px)',
        zIndex: 80,
        display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24,
        animation: 'sim-hu-fade 200ms cubic-bezier(0.16,1,0.3,1)',
      }}
    >
      <style>{`
        @keyframes sim-hu-fade { from { opacity: 0; } to { opacity: 1; } }
        @keyframes sim-hu-rise { from { transform: translateY(8px); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
      `}</style>

      <div
        onClick={e => e.stopPropagation()}
        style={{
          background: 'var(--c-surface)',
          borderRadius: 'var(--radius-lg)',
          boxShadow: '0 30px 60px -15px rgba(6,5,91,0.42), 0 12px 24px -8px rgba(6,5,91,0.16)',
          width: 'min(680px, 100%)',
          maxHeight: 'calc(100vh - 48px)',
          display: 'flex', flexDirection: 'column', overflow: 'hidden',
          animation: 'sim-hu-rise 280ms cubic-bezier(0.16,1,0.3,1)',
        }}
      >
        <header style={{ padding: '26px 30px 18px' }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 7,
            padding: '5px 11px', borderRadius: 'var(--radius-full)',
            background: '#e7f0ff', color: '#1d5fd0',
            fontSize: 10.5, fontWeight: 800, letterSpacing: '0.13em', textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 16v-4M12 8h.01" />
            </svg>
            Só pra te avisar
          </div>

          <h2
            ref={headlineRef}
            tabIndex={-1}
            id="sim-headsup-headline"
            style={{
              margin: '13px 0 0', fontFamily: 'var(--font-heading)', fontWeight: 700,
              fontSize: 21, lineHeight: 1.28, letterSpacing: '-0.01em',
              color: 'var(--c-text)', outline: 'none',
            }}
          >
            <span style={{ color: '#1d5fd0' }}>{pct}%</span> do material novo é igual a um já existente
          </h2>

          <p style={{ margin: '9px 0 0', color: 'var(--c-text-2)', fontSize: 13.5, lineHeight: 1.55, maxWidth: 560 }}>
            Pode ser proposital — uma abertura, vinheta ou encerramento compartilhado.
            Não trava nada; é só pra você saber antes de seguir.
          </p>
        </header>

        <div style={{ padding: '4px 30px 22px' }}>
          <SimilarityTimeline
            newTitle={newMaterial.title}
            otherTitle={similarMaterial.title}
            data={seg}
          />
        </div>

        <footer style={{
          padding: '16px 30px 22px', background: 'var(--c-bg)',
          borderTop: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12,
        }}>
          <span style={{ fontSize: 11.5, color: 'var(--c-text-3)', fontWeight: 500 }}>
            Aviso informativo — não bloqueia o upload.
          </span>
          <ContinueButton onClick={onContinue} />
        </footer>
      </div>
    </div>
  )
}

function ContinueButton({ onClick }) {
  return (
    <button
      type="button"
      onClick={onClick}
      autoFocus
      style={{
        padding: '10px 20px', borderRadius: 'var(--radius-md)',
        background: 'var(--c-action)', color: '#fff',
        border: '1.5px solid var(--c-action)',
        fontSize: 13, fontWeight: 700, fontFamily: 'var(--font-heading)',
        cursor: 'pointer', minWidth: 150, letterSpacing: '0.01em',
        boxShadow: '0 1px 2px rgba(232,30,117,0.18)',
        transition: 'all 100ms cubic-bezier(0.16,1,0.3,1)',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.background = 'var(--c-action-600, #C4185E)'
        e.currentTarget.style.transform = 'translateY(-1px)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.background = 'var(--c-action)'
        e.currentTarget.style.transform = 'translateY(0)'
      }}
    >
      Entendi, seguir
    </button>
  )
}
