// PostSaleHero.jsx — a capa do documento: wordmark gigante, faixa e orbs.
//
// A entrada usa Web Animations API em vez de CSS puro porque precisa encadear
// wordmark → faixa com timings próprios, e o WAAPI cancela limpo no unmount.
import { useEffect, useRef } from 'react'
import { prefersReducedMotion } from './motion'

export default function PostSaleHero({ periodLabel }) {
  const wordRef = useRef(null)
  const bandRef = useRef(null)

  useEffect(() => {
    if (prefersReducedMotion()) return
    const ease = 'cubic-bezier(.16,1,.3,1)'
    const w = wordRef.current?.animate(
      [
        { opacity: 0, filter: 'blur(12px)', transform: 'translateY(24px)' },
        { opacity: 1, filter: 'blur(0px)', transform: 'translateY(0)' },
      ],
      { duration: 900, easing: ease, fill: 'both' },
    )
    const b = bandRef.current?.animate(
      [
        { opacity: 0, transform: 'scaleX(.72)' },
        { opacity: 1, transform: 'scaleX(1)' },
      ],
      { duration: 700, delay: 380, easing: ease, fill: 'both' },
    )
    return () => { w?.cancel(); b?.cancel() }
  }, [])

  return (
    <header className="ps-hero">
      <div className="ps-hero-orb ps-hero-orb--pink" aria-hidden="true" />
      <div className="ps-hero-orb ps-hero-orb--blue" aria-hidden="true" />

      <div className="ps-hero-inner">
        <img className="ps-hero-logo" src="/E-monitor%20logo.png" alt="E-monitor" />

        <h1 ref={wordRef} className="ps-hero-word">
          pós<span className="ps-hero-word-accent">-</span>venda
        </h1>

        <p ref={bandRef} className="ps-hero-band">Vamos conferir os resultados?</p>

        {periodLabel && (
          <p className="ps-hero-meta">Relatório de performance · {periodLabel}</p>
        )}
      </div>
    </header>
  )
}
