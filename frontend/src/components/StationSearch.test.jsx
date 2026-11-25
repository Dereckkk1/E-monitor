import { describe, test, expect, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import StationSearch, { selectionLabel } from './StationSearch'
import { renderComProviders } from '../test-utils/render'
import { mockApi } from '../test-utils/api'
import { Controlado } from '../test-utils/area-componentes'

const SUGESTOES = {
  stations: [
    { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'MG' },
    { id: 'st-2', name: 'Rádio Outra AM', band: 'AM', frequency_mhz: 1010, city: 'Outra Cidade', state: 'SP' },
  ],
  cities: [{ city: 'Cidade Teste', state: 'MG', count: 3 }, { city: 'Cidade Solo', state: null, count: 1 }],
  states: [{ state: 'MG', count: 12 }],
}

function montar({ valor = null, banda = '', rotas = {} } = {}) {
  const chamadas = mockApi({ 'GET /stations/suggest': SUGESTOES, ...rotas })
  const onMudou = vi.fn()
  const r = renderComProviders(
    <Controlado Comp={StationSearch} inicial={valor} onMudou={onMudou} extra={{ band: banda }} />,
  )
  return { chamadas, onMudou, ...r }
}

const caixa = () => screen.getByRole('combobox', { name: 'Buscar emissora' })

async function digitar(texto) {
  fireEvent.focus(caixa())
  fireEvent.change(caixa(), { target: { value: texto } })
  // espera o atraso de 250ms: a opção de busca livre só ganha o texto novo depois dele
  await screen.findByText(`“${texto}”`, { selector: 'strong' }, { timeout: 2500 })
  return screen.getByRole('listbox')
}

// o nome vem em pedaços (trecho digitado destacado em <mark>); acha a opção pelo texto inteiro
const opcao = (lista, texto) => waitFor(() => {
  const o = within(lista).getAllByRole('option').find(b => b.textContent.includes(texto))
  if (!o) throw new Error(`opção "${texto}" ainda não apareceu`)
  return o
})

describe('selectionLabel', () => {
  test('texto que o campo mostra para cada tipo de seleção', () => {
    expect(selectionLabel(null)).toBe('')
    expect(selectionLabel({ kind: 'station', station: { name: 'Rádio Teste FM' } })).toBe('Rádio Teste FM')
    expect(selectionLabel({ kind: 'station' })).toBe('')
    expect(selectionLabel({ kind: 'city', city: 'Cidade Teste', state: 'MG' })).toBe('Cidade Teste/MG')
    expect(selectionLabel({ kind: 'city', city: 'Cidade Solo', state: null })).toBe('Cidade Solo')
    expect(selectionLabel({ kind: 'state', state: 'SP' })).toBe('SP')
    expect(selectionLabel({ kind: 'text', q: 'jazz' })).toBe('jazz')
  })
})

describe('StationSearch — digitar e escolher', () => {
  test('menos de 2 letras não abre o menu nem consulta o servidor', async () => {
    const { chamadas } = montar()
    fireEvent.focus(caixa())
    fireEvent.change(caixa(), { target: { value: 'a' } })
    await new Promise(r => setTimeout(r, 450))
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(chamadas.length).toBe(0)
  })

  test('depois do atraso consulta /stations/suggest (com a banda) e agrupa emissoras, cidades, estado e busca livre', async () => {
    const { chamadas } = montar({ banda: 'FM' })
    const lista = await digitar('teste')
    await opcao(lista, 'Rádio Teste FM')
    expect(chamadas[0].params).toEqual({ q: 'teste', band: 'FM' })
    expect(within(lista).getByRole('group', { name: 'Emissoras' })).toBeTruthy()
    expect(within(lista).getByRole('group', { name: 'Cidades' })).toBeTruthy()
    expect(within(lista).getByRole('group', { name: 'Estado' })).toBeTruthy()
    expect(within(lista).getByText('99,5 FM', { exact: false })).toBeTruthy()
    expect(within(lista).getByText('3 emissoras')).toBeTruthy()
    expect(within(lista).getByText('1 emissora')).toBeTruthy()
    expect(within(lista).getByText('Cidade Solo')).toBeTruthy()
    expect(within(lista).getByText('12 emissoras')).toBeTruthy()
    expect(within(lista).getByText(/em tudo$/).textContent).toContain('“teste”')
    // o trecho digitado fica destacado nos nomes
    expect(Array.from(lista.querySelectorAll('mark.ssg-hit')).map(m => m.textContent.toLowerCase()).includes('teste')).toBe(true)
  })

  test('escolher uma emissora devolve a seleção, escreve o nome no campo e fecha o menu', async () => {
    const { onMudou } = montar()
    const lista = await digitar('rádio')
    fireEvent.click(await opcao(lista, 'Rádio Outra AM'))
    expect(onMudou).toHaveBeenCalledWith(expect.objectContaining({ kind: 'station', id: 'st-2' }))
    expect(caixa().value).toBe('Rádio Outra AM')
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  test('escolher cidade, estado e busca livre devolve cada tipo de seleção', async () => {
    const { onMudou } = montar()
    let lista = await digitar('teste')
    fireEvent.click(await within(lista).findByText('3 emissoras'))
    expect(onMudou).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'city', city: 'Cidade Teste', state: 'MG', count: 3 }))
    expect(caixa().value).toBe('Cidade Teste/MG')

    lista = await digitar('mg')
    fireEvent.click(await within(lista).findByText(/Todas de/))
    expect(onMudou).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'state', state: 'MG' }))

    lista = await digitar('qualquer')
    fireEvent.click(await within(lista).findByText(/em tudo$/))
    expect(onMudou).toHaveBeenLastCalledWith({ kind: 'text', group: 'free', key: 'free', q: 'qualquer' })
  })

  test('Enter sem item ativo busca o texto livre; setas navegam e Enter escolhe o ativo', async () => {
    const { onMudou } = montar()
    const lista = await digitar('teste')
    await opcao(lista, 'Rádio Teste FM')
    fireEvent.keyDown(caixa(), { key: 'ArrowDown' })       // primeira opção
    expect(caixa().getAttribute('aria-activedescendant')).toMatch(/opt-s-st-1$/)
    fireEvent.keyDown(caixa(), { key: 'ArrowDown' })
    expect(caixa().getAttribute('aria-activedescendant')).toMatch(/opt-s-st-2$/)
    fireEvent.keyDown(caixa(), { key: 'ArrowUp' })
    fireEvent.keyDown(caixa(), { key: 'Enter' })
    expect(onMudou).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'station', id: 'st-1' }))
  })

  test('Enter antes do atraso de 250ms não faz nada (ainda não há consulta)', async () => {
    const { onMudou } = montar()
    fireEvent.focus(caixa())
    fireEvent.change(caixa(), { target: { value: 'jazz' } })
    fireEvent.keyDown(caixa(), { key: 'Enter' })
    expect(onMudou).not.toHaveBeenCalled()
  })

  test('Enter com texto e nada ativo manda a busca livre', async () => {
    const { onMudou } = montar()
    await digitar('jazz')
    fireEvent.keyDown(caixa(), { key: 'Enter' })
    expect(onMudou).toHaveBeenCalledWith({ kind: 'text', q: 'jazz' })
  })

  test('Esc fecha o menu; clicar fora também', async () => {
    montar()
    await digitar('teste')
    fireEvent.keyDown(caixa(), { key: 'Escape' })
    expect(screen.queryByRole('listbox')).toBeNull()
    fireEvent.focus(caixa())
    expect(await screen.findByRole('listbox')).toBeTruthy()
    fireEvent.mouseDown(document.body)
    expect(screen.queryByRole('listbox')).toBeNull()
  })
})

describe('StationSearch — limpar', () => {
  test('"Limpar busca" zera o campo e avisa com null', async () => {
    const { onMudou } = montar()
    const lista = await digitar('rádio')
    fireEvent.click(await opcao(lista, 'Rádio Outra AM'))
    fireEvent.click(screen.getByRole('button', { name: 'Limpar busca' }))
    expect(onMudou).toHaveBeenLastCalledWith(null)
    expect(caixa().value).toBe('')
    expect(screen.queryByRole('button', { name: 'Limpar busca' })).toBeNull()
  })

  test('abre com a seleção atual escrita no campo e já com o botão de limpar', () => {
    montar({ valor: { kind: 'state', state: 'SP' } })
    expect(caixa().value).toBe('SP')
    expect(screen.getByRole('button', { name: 'Limpar busca' })).toBeTruthy()
  })

  test('sem resultados do servidor ainda oferece a busca livre', async () => {
    montar({ rotas: { 'GET /stations/suggest': { stations: [], cities: [], states: [] } } })
    const lista = await digitar('xyz')
    expect(await within(lista).findByText(/em tudo$/)).toBeTruthy()
    expect(within(lista).queryByRole('group', { name: 'Emissoras' })).toBeNull()
  })
})
