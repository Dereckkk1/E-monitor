import { createContext, useCallback, useContext, useState } from 'react'

// Single-station "now playing" coordinator. Ported from the E-radios
// /marketplace player so the UX is identical: at most one stream plays at a
// time, and any card on /stations can ask "am I the playing one?" via
// isStationPlaying(url).
//
// Shape of currentStation: { url, name, logo }. Null = idle.

const RadioPlayerContext = createContext(null)

export function RadioPlayerProvider({ children }) {
  const [currentStation, setCurrentStation] = useState(null)

  const playStation = useCallback((station) => {
    setCurrentStation(station)
  }, [])

  const stopStation = useCallback(() => {
    setCurrentStation(null)
  }, [])

  const toggleStation = useCallback((station) => {
    setCurrentStation(prev => {
      if (prev?.url === station.url) return null
      return station
    })
  }, [])

  const isStationPlaying = useCallback(
    (streamingUrl) => currentStation?.url === streamingUrl,
    [currentStation]
  )

  return (
    <RadioPlayerContext.Provider
      value={{ currentStation, playStation, stopStation, toggleStation, isStationPlaying }}
    >
      {children}
    </RadioPlayerContext.Provider>
  )
}

export function useRadioPlayer() {
  const ctx = useContext(RadioPlayerContext)
  if (!ctx) throw new Error('useRadioPlayer must be used inside <RadioPlayerProvider>')
  return ctx
}
