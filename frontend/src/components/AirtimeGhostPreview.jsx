import FlowStepper from './FlowStepper'

// Espelha os 4 passos da AirtimeFiltersBar. Pro viewer de 1 cliente o passo
// 1 vem travado na barra, mas segue no stepper: some-lo mudaria a numeração
// entre o que ele lê aqui e o que vê no filtro.
const STEP_LABELS = ['Cliente', 'Competência', 'Campanhas', 'Período']

// SVG library — kept inline to avoid an external icon dependency on this
// screen. Calendar/megaphone/alert/search map to the same semantic intent
// as /detections so users see a consistent visual vocabulary.
const ICONS = {
  calendar: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <path d="M3 10h18M8 3v4M16 3v4" />
    </svg>
  ),
  campaign: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1z" />
      <path d="M16 8a4 4 0 0 1 0 8" />
      <path d="M19 5a8 8 0 0 1 0 14" />
    </svg>
  ),
  alert: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
      <path d="M10.3 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
    </svg>
  ),
  search: (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="7" />
      <path d="m21 21-4.3-4.3" />
    </svg>
  ),
}

/**
 * Empty-state ghost preview for /airtime-report. Renders 3 desaturated
 * row mockups behind a centered card. Variants control:
 *   - step:    1..4 → shows the FlowStepper at the top
 *   - icon:    'calendar' | 'campaign' | 'alert' | 'search'
 *   - accent:  'action' (default pink) | 'mute' (gray) — controls icon tint
 */
export default function AirtimeGhostPreview({
  step = null,
  icon = 'calendar',
  accent = 'action',
  title = 'Selecione uma campanha',
  description = 'Escolha uma campanha no filtro acima para ver as veiculações detectadas.',
  ctaLabel = null,
  onCta = null,
}) {
  const iconClass = accent === 'mute'
    ? 'detection-empty-icon detection-empty-icon--mute'
    : 'detection-empty-icon'

  return (
    <div className="airtime-ghost">
      {[1, 2, 3].map(i => (
        <div key={i} className="airtime-ghost-row" aria-hidden>
          <div className="airtime-ghost-row-stripe" />
          <div className="airtime-ghost-row-play" />
          <div className="airtime-ghost-row-datetime">
            <div className="airtime-ghost-bar" style={{ width: 80 }} />
            <div className="airtime-ghost-bar" style={{ width: 56, marginTop: 6 }} />
          </div>
          <div className="airtime-ghost-row-station">
            <div className="airtime-ghost-logo" />
            <div>
              <div className="airtime-ghost-bar" style={{ width: 130 }} />
              <div className="airtime-ghost-bar" style={{ width: 90, marginTop: 6 }} />
            </div>
          </div>
          <div className="airtime-ghost-row-metrics">
            <div className="airtime-ghost-bar" style={{ width: 50, marginBottom: 6 }} />
            <div className="airtime-ghost-bar" style={{ width: 60 }} />
          </div>
        </div>
      ))}

      <div className="airtime-ghost-card">
        {step != null && <FlowStepper step={step} steps={STEP_LABELS} />}
        <div className={iconClass}>{ICONS[icon] ?? ICONS.calendar}</div>
        <h3 className="airtime-ghost-title">{title}</h3>
        <p className="airtime-ghost-description">{description}</p>
        {ctaLabel && onCta && (
          <button type="button" className="airtime-ghost-cta" onClick={onCta}>{ctaLabel}</button>
        )}
      </div>
    </div>
  )
}
