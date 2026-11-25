import { describe, test, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import BadgePill from './BadgePill'
import CampaignDeficitChip from './CampaignDeficitChip'
import DeficitSplit from './DeficitSplit'
import { renderComProviders } from '../test-utils/render'

describe('BadgePill', () => {
  test('mostra o prefixo e o valor, com o title como dica', () => {
    render(<BadgePill variant="blue" value={7} prefix="+" title="bônus do dia" />)
    const pill = screen.getByTitle('bônus do dia')
    expect(pill.textContent).toBe('+7')
  })

  test('cada variante tem sua cor e variante desconhecida cai no cinza', () => {
    const { rerender } = render(<BadgePill variant="green" value={1} title="p" />)
    const verde = screen.getByTitle('p').style.color
    rerender(<BadgePill variant="red" value={1} title="p" />)
    const vermelho = screen.getByTitle('p').style.color
    rerender(<BadgePill variant="inexistente" value={1} title="p" />)
    const desconhecida = screen.getByTitle('p').style.color
    rerender(<BadgePill value={1} title="p" />)
    const padrao = screen.getByTitle('p').style.color
    expect(verde).not.toBe(vermelho)
    expect(desconhecida).toBe(padrao)
    expect(desconhecida).not.toBe(verde)
  })

  test('aceita zero e valores negativos sem esconder o número', () => {
    const { rerender } = render(<BadgePill value={0} title="p" />)
    expect(screen.getByTitle('p').textContent).toBe('0')
    rerender(<BadgePill value={-3} title="p" />)
    expect(screen.getByTitle('p').textContent).toBe('-3')
  })
})

describe('CampaignDeficitChip', () => {
  const dado = {
    campaign_id: 'camp-1', campaign_name: 'Campanha Teste', client_name: 'Cliente Teste',
    expected: 20, delivered: 14, deficit: 6, affected_by: [],
  }

  test('mostra campanha, cliente e os números e leva pra /campaigns?campaign=<id>', () => {
    renderComProviders(<CampaignDeficitChip data={dado} />)
    const link = screen.getByRole('link')
    expect(link.getAttribute('href')).toBe('/campaigns?campaign=camp-1')
    expect(screen.getByText('Campanha Teste')).toBeTruthy()
    expect(screen.getByText('Cliente Teste')).toBeTruthy()
    expect(screen.getByText('esperado').querySelector('b').textContent).toBe('20')
    expect(screen.getByText('entregue').querySelector('b').textContent).toBe('14')
    expect(screen.getByText('faltam').querySelector('b').textContent).toBe('6')
    expect(screen.queryByText('déficit')).toBeNull()
  })

  test('lacuna silenciosa (silent-gap) acende a pílula "déficit"', () => {
    renderComProviders(<CampaignDeficitChip data={{ ...dado, affected_by: ['silent-gap'] }} />)
    expect(screen.getByText('déficit')).toBeTruthy()
  })
})

describe('DeficitSplit', () => {
  test('total zero não renderiza nada', () => {
    const { container } = render(<DeficitSplit total={0} />)
    expect(container.firstChild).toBeNull()
  })

  test('sem a quebra: singular "falta 1" e plural "faltam N" com milhar em pt-BR', () => {
    const { container, rerender } = render(<DeficitSplit total={1} />)
    expect(container.textContent).toBe('falta 1')
    rerender(<DeficitSplit total={1234} />)
    expect(container.textContent).toBe('faltam 1.234')
  })

  test('com a quebra mostra "não tocou" e "fora do horário" e só os que são maiores que zero', () => {
    const { container, rerender } = render(<DeficitSplit total={5} absent={3} offSlot={2} />)
    expect(container.textContent).toBe('não tocou 3fora do horário 2')
    expect(container.firstChild.getAttribute('title')).toBe('Déficit 5 = 3 não tocou + 2 fora do horário')
    rerender(<DeficitSplit total={3} absent={3} offSlot={0} />)
    expect(container.textContent).toBe('não tocou 3')
    rerender(<DeficitSplit total={2} absent={0} offSlot={2} stack align="end" />)
    expect(container.textContent).toBe('fora do horário 2')
    expect(container.firstChild.className).toContain('dsplit--stack')
    expect(container.firstChild.className).toContain('dsplit--end')
  })
})
