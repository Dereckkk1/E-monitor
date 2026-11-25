import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import MonthNavigator from './MonthNavigator'
import MonthStepper from './MonthStepper'

describe('MonthNavigator', () => {
  test('mostra o mês por extenso e navega pro primeiro dia do mês anterior/seguinte', () => {
    const onChange = vi.fn()
    render(<MonthNavigator value={new Date(2026, 5, 17)} onChange={onChange} />)
    expect(screen.getByText('Junho 2026')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Mês anterior' }))
    expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 4, 1))
    fireEvent.click(screen.getByRole('button', { name: 'Próximo mês' }))
    expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 6, 1))
  })

  test('virada de ano: janeiro volta pra dezembro do ano anterior', () => {
    const onChange = vi.fn()
    render(<MonthNavigator value={new Date(2026, 0, 10)} onChange={onChange} />)
    expect(screen.getByText('Janeiro 2026')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Mês anterior' }))
    expect(onChange).toHaveBeenCalledWith(new Date(2025, 11, 1))
  })

  test('minDate e maxDate desabilitam as setas nos limites', () => {
    render(<MonthNavigator value={new Date(2026, 5, 1)} onChange={() => {}}
      minDate={new Date(2026, 5, 20)} maxDate={new Date(2026, 5, 25)} />)
    expect(screen.getByRole('button', { name: 'Mês anterior' }).disabled).toBe(true)
    expect(screen.getByRole('button', { name: 'Próximo mês' }).disabled).toBe(true)
  })

  test('dentro dos limites as setas ficam ativas', () => {
    render(<MonthNavigator value={new Date(2026, 5, 1)} onChange={() => {}}
      minDate={new Date(2026, 0, 1)} maxDate={new Date(2026, 11, 31)} />)
    expect(screen.getByRole('button', { name: 'Mês anterior' }).disabled).toBe(false)
    expect(screen.getByRole('button', { name: 'Próximo mês' }).disabled).toBe(false)
  })
})

describe('MonthStepper', () => {
  test('as setas passam o VALOR (AAAA-MM) já deslocado, inclusive na virada do ano', () => {
    const onChange = vi.fn()
    const { rerender } = render(<MonthStepper value="2026-12" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: 'Próximo mês' }))
    expect(onChange).toHaveBeenLastCalledWith('2027-01')
    rerender(<MonthStepper value="2026-01" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: 'Mês anterior' }))
    expect(onChange).toHaveBeenLastCalledWith('2025-12')
  })

  test('digitar no campo também devolve o valor (e não o evento)', () => {
    const onChange = vi.fn()
    const { container } = render(<MonthStepper value="2026-03" onChange={onChange} />)
    fireEvent.change(container.querySelector('input[type="month"]'), { target: { value: '2026-08' } })
    expect(onChange).toHaveBeenCalledWith('2026-08')
  })

  test('vazio parte do mês atual', () => {
    const onChange = vi.fn()
    render(<MonthStepper value="" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: 'Próximo mês' }))
    const hoje = new Date()
    const esperado = new Date(hoje.getFullYear(), hoje.getMonth() + 1, 1)
    expect(onChange).toHaveBeenCalledWith(`${esperado.getFullYear()}-${String(esperado.getMonth() + 1).padStart(2, '0')}`)
  })

  test('desabilitado: setas e campo travados e o clique não dispara onChange', () => {
    const onChange = vi.fn()
    const { container } = render(<MonthStepper value="2026-03" onChange={onChange} disabled />)
    expect(screen.getByRole('button', { name: 'Mês anterior' }).disabled).toBe(true)
    expect(container.querySelector('input').disabled).toBe(true)
    expect(container.firstChild.className).toContain('flow-month-step--disabled')
    fireEvent.click(screen.getByRole('button', { name: 'Próximo mês' }))
    expect(onChange).not.toHaveBeenCalled()
  })
})
