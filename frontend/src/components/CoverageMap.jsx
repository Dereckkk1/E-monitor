import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { geoCircle, geoMercator, geoPath } from 'd3-geo'
import gsap from 'gsap'
import brStates from '../assets/br-states.json'
import { buildLogoUrl } from '../utils/logoUrl'
import './CoverageMap.css'

/* Mapa de cobertura do /live-map.
 *
 * Sucede o BrazilMap NESTA TELA apenas — o BrazilMap continua servindo
 * /management e a captura PNG do pós-venda, e por isso não foi tocado.
 *
 * A ideia central: raio de antena e cidades cobertas só são legíveis em
 * ESCALAS DIFERENTES. Numa campanha espalhada pelo Brasil, o contorno de uma
 * A1 (57,8 km de alcance) tem poucos pixels — some. Então existe um único
 * espaço contínuo de zoom com dois pontos úteis:
 *
 *   panorama — todas as emissoras, discos desenhados em ESCALA VERDADEIRA
 *              (minúsculos, lidos como halo). Nada de raio inflado: o disco
 *              que você vê aqui é o mesmo que cresce no foco.
 *   foco     — a câmera enquadra o alcance de uma emissora; o disco vira anel
 *              medível e as cidades cobertas aparecem.
 *
 * O tour é autoplay sobre o foco, não um segundo modo.
 *
 * Renderização: SVG para o que é geográfico (estados, anéis) porque precisa
 * escalar com o zoom; HTML sobreposto para o que é anotação (marcadores,
 * rótulos de cidade) porque precisa ter tamanho constante. Tudo capturável
 * por html2canvas — nada de WebGL, que o botão "Baixar" não fotografa.
 */

const WIDTH = 720
const HEIGHT = 760

// Proporção do palco. O viewBox é uma JANELA sobre o espaço projetado, então
// esta constante não precisa bater com WIDTH/HEIGHT — mas TEM que bater com o
// aspect-ratio do CSS, senão o preserveAspectRatio letterboxa e a camada de
// anotação (posicionada em % do palco) desalinha da geografia. Por isso o CSS
// recebe o valor daqui em vez de repeti-lo.
export const STAGE_ASPECT = 1.2

// Km por grau de arco de grande-círculo. geoCircle mede o raio em GRAUS, então
// é essa constante que converte o raio da classe Anatel para a esfera.
const KM_PER_DEGREE = 111.195

// Enquadramento do foco: o alcance ocupa ~1/1.5 do quadro, deixando margem para
// as cidades logo além do anel aparecerem.
const FOCUS_PAD = 1.5
// Piso de zoom. Uma comunitária tem 1,5 km de alcance; sem piso a câmera
// mergulharia num quadro onde não sobra geografia nenhuma de referência.
const MIN_VIEW_W = 30

// Largura da ficha de foco + folgas, em px. Ela flutua SOBRE o palco, então o
// enquadramento precisa descontá-la: sem isso a emissora é centralizada no
// palco inteiro e acaba atrás da própria ficha. Abaixo de PANEL_SIDE_MIN_PX a
// ficha vai pro rodapé (ver CSS) e não rouba largura nenhuma.
const PANEL_PX = 270
const PANEL_SIDE_MIN_PX = 700
// Abaixo desse ponto a ficha vai pro rodapé (max-height 46% no CSS) e passa a
// roubar ALTURA em vez de largura — o desconto muda de eixo junto.
const PANEL_BOTTOM_FRAC = 0.46

const FLIGHT_MS = 900
const DWELL_MS = 3400
const PING_MS = 2600

const CODE_TO_UF = {
  '11': 'RO', '12': 'AC', '13': 'AM', '14': 'RR', '15': 'PA', '16': 'AP',
  '17': 'TO', '21': 'MA', '22': 'PI', '23': 'CE', '24': 'RN', '25': 'PB',
  '26': 'PE', '27': 'AL', '28': 'SE', '29': 'BA', '31': 'MG', '32': 'ES',
  '33': 'RJ', '35': 'SP', '41': 'PR', '42': 'SC', '43': 'RS', '50': 'MS',
  '51': 'MT', '52': 'GO', '53': 'DF',
}

