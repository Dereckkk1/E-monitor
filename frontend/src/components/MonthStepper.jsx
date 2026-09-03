import { shiftMonth } from '../utils/monthStep'

/**
 * MonthStepper — seletor de COMPETÊNCIA com passo de mês: ‹ [AAAA-MM] ›
 *
 * Substitui o `<input type="month">` solto das barras de filtro. As setas
 * existem porque navegar mês a mês é o gesto mais frequente dessas telas e o
 * seletor nativo obriga a abrir um calendário (ou digitar) pra andar um mês.
 *
 * Vale só pra competência. O filtro de PERÍODO (intervalo de dias, `.flow-range`)
 * fica como está: ali as duas pontas se movem de forma independente e um passo
 * de mês não significa nada.
 *
 * Props:
 *  - value:    'AAAA-MM' (pode vir vazio: a competência do /admin/pos-venda é
 *              opcional, e aí a seta parte do mês atual — ver monthStep.js)
 *  - onChange: (valor: string) => void  ← recebe o VALOR, não o evento
 *  - inputRef: ref encaminhada pro input (telas que dão foco no passo 1)
 */
export default function MonthStepper({
  id, value, onChange, disabled = false, inputRef, placeholder, title,
}) {
  const go = (delta) => { if (!disabled) onChange(shiftMonth(value, delta)) }

  return (
    <div className={`flow-month-step${disabled ? ' flow-month-step--disabled' : ''}`} title={title}>
      <button
        type="button" className="flow-month-step-btn" onClick={() => go(-1)}
        disabled={disabled} aria-label="Mês anterior" title="Mês anterior"
      >‹</button>
      <input
        id={id}
        ref={inputRef}
        type="month"
        className="flow-month-step-input"
        value={value ?? ''}
        onChange={e => onChange(e.target.value)}
        disabled={disabled}
        placeholder={placeholder}
      />
      <button
        type="button" className="flow-month-step-btn" onClick={() => go(1)}
        disabled={disabled} aria-label="Próximo mês" title="Próximo mês"
      >›</button>
    </div>
  )
}
