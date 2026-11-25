// webVitals.js — coleta leve de Web Vitals com PerformanceObserver nativo e envio
// pra POST /web-vitals. O PerformanceObserver e o addEventListener são dublês que o
// teste dispara à mão; o módulo é recarregado a cada teste porque cada métrica só é
// reportada uma vez por carregamento (Set em nível de módulo).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'

class ObservadorFalso {
  static instancias = []
  static falhaNoTipo = null
  constructor(cb) {
    this.cb = cb
    this.disconnect = vi.fn()
    ObservadorFalso.instancias.push(this)
  }
  observe(opcoes) {
    if (ObservadorFalso.falhaNoTipo === opcoes.type || ObservadorFalso.falhaNoTipo === '*') throw new Error('tipo não suportado')
    this.opcoes = opcoes
  }
}

const ouvintes = []
let visibilidade = 'visible'

// troca só getEntriesByType (trocar o `performance` inteiro quebra o próprio Vitest)
const entradasOriginais = Object.getOwnPropertyDescriptor(performance, 'getEntriesByType')
function definirEntradasDeNavegacao(fn) {
  Object.defineProperty(performance, 'getEntriesByType', { configurable: true, writable: true, value: fn })
}

function emitir(tipo, entradas) {
  const obs = ObservadorFalso.instancias.find(o => o.opcoes?.type === tipo)
  obs.cb({ getEntries: () => entradas })
}
function esconderAba() {
  visibilidade = 'hidden'
  for (const o of ouvintes.filter(x => x.tipo === 'visibilitychange')) o.fn()
}

async function iniciar({ token = 'token-de-teste', post } = {}) {
  vi.resetModules()
  const postMock = post ?? vi.fn(() => Promise.resolve({}))
  vi.doMock('../api/client', () => ({ default: { post: postMock } }))
  vi.doMock('../contexts/AuthContext', () => ({ getStoredToken: () => token }))
  const mod = await import('./webVitals.js')
  return { ...mod, post: postMock }
}

const enviados = (post) => post.mock.calls.map(c => c[1])

beforeEach(() => {
  ObservadorFalso.instancias = []
  ObservadorFalso.falhaNoTipo = null
  ouvintes.length = 0
  visibilidade = 'visible'
  vi.stubGlobal('PerformanceObserver', ObservadorFalso)
  vi.stubGlobal('addEventListener', (tipo, fn, opcoes) => { ouvintes.push({ tipo, fn, opcoes }) })
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibilidade })
  definirEntradasDeNavegacao(() => [])
})

afterEach(() => {
  if (entradasOriginais) Object.defineProperty(performance, 'getEntriesByType', entradasOriginais)
  else delete performance.getEntriesByType
  vi.unstubAllGlobals()
  vi.doUnmock('../api/client')
  vi.doUnmock('../contexts/AuthContext')
  delete document.visibilityState
})

describe('initWebVitals: FCP', () => {
  test('reporta o first-contentful-paint arredondado, com a página atual, e para de observar', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('paint', [{ name: 'first-paint', startTime: 5 }, { name: 'first-contentful-paint', startTime: 1234.6 }])
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith('/web-vitals', { name: 'FCP', value: 1235, rating: 'good', page: window.location.pathname })
    const obs = ObservadorFalso.instancias.find(o => o.opcoes.type === 'paint')
    expect(obs.disconnect).toHaveBeenCalled()
    expect(obs.opcoes).toEqual({ type: 'paint', buffered: true })
  })

  test('segunda emissão da mesma métrica não reenvia', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('paint', [{ name: 'first-contentful-paint', startTime: 900 }])
    emitir('paint', [{ name: 'first-contentful-paint', startTime: 950 }])
    expect(post).toHaveBeenCalledTimes(1)
  })

  test('entrada que não é first-contentful-paint é ignorada', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('paint', [{ name: 'first-paint', startTime: 100 }])
    expect(post).not.toHaveBeenCalled()
  })

  test.each([
    [1800, 'good'], // limite: ainda bom
    [1801, 'needs-improvement'],
    [3000, 'needs-improvement'],
    [3001, 'poor'],
  ])('faixas do Google: FCP %d ms -> %s', async (ms, rating) => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('paint', [{ name: 'first-contentful-paint', startTime: ms }])
    expect(enviados(post)[0]).toMatchObject({ name: 'FCP', value: ms, rating })
  })
})