function ufOf(feature) {
  const p = feature.properties || {}
  const sigla = p.SIGLA || p.sigla || p.UF || p.uf
  if (sigla) return String(sigla).toUpperCase()
  const code = String(p.codarea ?? p.codigo ?? feature.id ?? '').slice(0, 2)
  return CODE_TO_UF[code] || code
}

function healthOf(st) {
  const h = (st.health_status || '').toLowerCase()
  if (h === 'degraded' || h === 'unstable') return 'degraded'
  if (h === 'failing' || h === 'down' || h === 'offline' || h === 'error') return 'down'
  return 'ok'
}

const HEALTH_LABEL = { ok: 'Monitorando', degraded: 'Instável', down: 'Offline' }

function freqLabel(st) {
  if (st.frequency_mhz == null) return st.band || ''
  const n = Number(st.frequency_mhz)
  const v = st.band === 'AM' ? String(Math.round(n)) : n.toFixed(1).replace('.', ',')
  return `${v} ${st.band}`
}

function initialsOf(name = '') {
  return name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
}

// Piso do panorama: uma campanha de uma emissora só não deve abrir com a
// câmera colada no chão — sem geografia em volta ninguém se localiza.
const MIN_PANORAMA_W = 260
const PANORAMA_PAD = 1.18
// Quadro de partida enquanto não há emissora (fantasma do estado vazio).
const BRAZIL_VIEW = { x: 0, y: (HEIGHT - WIDTH / STAGE_ASPECT) / 2, w: WIDTH, h: WIDTH / STAGE_ASPECT }

/* Enquadra um bounding box projetado respeitando o aspect do palco. */
function viewForBounds(bounds, { pad = FOCUS_PAD, minW = MIN_VIEW_W } = {}) {
  const [[x0, y0], [x1, y1]] = bounds
  const cx = (x0 + x1) / 2
  const cy = (y0 + y1) / 2
  let w = Math.max(x1 - x0, 1) * pad
  let h = Math.max(y1 - y0, 1) * pad
  if (w / h > STAGE_ASPECT) h = w / STAGE_ASPECT
  else w = h * STAGE_ASPECT
  if (w < minW) { w = minW; h = w / STAGE_ASPECT }
  const maxW = WIDTH * 1.08
  if (w > maxW) { w = maxW; h = w / STAGE_ASPECT }
  return { x: cx - w / 2, y: cy - h / 2, w, h }
}

/* União dos bounding boxes — a pegada geográfica da campanha inteira. */
function unionBounds(list) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const b of list) {
    if (!b) continue
    x0 = Math.min(x0, b[0][0]); y0 = Math.min(y0, b[0][1])
    x1 = Math.max(x1, b[1][0]); y1 = Math.max(y1, b[1][1])
  }
  return isFinite(x0) ? [[x0, y0], [x1, y1]] : null
}

