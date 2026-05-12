import { useNavigate } from 'react-router-dom'
import WizardStepper from './WizardStepper'
import CampaignSummaryStrip from './CampaignSummaryStrip'

const STEP_META = {
  1: { eyebrow: 'Passo 1 de 4', kicker: 'Identificação' },
  2: { eyebrow: 'Passo 2 de 4', kicker: 'Onde vai tocar' },
  3: { eyebrow: 'Passo 3 de 4', kicker: 'O que vai tocar' },
  4: { eyebrow: 'Passo 4 de 4', kicker: 'Quando e quanto' },
}

/**
 * Shell of the 4-step campaign wizard.
 *
 * Props:
 *  - currentStep: 1..4
 *  - completedSteps: Array<number>
 *  - onStepClick: (step) => void
 *  - onPrev, onNext: handlers
 *  - nextDisabled: bool
 *  - nextLabel: string (default "Avançar →"; step 4 uses "Concluir campanha →")
 *  - prevLabel: string (default "← Voltar")
 *  - summary: props passed to CampaignSummaryStrip
 *  - title: string (campaign name in edit mode, "Nova campanha" otherwise)
 *  - children: ReactNode (step content)
 */
export default function WizardLayout({
  currentStep, completedSteps, onStepClick,
  onPrev, onNext,
  nextDisabled = false,
  nextLabel = 'Avançar →',
  prevLabel = '← Voltar',
  summary,
  title = 'Nova campanha',
  children,
}) {
  const navigate = useNavigate()
  const meta = STEP_META[currentStep] ?? STEP_META[1]
  const isLast = currentStep === 4

  return (
    <div style={{
      background: 'var(--c-bg)',
      minHeight: '100vh',
      display: 'flex',
      flexDirection: 'column',
    }}>
      {/* ── Header ── */}
      <header style={{
        background: 'var(--c-surface)',
        borderBottom: '1px solid var(--c-border)',
        padding: '20px 32px',
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        gap: 24,
      }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
          <span style={{
            fontSize: 10, fontWeight: 700,
            color: 'var(--c-action)',
            textTransform: 'uppercase', letterSpacing: '0.12em',
          }}>
            {meta.eyebrow} · {meta.kicker}
          </span>
          <h1 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 22, color: 'var(--c-text)', letterSpacing: '-0.01em',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {title}
          </h1>
        </div>
        <button
          onClick={() => navigate('/campaigns')}
          aria-label="Sair do wizard"
          title="Sair sem perder o progresso"
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '8px 14px', borderRadius: 'var(--radius-md)',
            background: 'transparent', color: 'var(--c-text-2)',
            border: '1px solid var(--c-border)',
            cursor: 'pointer', fontSize: 12, fontWeight: 600,
            fontFamily: 'var(--font-heading)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.borderColor = 'var(--c-text-3)'
            e.currentTarget.style.color = 'var(--c-text)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.borderColor = 'var(--c-border)'
            e.currentTarget.style.color = 'var(--c-text-2)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
          Sair
        </button>
      </header>

      {/* ── Stepper ── */}
      <WizardStepper
        currentStep={currentStep}
        completedSteps={completedSteps}
        onStepClick={onStepClick}
      />

      {/* ── Summary strip ── */}
      {summary && <CampaignSummaryStrip {...summary} />}

      {/* ── Step content ── */}
      <main style={{
        flex: 1,
        padding: '40px 32px 120px',
        overflowY: 'auto',
      }}>
        <div style={{ maxWidth: 1180, margin: '0 auto' }}>
          {children}
        </div>
      </main>

      {/* ── Sticky footer (Voltar / Avançar) ── */}
      <footer style={{
        position: 'sticky', bottom: 0, left: 0, right: 0,
        padding: '14px 32px',
        background: 'rgba(255,255,255,0.92)',
        backdropFilter: 'blur(12px)',
        borderTop: '1px solid var(--c-border)',
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        gap: 16,
        zIndex: 10,
      }}>
        <button
          onClick={onPrev}
          disabled={currentStep === 1}
          style={{
            padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'transparent', color: 'var(--c-text-2)',
            border: '1px solid var(--c-border)',
            cursor: currentStep === 1 ? 'not-allowed' : 'pointer',
            fontSize: 13, fontWeight: 600,
            fontFamily: 'var(--font-heading)',
            opacity: currentStep === 1 ? 0.45 : 1,
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          }}
          onMouseEnter={e => {
            if (currentStep === 1) return
            e.currentTarget.style.borderColor = 'var(--c-text-3)'
            e.currentTarget.style.color = 'var(--c-text)'
            e.currentTarget.style.transform = 'translateY(-1px)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.borderColor = 'var(--c-border)'
            e.currentTarget.style.color = 'var(--c-text-2)'
            e.currentTarget.style.transform = 'translateY(0)'
          }}
        >
          {prevLabel}
        </button>

        <div style={{ fontSize: 11, color: 'var(--c-text-3)', fontWeight: 500 }}>
          {currentStep} / 4
        </div>

        <button
          onClick={onNext}
          disabled={nextDisabled}
          style={{
            padding: '10px 22px', borderRadius: 'var(--radius-md)',
            background: nextDisabled ? 'var(--c-surface-2)' : 'var(--c-action)',
            color: nextDisabled ? 'var(--c-text-3)' : '#fff',
            border: 0,
            cursor: nextDisabled ? 'not-allowed' : 'pointer',
            fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            boxShadow: nextDisabled ? 'none' : 'var(--shadow-sm)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
            minWidth: isLast ? 200 : 140,
          }}
          onMouseEnter={e => {
            if (nextDisabled) return
            e.currentTarget.style.transform = 'translateY(-1px)'
            e.currentTarget.style.boxShadow = 'var(--shadow-md)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.transform = 'translateY(0)'
            e.currentTarget.style.boxShadow = nextDisabled ? 'none' : 'var(--shadow-sm)'
          }}
        >
          {nextLabel}
        </button>
      </footer>
    </div>
  )
}
