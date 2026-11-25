import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import StationsStep from './StationsStep'
import { renderComProviders } from '../../test-utils/render'
import { mockApi } from '../../test-utils/api'

const TODAS = [
  { id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'MG' },
  { id: 'st-2', name: 'Rádio Outra AM', band: 'AM', city: 'Outra Cidade', state: 'SP' },
  { id: 'st-3', name: 'Rádio Terceira FM', band: 'FM', frequency_mhz: 101.1, city: 'Cidade Três', state: 'MG' },
]

function montar({ selecao = [], todas = TODAS, rotas = {} } = {}) {
  const chamadas = mockApi({
    'GET /stations': { data: todas },
    'PUT /campaigns/camp-1/stations': { ok: true },
    ...rotas,
  })
  const r = renderComProviders(
    <StationsStep campaignId="camp-1" allStations={todas} currentSelection={selecao} />,
  )
  return { chamadas, ...r }
}

const puts = (chamadas) => chamadas.filter(c => c.chave === 'PUT /campaigns/camp-1/stations')

describe('StationsStep — seleção atual', () => {
  test('sem seleção mostra o contador zerado e o convite pra buscar', () => {
    montar()
    expect(screen.getByRole('heading', { name: 'Onde a campanha vai tocar?' })).toBeTruthy()
    expect(screen.getByText('Selecionadas').parentElement.textContent).toContain('0')
    expect(screen.getByText('Nenhuma emissora selecionada ainda')).toBeTruthy()
    expect(screen.getByText('emissoras')).toBeTruthy()
  })

  test('as emissoras da campanha aparecem agrupadas por UF, com contagem e dial', () => {
    montar({ selecao: ['st-1', 'st-2', 'st-3'] })
    expect(screen.getByText('emissoras em 2 UFs')).toBeTruthy()
    const mg = screen.getByText('MG').parentElement
    expect(mg.textContent).toContain('2 emissoras')
    const sp = screen.getByText('SP').parentElement
    expect(sp.textContent).toContain('1 emissora')
    expect(screen.getByText('FM 99.5 · Cidade Teste / MG')).toBeTruthy()
    // UFs em ordem alfabética: MG antes de SP
    const ufs = Array.from(document.querySelectorAll('span')).map(s => s.textContent).filter(t => t === 'MG' || t === 'SP')
    expect(ufs).toEqual(['MG', 'SP'])
  })

  test('uma só emissora usa o singular', () => {
    montar({ selecao: ['st-2'] })
    expect(screen.getByText('emissora em 1 UF')).toBeTruthy()
  })

  test('id da campanha que não está no catálogo é ignorado na tela', () => {
    montar({ selecao: ['st-1', 'st-fantasma'] })
    expect(screen.getByText('emissora em 1 UF')).toBeTruthy()
  })
})

describe('StationsStep — salvar sozinho', () => {
  test('remover uma emissora salva a seleção nova depois de 0,5s', async () => {
    const { chamadas } = montar({ selecao: ['st-1', 'st-2'] })
    fireEvent.click(screen.getAllByRole('button', { name: 'Remover emissora' })[0])
    expect(screen.getByText('emissora em 1 UF')).toBeTruthy()
    expect(puts(chamadas).length).toBe(0)     // ainda dentro do atraso
    await waitFor(() => expect(puts(chamadas).length).toBe(1), { timeout: 2500 })
    expect(puts(chamadas)[0].data).toEqual({ target_stations: ['st-2'] })
  })

  test('"Limpar tudo" salva a lista vazia', async () => {
    const { chamadas } = montar({ selecao: ['st-1', 'st-2'] })
    fireEvent.click(screen.getByRole('button', { name: 'Limpar tudo' }))
    expect(screen.getByText('Nenhuma emissora selecionada ainda')).toBeTruthy()
    await waitFor(() => expect(puts(chamadas).length).toBe(1), { timeout: 2500 })
    expect(puts(chamadas)[0].data).toEqual({ target_stations: [] })
  })

  test('sem mexer em nada nada é salvo (só edição do usuário dispara)', async () => {
    const { chamadas } = montar({ selecao: ['st-1'] })
    await new Promise(r => setTimeout(r, 800))
    expect(puts(chamadas).length).toBe(0)
  })

  test('sair da etapa com uma alteração pendente salva na hora (não perde o clique)', async () => {
    const { chamadas, unmount } = montar({ selecao: ['st-1', 'st-2'] })
    fireEvent.click(screen.getAllByRole('button', { name: 'Remover emissora' })[1])
    unmount()
    await waitFor(() => expect(puts(chamadas).length).toBe(1))
    expect(puts(chamadas)[0].data).toEqual({ target_stations: ['st-1'] })
  })

  test('emissora da campanha que o catálogo não trouxe é preservada no save', async () => {
    const { chamadas } = montar({ selecao: ['st-1', 'st-2', 'st-fantasma'] })
    fireEvent.click(screen.getAllByRole('button', { name: 'Remover emissora' })[0])
    await waitFor(() => expect(puts(chamadas).length).toBe(1), { timeout: 2500 })
    expect(puts(chamadas)[0].data.target_stations).toEqual(['st-2', 'st-fantasma'])
  })
})

describe('StationsStep — busca', () => {
  test('digitar no campo pesquisa no servidor (depois de 0,4s) e escolher uma opção adiciona e salva', async () => {
    const { chamadas } = montar({
      selecao: ['st-1'],
      rotas: { 'GET /stations': { data: TODAS } },
    })
    const caixa = screen.getByRole('combobox')
    fireEvent.focus(caixa)
    fireEvent.change(caixa, { target: { value: 'terceira' } })
    await waitFor(() => expect(chamadas.some(c => c.chave === 'GET /stations' && c.params?.q === 'terceira')).toBe(true), { timeout: 2500 })
    expect(chamadas.find(c => c.params?.q === 'terceira').params.limit).toBe(25)
    const opcao = await screen.findByText('Rádio Terceira FM (FM 101.1) · Cidade Três')
    fireEvent.click(opcao)
    await waitFor(() => expect(puts(chamadas).length).toBe(1), { timeout: 2500 })
    expect(puts(chamadas)[0].data.target_stations.sort()).toEqual(['st-1', 'st-3'])
    expect(within(document.body).getAllByText('Rádio Terceira FM').length).toBeGreaterThan(0)
  })

  test('sem resultado a mensagem muda conforme tenha ou não texto digitado', async () => {
    montar({ rotas: { 'GET /stations': { data: [] } } })
    const caixa = screen.getByRole('combobox')
    fireEvent.focus(caixa)
    fireEvent.keyDown(caixa, { key: 'ArrowDown', keyCode: 40 })
    expect(await screen.findByText('Comece a digitar pra buscar')).toBeTruthy()
    fireEvent.change(caixa, { target: { value: 'zzz' } })
    expect(await screen.findByText('Nenhuma emissora encontrada', {}, { timeout: 2500 })).toBeTruthy()
  })
})
