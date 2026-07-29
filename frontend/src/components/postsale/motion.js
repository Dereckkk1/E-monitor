// motion.js — animação do pós-venda sem dependência nova.
//
// Uma skill de landing page pediria GSAP; a regra 5 do CLAUDE.md veta instalar
// dep nova no Windows (o npm poda as opcionais linux do lockfile e o build do
// Cloudflare Pages quebra com "Missing: @emnapi/core from lock file").
// IntersectionObserver + Web Animations API + rAF cobrem tudo que esta página
// precisa: reveal em cascata, entrada do hero e contadores.
import { useEffect, useRef, useState } from 'react'

/**
 * prefersReducedMotion — checado em RUNTIME, não no import: o usuário pode
 * mudar a preferência do sistema com a aba aberta.
 */
export function prefersReducedMotion() {
  return typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
}

/**
 * useReveal — devolve [ref, shown]; `shown` vira true quando o elemento entra
 * na viewport.
 *
 * Dispara uma vez só (unobserve): reveal que re-anima ao rolar pra cima dá
 * sensação de página instável. Com reduced-motion, nasce true — o conteúdo
 * nunca fica invisível esperando animação que não vai rodar.
 */
export function useReveal({ threshold = 0.15, rootMargin = '0px 0px -10% 0px' } = {}) {
  const ref = useRef(null)
  const [inView, setInView] = useState(false)

  useEffect(() => {
    // Nada a observar quando não vai haver animação.
    if (prefersReducedMotion() || typeof IntersectionObserver === 'undefined') return
    const el = ref.current
    if (!el) return
    const io = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) { setInView(true); io.unobserve(el) }
    }, { threshold, rootMargin })
    io.observe(el)
    return () => io.disconnect()
  }, [threshold, rootMargin])

  // Derivado no render (e não via setState no effect): com reduced-motion ou
  // sem IntersectionObserver o conteúdo nasce visível, em vez de ficar preso
  // em opacity 0 esperando uma animação que nunca vai rodar.
  const shown = inView ||
    prefersReducedMotion() ||
    typeof IntersectionObserver === 'undefined'

  return [ref, shown]
}

/**
 * useCountUp — anima de 0 até `value` quando `start` vira true.
 *
 * Ease-out cúbico: rápido no início e assentando no fim, então o número final
 * fica legível antes da animação terminar. Com reduced-motion, entrega o valor
 * direto.
 */
export function useCountUp(value, start, duration = 900) {
  // Guardamos o PROGRESSO (0..1), não o valor: assim o setState acontece só
  // dentro do callback do rAF, e trocar `value` (payload recarregado) não
  // reinicia a contagem do zero.
  const [progress, setProgress] = useState(0)

  useEffect(() => {
    if (!start || prefersReducedMotion()) return
    let raf = 0
    let t0 = null
    const tick = (t) => {
      if (t0 === null) t0 = t
      const p = Math.min(1, (t - t0) / duration)
      setProgress(1 - Math.pow(1 - p, 3))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [start, duration])

  if (prefersReducedMotion()) return value
  // Antes de entrar na viewport mostra 0; no fim, progress chega exatamente a
  // 1, então o número final é o valor exato (sem resíduo de arredondamento).
  return start ? value * progress : 0
}

/** Formatadores compartilhados entre o preview do admin e a página pública. */
export const brl = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
export const int = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 0 })

/** Dial da emissora: "Passo Fundo/RS · 98,5 FM". Partes ausentes somem. */
export function stationDial(row) {
  const parts = []
  if (row.city) parts.push(row.state ? `${row.city}/${row.state}` : row.city)
  if (row.frequency_mhz) {
    parts.push(`${String(row.frequency_mhz).replace('.', ',')} ${row.band ?? ''}`.trim())
  } else if (row.band) {
    parts.push(row.band)
  }
  return parts.join(' · ')
}
