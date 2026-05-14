import { Fragment } from 'react'

/**
 * Progressive-disclosure stepper used by empty-states to teach the
 * Competência → Campanha → Período path. Pure presentation — the parent
 * tells it which step is active (1-based) and we render the visual states.
 *
 *   <FlowStepper step={2} steps={['Competência', 'Campanha', 'Período']} />
 *
 * Each step has 3 visual states: pending, active, done. Done steps show a
 * checkmark; the active step has the pink action background; pending is
 * muted gray. The hover/transition tokens come from index.css.
 */
export default function FlowStepper({ step, steps }) {
  return (
    <div className="detection-empty-stepper">
      {steps.map((label, idx) => {
        const n = idx + 1
        const state = n < step ? 'done' : n === step ? 'active' : 'pending'
        return (
          <Fragment key={n}>
            <span className={`detection-empty-stepper-item detection-empty-stepper-item--${state}`}>
              <span className="detection-empty-stepper-num">
                {state === 'done' ? (
                  <svg width="10" height="10" viewBox="0 0 12 12" fill="none">
                    <path d="M2 6.5l2.5 2.5L10 3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                ) : n}
              </span>
              {label}
            </span>
            {idx < steps.length - 1 && <span className="detection-empty-stepper-sep">›</span>}
          </Fragment>
        )
      })}
    </div>
  )
}
