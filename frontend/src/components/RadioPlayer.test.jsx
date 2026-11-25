import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, act } from '@testing-library/react'
import RadioPlayer from './RadioPlayer'
import { useRadioPlayer } from '../contexts/RadioPlayerContext'
import { renderComProviders } from '../test-utils/render'

const EMISSORA = { url: 'https://stream.example.com/a', name: 'Rádio Teste FM', logo: null }
const OUTRA = { url: 'https://stream.example.com/b', name: 'Rádio Outra AM', logo: null }

// Botões que dirigem o contexto real (o mesmo que o app usa nos cards de /stations)
function Controle() {
  const { toggleStation, playStation, isStationPlaying } = useRadioPlayer()
  return (
    <div>
      <button onClick={() => toggleStation(EMISSORA)}>alternar A</button>
      <button onClick={() => playStation(OUTRA)}>tocar B</button>
      <span data-testid="tocando-a">{String(isStationPlaying(EMISSORA.url))}</span>
    </div>
  )
}

function montar() {
  return renderComProviders(<><Controle /><RadioPlayer /></>)
}

const audio = () => document.querySelector('audio')
const status = () => document.querySelector('.radio-player-status').textContent

let play, pause, load
beforeEach(() => {
  localStorage.clear()
  play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue()
  pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  load = vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
})
afterEach(() => { play.mockRestore(); pause.mockRestore(); load.mockRestore() })

