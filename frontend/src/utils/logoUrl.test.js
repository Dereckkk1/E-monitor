// logoUrl.js — logo de emissora carregável: URL absoluta passa, proxy morto some,
// path do AppSheet vira a URL do getimageurl.
import { describe, test, expect } from 'vitest'
import { safeLogoUrl, buildLogoUrl } from './logoUrl.js'

describe('safeLogoUrl', () => {
  test('URL comum passa como veio', () => {
    expect(safeLogoUrl('https://example.com/logo.png')).toBe('https://example.com/logo.png')
    expect(safeLogoUrl('Rádios 2_Images/abc.png')).toBe('Rádios 2_Images/abc.png')
  })

  test('proxy aposentado do Vendor vira null (não dispara 404)', () => {
    expect(safeLogoUrl('/v1/internal/vendor-image?token=abc')).toBeNull()
  })

  test.each([null, undefined, '', 0, 42, {}, ['a']])('valor inválido (%j) vira null', (valor) => {
    expect(safeLogoUrl(valor)).toBeNull()
  })
})

describe('buildLogoUrl', () => {
  test('http e https passam direto', () => {
    expect(buildLogoUrl('https://example.com/a.png')).toBe('https://example.com/a.png')
    expect(buildLogoUrl('http://example.com/a.png')).toBe('http://example.com/a.png')
  })

  test('path absoluto na própria origem passa direto', () => {
    expect(buildLogoUrl('/logos/a.png')).toBe('/logos/a.png')
  })

  test('path relativo do AppSheet vira a URL de imagem com o arquivo codificado', () => {
    const url = buildLogoUrl('Rádios 2_Images/abc def.png')
    expect(url.startsWith('https://www.appsheet.com/image/getimageurl?')).toBe(true)
    expect(url).toContain(`fileName=${encodeURIComponent('Rádios 2_Images/abc def.png')}`)
    expect(url).toContain('appName=')
    expect(url).toContain('tableName=')
    expect(url.endsWith('&signature=')).toBe(true)
  })

  test('sem logo (ou proxy morto) devolve null e o avatar cai nas iniciais', () => {
    expect(buildLogoUrl('')).toBeNull()
    expect(buildLogoUrl(undefined)).toBeNull()
    expect(buildLogoUrl('/v1/internal/vendor-image?token=x')).toBeNull()
  })
})
