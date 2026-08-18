// Multi-token, accent-insensitive search helper.
// Mirrors the marketplace search from E-radios: each space-separated token
// must match at least one of the provided fields (token-AND, field-OR).

const COMBINING_MARKS = new RegExp('[\\u0300-\\u036f]', 'g')

export function normalizeText(s) {
  if (s == null) return ''
  return String(s)
    .normalize('NFD')
    .replace(COMBINING_MARKS, '')
    .toLowerCase()
}

export function tokenize(query) {
  if (!query) return []
  return query
    .trim()
    .split(/\s+/)
    .map(t => normalizeText(t))
    .filter(t => t.length >= 2 || /\d/.test(t))
}

// Returns true when every token matches at least one field on the item.
// `fields` is an array of either field names (string) or extractor functions.
export function matchesAllTokens(item, fields, tokens) {
  if (!tokens || tokens.length === 0) return true
  const haystacks = fields.map(f => {
    const v = typeof f === 'function' ? f(item) : item?.[f]
    return normalizeText(v)
  })
  return tokens.every(tok => haystacks.some(h => h.includes(tok)))
}

// ─── Dial ───────────────────────────────────────────────────────────────────

const DIAL_TOKEN = /^\d+([.,]\d+)?$/

// normalizeDial devolve o token de dial em forma canônica (ponto decimal, sem
// zero morto), ou '' quando o token não é numérico. Aceita vírgula porque é
// como se escreve dial no Brasil. Espelha normalizeDial() do backend em
// workers/internal/catalog/station_search.go — os dois têm que concordar, senão
// o highlight acende num resultado que o SQL não trouxe (ou vice-versa).
export function normalizeDial(tok) {
  if (!DIAL_TOKEN.test(tok ?? '')) return ''
  let s = String(tok).replace(',', '.')
  if (s.includes('.')) s = s.replace(/0+$/, '').replace(/\.$/, '')
  return s
}

// dialMatchesToken diz se a frequência da emissora satisfaz o token — exata ou
// por prefixo, como o SQL faz. Usado pelo highlight: um token numérico acende o
// dial inteiro em vez de tentar casar substring com o texto já formatado em
// pt-BR ("99,9"), que nunca bateria.
export function dialMatchesToken(freqMhz, tok) {
  const d = normalizeDial(tok)
  if (!d || freqMhz == null) return false
  const dial = String(freqMhz)
  return dial === d || dial.startsWith(d)
}

// ─── Highlight ──────────────────────────────────────────────────────────────

// Normaliza preservando o mapa de índices pro texto original, para que o
// highlight recorte o texto ACENTUADO nas posições certas. Normalizar caractere
// a caractere mantém o alinhamento mesmo quando um deles some (uma combining
// mark solta normaliza pra '' e simplesmente não entra no mapa).
function normalizeWithMap(s) {
  let norm = ''
  const map = []
  const str = String(s ?? '')
  for (let i = 0; i < str.length; i++) {
    const n = normalizeText(str[i])
    for (const ch of n) {
      norm += ch
      map.push(i)
    }
  }
  return { norm, map, str }
}

// highlightSegments quebra o texto nos trechos que casam com QUALQUER token,
// devolvendo [{ text, hit }] pronto pra virar <mark>. Trechos sobrepostos de
// tokens diferentes são fundidos num só.
export function highlightSegments(text, tokens) {
  const { norm, map, str } = normalizeWithMap(text)
  if (!str) return []
  const toks = (tokens ?? []).filter(Boolean)
  if (toks.length === 0) return [{ text: str, hit: false }]

  const ranges = []
  for (const tok of toks) {
    let from = 0
    for (;;) {
      const i = norm.indexOf(tok, from)
      if (i === -1) break
      ranges.push([map[i], map[i + tok.length - 1] + 1])
      from = i + 1 // sobreposto de propósito: "aa" em "aaa" marca tudo
    }
  }
  if (ranges.length === 0) return [{ text: str, hit: false }]

  ranges.sort((a, b) => a[0] - b[0])
  const merged = [ranges[0]]
  for (const [start, end] of ranges.slice(1)) {
    const last = merged[merged.length - 1]
    if (start <= last[1]) last[1] = Math.max(last[1], end)
    else merged.push([start, end])
  }

  const out = []
  let cursor = 0
  for (const [start, end] of merged) {
    if (start > cursor) out.push({ text: str.slice(cursor, start), hit: false })
    out.push({ text: str.slice(start, end), hit: true })
    cursor = end
  }
  if (cursor < str.length) out.push({ text: str.slice(cursor), hit: false })
  return out
}