describe('initWebVitals: LCP, CLS e INP (só ao esconder a aba)', () => {
  test('LCP usa a última entrada e só envia quando a aba fica oculta', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('largest-contentful-paint', [{ startTime: 1000 }, { startTime: 2600.4 }])
    expect(post).not.toHaveBeenCalled()
    esconderAba()
    expect(enviados(post)).toContainEqual(expect.objectContaining({ name: 'LCP', value: 2600, rating: 'needs-improvement' }))
  })

  test('LCP sem nenhuma entrada não é reportado', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    esconderAba()
    expect(enviados(post).map(e => e.name)).not.toContain('LCP')
  })

  test('aba que continua visível não dispara nada', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('largest-contentful-paint', [{ startTime: 3000 }])
    for (const o of ouvintes.filter(x => x.tipo === 'visibilitychange')) o.fn()
    expect(post).not.toHaveBeenCalled()
  })

  test('CLS soma os deslocamentos sem entrada recente do usuário', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('layout-shift', [
      { value: 0.05, hadRecentInput: false },
      { value: 0.2, hadRecentInput: true }, // causado por clique/teclado: não conta
      { value: 0.06, hadRecentInput: false },
    ])
    esconderAba()
    expect(enviados(post)).toContainEqual(expect.objectContaining({ name: 'CLS', value: 0.11, rating: 'needs-improvement' }))
  })

  test('CLS zero também é reportado (página estável é informação)', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    esconderAba()
    expect(enviados(post)).toContainEqual(expect.objectContaining({ name: 'CLS', value: 0, rating: 'good' }))
  })

  test('INP usa a pior duração de evento', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('event', [{ duration: 80 }, { duration: 350.2 }, { duration: 120 }])
    esconderAba()
    expect(enviados(post)).toContainEqual(expect.objectContaining({ name: 'INP', value: 350, rating: 'needs-improvement' }))
    const obs = ObservadorFalso.instancias.find(o => o.opcoes.type === 'event')
    expect(obs.opcoes).toEqual({ type: 'event', durationThreshold: 16, buffered: true })
  })

  test('os ouvintes de visibilidade são de uso único', async () => {
    const { initWebVitals } = await iniciar()
    initWebVitals()
    const lista = ouvintes.filter(x => x.tipo === 'visibilitychange')
    expect(lista).toHaveLength(3) // LCP, CLS, INP
    expect(lista.every(x => x.opcoes?.once === true)).toBe(true)
  })

  test('rating "poor" quando passa do limite', async () => {
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    emitir('event', [{ duration: 800 }])
    esconderAba()
    expect(enviados(post)).toContainEqual(expect.objectContaining({ name: 'INP', value: 800, rating: 'poor' }))
  })
})

describe('initWebVitals: TTFB', () => {
  test('lê o Navigation Timing e arredonda', async () => {
    definirEntradasDeNavegacao((tipo) => (tipo === 'navigation' ? [{ responseStart: 900.7 }] : []))
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    expect(enviados(post)).toContainEqual({ name: 'TTFB', value: 901, rating: 'needs-improvement', page: window.location.pathname })
  })

  test('responseStart zerado (página ainda carregando) não é reportado', async () => {
    definirEntradasDeNavegacao(() => [{ responseStart: 0 }])
    const { initWebVitals, post } = await iniciar()
    initWebVitals()
    expect(post).not.toHaveBeenCalled()
  })

  test('navegador sem getEntriesByType não derruba o init', async () => {
    definirEntradasDeNavegacao(undefined)
    const { initWebVitals } = await iniciar()
    expect(() => initWebVitals()).not.toThrow()
  })
})

describe('initWebVitals: ambiente e falhas', () => {
  test('sem PerformanceObserver no navegador não faz nada', async () => {
    const original = Object.getOwnPropertyDescriptor(globalThis, 'PerformanceObserver')
    delete globalThis.PerformanceObserver
    try {
      const { initWebVitals, post } = await iniciar()
      initWebVitals()
      expect(ObservadorFalso.instancias).toHaveLength(0)
      expect(ouvintes).toHaveLength(0)
      expect(post).not.toHaveBeenCalled()
    } finally {
      if (original) Object.defineProperty(globalThis, 'PerformanceObserver', original)
    }
  })

  test('navegador que não suporta algum tipo de entrada segue com os outros', async () => {
    ObservadorFalso.falhaNoTipo = 'layout-shift'
    const { initWebVitals, post } = await iniciar()
    expect(() => initWebVitals()).not.toThrow()
    emitir('paint', [{ name: 'first-contentful-paint', startTime: 500 }])
    expect(enviados(post).map(e => e.name)).toEqual(['FCP'])
  })

  test('nenhum tipo suportado: init não lança', async () => {
    ObservadorFalso.falhaNoTipo = '*'
    const { initWebVitals } = await iniciar()
    expect(() => initWebVitals()).not.toThrow()
  })

  test('sem sessão não há o que reportar: nenhuma chamada (evita 401 que expulsaria o visitante)', async () => {
    const { initWebVitals, post } = await iniciar({ token: null })
    initWebVitals()
    emitir('paint', [{ name: 'first-contentful-paint', startTime: 500 }])
    esconderAba()
    expect(post).not.toHaveBeenCalled()
  })

  test('falha no envio é silenciosa: telemetria nunca quebra a página', async () => {
    const post = vi.fn(() => Promise.reject(new Error('rede caiu')))
    const { initWebVitals } = await iniciar({ post })
    initWebVitals()
    emitir('paint', [{ name: 'first-contentful-paint', startTime: 500 }])
    await Promise.resolve()
    await Promise.resolve()
    expect(post).toHaveBeenCalledTimes(1) // e nenhum unhandled rejection derrubou o teste
  })
})
