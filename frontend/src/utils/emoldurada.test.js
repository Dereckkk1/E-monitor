// emoldurada.js — detecta quando a página roda dentro de um quadro (clickjacking).
import { describe, test, expect, afterEach, vi } from 'vitest'
import { emoldurada } from './emoldurada.js'

afterEach(() => { vi.unstubAllGlobals() })

describe('emoldurada', () => {
  test('página de topo: não está emoldurada', () => {
    expect(emoldurada()).toBe(false)
  })

  test('window.top diferente de window.self: emoldurada', () => {
    vi.stubGlobal('window', { top: { outra: 'janela' }, self: { esta: 'janela' } })
    expect(emoldurada()).toBe(true)
  })

  test('ler window.top estourando (quadro de outra origem) também conta como emoldurada', () => {
    const janela = { self: {} }
    Object.defineProperty(janela, 'top', { get() { throw new Error('SecurityError') } })
    vi.stubGlobal('window', janela)
    expect(emoldurada()).toBe(true)
  })
})
