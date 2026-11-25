// Helpers da área "componentes + steps de wizard + sugestões + pós-venda".
// Infra de teste (fora da cobertura); não é código de produção.
import { useState } from 'react'
import { fireEvent, screen, within } from '@testing-library/react'

/**
 * Faz um componente "controlado" (value/onChange) funcionar de verdade num teste:
 * guarda o estado e repassa cada onChange pro espião, pra conferir o payload.
 *
 *   const onMudou = vi.fn()
 *   renderComProviders(<Controlado Comp={BasicDataStep} inicial={{...}} onMudou={onMudou} extra={{ clients }} />)
 */
export function Controlado({ Comp, inicial, onMudou, extra = {}, propValor = 'value', propMudou = 'onChange' }) {
  const [valor, setValor] = useState(inicial)
  const props = {
    [propValor]: valor,
    [propMudou]: (novo) => {
      setValor(novo)
      onMudou?.(novo)
    },
    ...extra,
  }
  return <Comp {...props} />
}

/**
 * Escolhe uma opção num react-select (RSelect) identificado pelo `inputId`/rótulo.
 * `caixa` é o <input> interno do react-select (getByRole('combobox')).
 */
export function escolherOpcao(caixa, rotulo) {
  fireEvent.focus(caixa)
  fireEvent.keyDown(caixa, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40 })
  const opcao = screen.getByText(rotulo, { selector: '[class*="option"], [id*="option"]' })
  fireEvent.click(opcao)
}

/** Texto exato de um elemento folha (útil quando o texto é quebrado em spans). */
export function porTextoExato(texto, raiz = screen) {
  return raiz.getByText((_, el) =>
    el.textContent === texto && !Array.from(el.children).some(c => c.textContent === texto))
}

export { within }
