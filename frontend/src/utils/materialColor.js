// Stable per-material color derived from the material UUID. Same material
// always renders with the same hue across the report — used by the airtime
// detection row stripe and the "Total por áudio" panel rows so the colour
// reads as a single visual identity for the audio.

const PALETTE = [
  '#A16207', // amber-700
  '#E11D48', // rose-600
  '#9333EA', // purple-600
  '#3B82F6', // blue-500
  '#0891B2', // cyan-600
  '#16A34A', // green-600
  '#EA580C', // orange-600
  '#0D9488', // teal-600
  '#7C3AED', // violet-600
  '#DB2777', // pink-600
  '#65A30D', // lime-600
  '#1D4ED8', // indigo-700
  '#BE123C', // rose-700
  '#15803D', // green-700
  '#C2410C', // orange-700
  '#581C87', // purple-900
]

export function materialColor(key) {
  if (!key) return PALETTE[0]
  let h = 0
  for (let i = 0; i < key.length; i++) {
    h = (h * 31 + key.charCodeAt(i)) | 0
  }
  return PALETTE[Math.abs(h) % PALETTE.length]
}

export const MATERIAL_PALETTE = PALETTE
