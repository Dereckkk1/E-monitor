/**
 * Horizontal stepper with 4 fixed steps and a continuous progress bar.
 *
 * Props:
 *  - currentStep: 1 | 2 | 3 | 4
 *  - completedSteps: Array<number> — steps already done (for the ✓ icon)
 *  - onStepClick: (step) => void — fires when user clicks a step (controller decides if allowed)
 */
const STEPS = [
  { id: 1, label: 'Dados básicos' },
  { id: 2, label: 'Emissoras' },
  { id: 3, label: 'Materiais' },
  { id: 4, label: 'Distribuição' },
]

export default function WizardStepper({ currentStep, completedSteps = [], onStepClick }) {
  const progress = ((currentStep - 1) / (STEPS.length - 1)) * 100

  return (
    <div style={{
      display: 'flex', padding: '14px 24px', background: '#fff',
      borderBottom: '1px solid #f1f5f9', gap: 32, alignItems: 'center', position: 'relative',
    }}>
      {STEPS.map(step => {
        const done = completedSteps.includes(step.id)
        const active = step.id === currentStep
        const clickable = done || step.id < currentStep

        return (
          <button
            key={step.id}
            onClick={() => clickable && onStepClick?.(step.id)}
            disabled={!clickable}
            style={{
              display: 'flex', alignItems: 'center', gap: 10, fontSize: 12,
              color: active ? '#0f172a' : done ? '#15803d' : '#94a3b8',
              fontWeight: active ? 600 : 400,
              background: 'transparent', border: 0,
              cursor: clickable ? 'pointer' : 'default',
              padding: 0,
            }}
          >
            <span style={{
              width: 22, height: 22, borderRadius: '50%',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontWeight: 700, fontSize: 11,
              background: active ? '#E81E75' : done ? '#15803d' : '#f1f5f9',
              color: (active || done) ? '#fff' : '#94a3b8',
            }}>{done ? '✓' : step.id}</span>
            {step.label}
          </button>
        )
      })}
      <div style={{
        position: 'absolute', left: 24, right: 24, bottom: -1, height: 2,
        background: '#f1f5f9',
      }}>
        <div style={{
          height: '100%', width: `${progress}%`,
          background: '#E81E75',
          transition: 'width 250ms cubic-bezier(0.16,1,0.3,1)',
        }} />
      </div>
    </div>
  )
}
