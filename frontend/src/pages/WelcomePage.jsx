import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, Link } from 'react-router-dom'
import api from '../api/client'
import './WelcomePage.css'

/* ID do tutorial da plataforma no YouTube. Mesmo vídeo pra todos os convites. */
const VIDEO_ID = '7-ERG0-_O3s'

/* Ordem de fallback das thumbs do YouTube: nem todo vídeo tem maxres. */
const THUMB_SOURCES = [
  `https://i.ytimg.com/vi/${VIDEO_ID}/maxresdefault.jpg`,
  `https://i.ytimg.com/vi/${VIDEO_ID}/sddefault.jpg`,
  `https://i.ytimg.com/vi/${VIDEO_ID}/hqdefault.jpg`,
]

/* ── Página ──────────────────────────────────────────────────────────── */

export default function WelcomePage() {
  const { token } = useParams()
  const [status, setStatus] = useState('loading') // loading | ok | notfound | error
  const [data, setData] = useState(null)
  const rootRef = useRef(null)

  useEffect(() => {
    let alive = true
    // Sem setStatus('loading') síncrono aqui: o estado já nasce 'loading' e
    // chamar setState no corpo do efeito dispara render em cascata (regra
    // react-hooks/set-state-in-effect). O status só muda nos callbacks.
    api.get(`/public/welcome/${encodeURIComponent(token ?? '')}`)
      .then(({ data }) => {
        if (!alive) return
        setData(data)
        setStatus('ok')
      })
      .catch(err => {
        if (!alive) return
        // 404 = token inexistente, revogado ou usuário excluído.
        // 503 = feature desligada no servidor (sem WELCOME_ENC_KEY).
        setStatus(err?.response?.status === 404 ? 'notfound' : 'error')
      })
    return () => { alive = false }
  }, [token])

  useMotion(rootRef, status === 'ok')

  useEffect(() => {
    document.title = 'Boas-vindas | E-monitor'
  }, [])

  if (status === 'loading') return <WelcomeSkeleton />
  if (status !== 'ok') return <WelcomeUnavailable variant={status} />

  const first = (data.name || '').trim().split(/\s+/)[0] || ''
  const isClient = data.role === 'client'

  return (
    <div className="wel" ref={rootRef}>
      <Hero
        name={first}
        clientName={data.client_name}
        clientLogo={data.client_logo_url}
        isClient={isClient}
      />

      {/* Faixas alternadas: claro → navy (o vídeo) → claro. Sem isso o miolo
          da página vira uma laje única de quase-branco entre o hero e o rodapé,
          que é o que fazia o conteúdo parecer solto no vazio. */}
      <StepAccess email={data.email} password={data.password} />
      <StepVideo />
      <StepExplore isClient={isClient} />

      <SiteFooter />
    </div>
  )
}

/* ── Hero ────────────────────────────────────────────────────────────── */

function Hero({ name, clientName, clientLogo, isClient }) {
  return (
    <header className="wel-hero" data-wel-hero>
      <div className="wel-hero-media" data-wel-parallax aria-hidden="true" />
      <div className="wel-hero-veil" aria-hidden="true" />

      <div className="wel-hero-inner">
        <div className="wel-lockup">
          <img src="/E-monitor%20logo.png" alt="E-monitor" className="wel-hero-logo" />
          {isClient && clientName ? (
            <>
              <span className="wel-lockup-x" aria-hidden="true" />
              <ClientMark name={clientName} logo={clientLogo} />
            </>
          ) : null}
        </div>

        <h1 className="wel-hero-title">
          <Line>Boas-vindas{name ? `, ${name}` : ''}.</Line>
        </h1>

        <p className="wel-hero-lead" data-wel-fade>
          {isClient ? (
            <>
              A partir de agora, cada comercial{clientName ? <> d{genderedArticle(clientName)} <strong>{clientName}</strong></> : null}{' '}
              que for ao ar nas rádios monitoradas fica registrado aqui — com data,
              hora, emissora e o áudio da veiculação.
            </>
          ) : (
            <>
              Sua conta de administrador está pronta. Você tem acesso à operação
              inteira: emissoras, campanhas, veiculações e o monitoramento em
              tempo real.
            </>
          )}
        </p>

        <p className="wel-hero-hint" data-wel-fade>
          Três passos rápidos e você está dentro.
        </p>
      </div>

      <div className="wel-hero-fade" aria-hidden="true" />
    </header>
  )
}

