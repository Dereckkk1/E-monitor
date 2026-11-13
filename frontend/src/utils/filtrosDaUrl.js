// Leitura dos filtros que chegam na URL — os links que as tools do MCP
// devolvem (links.tela) abrem a tela já filtrada. Só o primeiro render lê a
// URL; depois a tela segue com o próprio estado (como /admin/station-failures
// já fazia). Tudo é validado aqui: um valor torto ou vazio vira "sem filtro",
// nunca um pedido que a API recusa.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const DIA = /^(\d{4})-(\d{2})-(\d{2})$/
const INTEIRO = /^-?\d+$/

// Os ids saem em minúsculas: é como a API os devolve, e as telas comparam o id
// da URL com o das listas (opções do seletor, carteira) por igualdade.
export function lerId(sp, chave) {
  const v = (sp.get(chave) ?? '').trim().toLowerCase()
  return UUID.test(v) ? v : null
}

// lerIds: lista em csv ("a,b,c"); descarta o que não é uuid e as repetições.
export function lerIds(sp, chave) {
  const ids = (sp.get(chave) ?? '').split(',').map(s => s.trim().toLowerCase()).filter(s => UUID.test(s))
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

// ── Campanhas selecionadas fora da página do seletor ──────────────────────
// Os seletores de campanha carregam só a 1ª página de /campaigns (200, ativas
// primeiro). Um link para uma campanha antiga chega com um id que não está
// nela: sem buscá-la à parte, a tela filtra por ela mas o seletor fica vazio.

// campanhasForaDaPagina: os ids selecionados que a página não trouxe. Com a
// página ainda carregando, nada (não dá para saber o que falta).
export function campanhasForaDaPagina(pagina, ids) {
  if (!Array.isArray(pagina) || !Array.isArray(ids) || ids.length === 0) return []
  const tem = new Set(pagina.map(c => c.id))
  return ids.filter(id => !tem.has(id))
}

// juntarCampanhas: a página + as campanhas buscadas à parte (sem repetir e sem
// os null de quem não foi encontrada). Sem extra, devolve a própria página.
export function juntarCampanhas(pagina, extras) {
  const base = Array.isArray(pagina) ? pagina : []
  const tem = new Set(base.map(c => c.id))
  const mais = []
  for (const c of extras ?? []) {
    if (c && !tem.has(c.id)) {
      tem.add(c.id)
      mais.push(c)
    }
  }
  return mais.length ? [...base, ...mais] : base
}

// clienteUnico: o client_id comum a todas as campanhas (todas encontradas), ou
// null se faltar alguma ou forem de clientes diferentes.
export function clienteUnico(campanhas, ids) {
  if (!Array.isArray(ids) || ids.length === 0) return null
  const porId = new Map((campanhas ?? []).map(c => [c.id, c]))
  let cliente = null
  for (const id of ids) {
    const c = porId.get(id)
    if (!c || (cliente && c.client_id !== cliente)) return null
    cliente = c.client_id
  }
  return cliente
}