function prefersReducedMotion() {
  return typeof window !== 'undefined'
    && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

/* ── Marcador de emissora (HTML, tamanho constante) ───────────────── */
function StationMarker({ pt, mode, isFocused, isHighlighted, isPinging, showLogo, onSelect, onHover }) {
  const [imgErr, setImgErr] = useState(false)
  const { st, health } = pt
  const logo = !imgErr ? buildLogoUrl(st.logo_url) : null

  const cls = [
    'cvm-marker',
    `cvm-marker--${health}`,
    isFocused && 'cvm-marker--focused',
    isHighlighted && 'cvm-marker--highlight',
    isPinging && 'cvm-marker--ping',
    showLogo && 'cvm-marker--logo',
  ].filter(Boolean).join(' ')

  return (
    <button
      type="button"
      className={cls}
      style={{ left: `${pt.left}%`, top: `${pt.top}%` }}
      onClick={() => onSelect(st.id)}
      onMouseEnter={() => onHover(st.id)}
      onMouseLeave={() => onHover(null)}
      onFocus={() => onHover(st.id)}
      onBlur={() => onHover(null)}
      aria-label={`${st.name} — ${[st.city, st.state].filter(Boolean).join('/')} ${freqLabel(st)}. ${HEALTH_LABEL[health]}.`}
      aria-pressed={isFocused}
    >
      {health !== 'down' && <span className="cvm-marker-halo" aria-hidden />}
      <span className="cvm-marker-face" aria-hidden data-initials={initialsOf(st.name)}>
        {showLogo && logo
          ? <img src={logo} alt="" onError={() => setImgErr(true)} loading="lazy" />
          : showLogo
            ? <span className="cvm-marker-initials">{initialsOf(st.name)}</span>
            : null}
      </span>
      {(isFocused || (mode === 'focus' && isHighlighted)) && (
        <span className="cvm-marker-tag" aria-hidden>{st.name}</span>
      )}
    </button>
  )
}

/* ── Mapa ─────────────────────────────────────────────────────────── */
export default function CoverageMap({
  stations = [],
  coverage = [],
  coverageLoading = false,
  highlightId = null,
  onHoverStation = null,
  recentDetections = [],
}) {
  const { projection, pathGen } = useMemo(() => {
    const proj = geoMercator().fitSize([WIDTH, HEIGHT], brStates)
    return { projection: proj, pathGen: geoPath(proj) }
  }, [])

  /* ── Geometria por emissora ──────────────────────────────────────
   * O círculo é ancorado na ANTENA do plano da Anatel quando ela existe; sem
   * ela sobra o centroide do município, que é o que temos.
   * geoCircle devolve um círculo geodésico de verdade, então a projeção o
   * distorce corretamente conforme a latitude — um raio em pixels seria
   * redondo no mapa e errado no chão. */
  const points = useMemo(() => {
    const mk = geoCircle().precision(2)
    return stations.map((st) => {
      const lat = st.anatel_latitude ?? st.latitude
      const lng = st.anatel_longitude ?? st.longitude
      const xy = projection([lng, lat])
      if (!xy) return null
      const coverKm = st.anatel_coverage_km ?? null
      const reachKm = st.anatel_reach_km ?? null
      const contour = coverKm ? mk.center([lng, lat]).radius(coverKm / KM_PER_DEGREE)() : null
      const reach = reachKm ? mk.center([lng, lat]).radius(reachKm / KM_PER_DEGREE)() : null
      return {
        st,
        lat,
        lng,
        x: xy[0],
        y: xy[1],
        health: healthOf(st),
        coverKm,
        reachKm,
        contourPath: contour ? pathGen(contour) : null,
        reachPath: reach ? pathGen(reach) : null,
        bounds: reach ? pathGen.bounds(reach) : null,
      }
    }).filter(Boolean)
  }, [stations, projection, pathGen])

  const byId = useMemo(() => new Map(points.map(p => [p.st.id, p])), [points])

  /* O panorama enquadra a PEGADA DA CAMPANHA, não o país. Uma campanha
   * regional abre já perto — que é o ponto: no quadro do Brasil inteiro
   * metade da tela é geografia sem emissora nenhuma. Inclui o bounding box
   * dos anéis, não só dos pontos, senão a cobertura da borda sai cortada. */
  const panorama = useMemo(() => {
    if (points.length === 0) return BRAZIL_VIEW
    const b = unionBounds(points.map(p => p.bounds
      ?? [[p.x - 6, p.y - 6], [p.x + 6, p.y + 6]]))
    if (!b) return BRAZIL_VIEW
    return viewForBounds(b, { pad: PANORAMA_PAD, minW: MIN_PANORAMA_W })
  }, [points])

  /* A câmera NASCE na pegada — não parte do Brasil inteiro para depois voar
   * até ela. Abrir a tela com uma animação de aproximação seria coreografia
   * de carregamento: o operador entra para trabalhar, não para assistir.
   * (Trocar de campanha remonta o componente via `key`, então o enquadramento
   * inicial acompanha a seleção sem precisar de efeito sincronizando estado.) */
  const [view, setView] = useState(panorama)
  const [focusId, setFocusId] = useState(null)
  const [hoverId, setHoverId] = useState(null)
  const [touring, setTouring] = useState(false)
  const [pinging, setPinging] = useState(() => new Set())
  // Marca d'água do feed: guarda o topo do ciclo anterior para saber o que é
  // veiculação NOVA. Em estado, não em ref — ler/escrever ref durante o render
  // não é permitido, e este valor participa da decisão de render.
  const [seenTopDetId, setSeenTopDetId] = useState(() => recentDetections[0]?.id ?? null)

  // CÓPIA, nunca a referência do memo: gsap.to() anima MUTANDO o objeto-alvo,
  // então guardar `panorama` aqui faria cada voo reescrever o próprio
  // enquadramento de panorama — depois do primeiro foco, "voltar ao panorama"
  // voltava para o último foco.
  const viewRef = useRef({ ...panorama })
  const tweenRef = useRef(null)
  const tourTimer = useRef(null)
  const stageRef = useRef(null)
  const [stageW, setStageW] = useState(0)

  // Largura real do palco: o deslocamento da câmera e a exclusão de rótulos
  // sob a ficha são ambos em px, e o palco é fluido.
  useEffect(() => {
    const el = stageRef.current
    if (!el || typeof ResizeObserver === 'undefined') return undefined
    const ro = new ResizeObserver(([e]) => setStageW(e.contentRect.width))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Fração do palco coberta pela ficha (0 quando ela está no rodapé).
  const sidePanel = stageW >= PANEL_SIDE_MIN_PX
  const panelFrac = sidePanel && stageW > 0 ? Math.min(0.42, PANEL_PX / stageW) : 0
  const panelVFrac = sidePanel ? 0 : PANEL_BOTTOM_FRAC

  /* Ordem do tour: norte → sul. A câmera varrendo o país numa direção lê como
   * intenção; a ordem alfabética lê como lista embaralhada no mapa. */
  const tourOrder = useMemo(
    () => points.map(p => p.st.id).sort((a, b) => byId.get(b).lat - byId.get(a).lat),
    [points, byId],
  )

  const activeUFs = useMemo(() => {
    const set = new Set()
    for (const st of stations) if (st.state) set.add(String(st.state).toUpperCase())
    return set
  }, [stations])

  const citiesByStation = useMemo(() => {
    const m = new Map()
    for (const c of coverage) {
      if (!m.has(c.station_id)) m.set(c.station_id, [])
      m.get(c.station_id).push(c)
    }
    return m
  }, [coverage])

  /* ── Câmera ──────────────────────────────────────────────────────── */
  const flyTo = useCallback((target) => {
    tweenRef.current?.kill()
    if (prefersReducedMotion()) {
      viewRef.current = { ...target }
      setView({ ...target })
      return
    }
    tweenRef.current = gsap.to(viewRef.current, {
      ...target,
      duration: FLIGHT_MS / 1000,
      ease: 'expo.out',
      onUpdate: () => setView({ ...viewRef.current }),
    })
  }, [])

  const focusStation = useCallback((id) => {
    const p = id ? byId.get(id) : null
    if (!p) {
      setFocusId(null)
      flyTo(panorama)
      return
    }
    setFocusId(id)
    // Sem raio (AM, ou emissora que o cruzamento não identificou) não há
    // bounding box do alcance — enquadra por um piso fixo em vez de sumir.
    const b = p.bounds ?? [[p.x - MIN_VIEW_W / 2, p.y - MIN_VIEW_W / 2], [p.x + MIN_VIEW_W / 2, p.y + MIN_VIEW_W / 2]]
    const v = viewForBounds(b)
    // Desloca a janela para descontar a ficha: a emissora anda para o lado
    // (ou para cima) e fica centrada na área que sobra, em vez de ficar atrás
    // dela. Um eixo por vez — a ficha só ocupa um dos dois.
    flyTo({
      ...v,
      x: v.x - (panelFrac / 2) * v.w,
      y: v.y + (panelVFrac / 2) * v.h,
    })
  }, [byId, flyTo, panorama, panelFrac, panelVFrac])

  const goPanorama = useCallback(() => {
    setTouring(false)
    setFocusId(null)
    flyTo(panorama)
  }, [flyTo, panorama])

  /* ── Tour ────────────────────────────────────────────────────────── */
  /* O tour é um CICLO: roda até o usuário parar. Ao passar da última emissora
   * volta para a primeira — o módulo em step() é o que faz a volta, tanto para
   * frente quanto para trás. */
  const step = useCallback((delta) => {
    if (tourOrder.length === 0) return
    const i = focusId ? tourOrder.indexOf(focusId) : -1
    const next = tourOrder[(i + delta + tourOrder.length * 2) % tourOrder.length]
    focusStation(next)
  }, [tourOrder, focusId, focusStation])

  // Batida do ciclo. Existe porque o re-arme do timer NÃO pode depender de
  // `focusId` mudar: numa campanha de uma emissora só, avançar aponta para ela
  // mesma, o React descarta o render por igualdade e o efeito nunca roda de
  // novo — o tour congelava em silêncio, com o botão ainda dizendo "Pausar".
  // Com a batida própria, o ciclo se re-arma sempre.
  const [tourTick, setTourTick] = useState(0)

  // `step` muda de identidade sempre que os dados chegam (o refresh de 20s
  // recria `points` → `byId` → `focusStation` → `step`). Se ele estivesse nas
  // dependências do efeito, cada refresh reiniciaria a permanência e a emissora
  // atual ficaria em quadro mais tempo que o previsto, sem motivo. Via ref, o
  // ciclo só se re-arma quando a batida bate.
  const stepRef = useRef(step)
  useEffect(() => { stepRef.current = step })

  useEffect(() => {
    if (!touring) {
      clearTimeout(tourTimer.current)
      return undefined
    }
    tourTimer.current = setTimeout(() => {
      stepRef.current(1)
      setTourTick(t => t + 1)
    }, FLIGHT_MS + DWELL_MS)
    return () => clearTimeout(tourTimer.current)
  }, [touring, tourTick])

  const toggleTour = useCallback(() => {
    const ligando = !touring
    setTouring(ligando)
    // Ligar sem foco começa pelo primeiro da ordem. Fica no handler, e não num
    // efeito, porque é consequência direta do clique — e fora do updater do
    // setTouring, que precisa ser puro (o React pode reexecutá-lo).
    if (ligando && !focusId && tourOrder.length > 0) focusStation(tourOrder[0])
  }, [touring, focusId, tourOrder, focusStation])

  useEffect(() => () => {
    tweenRef.current?.kill()
    clearTimeout(tourTimer.current)
  }, [])



  /* ── Ping de veiculação nova ─────────────────────────────────────── */
  // Compara o topo do feed com a marca d'água do ciclo anterior, no render
  // (não em efeito) para o ping entrar no MESMO quadro em que a linha nova
  // aparece no feed — em efeito, mapa e feed piscariam desencontrados.
  const topDetId = recentDetections[0]?.id ?? null
  if (topDetId !== seenTopDetId) {
    setSeenTopDetId(topDetId)
    // seenTopDetId nulo = primeiro feed que chega (a tela abriu carregando).
    // Sem esta guarda, tudo que já estava no feed pulsaria como se tivesse
    // acabado de tocar.
    if (seenTopDetId && topDetId) {
      const fresh = []
      for (const d of recentDetections) {
        if (d.id === seenTopDetId) break
        if (d.station_id) fresh.push(d.station_id)
      }
      if (fresh.length > 0) setPinging(prev => new Set([...prev, ...fresh]))
    }
  }

  // Limpeza é assíncrona por natureza. Um ping novo re-arma o timer e estende
  // a janela dos que já estavam piscando — o custo é um pulso um pouco mais
  // longo num burst, contra a complexidade de um timer por emissora.
  useEffect(() => {
    if (pinging.size === 0) return undefined
    const t = setTimeout(() => setPinging(new Set()), PING_MS)
    return () => clearTimeout(t)
  }, [pinging])

  /* ── Derivados de render ─────────────────────────────────────────── */
  const zoom = panorama.w / view.w
  const mode = focusId ? 'focus' : 'panorama'
  const focused = focusId ? byId.get(focusId) : null
  // Com poucas emissoras o logo cabe e é o que o mapa tem de mais
  // reconhecível. Acima disso viram bolotas sobrepostas e o ponto lê melhor.
  const showLogos = points.length <= 40

  const toPct = useCallback((x, y) => ({
    left: ((x - view.x) / view.w) * 100,
    top: ((y - view.y) / view.h) * 100,
  }), [view])

  const markers = useMemo(() => points
    .map(p => ({ ...p, ...toPct(p.x, p.y) }))
    // Sul por cima: dá uma ordem de empilhamento estável no aglomerado
    // Sudeste em vez de deixar o z-index ao acaso da ordem do array.
    .sort((a, b) => a.lat - b.lat),
  [points, toPct])

  const focusCities = useMemo(() => {
    if (!focused) return []
    const rows = citiesByStation.get(focused.st.id) || []
    return rows.map((c) => {
      const xy = projection([c.longitude, c.latitude])
      if (!xy) return null
      return {
        ...c,
        ...toPct(xy[0], xy[1]),
        inContour: focused.coverKm != null && c.distance_km <= focused.coverKm,
      }
    }).filter(Boolean)
  }, [focused, citiesByStation, projection, toPct])

  /* Rotular tudo vira um emaranhado (uma E1 chega a 90 cidades). Percorre da
   * mais próxima para a mais distante e só aceita o rótulo se ele não colidir
   * com nenhum já aceito — a caixa de teste é elíptica porque um rótulo é
   * largo e baixo. Quem perde o rótulo continua como ponto no mapa e aparece
   * inteiro na lista lateral, então nada de informação se perde.
   *
   * Percentuais do palco: ~9% de largura ≈ 78px, ~3% de altura ≈ 22px. */
  const labelled = useMemo(() => {
    const RX = 9
    const RY = 3
    const set = new Set()
    const placed = []
    // Zona da ficha: rótulo ali sai pela metade atrás dela. A cidade continua
    // como ponto e na lista, então nada some — só o rótulo.
    const panelEdge = panelFrac * 100 + 2
    const panelTopEdge = panelVFrac ? (1 - panelVFrac) * 100 - 2 : 101
    for (const c of focusCities) {
      if (set.size >= 16) break
      if (c.left < panelEdge || c.top > panelTopEdge) continue
      const collides = placed.some(([lx, ly]) => {
        const dx = (c.left - lx) / RX
        const dy = (c.top - ly) / RY
        return dx * dx + dy * dy < 1
      })
      if (collides) continue
      set.add(c.ibge_code)
      placed.push([c.left, c.top])
    }
    return set
  }, [focusCities, panelFrac, panelVFrac])

  const statePaths = useMemo(() => (
    <g className="cvm-states">
      {brStates.features.map((f, i) => {
        const uf = ufOf(f)
        return (
          <path
            key={uf || i}
            d={pathGen(f)}
            className={`cvm-state${activeUFs.has(uf) ? ' cvm-state--active' : ''}`}
          />
        )
      })}
    </g>
  ), [pathGen, activeUFs])

  const ufLabels = useMemo(() => brStates.features.map((f, i) => {
    const uf = ufOf(f)
    const c = pathGen.centroid(f)
    if (!c || !isFinite(c[0]) || !isFinite(c[1])) return null
    return { uf, x: c[0], y: c[1], key: uf || i, active: activeUFs.has(uf) }
  }).filter(Boolean), [pathGen, activeUFs])

  const hasStations = points.length > 0

  return (
    <div className={`cvm cvm--${mode}${touring ? ' cvm--touring' : ''}`}>
      <div className="cvm-stage" ref={stageRef} style={{ aspectRatio: String(STAGE_ASPECT) }}>
        <svg
          className="cvm-svg"
          viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label={`Mapa do Brasil com ${points.length} emissoras monitoradas`}
        >
          {statePaths}

          {/* Anéis de cobertura. Desenhados em escala verdadeira sempre: no
              panorama são halos minúsculos, e é o mesmo anel que cresce no
              foco — a continuidade explica sozinha por que o raio "some" de
              longe. */}
          <g className="cvm-rings">
            {points.map(p => (p.reachPath || p.contourPath) && (
              <g
                key={p.st.id}
                className={`cvm-ring-group${focusId === p.st.id ? ' is-focused' : ''}${
                  focusId && focusId !== p.st.id ? ' is-dimmed' : ''}`}
              >
                {p.reachPath && <path d={p.reachPath} className="cvm-ring cvm-ring--reach" />}
                {p.contourPath && <path d={p.contourPath} className="cvm-ring cvm-ring--contour" />}
              </g>
            ))}
          </g>

          {/* Texto em SVG escala com o viewBox, então a sigla precisa de
              font-size contra-escalado — senão um zoom de 15× rende um "GO"
              de 165px atravessando o mapa. Some no zoom porque ali a sigla do
              estado já não é a referência que o usuário usa. */}
          <g className="cvm-uf-labels" style={{ opacity: zoom > 2.2 ? 0 : 1 }}>
            {ufLabels.map(l => (
              <text
                key={l.key}
                x={l.x}
                y={l.y}
                fontSize={11 / zoom}
                className={`cvm-uf${l.active ? ' cvm-uf--active' : ''}`}
                textAnchor="middle"
              >
                {l.uf}
              </text>
            ))}
          </g>
        </svg>

        {/* Camada de anotação: tamanho constante independente do zoom. */}
        <div className="cvm-overlay">
          {focused && focusCities.map(c => (
            <span
              key={c.ibge_code}
              className={`cvm-city${c.inContour ? ' cvm-city--core' : ' cvm-city--spill'}${
                c.left > 84 ? ' cvm-city--flip' : ''}`}
              style={{ left: `${c.left}%`, top: `${c.top}%` }}
            >
              <span className="cvm-city-dot" aria-hidden />
              {labelled.has(c.ibge_code) && (
                <span className="cvm-city-name">{c.city}</span>
              )}
            </span>
          ))}

          {markers.map(p => (
            <StationMarker
              key={p.st.id}
              pt={p}
              mode={mode}
              isFocused={focusId === p.st.id}
              isHighlighted={highlightId === p.st.id || hoverId === p.st.id}
              isPinging={pinging.has(p.st.id)}
              showLogo={showLogos}
              onSelect={(id) => {
                setTouring(false)
                focusStation(focusId === id ? null : id)
              }}
              onHover={(id) => {
                setHoverId(id)
                onHoverStation?.(id)
              }}
            />
          ))}
        </div>

        {/* Ficha da emissora em foco */}
        {focused && (
          <FocusPanel
            point={focused}
            cities={focusCities}
            loading={coverageLoading}
            onClose={goPanorama}
          />
        )}
      </div>

      {hasStations && (
        <div className="cvm-controls" role="group" aria-label="Controles do mapa">
          <button
            type="button"
            className={`cvm-ctl cvm-ctl--play${touring ? ' is-on' : ''}`}
            onClick={toggleTour}
            aria-pressed={touring}
          >
            {touring ? <IconPause /> : <IconPlay />}
            <span>{touring ? 'Pausar' : 'Tour'}</span>
          </button>

          <span className="cvm-ctl-sep" aria-hidden />

          <button
            type="button"
            className="cvm-ctl cvm-ctl--icon"
            onClick={() => { setTouring(false); step(-1) }}
            aria-label="Emissora anterior"
          >
            <IconPrev />
          </button>
          <span className="cvm-counter">
            {focusId
              ? `${tourOrder.indexOf(focusId) + 1}/${tourOrder.length}`
              : `${tourOrder.length} ${tourOrder.length === 1 ? 'emissora' : 'emissoras'}`}
          </span>
          <button
            type="button"
            className="cvm-ctl cvm-ctl--icon"
            onClick={() => { setTouring(false); step(1) }}
            aria-label="Próxima emissora"
          >
            <IconNext />
          </button>

          <span className="cvm-ctl-sep" aria-hidden />

          <button
            type="button"
            className="cvm-ctl"
            onClick={goPanorama}
            disabled={!focusId && !touring && Math.abs(view.w - panorama.w) < 1}
          >
            <IconExpand />
            <span>Panorama</span>
          </button>
        </div>
      )}
    </div>
  )
}

/* ── Ficha lateral da emissora em foco ────────────────────────────── */
function FocusPanel({ point, cities, loading, onClose }) {
  const [imgErr, setImgErr] = useState(false)
  const { st, coverKm, reachKm } = point
  const logo = !imgErr ? buildLogoUrl(st.logo_url) : null
  const core = cities.filter(c => c.inContour).length
  const spill = cities.length - core

  return (
    <aside className="cvm-focus">
      <button type="button" className="cvm-focus-close" onClick={onClose} aria-label="Voltar ao panorama">
        <IconClose />
      </button>

      <div className="cvm-focus-head">
        <span className="cvm-focus-logo" data-initials={initialsOf(st.name)}>
          {logo
            ? <img src={logo} alt="" onError={() => setImgErr(true)} />
            : <span className="cvm-focus-initials">{initialsOf(st.name)}</span>}
        </span>
        <span className="cvm-focus-id">
          <strong className="cvm-focus-name">{st.name}</strong>
          <span className="cvm-focus-meta">
            {freqLabel(st)}
            {(st.city || st.state) && ` · ${[st.city, st.state].filter(Boolean).join('/')}`}
          </span>
        </span>
      </div>

      {coverKm ? (
        <dl className="cvm-focus-radii">
          <div className="cvm-focus-radius cvm-focus-radius--core">
            <dt>Contorno protegido</dt>
            <dd>{String(coverKm).replace('.', ',')} km</dd>
          </div>
          <div className="cvm-focus-radius cvm-focus-radius--spill">
            <dt>Com transbordo</dt>
            <dd>{String(reachKm).replace('.', ',')} km</dd>
          </div>
        </dl>
      ) : (
        <p className="cvm-focus-noradius">
          {st.band === 'AM'
            ? 'Cobertura não estimada — em Onda Média a norma define o contorno em intensidade de campo, não em distância.'
            : 'Cobertura não estimada — esta emissora não foi identificada no Plano Básico da Anatel.'}
        </p>
      )}

      {coverKm && (
        <div className="cvm-focus-cities">
          <div className="cvm-focus-cities-head">
            <span>Cidades ao alcance</span>
            <strong>{loading ? '—' : cities.length}</strong>
          </div>
          {loading ? (
            <div className="cvm-focus-loading" aria-live="polite">Carregando cobertura…</div>
          ) : cities.length === 0 ? (
            <div className="cvm-focus-loading">Nenhuma cidade mapeada.</div>
          ) : (
            <>
              <p className="cvm-focus-split">
                {core} no contorno{spill > 0 && <> · {spill} no transbordo</>}
              </p>
              <ul className="cvm-focus-list">
                {cities.map(c => (
                  <li key={c.ibge_code} className={c.inContour ? 'is-core' : 'is-spill'}>
                    <span className="cvm-focus-city">{c.city}</span>
                    <span className="cvm-focus-dist">{String(c.distance_km).replace('.', ',')} km</span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </aside>
  )
}

/* ── Ícones (traço 1.6, mesmo vocabulário do resto do sistema) ────── */
const ico = { viewBox: '0 0 16 16', width: 14, height: 14, fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round', strokeLinejoin: 'round', 'aria-hidden': true }
const IconPlay = () => <svg {...ico} fill="currentColor" stroke="none"><path d="M5.5 3.6a.6.6 0 0 1 .92-.5l6 4.4a.6.6 0 0 1 0 1l-6 4.4a.6.6 0 0 1-.92-.5z" /></svg>
const IconPause = () => <svg {...ico} fill="currentColor" stroke="none"><rect x="4.5" y="3.5" width="2.6" height="9" rx="0.8" /><rect x="8.9" y="3.5" width="2.6" height="9" rx="0.8" /></svg>
const IconPrev = () => <svg {...ico}><path d="M10 3.5 5.5 8l4.5 4.5" /></svg>
const IconNext = () => <svg {...ico}><path d="M6 3.5 10.5 8 6 12.5" /></svg>
const IconExpand = () => <svg {...ico}><path d="M6 2.5H2.5V6M10 2.5h3.5V6M6 13.5H2.5V10M10 13.5h3.5V10" /></svg>
const IconClose = () => <svg {...ico}><path d="M4 4l8 8M12 4l-8 8" /></svg>
