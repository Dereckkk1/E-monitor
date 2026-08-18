import FlowStepper from './FlowStepper'

/**
 * Estado vazio padrão das telas com barra de filtros em passos.
 *
 * Anatomia (a mesma de /detections e /materials, que montam esse DOM à mão):
 * uma silhueta desbotada do resultado ao fundo + um cartão centrado com o
 * stepper, o ícone, o que fazer e a ação. A silhueta é o que diferencia
 * "ensinar a tela" de "nada aqui": o usuário vê o formato do que vai receber
 * antes de escolher qualquer filtro.
 *
 *   <FlowEmptyState
 *     step={2} steps={['Cliente', 'Campanha']}
 *     icon={<IconeQualquer />}
 *     title={<>Escolha uma <strong>campanha</strong></>}
 *     description="…"
 *     actions={<button className="detection-empty-cta">…</button>}
 *     ghost={<SilhuetaDaTela />}
 *   />
 *
 * As classes continuam com o prefixo `detection-` por serem as originais de
 * /detections — renomear obrigaria a mexer em duas telas que já funcionam.
 * O bloco em index.css está documentado como compartilhado.
 */
export default function FlowEmptyState({
  step = null,
  steps = null,
  icon = null,
  tone = 'action', // 'action' | 'warn' | 'mute'
  title,
  description = null,
  actions = null,
  ghost = null,
  className = '',
}) {
  const toneClass = tone === 'warn'
    ? ' detection-empty-icon--warn'
    : tone === 'mute' ? ' detection-empty-icon--mute' : ''

  return (
    <div className={`detection-empty${className ? ` ${className}` : ''}`} role="status" aria-live="polite">
      {ghost && (
        <div className="detection-empty-ghost" aria-hidden="true">{ghost}</div>
      )}
      <div className="detection-empty-card">
        {step != null && steps && <FlowStepper step={step} steps={steps} />}
        {icon && <div className={`detection-empty-icon${toneClass}`}>{icon}</div>}
        <h3>{title}</h3>
        {description && <p>{description}</p>}
        {actions && <div className="detection-empty-actions">{actions}</div>}
      </div>
    </div>
  )
}
