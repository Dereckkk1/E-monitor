// Polyfills do jsdom (setupFiles do Vitest). Só preenche o que o jsdom não tem
// e o app usa ao renderizar: gráficos (Recharts → ResizeObserver), media query,
// rolagem, observador de interseção, URL de blob e canvas (html2canvas/jsPDF).
// NÃO declare hooks de teste aqui (afterEach etc.): no Vitest 5 isso quebra.

class ResizeObserverFalso {
  constructor(cb) { this.cb = cb }
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverFalso

class IntersectionObserverFalso {
  constructor() {}
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return [] }
}
globalThis.IntersectionObserver ??= IntersectionObserverFalso

if (!window.matchMedia) {
  window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent() { return false },
  })
}

window.scrollTo ??= () => {}
Element.prototype.scrollIntoView ??= function scrollIntoView() {}

URL.createObjectURL ??= () => 'blob:teste'
URL.revokeObjectURL ??= () => {}

// jsdom loga "Not implemented: HTMLCanvasElement.prototype.getContext" a cada
// chamada; devolver null é o mesmo resultado, sem o ruído.
HTMLCanvasElement.prototype.getContext = () => null
