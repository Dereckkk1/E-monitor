// Empty state = preview fantasma (DESIGN.md §4.7 + pro-system-ui §8): desenha
// uma silhueta do conteúdo real, esmaecida, com o prompt de ação por cima.
// Nada de "ícone triste + texto".
import { IconPlus } from './icons'

function GhostRows({ variant }) {
  if (variant === 'board') {
    return (
      <div className="sug-ghost sug-ghost--board" aria-hidden="true">
        {[3, 2, 2, 1, 1].map((count, c) => (
          <div className="sug-ghost-col" key={c}>
            <div className="sug-ghost-colhead" />
            {Array.from({ length: count }).map((_, i) => (
              <div className="sug-ghost-bcard" key={i}>
                <div className="sug-ghost-line" style={{ width: '40%' }} />
                <div className="sug-ghost-line" style={{ width: '85%' }} />
              </div>
            ))}
          </div>
        ))}
      </div>
    )
  }
  return (
    <div className="sug-ghost sug-ghost--cards" aria-hidden="true">
      {Array.from({ length: 6 }).map((_, i) => (
        <div className="sug-ghost-card" key={i}>
          <div className="sug-ghost-row"><span className="sug-ghost-pill" /><span className="sug-ghost-pill" /></div>
          <div className="sug-ghost-line" style={{ width: `${70 - (i % 3) * 12}%` }} />
          <div className="sug-ghost-line sug-ghost-line--dim" style={{ width: '92%' }} />
          <div className="sug-ghost-line sug-ghost-line--dim" style={{ width: '60%' }} />
        </div>
      ))}
    </div>
  )
}

export default function EmptyState({ title, text, ctaLabel, onCta, variant = 'cards' }) {
  return (
    <div className="sug-emptywrap">
      <GhostRows variant={variant} />
      <div className="sug-emptyprompt">
        <h3 className="sug-emptyprompt-title">{title}</h3>
        <p className="sug-emptyprompt-text">{text}</p>
        {ctaLabel && (
          <button className="btn btn-primary sug-emptyprompt-cta" onClick={onCta}>
            <IconPlus /> {ctaLabel}
          </button>
        )}
      </div>
    </div>
  )
}
