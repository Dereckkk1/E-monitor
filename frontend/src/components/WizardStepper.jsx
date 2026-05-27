/**
 * Horizontal stepper with 4 fixed steps, an icon per step, and a continuous
 * progress line that fills as the user advances.
 *
 * Props:
 *  - currentStep: 1 | 2 | 3 | 4
 *  - completedSteps: Array<number> — steps already done (gets a check)
 *  - onStepClick: (step) => void — the controller decides if click is allowed
 */
function IconInfo() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="8" r="6" />
      <path d="M8 7v4M8 4.5h.01" />
    </svg>
  )
}
function IconRadio() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="6" width="12" height="7" rx="1.5" />
      <path d="M5 4.5l5-2.5" />
      <circle cx="11" cy="9.5" r="1.2" />
      <path d="M4.5 9.5h3" />
    </svg>
  )
}
function IconSignal() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 9a6 6 0 0 1 12 0" />
      <path d="M4.5 9a3.5 3.5 0 0 1 7 0" />
      <circle cx="8" cy="9" r="1" />
    </svg>
  )
}
function IconStack() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 5l6-3 6 3-6 3-6-3z" />
      <path d="M2 8l6 3 6-3" />
      <path d="M2 11l6 3 6-3" />
    </svg>
  )
}
function IconCalendar() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="3" width="12" height="11" rx="1.5" />
      <path d="M11 1.5v3M5 1.5v3M2 6.5h12" />
    </svg>
  )
}
function IconCheck() {
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 8.5l3.5 3.5L13 5" />
    </svg>
  )
}
function IconMoney() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="4" width="12" height="8" rx="1.4" />
      <circle cx="8" cy="8" r="1.8" />
      <path d="M4.5 6h.01M11.5 10h.01" />
    </svg>
  )
}

const STEPS = [
  { id: 1, label: 'Dados básicos', hint: 'Nome, cliente, período',     Icon: IconInfo },
  { id: 2, label: 'Emissoras',     hint: 'Quem vai monitorar',         Icon: IconRadio },
  { id: 3, label: 'Conexão',       hint: 'Testar e ajustar streams',   Icon: IconSignal },
  { id: 4, label: 'Materiais',     hint: 'Áudios da campanha',         Icon: IconStack },
  { id: 5, label: 'Distribuição',  hint: 'Regras de veiculação',       Icon: IconCalendar },
  { id: 6, label: 'Valores',       hint: 'Investimento por emissora',  Icon: IconMoney },
]

export default function WizardStepper({ currentStep, completedSteps = [], onStepClick }) {
  // Continuous progress line: 0% at step 1, 100% at step 4.
  // The line lives between the centers of step 1 and step 4 icons.
  const progress = ((currentStep - 1) / (STEPS.length - 1)) * 100

  return (
    <div style={{
      background: 'var(--c-surface)',
      borderBottom: '1px solid var(--c-border)',
      padding: '20px 32px 22px',
    }}>
      <div style={{ position: 'relative', display: 'flex', justifyContent: 'space-between', maxWidth: 920, margin: '0 auto' }}>
        {/* Track behind all steps */}
        <div style={{
          position: 'absolute',
          top: 18, left: '6%', right: '6%', height: 2,
          background: 'var(--c-surface-2)', borderRadius: 999,
          zIndex: 0,
        }} />
        {/* Filled portion */}
        <div style={{
          position: 'absolute',
          top: 18, left: '6%', height: 2,
          width: `calc(${progress}% * 0.88)`,
          background: 'var(--c-action)', borderRadius: 999,
          transition: 'width 300ms cubic-bezier(0.16,1,0.3,1)',
          zIndex: 1,
        }} />

        {STEPS.map(step => {
          const done = completedSteps.includes(step.id)
          const active = step.id === currentStep
          const clickable = done || step.id < currentStep

          // Visual states for the round button:
          //   active  → action color filled
          //   done    → success color filled
          //   pending → neutral outlined
          const circleStyle = active ? {
            background: 'var(--c-action)', color: '#fff',
            border: '2px solid var(--c-action)',
            boxShadow: '0 0 0 5px var(--c-action-light, rgba(232,30,117,0.12))',
          } : done ? {
            background: 'var(--c-success)', color: '#fff',
            border: '2px solid var(--c-success)',
          } : {
            background: 'var(--c-surface)', color: 'var(--c-text-3)',
            border: '2px solid var(--c-surface-2)',
          }

          const labelColor = active ? 'var(--c-text)'
            : done ? 'var(--c-text)'
            : 'var(--c-text-3)'

          const Icon = step.Icon

          return (
            <button
              key={step.id}
              onClick={() => clickable && onStepClick?.(step.id)}
              disabled={!clickable}
              title={clickable ? `Ir para ${step.label}` : 'Complete os passos anteriores'}
              style={{
                position: 'relative', zIndex: 2,
                display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8,
                background: 'transparent', border: 0,
                cursor: clickable ? 'pointer' : 'default',
                padding: 0, minWidth: 0, flex: '0 0 auto',
                fontFamily: 'var(--font-heading)',
                transition: 'transform 150ms cubic-bezier(0.16,1,0.3,1)',
              }}
              onMouseEnter={e => { if (clickable) e.currentTarget.style.transform = 'translateY(-1px)' }}
              onMouseLeave={e => { e.currentTarget.style.transform = 'translateY(0)' }}
            >
              <span style={{
                width: 36, height: 36, borderRadius: '50%',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontWeight: 700, fontSize: 13,
                transition: 'all 200ms cubic-bezier(0.16,1,0.3,1)',
                ...circleStyle,
              }}>
                {done ? <IconCheck /> : <Icon />}
              </span>
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2 }}>
                <span style={{
                  fontSize: 12, fontWeight: active ? 700 : 600,
                  color: labelColor,
                  letterSpacing: '-0.005em',
                }}>
                  {step.label}
                </span>
                <span style={{
                  fontSize: 10, fontWeight: 500,
                  color: 'var(--c-text-3)',
                  display: active ? 'block' : 'none',
                }}>
                  {step.hint}
                </span>
              </div>
            </button>
          )
        })}
      </div>
    </div>
  )
}