/**
 * ClientMark é a marca do cliente no lockup do hero.
 *
 * Placa branca com o logo em `contain`, não avatar circular: logo de cliente é
 * quase sempre horizontal e um crop redondo (o que o ClientAvatar faz, com
 * object-fit: cover) decepa o wordmark. A placa também resolve o contraste —
 * muitos logos são escuros e sumiriam sobre o hero navy.
 *
 * Sem logo cadastrado, ou se a URL falhar, cai no nome em texto.
 */
function ClientMark({ name, logo }) {
  const [ok, setOk] = useState(Boolean(logo))

  if (!ok) return <span className="wel-lockup-name">{name}</span>

  return (
    <span className="wel-lockup-plate">
      <img
        src={logo}
        alt={name}
        className="wel-lockup-logo"
        onError={() => setOk(false)}
      />
    </span>
  )
}

/* Line envolve o texto num wrapper com overflow escondido, pra que o GSAP
   possa deslizá-lo de baixo pra cima. Sem JS o texto já está na posição. */
function Line({ children }) {
  return (
    <span className="wel-line">
      <span className="wel-line-in" data-wel-line>{children}</span>
    </span>
  )
}

/* ── Passo 1: acesso ─────────────────────────────────────────────────── */

function StepAccess({ email, password }) {
  const [revealed, setRevealed] = useState(true)

  return (
    <Step n={1} title="Acesse sua conta" id="acesso" split>
      <div className="wel-access-side">
        {/* Nada de "ao lado"/"abaixo": o layout é de duas colunas no desktop e
            empilhado no mobile, então referência posicional mente em um dos dois. */}
        <p className="wel-step-lead">
          Abra a plataforma numa aba nova e entre com as credenciais desta
          página. Assim o guia continua aberto enquanto você faz o primeiro login.
        </p>

        {/* Nova aba de propósito: se o login abrisse por cima, o cliente
            perderia o guia (e a senha) no meio do caminho. */}
        <a
          href="/login"
          className="wel-cta"
          target="_blank"
          rel="noopener noreferrer"
        >
          Acessar minha conta
          <ArrowIcon />
        </a>
      </div>

      <div className="wel-creds" data-wel-fade>
        <div className="wel-creds-head">
          <KeyIcon />
          <span>Seus dados de acesso</span>
          <button
            type="button"
            className="wel-creds-toggle"
            onClick={() => setRevealed(v => !v)}
            aria-pressed={revealed}
          >
            {revealed ? <EyeOffIcon /> : <EyeIcon />}
            {revealed ? 'Ocultar senha' : 'Mostrar senha'}
          </button>
        </div>

        <CredRow label="E-mail" value={email} />
        <CredRow label="Senha" value={password} masked={!revealed} mono />

        <p className="wel-creds-note">
          Esta é a senha inicial que criamos para você. Depois de entrar, troque-a
          em <strong>Minha conta</strong> — esta página continuará mostrando a
          senha original, então guarde a nova em outro lugar.
        </p>
      </div>
    </Step>
  )
}

function CredRow({ label, value, masked = false, mono = false }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef(null)

  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = useCallback(async () => {
    const ok = await copyText(value)
    if (!ok) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 2200)
  }, [value])

  return (
    <div className="wel-cred">
      <span className="wel-cred-label">{label}</span>
      <span className={`wel-cred-value${mono ? ' is-mono' : ''}`}>
        {masked ? '•'.repeat(Math.max(8, value.length)) : value}
      </span>
      <button
        type="button"
        className={`wel-cred-copy${copied ? ' is-copied' : ''}`}
        onClick={copy}
        aria-label={`Copiar ${label.toLowerCase()}`}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
        <span>{copied ? 'Copiado' : 'Copiar'}</span>
      </button>
      <span className="wel-sr-live" role="status" aria-live="polite">
        {copied ? `${label} copiado.` : ''}
      </span>
    </div>
  )
}

/* ── Passo 2: vídeo ──────────────────────────────────────────────────── */

