// Returns the logo URL as-is, or null when it points to the now-removed
// Audiency image proxy. Avatar components treat null as "no logo" and fall
// back to initials.
//
// Background: historically /v1/internal/audiency-image?token=… proxied
// browser <img> requests to api.audiency.io to keep the apiKey off the
// client. Every page view multiplied into N hits on Audiency's side. The
// proxy is gone; we gate render so we don't fire 404s either.
export function safeLogoUrl(url) {
  if (!url) return null
  if (typeof url !== 'string') return null
  if (url.startsWith('/v1/internal/audiency-image')) return null
  return url
}

// ─── buildLogoUrl ───────────────────────────────────────────────────────────

const APPSHEET_BASE    = 'https://www.appsheet.com/image/getimageurl'
const APPSHEET_APP     = 'E-R%C3%A1dios-408183446-24-03-22-2'
const APPSHEET_TABLE   = 'R%C3%A1dios%202'
const APPSHEET_VERSION = '1.002203'

// Resolve o logo de uma EMISSORA para uma URL carregável.
//
// stations.logo_url quase nunca é uma URL: o import do Audiency grava o path
// relativo do AppSheet ("Rádios 2_Images/abc.png"), que num <img src> resolve
// contra a origem da página e 404a. Quem renderiza logo de emissora tem que
// passar por aqui — usar safeLogoUrl sozinho devolve o path cru e o avatar cai
// silenciosamente nas iniciais (foi o que aconteceu no seletor do /insights).
//
// Vive aqui, e não dentro do StationAvatar, porque não é só ele que desenha
// emissora: o seletor do /insights tem avatar próprio (menor, quadrado) e
// precisa da MESMA resolução.
export function buildLogoUrl(path) {
  const safe = safeLogoUrl(path)
  if (!safe) return null
  if (safe.startsWith('http://') || safe.startsWith('https://')) return safe
  // Path relativo à própria origem. Passa direto — o Vite faz proxy de /v1 pro
  // backend em dev, e em prod estão no mesmo host.
  if (safe.startsWith('/')) return safe
  // Path estilo AppSheet, ex. "Rádios 2_Images/abc.jpg"
  return `${APPSHEET_BASE}?appName=${APPSHEET_APP}&tableName=${APPSHEET_TABLE}&fileName=${encodeURIComponent(safe)}&appVersion=${APPSHEET_VERSION}&signature=`
}
