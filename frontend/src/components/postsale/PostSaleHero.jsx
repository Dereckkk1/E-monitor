// PostSaleHero.jsx — a capa, no mesmo desenho do hero da /boasvindas:
// foto escura + véu diagonal, lockup E-monitor × cliente, título em máscara de
// linha e fade pro fundo claro da faixa seguinte.
//
// O título É o "pós-venda" grande que o documento pede: aqui ele ocupa o papel
// de h1 na mesma escala do hero de boas-vindas (teto de 5rem — acima disso a
// página grita em vez de acolher).
import { useEffect, useRef, useState } from 'react'

import { prefersReducedMotion } from './motion'

function ClientMark({ name, logo }) {
  const [ok, setOk] = useState(Boolean(logo))
  if (!ok || !logo) return <span className="ps-lockup-name">{name}</span>
  return (
    <span className="ps-lockup-plate">
      <img src={logo} alt={name} className="ps-lockup-logo" onError={() => setOk(false)} />
    </span>
  )
}

export default function PostSaleHero({ clientName, clientLogo, periodLabel }) {
  const lineRef = useRef(null)
  const leadRef = useRef(null)
  const mediaRef = useRef(null)

  useEffect(() => {
    if (prefersReducedMotion()) return
    const ease = 'cubic-bezier(0.22, 1, 0.36, 1)'

    // A linha sobe de dentro da máscara (o overflow do .ps-line é o que segura).
    const line = lineRef.current?.animate(
      [{ transform: 'translateY(115%)' }, { transform: 'translateY(0)' }],
      { duration: 1100, easing: ease, fill: 'backwards' },
    )
    const lead = leadRef.current?.animate(
      [{ opacity: 0, transform: 'translateY(14px)' }, { opacity: 1, transform: 'none' }],
      { duration: 900, delay: 260, easing: ease, fill: 'backwards' },
    )

    // Parallax leve só na camada da imagem, como no hero da /boasvindas.
    let onScroll = null
    const media = mediaRef.current
    if (media) {
      onScroll = () => {
        const y = Math.min(window.scrollY, 700) * 0.12
        media.style.transform = `translate3d(0, ${y}px, 0)`
      }
      window.addEventListener('scroll', onScroll, { passive: true })
    }

    return () => {
      line?.cancel()
      lead?.cancel()
      if (onScroll) window.removeEventListener('scroll', onScroll)
    }
  }, [])

  return (
    <header className="ps-hero">
      <div ref={mediaRef} className="ps-hero-media" aria-hidden="true" />
      <div className="ps-hero-veil" aria-hidden="true" />

      <div className="ps-shell ps-hero-inner">
        <div className="ps-lockup">
          <img src="/E-monitor%20logo.png" alt="E-monitor" className="ps-hero-logo" />
          {clientName && (
            <>
              <span className="ps-lockup-x" aria-hidden="true" />
              <ClientMark name={clientName} logo={clientLogo} />
            </>
          )}
        </div>

        <h1 className="ps-hero-title">
          <span className="ps-line">
            <span ref={lineRef} className="ps-line-in">pós-venda</span>
          </span>
        </h1>

        <p ref={leadRef} className="ps-hero-lead">
          Vamos conferir os resultados?
        </p>

        {periodLabel && (
          <p className="ps-hero-hint">Relatório de performance · {periodLabel}</p>
        )}
      </div>

      <div className="ps-hero-fade" aria-hidden="true" />
    </header>
  )
}