function StepVideo() {
  const [playing, setPlaying] = useState(false)
  const [thumbIdx, setThumbIdx] = useState(0)

  return (
    <Step n={2} title="Veja a plataforma funcionando" id="video" tone="dark">
      <p className="wel-step-lead">
        Um tour curto pelo sistema: onde acompanhar suas campanhas, como ler o
        relatório de veiculação e onde ouvir a evidência de cada tocada.
      </p>

      <div className="wel-video" data-wel-fade>
        {playing ? (
          <iframe
            className="wel-video-frame"
            src={`https://www.youtube-nocookie.com/embed/${VIDEO_ID}?autoplay=1&rel=0&modestbranding=1`}
            title="Tutorial da plataforma E-monitor"
            allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
            allowFullScreen
          />
        ) : (
          /* Fachada: só a thumb até o clique. A página abre instantânea e o
             visitante não é entregue ao YouTube antes de pedir. */
          <button
            type="button"
            className="wel-video-facade"
            onClick={() => setPlaying(true)}
            aria-label="Assistir ao tutorial da plataforma E-monitor"
          >
            <img
              src={THUMB_SOURCES[thumbIdx]}
              alt=""
              className="wel-video-thumb"
              loading="lazy"
              decoding="async"
              onError={() => setThumbIdx(i => Math.min(i + 1, THUMB_SOURCES.length - 1))}
            />
            <span className="wel-video-play" aria-hidden="true">
              <PlayIcon />
            </span>
            <span className="wel-video-caption" aria-hidden="true">
              Tutorial da plataforma
            </span>
          </button>
        )}
      </div>
    </Step>
  )
}

/* ── Passo 3: o que dá pra fazer ─────────────────────────────────────── */

/* O verbo é o lead-in da frase, não um título separado: lido em voz alta,
   "Acompanhe suas campanhas em tempo real" é uma oração só. */
const CLIENT_ABILITIES = [
  {
    verb: 'Acompanhe',
    text: 'suas campanhas em tempo real — quais comerciais foram ao ar hoje, em quais emissoras e a que horas.',
  },
  {
    verb: 'Ouça',
    text: 'a evidência de cada veiculação. Todo registro guarda o trecho de áudio capturado direto do ar.',
  },
  {
    verb: 'Exporte',
    text: 'o relatório que você precisar, em PDF ou planilha, com o período e o recorte que fizerem sentido.',
  },
]

const ADMIN_ABILITIES = [
  {
    verb: 'Monitore',
    text: 'a operação inteira: emissoras no ar, workers ativos e o estado de cada stream capturado.',
  },
  {
    verb: 'Gerencie',
    text: 'campanhas, materiais e regras de distribuição, com a grade de veiculação por emissora e por dia.',
  },
  {
    verb: 'Investigue',
    text: 'qualquer detecção até a evidência de áudio, com o histórico completo por cliente e por período.',
  },
]

function StepExplore({ isClient }) {
  const abilities = isClient ? CLIENT_ABILITIES : ADMIN_ABILITIES

  return (
    <Step n={3} title="O que você encontra por aqui" id="explorar" wide>
      <div className="wel-explore">
        <ul className="wel-abilities">
          {abilities.map(a => (
            <li key={a.verb} className="wel-ability" data-wel-fade>
              <p className="wel-ability-text">
                <strong className="wel-ability-verb">{a.verb}</strong> {a.text}
              </p>
            </li>
          ))}
        </ul>

        <figure className="wel-shot" data-wel-shot>
          <div className="wel-shot-frame">
            <div className="wel-shot-bar" aria-hidden="true">
              <span /><span /><span />
              <em>e-monitor.online</em>
            </div>
            <img
              src="/welcome-dashboard.png"
              alt="Dashboard de veiculação do E-monitor, com impactos, CPM, investimento e gráficos de perfil de audiência."
              className="wel-shot-img"
              width="1440"
              height="900"
              loading="lazy"
              decoding="async"
            />
          </div>
          <figcaption className="wel-shot-cap">
            O painel de indicadores, com os números da sua veiculação.
          </figcaption>
        </figure>
      </div>
    </Step>
  )
}

/* ── Casca de passo ──────────────────────────────────────────────────── */

/**
 * Step é a casca de um passo. A seção é full-bleed (a faixa ocupa a largura da
 * tela); o container interno é que limita a medida do conteúdo. É isso que
 * permite alternar o fundo sem quebrar o alinhamento da coluna de texto.
 *
 *  - tone="dark"  → faixa navy (o vídeo, que pede cinema)
 *  - split        → corpo em duas colunas no desktop
 *  - wide         → corpo com gap maior (lista + print)
 */
function Step({ n, title, id, tone = 'light', wide = false, split = false, children }) {
  const cls = [
    'wel-step',
    tone === 'dark' ? 'is-dark' : '',
    wide ? 'is-wide' : '',
    split ? 'is-split' : '',
  ].filter(Boolean).join(' ')

  return (
    <section className={cls} id={id} aria-labelledby={`${id}-t`}>
      <div className="wel-step-inner">
        <div className="wel-step-head">
          <span className="wel-step-mark" aria-hidden="true">
            <span className="wel-step-ring" />
            <span className="wel-step-ring wel-step-ring-2" />
            <span className="wel-step-num">{n}</span>
          </span>
          <h2 className="wel-step-title" id={`${id}-t`}>{title}</h2>
        </div>
        <div className="wel-step-body">{children}</div>
      </div>
    </section>
  )
}

