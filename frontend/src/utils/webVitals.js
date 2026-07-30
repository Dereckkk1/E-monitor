// Coleta leve de Web Vitals usando APIs nativas (PerformanceObserver +
// Navigation Timing). Reporta para POST /v1/internal/web-vitals.
//
// Não usamos o pacote `web-vitals` do Google para evitar dependência extra —
// implementamos as métricas que dão pra ler direto sem ele:
//   - FCP  (First Contentful Paint)        via PerformanceObserver('paint')
//   - LCP  (Largest Contentful Paint)      via PerformanceObserver('largest-contentful-paint')
//   - INP  (proxy via PerformanceObserver('event'))
//   - TTFB (Navigation Timing)
//   - CLS  (PerformanceObserver('layout-shift'))
//
// O backend valida nome ∈ {LCP, FID, INP, CLS, FCP, TTFB} e rating ∈
// {good, needs-improvement, poor}. Calculamos o rating no client conforme as
// faixas do Google.

import api from '../api/client'
import { getStoredToken } from '../contexts/AuthContext'

function rate(name, value) {
  // Thresholds oficiais Google (boa | precisa melhorar | ruim).
  const T = {
    LCP:  [2500, 4000],
    FID:  [100, 300],
    INP:  [200, 500],
    CLS:  [0.1, 0.25],
    FCP:  [1800, 3000],
    TTFB: [800, 1800],
  }[name]
  if (!T) return 'good'
  if (value <= T[0]) return 'good'
  if (value <= T[1]) return 'needs-improvement'
  return 'poor'
}

function send(name, value) {
  // Sem sessão não há o que reportar: o endpoint exige JWT, então a chamada
  // seria um 401 garantido. Pior, o `.catch` abaixo não segura o estrago — o
  // interceptor do axios roda ANTES dele e chutava o visitante de páginas
  // públicas (/boasvindas) pro /login. Telemetria nunca pode custar a página.
  if (!getStoredToken()) return

  // Não esperamos resposta — fire and forget. O backend devolve 204/202;
  // erros são silenciosos (não queremos quebrar a UX por telemetria).
  const page = window.location.pathname
  api.post('/web-vitals', { name, value, rating: rate(name, value), page })
    .catch(() => {})
}

// Reporta no máximo uma vez por métrica por carregamento (exceto INP/CLS, que
// re-emitem com novos valores se ficarem piores).
const reported = new Set()
function reportOnce(name, value) {
  if (reported.has(name)) return
  reported.add(name)
  send(name, value)
}

export function initWebVitals() {
  if (typeof window === 'undefined' || !('PerformanceObserver' in window)) return

  // FCP — entrada 'first-contentful-paint' do tipo 'paint'.
  try {
    const obs = new PerformanceObserver((list) => {
      for (const e of list.getEntries()) {
        if (e.name === 'first-contentful-paint') {
          reportOnce('FCP', Math.round(e.startTime))
          obs.disconnect()
          break
        }
      }
    })
    obs.observe({ type: 'paint', buffered: true })
  } catch { /* navegador antigo */ }

  // LCP — emite na finalização (visibility change / page unload).
  try {
    let lastLCP = 0
    const obs = new PerformanceObserver((list) => {
      for (const e of list.getEntries()) lastLCP = e.startTime
    })
    obs.observe({ type: 'largest-contentful-paint', buffered: true })
    addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden' && lastLCP > 0) {
        reportOnce('LCP', Math.round(lastLCP))
      }
    }, { once: true })
  } catch { /* */ }

  // CLS — soma incrementos de layout-shift sem flag hadRecentInput.
  try {
    let cls = 0
    const obs = new PerformanceObserver((list) => {
      for (const e of list.getEntries()) {
        if (!e.hadRecentInput) cls += e.value
      }
    })
    obs.observe({ type: 'layout-shift', buffered: true })
    addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') {
        reportOnce('CLS', Math.round(cls * 1000) / 1000)
      }
    }, { once: true })
  } catch { /* */ }

  // INP — pega o pior 'event' duration. Aproximação simples.
  try {
    let worstINP = 0
    const obs = new PerformanceObserver((list) => {
      for (const e of list.getEntries()) {
        if (e.duration > worstINP) worstINP = e.duration
      }
    })
    obs.observe({ type: 'event', durationThreshold: 16, buffered: true })
    addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden' && worstINP > 0) {
        reportOnce('INP', Math.round(worstINP))
      }
    }, { once: true })
  } catch { /* */ }

  // TTFB — Navigation Timing API.
  try {
    const nav = performance.getEntriesByType('navigation')[0]
    if (nav && nav.responseStart > 0) {
      reportOnce('TTFB', Math.round(nav.responseStart))
    }
  } catch { /* */ }
}