describe('RadioPlayer — ciclo de vida', () => {
  test('sem emissora escolhida não renderiza nada', () => {
    montar()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  test('escolher uma emissora abre o player, carrega o stream e tenta tocar sozinho', () => {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
    expect(screen.getByRole('dialog', { name: 'Player Rádio Teste FM' })).toBeTruthy()
    expect(screen.getByText('Rádio Teste FM', { selector: '.radio-player-name' })).toBeTruthy()
    expect(audio().getAttribute('src')).toBe(EMISSORA.url)
    expect(load).toHaveBeenCalled()
    expect(play).toHaveBeenCalled()
    expect(status()).toBe('Conectando…')
    expect(screen.getByTestId('tocando-a').textContent).toBe('true')
  })

  test('alternar a mesma emissora fecha o player; trocar por outra carrega o novo stream', () => {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
    fireEvent.click(screen.getByText('tocar B'))
    expect(screen.getByRole('dialog', { name: 'Player Rádio Outra AM' })).toBeTruthy()
    expect(audio().getAttribute('src')).toBe(OUTRA.url)
    fireEvent.click(screen.getByText('alternar A'))
    fireEvent.click(screen.getByText('alternar A'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  test('Fechar para o áudio e some com o player', () => {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
    fireEvent.click(screen.getByRole('button', { name: 'Fechar player' }))
    expect(pause).toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByTestId('tocando-a').textContent).toBe('false')
  })
})

describe('RadioPlayer — estados do stream', () => {
  function abrir() {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
  }

  test('playing → "Ao vivo" com barra e botão Pausar; pause → "Pausado"', () => {
    abrir()
    fireEvent.canPlay(audio())
    fireEvent.playing(audio())
    expect(status()).toBe('Ao vivo')
    expect(document.querySelector('.radio-player-live-bar')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Pausar' })).toBeTruthy()
    fireEvent.pause(audio())
    expect(status()).toBe('Pausado')
    expect(screen.getByRole('button', { name: 'Reproduzir' })).toBeTruthy()
  })

  test('waiting volta pra "Conectando…"', () => {
    abrir()
    fireEvent.playing(audio())
    fireEvent.waiting(audio())
    expect(status()).toBe('Conectando…')
  })

  test('erro do elemento de áudio mostra "Erro" e trava o botão de tocar', () => {
    abrir()
    fireEvent.error(audio())
    expect(status()).toBe('Erro')
    expect(screen.getByRole('button', { name: 'Reproduzir' }).disabled).toBe(true)
  })

  test('play() rejeitado (não-Abort) vira "Erro"; AbortError é ignorado', async () => {
    play.mockRejectedValueOnce(Object.assign(new Error('bloqueado'), { name: 'NotAllowedError' }))
    abrir()
    await waitFor(() => expect(status()).toBe('Erro'))
  })

  test('AbortError no autoplay não vira erro', async () => {
    play.mockRejectedValueOnce(Object.assign(new Error('abortado'), { name: 'AbortError' }))
    abrir()
    await act(async () => { await Promise.resolve() })
    expect(status()).toBe('Conectando…')
  })

  test('Pausar manual interrompe o áudio; Reproduzir de novo recarrega e toca', async () => {
    abrir()
    fireEvent.playing(audio())
    pause.mockClear()
    fireEvent.click(screen.getByRole('button', { name: 'Pausar' }))
    expect(pause).toHaveBeenCalled()
    expect(status()).toBe('Pausado')

    load.mockClear(); play.mockClear()
    fireEvent.click(screen.getByRole('button', { name: 'Reproduzir' }))
    expect(load).toHaveBeenCalled()
    expect(play).toHaveBeenCalled()
    await waitFor(() => expect(status()).toBe('Ao vivo'))
  })

  test('Reproduzir manual com falha vira "Erro"', async () => {
    abrir()
    fireEvent.pause(audio())
    play.mockRejectedValueOnce(Object.assign(new Error('rede'), { name: 'NetworkError' }))
    fireEvent.click(screen.getByRole('button', { name: 'Reproduzir' }))
    await waitFor(() => expect(status()).toBe('Erro'))
  })
})

describe('RadioPlayer — volume', () => {
  function abrir() {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
  }

  test('o volume começa em 0,8 e o slider ajusta o elemento de áudio', () => {
    abrir()
    const slider = screen.getByLabelText('Volume')
    expect(slider.value).toBe('0.8')
    expect(audio().volume).toBeCloseTo(0.8)
    fireEvent.change(slider, { target: { value: '0.3' } })
    expect(audio().volume).toBeCloseTo(0.3)
    expect(screen.getByLabelText('Volume').value).toBe('0.3')
  })

  test('slider em zero muta; arrastar de novo desmuta', () => {
    abrir()
    const slider = screen.getByLabelText('Volume')
    fireEvent.change(slider, { target: { value: '0' } })
    expect(screen.getByRole('button', { name: 'Ativar som' })).toBeTruthy()
    fireEvent.change(slider, { target: { value: '0.5' } })
    expect(screen.getByRole('button', { name: 'Mutar' })).toBeTruthy()
  })

  test('botão de mutar zera o volume e o segundo clique restaura o anterior', () => {
    abrir()
    fireEvent.change(screen.getByLabelText('Volume'), { target: { value: '0.6' } })
    fireEvent.click(screen.getByRole('button', { name: 'Mutar' }))
    expect(audio().volume).toBe(0)
    expect(screen.getByLabelText('Volume').value).toBe('0')
    fireEvent.click(screen.getByRole('button', { name: 'Ativar som' }))
    expect(audio().volume).toBeCloseTo(0.6)
  })

  test('passar o mouse abre o popover do volume', () => {
    abrir()
    const popover = document.querySelector('.radio-player-volume-popover')
    expect(popover.className).not.toContain('is-open')
    fireEvent.mouseEnter(document.querySelector('.radio-player-volume'))
    expect(popover.className).toContain('is-open')
    fireEvent.mouseLeave(document.querySelector('.radio-player-volume'))
    expect(popover.className).not.toContain('is-open')
  })
})

describe('RadioPlayer — arrastar', () => {
  function abrir() {
    montar()
    fireEvent.click(screen.getByText('alternar A'))
    return screen.getByRole('dialog')
  }

  test('arrastar o puxador move o player, e soltar grava a posição', () => {
    const player = abrir()
    expect(player.className).not.toContain('radio-player--dragged')
    const puxador = screen.getByRole('button', { name: 'Arrastar player' })
    fireEvent.mouseDown(puxador, { clientX: 10, clientY: 10 })
    fireEvent.mouseMove(window, { clientX: 70, clientY: 50 })
    expect(player.style.left).toBe('60px')
    expect(player.style.top).toBe('40px')
    expect(player.className).toContain('radio-player--dragged')
    fireEvent.mouseUp(window)
    expect(JSON.parse(localStorage.getItem('radioPlayerPosition'))).toEqual({ x: 60, y: 40 })
  })

  test('movimento de até 3px não conta como arrasto (é só um clique)', () => {
    const player = abrir()
    fireEvent.mouseDown(screen.getByRole('button', { name: 'Arrastar player' }), { clientX: 10, clientY: 10 })
    fireEvent.mouseMove(window, { clientX: 12, clientY: 11 })
    expect(player.className).not.toContain('radio-player--dragged')
  })

  test('a posição é limitada à janela (nunca negativa)', () => {
    const player = abrir()
    fireEvent.mouseDown(screen.getByRole('button', { name: 'Arrastar player' }), { clientX: 100, clientY: 100 })
    fireEvent.mouseMove(window, { clientX: 0, clientY: 0 })
    expect(player.style.left).toBe('0px')
    expect(player.style.top).toBe('0px')
  })

  test('toque também arrasta o player', () => {
    const player = abrir()
    fireEvent.touchStart(screen.getByRole('button', { name: 'Arrastar player' }), { touches: [{ clientX: 5, clientY: 5 }] })
    fireEvent.touchMove(window, { touches: [{ clientX: 45, clientY: 25 }] })
    expect(player.style.left).toBe('40px')
    expect(player.style.top).toBe('20px')
    fireEvent.touchEnd(window)
    expect(JSON.parse(localStorage.getItem('radioPlayerPosition'))).toEqual({ x: 40, y: 20 })
  })

  test('lê a posição salva ao abrir e a reencaixa quando a janela é redimensionada', () => {
    localStorage.setItem('radioPlayerPosition', JSON.stringify({ x: 5000, y: 4000 }))
    const player = abrir()
    expect(player.className).toContain('radio-player--dragged')
    fireEvent(window, new Event('resize'))
    expect(parseInt(player.style.left, 10)).toBeLessThanOrEqual(window.innerWidth)
    expect(parseInt(player.style.top, 10)).toBeLessThanOrEqual(window.innerHeight)
  })

  test('posição salva corrompida é ignorada', () => {
    localStorage.setItem('radioPlayerPosition', '{nao-json')
    const player = abrir()
    expect(player.className).not.toContain('radio-player--dragged')
  })
})