/* ── Rodapé ──────────────────────────────────────────────────────────── */

function SiteFooter() {
  /* O PNG da E-Mídias ainda não está no bucket. Sem fallback, o onError some
     com a imagem e a assinatura "Tecnologia em publicidade" fica órfã no
     canto. O wordmark em texto segura a identidade até o arquivo chegar. */
  const [corpLogoOk, setCorpLogoOk] = useState(true)

  return (
    <footer className="wel-footer">
      <div className="wel-footer-inner">
        <div className="wel-footer-brand">
          <img src="/E-monitor%20logo.png" alt="E-monitor" className="wel-footer-logo" />
          <p className="wel-footer-tagline">Cada comercial que vai ao ar, registrado.</p>
        </div>

        <div className="wel-footer-corp">
          {corpLogoOk ? (
            <img
              src="/emidias-logo.png"
              alt="E-Mídias"
              className="wel-footer-corp-logo"
              onError={() => setCorpLogoOk(false)}
            />
          ) : (
            <span className="wel-footer-corp-mark">E-Mídias</span>
          )}
          <span className="wel-footer-corp-sub">Tecnologia em publicidade</span>
        </div>
      </div>

      <p className="wel-footer-legal">
        Precisa de ajuda? Fale com quem cuida da sua conta.
      </p>
    </footer>
  )
}

/* ── Estados de exceção ──────────────────────────────────────────────── */

function WelcomeSkeleton() {
  return (
    <div className="wel wel-skeleton" aria-busy="true" aria-live="polite">
      <div className="wel-hero wel-hero-plain">
        <div className="wel-hero-inner">
          <span className="sk sk-logo" />
          <span className="sk sk-title" />
          <span className="sk sk-lead" />
          <span className="sk sk-lead sk-short" />
        </div>
      </div>
      <span className="wel-sr-only">Carregando suas boas-vindas…</span>
    </div>
  )
}

function WelcomeUnavailable({ variant }) {
  const notFound = variant === 'notfound'
  return (
    <div className="wel wel-empty">
      <div className="wel-empty-card">
        <img src="/E-monitor%20logo.png" alt="E-monitor" className="wel-empty-logo" />
        <h1 className="wel-empty-title">
          {notFound ? 'Este link não está mais disponível' : 'Não conseguimos abrir seu convite'}
        </h1>
        <p className="wel-empty-text">
          {notFound
            ? 'O convite pode ter sido cancelado, ou o endereço veio incompleto do email. Peça um novo para quem cuida da sua conta.'
            : 'Houve uma falha ao carregar esta página. Tente novamente em alguns instantes.'}
        </p>
        <Link to="/login" className="wel-empty-cta">Ir para o login</Link>
      </div>
    </div>
  )
}

/* ── Movimento ───────────────────────────────────────────────────────── */

/**
 * useMotion carrega o GSAP sob demanda e anima a página.
 *
 * Três decisões que importam:
 *
 *  1. O import é DINÂMICO. O bundler corta o GSAP num chunk próprio, então só
 *     esta rota paga os ~34KB — o resto do sistema não carrega nada a mais.
 *  2. O CSS já deixa tudo VISÍVEL por padrão. O GSAP só esconde (gsap.set) no
 *     instante em que vai animar. Se o import falhar, o JS quebrar ou a página
 *     for renderizada sem script, o conteúdo aparece inteiro — nunca em branco.
 *  3. prefers-reduced-motion desliga tudo: nada de parallax, nada de reveal.
 */
