import { describe, test, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import StationAvatar from './StationAvatar'
import ClientAvatar from './ClientAvatar'

describe('StationAvatar', () => {
  test('sem logo mostra as iniciais das duas primeiras palavras, em maiúsculas', () => {
    const { container } = render(<StationAvatar station={{ name: 'rádio teste fm' }} />)
    expect(container.querySelector('.station-avatar-fallback').textContent).toBe('RT')
  })

  test('nome vazio ou ausente não quebra (sem iniciais)', () => {
    const { container, rerender } = render(<StationAvatar station={{ name: '' }} />)
    expect(container.querySelector('.station-avatar-fallback').textContent).toBe('')
    rerender(<StationAvatar station={undefined} />)
    expect(container.querySelector('.station-avatar-fallback').textContent).toBe('')
  })

  test('a cor do fallback é estável para o mesmo nome e o tamanho segue a prop', () => {
    const { container, rerender } = render(<StationAvatar station={{ name: 'Rádio Teste FM' }} size={40} />)
    const a = container.querySelector('.station-avatar-fallback')
    const corA = a.style.background
    expect(a.style.width).toBe('40px')
    expect(a.style.fontSize).toBe('14px') // 40 * 0.36
    rerender(<StationAvatar station={{ name: 'Rádio Teste FM' }} size={40} />)
    expect(container.querySelector('.station-avatar-fallback').style.background).toBe(corA)
  })

  test('com logo absoluto renderiza <img> com alt do nome; se a imagem falha volta às iniciais', () => {
    const { container } = render(<StationAvatar station={{ name: 'Rádio Teste FM', logo_url: 'https://cdn.example.com/logo.png' }} size={36} />)
    const img = screen.getByAltText('Rádio Teste FM')
    expect(img.getAttribute('src')).toBe('https://cdn.example.com/logo.png')
    expect(img.getAttribute('width')).toBe('36')
    fireEvent.error(img)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('.station-avatar-fallback').textContent).toBe('RT')
  })

  test('path relativo ao estilo AppSheet é resolvido pro endpoint de imagem; proxy removido cai nas iniciais', () => {
    const { container, rerender } = render(<StationAvatar station={{ name: 'Rádio Teste FM', logo_url: 'Rádios 2_Images/abc.png' }} />)
    expect(container.querySelector('img').getAttribute('src')).toContain('appsheet.com/image/getimageurl')
    expect(container.querySelector('img').getAttribute('src')).toContain(encodeURIComponent('Rádios 2_Images/abc.png'))
    rerender(<StationAvatar station={{ name: 'Rádio Teste FM', logo_url: '/v1/internal/vendor-image?token=x' }} />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('.station-avatar-fallback')).not.toBeNull()
  })

  test('path absoluto na própria origem passa direto', () => {
    const { container } = render(<StationAvatar station={{ name: 'Rádio Teste FM', logo_url: '/media/logo.png' }} />)
    expect(container.querySelector('img').getAttribute('src')).toBe('/media/logo.png')
  })
})

describe('ClientAvatar', () => {
  test('sem logo mostra a inicial do nome e o nome no title', () => {
    render(<ClientAvatar client={{ name: 'cliente teste' }} />)
    const el = screen.getByTitle('cliente teste')
    expect(el.textContent).toBe('C')
  })

  test('aceita o formato client_name / client_logo_url do sininho', () => {
    render(<ClientAvatar client={{ client_name: 'Marca Teste', client_logo_url: 'https://cdn.example.com/m.png' }} />)
    expect(screen.getByAltText('Marca Teste').getAttribute('src')).toBe('https://cdn.example.com/m.png')
  })

  test('sem nome nenhum mostra "?"', () => {
    render(<ClientAvatar client={null} />)
    expect(screen.getByTitle('?').textContent).toBe('?')
  })

  test('imagem quebrada cai na inicial; trocar a URL tenta a imagem de novo', () => {
    const { rerender } = render(<ClientAvatar client={{ name: 'Marca Teste', logo_url: 'https://cdn.example.com/a.png' }} />)
    fireEvent.error(screen.getByAltText('Marca Teste'))
    expect(screen.queryByAltText('Marca Teste')).toBeNull()
    expect(screen.getByTitle('Marca Teste').textContent).toBe('M')
    rerender(<ClientAvatar client={{ name: 'Marca Teste', logo_url: 'https://cdn.example.com/b.png' }} />)
    expect(screen.getByAltText('Marca Teste').getAttribute('src')).toBe('https://cdn.example.com/b.png')
  })

  test('o tamanho vira largura/altura e a fonte nunca fica menor que 10px', () => {
    render(<ClientAvatar client={{ name: 'Marca Teste' }} size={16} />)
    const el = screen.getByTitle('Marca Teste')
    expect(el.style.width).toBe('16px')
    expect(el.style.fontSize).toBe('10px')
  })
})
