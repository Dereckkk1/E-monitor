import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import CampaignSummaryStrip from './CampaignSummaryStrip'
import CoverageSummary from './CoverageSummary'
import DayCell from './DayCell'

function hexParaRgb(hex) {
  const n = parseInt(hex.slice(1), 16)
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`
}

describe('CampaignSummaryStrip', () => {
  test('sem nome mostra "Nova campanha" e período em branco', () => {
    render(<CampaignSummaryStrip />)
    expect(screen.getByText('Nova campanha')).toBeTruthy()
    expect(screen.getByText('—')).toBeTruthy()
    expect(screen.queryByText(/distribuídos/)).toBeNull()
  })

  test('mostra nome, cliente, período em dd/mm/aaaa e contagens', () => {
    render(<CampaignSummaryStrip name="Campanha Teste" clientName="Cliente Teste"
      startDate="2026-03-01" endDate="2026-03-31" stationCount={12} materialCount={3} />)
    expect(screen.getByText(/Campanha Teste/)).toBeTruthy()
    expect(screen.getByText('· Cliente Teste')).toBeTruthy()
    expect(screen.getByText('01/03/2026 → 31/03/2026')).toBeTruthy()
    expect(screen.getByText('12')).toBeTruthy()
    expect(screen.getByText('3')).toBeTruthy()
  })

  test.each([
    [5, 5, '#dcfce7'],   // tudo distribuído: verde
    [2, 5, '#fef9c3'],   // parcial: amarelo
    [0, 5, '#fee2e2'],   // nada: vermelho
  ])('chip de distribuição %i de %i usa o tom certo', (feitos, total, fundo) => {
    render(<CampaignSummaryStrip distributedCount={feitos} distributedTotal={total} />)
    const chip = screen.getByText(`${feitos} de ${total} distribuídos`)
    expect(chip.style.background).toBe(hexParaRgb(fundo))
  })
})

describe('CoverageSummary', () => {
  test('soma as linhas do período e calcula a cobertura em %', () => {
    render(<CoverageSummary summary={[
      { expected: 10, in_slot: 8, deficit: 2, bonus: 1, out_slot: 0, out_date: 0 },
      { expected: 10, in_slot: 9, deficit: 1, bonus: 0, out_slot: 2, out_date: 3 },
    ]} />)
    expect(screen.getByText('85%')).toBeTruthy()   // 17 / 20
    expect(screen.getByText('Faltou').nextSibling.textContent).toBe('3')
    expect(screen.getByText('Bônus').nextSibling.textContent).toBe('+1')
    expect(screen.getByText('Fora faixa').nextSibling.textContent).toBe('+2')
    expect(screen.getByText('Fora data').nextSibling.textContent).toBe('+3')
  })

  test('sem esperado mostra "—" e esconde os blocos zerados', () => {
    render(<CoverageSummary summary={[]} />)
    expect(screen.getByText('—')).toBeTruthy()
    expect(screen.queryByText('Faltou')).toBeNull()
    expect(screen.queryByText('Bônus')).toBeNull()
  })

  test.each([
    [95, '#15803d'], [80, '#a16207'], [50, '#b91c1c'],
  ])('cobertura de %i%% pinta o número na cor da faixa', (pct, cor) => {
    render(<CoverageSummary summary={[{ expected: 100, in_slot: pct }]} />)
    expect(screen.getByText(`${pct}%`).style.color).toBe(hexParaRgb(cor))
  })
})

describe('DayCell', () => {
  test('mostra só os contadores maiores que zero, com sinal no déficit e nos extras', () => {
    const { container } = render(<DayCell expected={3} inSlot={2} deficit={1} bonus={4} outSlot={0} outDate={5} />)
    expect(container.textContent).toBe('32-1+4+5')
  })

  test('clique dispara onClick, mas célula fora do período não reage', () => {
    const onClick = vi.fn()
    const { container, rerender } = render(<DayCell expected={1} onClick={onClick} hint="dica" />)
    fireEvent.click(container.firstChild)
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(container.firstChild.getAttribute('title')).toBe('dica')
    rerender(<DayCell expected={1} onClick={onClick} isOutsideRange />)
    fireEvent.click(container.firstChild)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  test('com override a meta troca de cor e aparece o ponto de marcação', () => {
    const { container, rerender } = render(<DayCell expected={2} />)
    const corNormal = container.querySelector('span[title], span[style*="min-width: 18px"]').style.background
    expect(container.querySelectorAll('span[style*="border-radius: 50%"]').length).toBe(0)
    rerender(<DayCell expected={2} hasOverride />)
    expect(container.querySelector('span[style*="min-width: 18px"]').style.background).not.toBe(corNormal)
    expect(container.querySelectorAll('span[style*="border-radius: 50%"]').length).toBe(1)
  })

  test('com callbacks, passar o mouse mostra − e +, que chamam sem disparar o clique da célula', () => {
    const onClick = vi.fn(), onInc = vi.fn(), onDec = vi.fn()
    const { container } = render(<DayCell expected={2} onClick={onClick} onIncrement={onInc} onDecrement={onDec} />)
    expect(screen.queryByRole('button', { name: 'Aumentar' })).toBeNull()
    fireEvent.mouseEnter(container.firstChild)
    fireEvent.click(screen.getByRole('button', { name: 'Aumentar' }))
    fireEvent.click(screen.getByRole('button', { name: 'Diminuir' }))
    expect(onInc).toHaveBeenCalledTimes(1)
    expect(onDec).toHaveBeenCalledTimes(1)
    expect(onClick).not.toHaveBeenCalled()
    fireEvent.mouseLeave(container.firstChild)
    expect(screen.queryByRole('button', { name: 'Aumentar' })).toBeNull()
  })

  test('célula fora do período nunca mostra os botões de ajuste', () => {
    const { container } = render(<DayCell isOutsideRange onIncrement={() => {}} />)
    fireEvent.mouseEnter(container.firstChild)
    expect(screen.queryByRole('button', { name: 'Aumentar' })).toBeNull()
  })
})