function useMotion(rootRef, ready) {
  useEffect(() => {
    if (!ready || !rootRef.current) return
    const root = rootRef.current

    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reduce) return

    let ctx
    let cancelled = false

    ;(async () => {
      let gsap, ScrollTrigger
      try {
        ;({ gsap } = await import('gsap'))
        ;({ ScrollTrigger } = await import('gsap/ScrollTrigger'))
      } catch {
        return // sem GSAP a página continua íntegra, só estática
      }
      if (cancelled) return
      gsap.registerPlugin(ScrollTrigger)

      ctx = gsap.context(() => {
        const eio = 'expo.out'

        /* Entrada do hero: as linhas sobem de dentro da máscara. */
        const intro = gsap.timeline({ defaults: { ease: eio } })
        intro.from('[data-wel-line]', {
          yPercent: 115, duration: 1.1, stagger: 0.08,
        })
        intro.from('.wel-hero-logo', {
          y: 14, autoAlpha: 0, duration: 0.8,
        }, 0.1)
        intro.from('.wel-hero-inner [data-wel-fade]', {
          y: 18, autoAlpha: 0, duration: 0.9, stagger: 0.1,
        }, 0.35)

        /* Parallax: a imagem sobe mais devagar que o conteúdo. Scrub linka o
           progresso ao scroll em vez de disparar uma duração fixa. */
        gsap.to('[data-wel-parallax]', {
          yPercent: 14,
          ease: 'none',
          scrollTrigger: {
            trigger: '[data-wel-hero]',
            start: 'top top',
            end: 'bottom top',
            scrub: 0.6,
          },
        })

        /* Cada passo entra quando encosta na viewport. O marcador vem primeiro
           (é o índice visual), o corpo logo atrás. */
        gsap.utils.toArray('.wel-step').forEach(step => {
          const st = { trigger: step, start: 'top 78%', once: true }

          gsap.from(step.querySelector('.wel-step-mark'), {
            scale: 0.6, autoAlpha: 0, duration: 0.7, ease: 'back.out(1.7)',
            scrollTrigger: st,
          })
          gsap.from(step.querySelector('.wel-step-title'), {
            y: 20, autoAlpha: 0, duration: 0.8, ease: eio,
            scrollTrigger: st,
          })
          gsap.from(step.querySelectorAll('.wel-step-body > *'), {
            y: 24, autoAlpha: 0, duration: 0.85, stagger: 0.1, ease: eio,
            scrollTrigger: { ...st, start: 'top 74%' },
          })
        })

        /* Itens da lista e o print entram individualmente. */
        gsap.utils.toArray('.wel-ability').forEach((el, i) => {
          gsap.from(el, {
            x: -18, autoAlpha: 0, duration: 0.7, ease: eio, delay: i * 0.06,
            scrollTrigger: { trigger: el, start: 'top 85%', once: true },
          })
        })
        gsap.from('[data-wel-shot]', {
          y: 40, autoAlpha: 0, duration: 1, ease: eio,
          scrollTrigger: { trigger: '[data-wel-shot]', start: 'top 88%', once: true },
        })
      }, root)

      /* Imagens abaixo da dobra mudam a altura do documento ao carregar; sem
         este refresh os gatilhos ficam calibrados na altura errada. */
      const onLoad = () => ScrollTrigger.refresh()
      window.addEventListener('load', onLoad)
      ctx.add(() => window.removeEventListener('load', onLoad))
    })()

    return () => {
      cancelled = true
      ctx?.revert()
    }
  }, [rootRef, ready])
}

/* ── Utilidades ──────────────────────────────────────────────────────── */

/** copyText usa a Clipboard API e cai num fallback pra contexto não-seguro. */
async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch { /* cai no fallback */ }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

/** genderedArticle escolhe "da"/"do" pelo artigo do nome do cliente.
 *  Heurística simples: nomes terminados em "o" tendem a masculino. Erra em
 *  siglas e nomes próprios, então serve só pra soar natural na maioria. */
function genderedArticle(name) {
  const w = (name || '').trim().split(/\s+/)[0]?.toLowerCase() ?? ''
  return /o$/.test(w) ? 'o' : 'a'
}

/* ── Ícones ──────────────────────────────────────────────────────────── */

function ArrowIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M5 12h14M13 6l6 6-6 6" />
    </svg>
  )
}
function KeyIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="8" cy="15" r="4" />
      <path d="m10.85 12.15 8.15-8.15M17 6l2 2M14 9l2 2" />
    </svg>
  )
}
function CopyIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="9" y="9" width="12" height="12" rx="2" />
      <path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1" />
    </svg>
  )
}
function CheckIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m20 6-11 11-5-5" />
    </svg>
  )
}
function EyeIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  )
}
function EyeOffIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
      <line x1="1" y1="1" x2="23" y2="23" />
    </svg>
  )
}
function PlayIcon() {
  return (
    <svg width="26" height="26" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M8 5.14v13.72a.5.5 0 0 0 .77.42l10.5-6.86a.5.5 0 0 0 0-.84L8.77 4.72a.5.5 0 0 0-.77.42z" />
    </svg>
  )
}
