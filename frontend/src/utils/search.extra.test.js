// search.js — normalização e casamento multi-token (token-AND, campo-OR). Complementa
// search.test.mjs, que cobre tokenize/dial/highlight.
import { describe, test, expect } from 'vitest'
import { normalizeText, matchesAllTokens, tokenize } from './search.js'

describe('normalizeText', () => {
  test('tira acento e baixa a caixa', () => {
    expect(normalizeText('Rádio São João')).toBe('radio sao joao')
    expect(normalizeText('ÇÃO')).toBe('cao')
  })
  test('nulo e indefinido viram string vazia; número vira texto', () => {
    expect(normalizeText(null)).toBe('')
    expect(normalizeText(undefined)).toBe('')
    expect(normalizeText(99.9)).toBe('99.9')
  })
})

describe('matchesAllTokens', () => {
  const emissora = { name: 'Rádio Teste FM', city: 'Cidade Teste', state: 'SP', frequency_mhz: 99.9 }

  test('sem tokens tudo casa (busca vazia não filtra)', () => {
    expect(matchesAllTokens(emissora, ['name'], [])).toBe(true)
    expect(matchesAllTokens(emissora, ['name'], null)).toBe(true)
    expect(matchesAllTokens(emissora, ['name'], undefined)).toBe(true)
  })

  test('cada token precisa casar em algum campo (AND entre tokens, OR entre campos)', () => {
    const campos = ['name', 'city', 'state']
    expect(matchesAllTokens(emissora, campos, tokenize('radio sp'))).toBe(true) // nome + UF
    expect(matchesAllTokens(emissora, campos, tokenize('cidade fm'))).toBe(true) // cidade + nome
    expect(matchesAllTokens(emissora, campos, tokenize('radio rj'))).toBe(false) // UF não casa
  })

  test('ignora acento e caixa dos dois lados', () => {
    expect(matchesAllTokens({ name: 'São Paulo' }, ['name'], tokenize('SAO'))).toBe(true)
  })

  test('campo pode ser função extratora', () => {
    const campos = [e => `${e.city}/${e.state}`, e => e.frequency_mhz]
    expect(matchesAllTokens(emissora, campos, tokenize('teste/sp'))).toBe(true)
    expect(matchesAllTokens(emissora, campos, ['99.9'])).toBe(true)
  })

  test('campo ausente no item não quebra e simplesmente não casa', () => {
    expect(matchesAllTokens({ name: 'Rádio Teste' }, ['name', 'city'], ['cidade'])).toBe(false)
    expect(matchesAllTokens(null, ['name'], ['radio'])).toBe(false)
  })
})
