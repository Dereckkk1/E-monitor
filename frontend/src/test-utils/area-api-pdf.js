// Dublê de jsPDF + jspdf-autotable pros testes dos geradores de PDF (NÃO é código
// de produção). Registra cada chamada ao documento em ordem, pra o teste conferir
// o que o gerador DECIDE (textos, números, tabelas, nome do arquivo) sem depender
// de pixels. O autotable falso reproduz o contrato do real: chama didParseCell e
// didDrawCell por célula, na ordem, e deixa `doc.lastAutoTable.finalY`.
//
// O estado mora em globalThis porque o teste usa vi.resetModules() (pra zerar o
// cache da logo dos geradores): sem isso a fábrica do vi.mock reimportaria este
// arquivo e o teste enxergaria outro objeto de estado.
const G = (globalThis.__areaApiPdf ??= { docs: [], tabelas: [], finalY: 120, addImageFalha: false })

export const estadoPdf = G

export function reiniciarPdfFalso({ finalY = 120 } = {}) {
  G.docs.length = 0
  G.tabelas.length = 0
  G.finalY = finalY
  G.addImageFalha = false
}

const METODOS_SIMPLES = [
  'setFillColor', 'setTextColor', 'setDrawColor', 'setFont', 'setLineWidth',
  'rect', 'roundedRect', 'circle', 'line', 'text', 'save',
]

export class JsPdfFalso {
  constructor(opcoes = {}) {
    this.opcoes = opcoes
    const mm = opcoes.unit === 'mm'
    const [l, a] = mm ? [210, 297] : [595.28, 841.89]
    const paisagem = opcoes.orientation === 'landscape'
    this.largura = paisagem ? a : l
    this.altura = paisagem ? l : a
    this.chamadas = []
    this.paginas = 1
    this.atual = 1
    this.tamanhoFonte = 16
    this.lastAutoTable = undefined
    this.internal = { pageSize: { getWidth: () => this.largura, getHeight: () => this.altura } }
    G.docs.push(this)
  }

  _reg(nome, args) {
    this.chamadas.push({ nome, args, pagina: this.atual })
  }

  setFontSize(n) { this.tamanhoFonte = n; this._reg('setFontSize', [n]); return this }
  getFontSize() { return this.tamanhoFonte }
  // largura proporcional ao corpo da fonte: encolher a fonte faz o texto caber
  getTextWidth(t) { return String(t).length * this.tamanhoFonte * 0.2 }
  splitTextToSize(t) { return [String(t)] }
  addImage(...args) {
    this._reg('addImage', args)
    if (G.addImageFalha) throw new Error('imagem inválida')
    return this
  }
  addPage() { this.paginas += 1; this.atual = this.paginas; this._reg('addPage', []); return this }
  setPage(n) { this.atual = n; return this }
  getNumberOfPages() { return this.paginas }
  getCurrentPageInfo() { return { pageNumber: this.atual } }

  /** Todos os textos desenhados (arrays viram itens soltos), na ordem. */
  textos() {
    return this.chamadas.filter(c => c.nome === 'text').flatMap(c => (Array.isArray(c.args[0]) ? c.args[0] : [c.args[0]]))
  }
  /** Chamadas de `text` com a posição: { texto, x, y, pagina }. */
  textosComPosicao() {
    return this.chamadas.filter(c => c.nome === 'text').map(c => ({ texto: c.args[0], x: c.args[1], y: c.args[2], pagina: c.pagina }))
  }
  de(nome) { return this.chamadas.filter(c => c.nome === nome) }
  get arquivoSalvo() { return this.de('save')[0]?.args[0] }
}

for (const nome of METODOS_SIMPLES) {
  JsPdfFalso.prototype[nome] = function metodo(...args) {
    this._reg(nome, args)
    return this
  }
}

/** Stand-in do `autoTable(doc, opcoes)` do jspdf-autotable. */
export function autoTableFalso(doc, opcoes) {
  const tabela = { doc, opcoes, celulas: [], pagina: doc.atual }
  G.tabelas.push(tabela)

  ;(opcoes.head?.[0] ?? []).forEach((raw, j) => {
    opcoes.didParseCell?.({
      section: 'head', row: { index: 0 }, column: { index: j },
      cell: { raw, text: [String(raw)], styles: {} },
    })
  })

  ;(opcoes.body ?? []).forEach((linha, i) => {
    linha.forEach((raw, j) => {
      const conteudo = raw !== null && typeof raw === 'object' ? raw.content : raw
      const cell = {
        raw, text: [String(conteudo)], styles: {},
        x: 15 + j * 20, y: 100 + i * 60, width: 20, height: 60,
      }
      const data = { section: 'body', row: { index: i }, column: { index: j }, cell, doc }
      opcoes.didParseCell?.(data)
      tabela.celulas.push({ linha: i, coluna: j, cell })
      opcoes.didDrawCell?.(data)
    })
  })

  doc.lastAutoTable = { finalY: G.finalY }
}
