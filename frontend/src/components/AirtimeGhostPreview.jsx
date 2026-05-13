// Empty-state ghost preview. Renders 3 desaturated airtime-row mockups behind
// a centered CTA card. Used for "no campaign" and "no detections" states.
// Follows design.md §4.7 "tutorial estilizado" pattern.

export default function AirtimeGhostPreview({
  title = 'Selecione uma campanha',
  description = 'Escolha uma campanha no filtro acima para ver as veiculações detectadas.',
  ctaLabel = null,
  onCta = null,
}) {
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
        <svg className="airtime-ghost-icon" width="64" height="64" viewBox="0 0 64 64" fill="none" aria-hidden>
          <circle cx="32" cy="32" r="22" stroke="currentColor" strokeWidth="2.5" />
          <path d="M32 18v14l9 6" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
        </svg>
        <h3 className="airtime-ghost-title">{title}</h3>
        <p className="airtime-ghost-description">{description}</p>
        {ctaLabel && onCta && (
          <button type="button" className="airtime-ghost-cta" onClick={onCta}>{ctaLabel}</button>
        )}
      </div>
    </div>
  )
}
