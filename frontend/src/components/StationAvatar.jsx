import { useState } from 'react'
// buildLogoUrl saiu daqui pro utils em 2026-08-18: o seletor de emissoras do
// /insights desenha o próprio avatar e precisava da mesma resolução de path
// do AppSheet — sem ela, logo de emissora nunca carregava lá.
import { buildLogoUrl } from '../utils/logoUrl'

function getInitials(name) {
  return (name ?? '')
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map(w => w[0].toUpperCase())
    .join('')
}

const PALETTE = [
  'oklch(0.55 0.22 0)',     // rosa
  'oklch(0.40 0.18 262)',   // navy
  'oklch(0.52 0.18 145)',   // green
  'oklch(0.52 0.16 195)',   // teal
  'oklch(0.55 0.17 55)',    // amber
  'oklch(0.48 0.18 295)',   // purple
  'oklch(0.52 0.17 230)',   // blue
]

function hashColor(name) {
  let h = 0
  for (const c of name ?? '') h = (Math.imul(h, 31) + c.charCodeAt(0)) | 0
  return PALETTE[Math.abs(h) % PALETTE.length]
}

export default function StationAvatar({ station, size = 48 }) {
  const [imgErr, setImgErr] = useState(false)
  const url = !imgErr && station?.logo_url ? buildLogoUrl(station.logo_url) : null

  if (url) {
    return (
      <img
        src={url}
        alt={station.name}
        width={size}
        height={size}
        className="station-avatar-img"
        style={{ width: size, height: size }}
        onError={() => setImgErr(true)}
      />
    )
  }

  return (
    <div
      className="station-avatar-fallback"
      style={{
        width: size,
        height: size,
        background: hashColor(station?.name),
        fontSize: Math.round(size * 0.36),
      }}
    >
      {getInitials(station?.name ?? '')}
    </div>
  )
}
