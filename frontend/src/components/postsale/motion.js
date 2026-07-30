// motion.js — animação do pós-venda sem dependência nova.
//
// A tela de boas-vindas usa GSAP (a branch dela adicionou a dep). Aqui a regra 5
// do CLAUDE.md veta instalar dep nova no Windows — o npm poda as opcionais linux
// do lockfile e o build do Cloudflare Pages quebra. IntersectionObserver + Web
// Animations API + rAF cobrem o que esta página precisa.
//
// A REGRA DE MOVIMENTO É A MESMA DA /boasvindas: tudo nasce VISÍVEL e o JS só
// esconde no instante em que vai animar (o equivalente de `gsap.from`). Nenhum
// `opacity: 0` mora no CSS — se o JS falhar, ou se o renderer não rolar a página
// (screenshot de página inteira, html2canvas, impressão, aba em background), o
// documento aparece inteiro em vez de sair em branco.
import { useEffect, useRef, useState } from 'react'

const EASE = 'cubic-bezier(0.22, 1, 0.36, 1)' // mesma curva da /boasvindas

/** Respeita a preferência do SO. Checado em runtime, não no import. */
export function prefersReducedMotion() {
  return typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
}

/**
 * useRevealOnce — devolve um ref. Quando o elemento entra na viewport, roda um
 * fade+slide DE opacity 0 PARA o estado natural dele, uma vez só.
 *
 * Como o keyframe inicial vive no JS (e não no CSS), não existe cenário em que o
 * conteúdo fique invisível esperando uma animação que não vai rodar.
 */
export function useRevealOnce({ y = 18, duration = 620, delay = 0 } = {}) {
  const ref = useRef(null)

  useEffect(() => {
    if (prefersReducedMotion() || typeof IntersectionObserver === 'undefined') return
    const el = ref.current
    if (!el) return

    let anim = null
    const io = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) return
      io.unobserve(el)
      anim = el.animate(
        [
          { opacity: 0, transform: `translateY(${y}px)` },
          { opacity: 1, transform: 'none' },
        ],
        { duration, delay, easing: EASE, fill: 'backwards' },
      )
    }, { threshold: 0.12, rootMargin: '0px 0px -8% 0px' })

    io.observe(el)
    return () => { io.disconnect(); anim?.cancel() }
  }, [y, duration, delay])

  return ref
}

/**
 * useCountUp — devolve [ref, display] para animar um número de 0 até `value`.
 *
 * O DEFAULT é o valor real: `progress === null` significa "não estou animando",
 * e aí o display é `value`. Isso é deliberado e não é detalhe de animação — a
 * versão anterior gateava o VALOR na interseção e o documento exibia
 * "R$ 0,00" para tudo que estava abaixo da dobra em qualquer renderer que não
 * rola a página (screenshot de página inteira, impressão, aba em background).
 * Num relatório financeiro isso não é enfeite quebrado, é número errado.
 *
 * Elemento já visível no load não anima: contar de 0 no que o leitor já está
 * lendo daria um flash de zero. Quem está abaixo da dobra anima ao ser
 * alcançado, que é onde a contagem tem graça.
 */
export function useCountUp(value, duration = 900) {
  const ref = useRef(null)
  const [progress, setProgress] = useState(null)

  useEffect(() => {
    if (prefersReducedMotion() || typeof IntersectionObserver === 'undefined') return
    const el = ref.current
    if (!el) return

    const box = el.getBoundingClientRect()
    if (box.top < window.innerHeight && box.bottom > 0) return // já à vista

    let raf = 0
    const io = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) return
      io.unobserve(el)
      let t0 = null
      const tick = (t) => {
        if (t0 === null) t0 = t
        const p = Math.min(1, (t - t0) / duration)
        setProgress(p < 1 ? 1 - Math.pow(1 - p, 3) : null) // null no fim: volta ao valor exato
        if (p < 1) raf = requestAnimationFrame(tick)
      }
      raf = requestAnimationFrame(tick)
    }, { threshold: 0.2 })

    io.observe(el)
    return () => { io.disconnect(); cancelAnimationFrame(raf) }
  }, [duration])

  return [ref, progress === null ? value : value * progress]
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
