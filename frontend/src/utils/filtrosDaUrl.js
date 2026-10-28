// Leitura dos filtros que chegam na URL — os links que as tools do MCP
// devolvem (links.tela) abrem a tela já filtrada. Só o primeiro render lê a
// URL; depois a tela segue com o próprio estado (como /admin/station-failures
// já fazia). Tudo é validado aqui: um valor torto ou vazio vira "sem filtro",
// nunca um pedido que a API recusa.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const DIA = /^(\d{4})-(\d{2})-(\d{2})$/
const INTEIRO = /^-?\d+$/

export function lerId(sp, chave) {
  const v = (sp.get(chave) ?? '').trim()
  return UUID.test(v) ? v : null
}

// lerIds: lista em csv ("a,b,c"); descarta o que não é uuid e as repetições.
export function lerIds(sp, chave) {
  const ids = (sp.get(chave) ?? '').split(',').map(s => s.trim()).filter(s => UUID.test(s))
  return [...new Set(ids)]
}

function diaValido(v) {
  const m = DIA.exec(v ?? '')
  if (!m) return false
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return d.getFullYear() === Number(m[1]) && d.getMonth() === Number(m[2]) - 1 && d.getDate() === Number(m[3])
}

// lerPeriodo: { from, to } com os dois dias válidos e from <= to; senão null.
export function lerPeriodo(sp, de = 'from', ate = 'to') {
  const from = sp.get(de), to = sp.get(ate)
  if (!diaValido(from) || !diaValido(to) || from > to) return null
  return { from, to }
}

export function lerOpcao(sp, chave, validas) {
  const v = sp.get(chave)
  return v != null && validas.includes(v) ? v : null
}

export function lerInteiro(sp, chave, validos) {
  const bruto = (sp.get(chave) ?? '').trim()
  if (!INTEIRO.test(bruto)) return null
  const n = Number(bruto)
  return validos.includes(n) ? n : null
}

// temFiltro: alguma das chaves está na URL (mesmo vazia) — é o sinal para a
// tela limpar a URL depois de ler.
export function temFiltro(sp, chaves) {
  return chaves.some(k => sp.has(k))
}
