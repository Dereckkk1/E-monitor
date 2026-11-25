// RadioPlayerContext: no máximo UMA emissora tocando por vez; qualquer card de
// /stations pergunta "sou eu quem toca?" por isStationPlaying(url).
import { describe, test, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { RadioPlayerProvider, useRadioPlayer } from './RadioPlayerContext'

const wrapper = ({ children }) => <RadioPlayerProvider>{children}</RadioPlayerProvider>
const montar = () => renderHook(() => useRadioPlayer(), { wrapper })

const RADIO_A = { url: 'https://example.com/a.mp3', name: 'Rádio Teste A', logo: null }
const RADIO_B = { url: 'https://example.com/b.mp3', name: 'Rádio Teste B', logo: 'https://example.com/b.png' }

describe('RadioPlayerProvider', () => {
  test('começa ocioso: nada tocando', () => {
    const { result } = montar()
    expect(result.current.currentStation).toBeNull()
    expect(result.current.isStationPlaying(RADIO_A.url)).toBe(false)
  })

  test('playStation toca a emissora e troca a anterior (nunca duas ao mesmo tempo)', () => {
    const { result } = montar()
    act(() => { result.current.playStation(RADIO_A) })
    expect(result.current.currentStation).toEqual(RADIO_A)
    expect(result.current.isStationPlaying(RADIO_A.url)).toBe(true)
    expect(result.current.isStationPlaying(RADIO_B.url)).toBe(false)

    act(() => { result.current.playStation(RADIO_B) })
    expect(result.current.currentStation).toEqual(RADIO_B)
    expect(result.current.isStationPlaying(RADIO_A.url)).toBe(false)
    expect(result.current.isStationPlaying(RADIO_B.url)).toBe(true)
  })

  test('stopStation volta ao ocioso', () => {
    const { result } = montar()
    act(() => { result.current.playStation(RADIO_A) })
    act(() => { result.current.stopStation() })
    expect(result.current.currentStation).toBeNull()
    expect(result.current.isStationPlaying(RADIO_A.url)).toBe(false)
  })

  test('toggleStation: toca, para ao repetir a mesma, e troca por outra', () => {
    const { result } = montar()
    act(() => { result.current.toggleStation(RADIO_A) })
    expect(result.current.currentStation).toEqual(RADIO_A)

    act(() => { result.current.toggleStation(RADIO_A) })
    expect(result.current.currentStation).toBeNull()

    act(() => { result.current.toggleStation(RADIO_A) })
    act(() => { result.current.toggleStation(RADIO_B) })
    expect(result.current.currentStation).toEqual(RADIO_B)
  })

  test('isStationPlaying compara pela URL do stream', () => {
    const { result } = montar()
    act(() => { result.current.playStation({ ...RADIO_A, name: 'outro nome' }) })
    expect(result.current.isStationPlaying(RADIO_A.url)).toBe(true)
    expect(result.current.isStationPlaying(undefined)).toBe(false)
  })
})

describe('useRadioPlayer fora do provider', () => {
  test('lança um erro que diz o que faltou', () => {
    const erro = console.error
    console.error = () => {}
    try {
      expect(() => renderHook(() => useRadioPlayer())).toThrow('useRadioPlayer must be used inside <RadioPlayerProvider>')
    } finally {
      console.error = erro
    }
  })
})
