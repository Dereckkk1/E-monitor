import { useState } from 'react'

const APPSHEET_BASE    = 'https://www.appsheet.com/image/getimageurl'
const APPSHEET_APP     = 'E-R%C3%A1dios-408183446-24-03-22-2'
const APPSHEET_TABLE   = 'R%C3%A1dios%202'
const APPSHEET_VERSION = '1.002203'

function buildLogoUrl(path) {
  if (!path) return null
  if (path.startsWith('http://') || path.startsWith('https://')) return path
  // Same-origin relative path (e.g. our Audiency image proxy at
  // /v1/internal/audiency-image?token=...). Pass through untouched —
  // Vite proxies /v1 to the backend in dev; in prod they're on the same
  // host. WITHOUT this branch the path would be misinterpreted as an
  // AppSheet filename and wrapped in the AppSheet image URL.
  if (path.startsWith('/')) return path
  // AppSheet-style path e.g. "Rádios 2_Images/abc.jpg"
  return `${APPSHEET_BASE}?appName=${APPSHEET_APP}&tableName=${APPSHEET_TABLE}&fileName=${encodeURIComponent(path)}&appVersion=${APPSHEET_VERSION}&signature=`
}

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
