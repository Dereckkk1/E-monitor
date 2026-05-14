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
