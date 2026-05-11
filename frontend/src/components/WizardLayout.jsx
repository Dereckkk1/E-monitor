import { useNavigate } from 'react-router-dom'
import WizardStepper from './WizardStepper'
import CampaignSummaryStrip from './CampaignSummaryStrip'

/**
 * Skeleton for the 4-step campaign wizard.
 *
 * Props:
 *  - currentStep: 1..4
 *  - completedSteps: Array<number>
 *  - onStepClick: (step) => void
 *  - onPrev: () => void
 *  - onNext: () => void
 *  - nextDisabled: bool
 *  - nextLabel: string (default "Avançar →"; step 4 uses "Concluir campanha →")
 *  - prevLabel: string (default "← Voltar")
 *  - summary: props passed to CampaignSummaryStrip
 *  - children: ReactNode (step content)
 *  - title: string (header text, default "Nova campanha")
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

  return (
    <div style={{
      background: 'var(--c-surface)',
      border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      overflow: 'hidden',
      boxShadow: 'var(--shadow-sm)',
      margin: 16,
      display: 'flex',
      flexDirection: 'column',
      minHeight: 'calc(100vh - 32px)',
    }}>
      <div style={{
        padding: '18px 24px', borderBottom: '1px solid var(--c-border)',
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
      }}>
        <h2 style={{ margin: 0, fontSize: 16, fontFamily: 'var(--font-heading)', fontWeight: 700 }}>{title}</h2>
        <button
          onClick={() => navigate('/campaigns')}
          aria-label="Fechar"
          style={{
            width: 28, height: 28, borderRadius: 'var(--radius-md)',
            background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            cursor: 'pointer', border: 0, fontSize: 14,
          }}
        >×</button>
      </div>

      <WizardStepper
        currentStep={currentStep}
        completedSteps={completedSteps}
        onStepClick={onStepClick}
      />

      {summary && <CampaignSummaryStrip {...summary} />}

      <div style={{ flex: 1, padding: '24px' }}>
        {children}
      </div>

      <div style={{
        padding: '14px 24px', borderTop: '1px solid var(--c-border)',
        display: 'flex', justifyContent: 'space-between', background: 'var(--c-surface)',
      }}>
        <button
          className="btn btn-secondary btn-sm"
          onClick={onPrev}
          disabled={currentStep === 1}
        >{prevLabel}</button>
        <button
          className="btn btn-primary btn-sm"
          onClick={onNext}
          disabled={nextDisabled}
        >{nextLabel}</button>
      </div>
    </div>
  )
}
