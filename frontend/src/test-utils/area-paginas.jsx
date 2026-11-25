// Helpers da área "páginas de primeiro nível" (NÃO é código de produção).
import { useLocation } from 'react-router-dom'
import { screen, fireEvent, waitFor } from '@testing-library/react'

/**
 * Mostra a URL atual do MemoryRouter pra testar navegação:
 *   renderComProviders(<><Pagina /><LocalizacaoAtual /></>)
 *   expect(screen.getByTestId('local').textContent).toBe('/campaigns?status=ativa')
 */
export function LocalizacaoAtual() {
  const { pathname, search } = useLocation()
  return <div data-testid="local">{pathname + search}</div>
}

/** Texto da URL atual (pathname+search) lido do probe LocalizacaoAtual. */
export function urlAtual(screen) {
  return screen.getByTestId('local').textContent
}

/** ISO (UTC) de "agora - segundos", pros campos tipo last_pcm_at / last_incident_at. */
export function isoHa(segundos, base = Date.now()) {
  return new Date(base - segundos * 1000).toISOString()
}

/**
 * Escolhe uma opção num RSelect (react-select): abre o menu pelo input e clica
 * na opção cujo TEXTO contém o rótulo (o nome acessível da opção não serve: o
 * texto vem partido em vários elementos). `input` = o <input> do select (getByLabelText / getByRole('combobox')).
 */
export async function escolherOpcao(input, rotulo) {
  fireEvent.focus(input)
  fireEvent.keyDown(input, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40 })
  const opcao = await waitFor(() => {
    const achada = screen.getAllByRole('option').find(o => o.textContent.includes(rotulo))
    if (!achada) throw new Error(`opção "${rotulo}" não apareceu`)
    return achada
  })
  fireEvent.click(opcao)
}
