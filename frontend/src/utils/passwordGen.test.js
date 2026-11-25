// passwordGen.js — senha forte gerada com crypto, sem caracteres ambíguos.
import { describe, test, expect, afterEach, vi } from 'vitest'
import { generateStrongPassword } from './passwordGen.js'

afterEach(() => { vi.restoreAllMocks() })

const contar = (s, re) => (s.match(re) ?? []).length

describe('generateStrongPassword', () => {
  test('16 caracteres por padrão', () => {
    expect(generateStrongPassword()).toHaveLength(16)
  })

  test('respeita o tamanho pedido, com piso de 12', () => {
    expect(generateStrongPassword(24)).toHaveLength(24)
    expect(generateStrongPassword(12)).toHaveLength(12)
    expect(generateStrongPassword(5)).toHaveLength(12)
    expect(generateStrongPassword(0)).toHaveLength(12)
  })

  test('sempre traz as 4 famílias: minúscula, maiúscula, dígito e símbolo', () => {
    for (let i = 0; i < 100; i++) {
      const s = generateStrongPassword(12)
      expect(s).toMatch(/[a-z]/)
      expect(s).toMatch(/[A-Z]/)
      expect(s).toMatch(/[0-9]/)
      expect(s).toMatch(/[!@#$%&*?]/)
    }
  })

  test('nunca usa caracteres ambíguos (l o 1 0 O I)', () => {
    for (let i = 0; i < 300; i++) {
      expect(generateStrongPassword(32)).not.toMatch(/[lo10OI]/)
    }
  })

  test('só usa o alfabeto permitido', () => {
    for (let i = 0; i < 100; i++) {
      expect(generateStrongPassword(20)).toMatch(/^[a-km-zA-HJ-NP-Z2-9!@#$%&*?]+$/)
    }
  })

  test('usa o gerador criptográfico e senhas consecutivas diferem', () => {
    const espiao = vi.spyOn(globalThis.crypto, 'getRandomValues')
    const a = generateStrongPassword()
    const b = generateStrongPassword()
    expect(espiao).toHaveBeenCalled()
    expect(a).not.toBe(b)
  })

  test('com aleatoriedade fixa em 0: 1 de cada família garantida + o resto pelo primeiro símbolo do alfabeto', () => {
    vi.spyOn(globalThis.crypto, 'getRandomValues').mockImplementation((buf) => { buf[0] = 0; return buf })
    const s = generateStrongPassword(16)
    // garantidas: a, A, 2, ! ; as 12 restantes saem de ALL[0] = 'a'
    expect(contar(s, /a/g)).toBe(13)
    expect(contar(s, /A/g)).toBe(1)
    expect(contar(s, /2/g)).toBe(1)
    expect(contar(s, /!/g)).toBe(1)
  })
})
